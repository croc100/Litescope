package migrate

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// trackingTable records which versioned migrations have been applied to a
// database. The checksum lets us detect a migration file edited after it ran.
const trackingTable = "litescope_schema_migrations"

// MigrationFile is one file in the migrations directory.
type MigrationFile struct {
	Version  string `json:"version"`  // zero-padded sequence, e.g. "0003"
	Name     string `json:"name"`     // human slug
	Path     string `json:"-"`
	Checksum string `json:"checksum"` // sha256 of file contents
}

// AppliedRecord is a row from the tracking table.
type AppliedRecord struct {
	Version   string `json:"version"`
	Name      string `json:"name"`
	Checksum  string `json:"checksum"`
	AppliedAt string `json:"applied_at"`
}

// Status compares the migrations directory against what a database has applied.
type Status struct {
	Applied []AppliedRecord `json:"applied"`
	Pending []MigrationFile `json:"pending"`
	// Drifted lists versions whose file checksum no longer matches what was
	// applied — the migration history itself changed.
	Drifted []string `json:"drifted,omitempty"`
}

var migrationFileRe = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// LoadDir reads and version-sorts the migration files in dir. A missing
// directory is treated as empty.
func LoadDir(dir string) ([]MigrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []MigrationFile
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if seen[m[1]] {
			return nil, fmt.Errorf("duplicate migration version %s", m[1])
		}
		seen[m[1]] = true
		path := filepath.Join(dir, e.Name())
		sum, err := checksumFile(path)
		if err != nil {
			return nil, err
		}
		files = append(files, MigrationFile{Version: m[1], Name: m[2], Path: path, Checksum: sum})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	return files, nil
}

// New creates the next migration file in dir with the given name and SQL body
// (which may be empty). It returns the created path.
func New(dir, name, body string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	files, err := LoadDir(dir)
	if err != nil {
		return "", err
	}
	next := 1
	if len(files) > 0 {
		last := files[len(files)-1].Version
		var n int
		fmt.Sscanf(last, "%d", &n)
		next = n + 1
	}
	slug := slugRe.ReplaceAllString(strings.ToLower(name), "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		slug = "migration"
	}
	filename := fmt.Sprintf("%04d_%s.sql", next, slug)
	path := filepath.Join(dir, filename)
	if body == "" {
		body = fmt.Sprintf("-- migration %04d: %s\n-- write your schema changes below\n", next, name)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// GetStatus returns applied vs pending migrations for a database.
func GetStatus(dbPath, dir string) (*Status, error) {
	files, err := LoadDir(dir)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	applied, err := appliedRecords(db)
	if err != nil {
		return nil, err
	}
	appliedByVersion := map[string]AppliedRecord{}
	for _, a := range applied {
		appliedByVersion[a.Version] = a
	}

	st := &Status{Applied: applied}
	for _, f := range files {
		if rec, ok := appliedByVersion[f.Version]; ok {
			if rec.Checksum != "" && rec.Checksum != f.Checksum {
				st.Drifted = append(st.Drifted, f.Version)
			}
			continue
		}
		st.Pending = append(st.Pending, f)
	}
	return st, nil
}

// UpResult reports the outcome of applying pending migrations.
type UpResult struct {
	Applied []string `json:"applied"` // versions applied in this run
}

// Up applies every pending migration in order, each through the safe Apply
// pipeline (backup, single transaction, FK + integrity verification, rollback),
// recording it in the tracking table. It stops at the first failure. A checksum
// mismatch on an already-applied migration aborts before doing anything.
func Up(dbPath, dir string, opts ApplyOptions) (*UpResult, error) {
	st, err := GetStatus(dbPath, dir)
	if err != nil {
		return nil, err
	}
	if len(st.Drifted) > 0 {
		return nil, fmt.Errorf("migration history drift: file checksum changed for version(s) %s — applied migrations must not be edited",
			strings.Join(st.Drifted, ", "))
	}

	res := &UpResult{}
	for _, f := range st.Pending {
		body, err := os.ReadFile(f.Path)
		if err != nil {
			return res, err
		}
		if _, err := Apply(dbPath, string(body), opts); err != nil {
			return res, fmt.Errorf("migration %s_%s failed: %w", f.Version, f.Name, err)
		}
		// Dry-run rolls back each migration, so don't record it as applied.
		if !opts.DryRun {
			if err := recordApplied(dbPath, f); err != nil {
				return res, fmt.Errorf("migration %s applied but recording it failed: %w", f.Version, err)
			}
		}
		res.Applied = append(res.Applied, f.Version)
	}
	return res, nil
}

// ── tracking table ──────────────────────────────────────────────────────────

func ensureTracking(db *sql.DB) error {
	_, err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (
		version    TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		checksum   TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`, trackingTable))
	return err
}

func appliedRecords(db *sql.DB) ([]AppliedRecord, error) {
	if err := ensureTracking(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(fmt.Sprintf("SELECT version, name, checksum, applied_at FROM %q ORDER BY version", trackingTable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppliedRecord
	for rows.Next() {
		var a AppliedRecord
		if err := rows.Scan(&a.Version, &a.Name, &a.Checksum, &a.AppliedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func recordApplied(dbPath string, f MigrationFile) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := ensureTracking(db); err != nil {
		return err
	}
	_, err = db.Exec(
		fmt.Sprintf("INSERT INTO %q (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)", trackingTable),
		f.Version, f.Name, f.Checksum, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func checksumFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
