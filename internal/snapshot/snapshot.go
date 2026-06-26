// Package snapshot provides point-in-time backups for local SQLite files —
// the file-superpower moat extended beyond D1 Time Travel. Snapshots are
// consistent VACUUM INTO copies (safe even under WAL), stored in a sibling
// .litescope-snapshots directory, with listing, restore, and retention.
package snapshot

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/migrate"

	_ "modernc.org/sqlite"
)

// DirName is the sibling directory where snapshots for a database are stored.
const DirName = ".litescope-snapshots"

const tsLayout = "20060102-150405"

// Snapshot is one stored point-in-time copy of a database.
type Snapshot struct {
	Path      string    `json:"path"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

// CreateOptions controls a snapshot.
type CreateOptions struct {
	Label string // optional human label, recorded in the filename
	Keep  int    // retention: keep at most N snapshots (0 = keep all)
}

// Dir returns the snapshot directory for a database file.
func Dir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), DirName)
}

func base(dbPath string) string {
	return strings.TrimSuffix(filepath.Base(dbPath), filepath.Ext(dbPath))
}

// Create takes a consistent VACUUM INTO snapshot of dbPath. It refuses to
// snapshot a corrupt database, verifies the snapshot's integrity, and applies
// retention when opts.Keep > 0.
func Create(dbPath string, opts CreateOptions) (*Snapshot, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("database not found: %s", dbPath)
	}

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()
	if err := quickCheck(db); err != nil {
		return nil, fmt.Errorf("integrity check failed — refusing to snapshot a corrupt database: %w", err)
	}

	dir := Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	now := time.Now()
	label := sanitizeLabel(opts.Label)
	stem := base(dbPath) + "__" + now.Format(tsLayout)
	if label != "" {
		stem += "__" + label
	}
	// Guard against collisions when several snapshots land in the same second.
	dest := filepath.Join(dir, stem+".db")
	for n := 2; fileExists(dest); n++ {
		dest = filepath.Join(dir, fmt.Sprintf("%s-%d.db", stem, n))
	}

	if _, err := db.Exec("VACUUM INTO ?", dest); err != nil {
		return nil, fmt.Errorf("snapshot failed: %w", err)
	}

	// Verify the snapshot we just wrote is itself sound.
	if err := verifyFile(dest); err != nil {
		os.Remove(dest)
		return nil, fmt.Errorf("snapshot verification failed: %w", err)
	}

	fi, err := os.Stat(dest)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{Path: dest, Label: label, CreatedAt: now, SizeBytes: fi.Size()}

	if opts.Keep > 0 {
		if _, err := Prune(dbPath, opts.Keep); err != nil {
			return snap, fmt.Errorf("snapshot created but retention failed: %w", err)
		}
	}
	return snap, nil
}

// List returns all snapshots for a database, newest first.
func List(dbPath string) ([]Snapshot, error) {
	dir := Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	prefix := base(dbPath) + "__"
	var out []Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		ts, label := parseName(e.Name(), prefix)
		if ts.IsZero() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Snapshot{
			Path:      filepath.Join(dir, e.Name()),
			Label:     label,
			CreatedAt: ts,
			SizeBytes: info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Latest returns the most recent snapshot for a database, if any.
func Latest(dbPath string) (*Snapshot, bool, error) {
	snaps, err := List(dbPath)
	if err != nil || len(snaps) == 0 {
		return nil, false, err
	}
	return &snaps[0], true, nil
}

// Restore overwrites dbPath with a snapshot. The snapshot is integrity-checked
// first, and the current database is itself snapshotted as a safety net before
// being overwritten (unless safetyNet is false).
func Restore(dbPath, snapshotPath string, safetyNet bool) error {
	if err := verifyFile(snapshotPath); err != nil {
		return fmt.Errorf("refusing to restore a corrupt snapshot: %w", err)
	}
	if safetyNet {
		if _, err := os.Stat(dbPath); err == nil {
			if _, err := Create(dbPath, CreateOptions{Label: "pre-restore"}); err != nil {
				return fmt.Errorf("pre-restore safety snapshot failed: %w", err)
			}
		}
	}
	return migrate.Restore(dbPath, snapshotPath)
}

// Prune deletes the oldest snapshots, keeping at most keep newest. It returns
// the snapshots that were removed.
func Prune(dbPath string, keep int) ([]Snapshot, error) {
	if keep < 0 {
		keep = 0
	}
	snaps, err := List(dbPath)
	if err != nil {
		return nil, err
	}
	if len(snaps) <= keep {
		return nil, nil
	}
	var removed []Snapshot
	for _, s := range snaps[keep:] {
		if err := os.Remove(s.Path); err != nil {
			return removed, err
		}
		removed = append(removed, s)
	}
	return removed, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func parseName(name, prefix string) (time.Time, string) {
	rest := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".db")
	parts := strings.SplitN(rest, "__", 2)
	// The timestamp is fixed-width; a "-N" collision suffix may follow it.
	stamp := parts[0]
	if len(stamp) > len(tsLayout) {
		stamp = stamp[:len(tsLayout)]
	}
	ts, err := time.ParseInLocation(tsLayout, stamp, time.Local)
	if err != nil {
		return time.Time{}, ""
	}
	label := ""
	if len(parts) == 2 {
		label = parts[1]
	}
	return ts, label
}

func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func quickCheck(db *sql.DB) error {
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("quick_check: %s", result)
	}
	return nil
}

func verifyFile(path string) error {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	return quickCheck(db)
}
