package check

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/diff"
	_ "modernc.org/sqlite"
)

// Result holds the outcome of a backup integrity check.
type Result struct {
	Path            string
	ReferencePath   string `json:",omitempty"`
	IntegrityOK     bool
	IntegrityErrors []string      `json:",omitempty"`
	SchemaOK        *bool         `json:",omitempty"` // nil when no reference given
	SchemaDiff      *diff.Result  `json:",omitempty"`
	DataOK          *bool         `json:",omitempty"` // nil when --data not requested
	Tables          []TableStat   `json:",omitempty"`
	Passed          bool
}

type TableStat struct {
	Name        string
	BackupRows  int64
	RefRows     int64  `json:",omitempty"`
	RowsMatch   *bool  `json:",omitempty"`
}

// Check runs integrity checks on the backup database.
// If referencePath is non-empty, also compares schema against it.
// If withData is true, also compares row counts per table.
func Check(backupPath, referencePath string, withData bool) (*Result, error) {
	result := &Result{
		Path:          backupPath,
		ReferencePath: referencePath,
	}

	// 1. PRAGMA integrity_check
	errors, err := integrityCheck(backupPath)
	if err != nil {
		return nil, fmt.Errorf("integrity check: %w", err)
	}
	result.IntegrityErrors = errors
	result.IntegrityOK = len(errors) == 0

	// 2. Schema comparison (requires reference)
	if referencePath != "" {
		d, err := diff.Compare(referencePath, backupPath)
		if err != nil {
			return nil, fmt.Errorf("schema comparison: %w", err)
		}
		schemaOK := len(d.Schema) == 0
		result.SchemaOK = &schemaOK
		if !schemaOK {
			result.SchemaDiff = d
		}

		// 3. Row counts (requires reference)
		if withData {
			stats, err := compareRowCounts(referencePath, backupPath)
			if err != nil {
				return nil, fmt.Errorf("row count comparison: %w", err)
			}
			result.Tables = stats
			allMatch := true
			for _, s := range stats {
				if s.RowsMatch != nil && !*s.RowsMatch {
					allMatch = false
					break
				}
			}
			result.DataOK = &allMatch
		}
	} else if withData {
		// No reference — just report backup row counts
		stats, err := rowCounts(backupPath)
		if err != nil {
			return nil, fmt.Errorf("row counts: %w", err)
		}
		result.Tables = stats
	}

	result.Passed = result.IntegrityOK &&
		(result.SchemaOK == nil || *result.SchemaOK) &&
		(result.DataOK == nil || *result.DataOK)

	return result, nil
}

// ── SQLite helpers ────────────────────────────────────────────────────────────

func openDB(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("database file not found: %s", path)
	}
	return sql.Open("sqlite", path+"?mode=ro")
}

func integrityCheck(path string) ([]string, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("PRAGMA integrity_check")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var errors []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return nil, err
		}
		if msg != "ok" {
			errors = append(errors, msg)
		}
	}
	return errors, rows.Err()
}

func rowCounts(path string) ([]TableStat, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return queryRowCounts(db)
}

func compareRowCounts(refPath, backupPath string) ([]TableStat, error) {
	refDB, err := openDB(refPath)
	if err != nil {
		return nil, err
	}
	defer refDB.Close()

	backupDB, err := openDB(backupPath)
	if err != nil {
		return nil, err
	}
	defer backupDB.Close()

	refCounts, err := queryRowCounts(refDB)
	if err != nil {
		return nil, err
	}

	backupCounts, err := queryRowCounts(backupDB)
	if err != nil {
		return nil, err
	}

	// Merge by table name
	backupMap := make(map[string]int64, len(backupCounts))
	for _, s := range backupCounts {
		backupMap[s.Name] = s.BackupRows
	}

	var stats []TableStat
	for _, ref := range refCounts {
		bRows, exists := backupMap[ref.Name]
		match := exists && bRows == ref.BackupRows
		stats = append(stats, TableStat{
			Name:       ref.Name,
			BackupRows: bRows,
			RefRows:    ref.BackupRows,
			RowsMatch:  &match,
		})
	}
	return stats, nil
}

func queryRowCounts(db *sql.DB) ([]TableStat, error) {
	tables, err := db.Query(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
	)
	if err != nil {
		return nil, err
	}
	defer tables.Close()

	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}

	var stats []TableStat
	for _, name := range names {
		var count int64
		row := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", name))
		if err := row.Scan(&count); err != nil {
			return nil, err
		}
		stats = append(stats, TableStat{Name: name, BackupRows: count})
	}
	return stats, nil
}
