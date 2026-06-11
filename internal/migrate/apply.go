package migrate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ApplyOptions controls how a migration is executed.
type ApplyOptions struct {
	DryRun    bool   // execute inside a transaction, then roll back
	BackupDir string // where to write the pre-migration backup ("" = alongside the DB)
	NoBackup  bool   // skip backup (dry-run never backs up)
}

// ApplyResult reports what happened during Apply.
type ApplyResult struct {
	Executed   int           // statements executed
	BackupPath string        // "" when no backup was taken
	Duration   time.Duration
	DryRun     bool
	Restored   bool // true when the backup was restored after a failure
}

// Apply runs migration SQL against a local SQLite database.
//
// Safety sequence:
//  1. Pre-flight integrity check — refuse to migrate a corrupt database
//  2. Backup via VACUUM INTO (unless dry-run or disabled)
//  3. Execute all statements inside a single transaction
//  4. Post-flight: PRAGMA foreign_key_check + integrity check inside the transaction
//  5. Commit — or roll back on any failure; restore the backup if commit itself failed
func Apply(dbPath, sqlText string, opts ApplyOptions) (*ApplyResult, error) {
	start := time.Now()
	res := &ApplyResult{DryRun: opts.DryRun}

	if _, err := os.Stat(dbPath); err != nil {
		return res, fmt.Errorf("database not found: %s", dbPath)
	}

	stmts := SplitStatements(sqlText)
	if len(stmts) == 0 {
		return res, fmt.Errorf("no executable statements in migration")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return res, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	// 1. Pre-flight: never migrate a corrupt database.
	if err := quickCheck(db); err != nil {
		return res, fmt.Errorf("pre-flight integrity check failed — migration refused: %w", err)
	}

	// 2. Backup.
	if !opts.DryRun && !opts.NoBackup {
		backupPath, err := backup(db, dbPath, opts.BackupDir)
		if err != nil {
			return res, fmt.Errorf("backup failed — migration refused: %w", err)
		}
		res.BackupPath = backupPath
	}

	// 3. Execute inside a single transaction.
	tx, err := db.Begin()
	if err != nil {
		return res, fmt.Errorf("begin transaction: %w", err)
	}

	for i, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return res, fmt.Errorf("statement %d failed (rolled back, database unchanged):\n  %s\n  %w",
				i+1, firstLine(stmt), err)
		}
		res.Executed++
	}

	// 4. Post-flight checks inside the transaction — catch FK breakage
	//    introduced by table rebuilds before anything is committed.
	if err := foreignKeyCheck(tx); err != nil {
		tx.Rollback()
		return res, fmt.Errorf("post-migration foreign key check failed (rolled back, database unchanged): %w", err)
	}

	if opts.DryRun {
		tx.Rollback()
		res.Duration = time.Since(start)
		return res, nil
	}

	// 5. Commit; restore backup if the commit itself fails.
	if err := tx.Commit(); err != nil {
		if res.BackupPath != "" {
			if rerr := restore(dbPath, res.BackupPath); rerr == nil {
				res.Restored = true
				return res, fmt.Errorf("commit failed — database restored from backup: %w", err)
			}
		}
		return res, fmt.Errorf("commit failed: %w", err)
	}

	// Final standalone integrity verification on the committed database.
	if err := quickCheck(db); err != nil {
		if res.BackupPath != "" {
			if rerr := restore(dbPath, res.BackupPath); rerr == nil {
				res.Restored = true
				return res, fmt.Errorf("post-commit integrity check failed — database restored from backup: %w", err)
			}
		}
		return res, fmt.Errorf("post-commit integrity check failed and restore was not possible: %w", err)
	}

	res.Duration = time.Since(start)
	return res, nil
}

// ── Checks ────────────────────────────────────────────────────────────────────

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

func foreignKeyCheck(tx *sql.Tx) error {
	rows, err := tx.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()

	var violations []string
	for rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		violations = append(violations, fmt.Sprintf("%s → %s (rowid %d)", table, parent, rowid.Int64))
		if len(violations) >= 5 {
			violations = append(violations, "…")
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(violations) > 0 {
		return fmt.Errorf("%d foreign key violation(s): %s", len(violations), strings.Join(violations, ", "))
	}
	return nil
}

// ── Backup / restore ──────────────────────────────────────────────────────────

func backup(db *sql.DB, dbPath, backupDir string) (string, error) {
	dir := backupDir
	if dir == "" {
		dir = filepath.Dir(dbPath)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	base := strings.TrimSuffix(filepath.Base(dbPath), filepath.Ext(dbPath))
	name := fmt.Sprintf("%s.backup-%s.db", base, time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)

	// VACUUM INTO produces a consistent point-in-time copy even with WAL mode.
	if _, err := db.Exec("VACUUM INTO ?", path); err != nil {
		return "", err
	}
	return path, nil
}

func restore(dbPath, backupPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	// Remove WAL/SHM sidecars — they belong to the failed state.
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	return os.WriteFile(dbPath, data, 0644)
}

// ── SQL statement splitting ───────────────────────────────────────────────────

// SplitStatements splits SQL text into individual statements.
// It respects single/double-quoted strings and "--" line comments,
// and skips comment-only or empty fragments.
func SplitStatements(sqlText string) []string {
	var stmts []string
	var cur strings.Builder

	inSingle, inDouble, inComment := false, false, false
	runes := []rune(sqlText)

	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if inComment {
			cur.WriteRune(c)
			if c == '\n' {
				inComment = false
			}
			continue
		}

		switch {
		case inSingle:
			cur.WriteRune(c)
			if c == '\'' {
				// '' escapes a quote inside a string
				if i+1 < len(runes) && runes[i+1] == '\'' {
					cur.WriteRune(runes[i+1])
					i++
				} else {
					inSingle = false
				}
			}
		case inDouble:
			cur.WriteRune(c)
			if c == '"' {
				if i+1 < len(runes) && runes[i+1] == '"' {
					cur.WriteRune(runes[i+1])
					i++
				} else {
					inDouble = false
				}
			}
		case c == '\'':
			inSingle = true
			cur.WriteRune(c)
		case c == '"':
			inDouble = true
			cur.WriteRune(c)
		case c == '-' && i+1 < len(runes) && runes[i+1] == '-':
			inComment = true
			cur.WriteRune(c)
		case c == ';':
			if s := cleanStatement(cur.String()); s != "" {
				stmts = append(stmts, s)
			}
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if s := cleanStatement(cur.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

// cleanStatement trims whitespace and returns "" when the fragment
// contains only comments or whitespace.
func cleanStatement(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	for _, line := range strings.Split(trimmed, "\n") {
		l := strings.TrimSpace(line)
		if l != "" && !strings.HasPrefix(l, "--") {
			return trimmed
		}
	}
	return ""
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if l != "" && !strings.HasPrefix(l, "--") {
			return l
		}
	}
	return strings.TrimSpace(s)
}
