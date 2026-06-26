package connector

import (
	"database/sql"
	"fmt"

	"github.com/croc100/litescope/internal/schema"
	_ "modernc.org/sqlite"
)

type fileConnector struct {
	path string
}

func openFile(path string) (Connector, error) {
	return &fileConnector{path: path}, nil
}

func (f *fileConnector) Schema() (*schema.Schema, error) {
	return schema.Load(f.path)
}

func (f *fileConnector) Close() error { return nil }
func (f *fileConnector) DSN() string  { return f.path }

func (f *fileConnector) Capabilities() ExecCapabilities {
	return ExecCapabilities{Transactional: true, LocalBackup: true, Provider: "local"}
}

// Exec runs the statements inside a single transaction, rolling back on any
// failure (and always, when dryRun is set). Callers that need a backup should
// use migrate.Apply, which wraps this with a VACUUM INTO backup.
func (f *fileConnector) QueryRows(query string) ([]map[string]interface{}, error) {
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", f.path, err)
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			row[col] = vals[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (f *fileConnector) Exec(statements []string, dryRun bool) error {
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		return fmt.Errorf("open %s: %w", f.path, err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for i, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("statement %d failed (rolled back): %w", i+1, err)
		}
	}
	if dryRun {
		return tx.Rollback()
	}
	return tx.Commit()
}
