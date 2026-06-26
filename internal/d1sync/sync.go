// Package d1sync implements full schema+data sync between a Cloudflare D1
// database and a local SQLite file.
//
//   - Pull: D1 → local SQLite  (snapshot D1 for local dev / inspection)
//   - Push: local SQLite → D1  (seed or restore a D1 database)
package d1sync

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/croc100/litescope/internal/connector"
	_ "modernc.org/sqlite"
)

// PullOptions controls the Pull operation.
type PullOptions struct {
	// BatchSize is the number of rows fetched per SELECT page (default 500).
	BatchSize int
	// ProgressFn is called after each table is pulled; may be nil.
	ProgressFn func(table string, rows int)
}

// PushOptions controls the Push operation.
type PushOptions struct {
	// BatchSize is the number of INSERT rows sent per D1 API call (default 100).
	// D1's HTTP API has a 100 statement limit per request.
	BatchSize int
	// DropExisting drops tables in D1 that already exist before recreating them.
	DropExisting bool
	// ProgressFn is called after each table is pushed; may be nil.
	ProgressFn func(table string, rows int)
}

// TableData holds the DDL and rows for one table.
type TableData struct {
	Name string
	DDL  string
	Rows []map[string]interface{}
}

// Pull copies the full contents of a D1 database into a local SQLite file.
// The local file is created (or overwritten). Only user tables are copied;
// Cloudflare-internal tables (_cf_*) are skipped.
func Pull(d1DSN, localPath string, opts PullOptions) error {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}

	d1, err := connector.Open(d1DSN)
	if err != nil {
		return fmt.Errorf("open D1: %w", err)
	}
	defer d1.Close()

	tables, err := d1TableDDLs(d1)
	if err != nil {
		return err
	}

	// Open (or create) local SQLite.
	localDB, err := sql.Open("sqlite", localPath)
	if err != nil {
		return fmt.Errorf("open local %s: %w", localPath, err)
	}
	defer localDB.Close()

	for _, td := range tables {
		// Drop + recreate locally.
		if _, err := localDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %q", td.Name)); err != nil {
			return fmt.Errorf("drop local %s: %w", td.Name, err)
		}
		if _, err := localDB.Exec(td.DDL); err != nil {
			return fmt.Errorf("create local %s: %w\nDDL: %s", td.Name, err, td.DDL)
		}

		// Fetch rows from D1 in pages and insert locally.
		total := 0
		for offset := 0; ; offset += opts.BatchSize {
			rows, err := connector.Query(d1, fmt.Sprintf(
				"SELECT * FROM %q LIMIT %d OFFSET %d", td.Name, opts.BatchSize, offset,
			))
			if err != nil {
				return fmt.Errorf("fetch %s offset %d: %w", td.Name, offset, err)
			}
			if len(rows) == 0 {
				break
			}
			if err := insertRows(localDB, td.Name, rows); err != nil {
				return fmt.Errorf("insert into local %s: %w", td.Name, err)
			}
			total += len(rows)
			if len(rows) < opts.BatchSize {
				break
			}
		}
		if opts.ProgressFn != nil {
			opts.ProgressFn(td.Name, total)
		}
	}
	return nil
}

// Push copies the full contents of a local SQLite file into a D1 database.
// Only user tables are pushed; SQLite-internal tables (sqlite_*) are skipped.
func Push(localPath, d1DSN string, opts PushOptions) error {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50 // conservative: D1 has limits on payload size
	}

	localDB, err := sql.Open("sqlite", localPath)
	if err != nil {
		return fmt.Errorf("open local %s: %w", localPath, err)
	}
	defer localDB.Close()

	tables, err := localTableDDLs(localDB)
	if err != nil {
		return err
	}

	d1, err := connector.Open(d1DSN)
	if err != nil {
		return fmt.Errorf("open D1: %w", err)
	}
	defer d1.Close()

	exec, ok := connector.AsExecutor(d1)
	if !ok {
		return fmt.Errorf("D1 connector does not support execution")
	}

	for _, td := range tables {
		var stmts []string
		if opts.DropExisting {
			stmts = append(stmts, fmt.Sprintf("DROP TABLE IF EXISTS %q", td.Name))
		}
		stmts = append(stmts, td.DDL)
		if err := exec.Exec(stmts, false); err != nil {
			return fmt.Errorf("create D1 table %s: %w", td.Name, err)
		}

		// Fetch rows from local and push to D1 in batches.
		rows, err := localQueryRows(localDB, fmt.Sprintf("SELECT * FROM %q", td.Name))
		if err != nil {
			return fmt.Errorf("read local %s: %w", td.Name, err)
		}

		for i := 0; i < len(rows); i += opts.BatchSize {
			end := i + opts.BatchSize
			if end > len(rows) {
				end = len(rows)
			}
			batch := rows[i:end]
			inserts, err := buildInserts(td.Name, batch)
			if err != nil {
				return fmt.Errorf("build inserts for %s: %w", td.Name, err)
			}
			if err := exec.Exec(inserts, false); err != nil {
				return fmt.Errorf("insert D1 %s (batch %d): %w", td.Name, i/opts.BatchSize+1, err)
			}
		}

		if opts.ProgressFn != nil {
			opts.ProgressFn(td.Name, len(rows))
		}
	}
	return nil
}

// ── D1 helpers ────────────────────────────────────────────────────────────────

// d1TableDDLs returns the DDL and name for every user table in the D1 database.
func d1TableDDLs(d1 connector.Connector) ([]TableData, error) {
	rows, err := connector.Query(d1,
		"SELECT name, sql FROM sqlite_master WHERE type='table' "+
			"AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("list D1 tables: %w", err)
	}
	var out []TableData
	for _, row := range rows {
		name := fmt.Sprintf("%v", row["name"])
		ddl := fmt.Sprintf("%v", row["sql"])
		if ddl == "" || ddl == "<nil>" {
			continue
		}
		out = append(out, TableData{Name: name, DDL: ddl})
	}
	return out, nil
}

// ── local SQLite helpers ──────────────────────────────────────────────────────

func localTableDDLs(db *sql.DB) ([]TableData, error) {
	rows, err := db.Query(
		"SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY rowid",
	)
	if err != nil {
		return nil, fmt.Errorf("list local tables: %w", err)
	}
	defer rows.Close()

	var out []TableData
	for rows.Next() {
		var name, ddl string
		if err := rows.Scan(&name, &ddl); err != nil {
			return nil, err
		}
		out = append(out, TableData{Name: name, DDL: ddl})
	}
	return out, rows.Err()
}

func localQueryRows(db *sql.DB, query string) ([]map[string]interface{}, error) {
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

func insertRows(db *sql.DB, table string, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}
	// Collect column order from first row.
	cols := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		cols = append(cols, k)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	placeholders := make([]string, len(cols))
	for i := range cols {
		placeholders[i] = "?"
	}
	colsQuoted := make([]string, len(cols))
	for i, c := range cols {
		colsQuoted[i] = fmt.Sprintf("%q", c)
	}
	stmt, err := tx.Prepare(fmt.Sprintf(
		"INSERT OR IGNORE INTO %q (%s) VALUES (%s)",
		table, strings.Join(colsQuoted, ","), strings.Join(placeholders, ","),
	))
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, row := range rows {
		vals := make([]interface{}, len(cols))
		for i, c := range cols {
			vals[i] = row[c]
		}
		if _, err := stmt.Exec(vals...); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// buildInserts generates INSERT OR IGNORE statements for a batch of rows.
func buildInserts(table string, rows []map[string]interface{}) ([]string, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	cols := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		cols = append(cols, k)
	}
	colsQuoted := make([]string, len(cols))
	for i, c := range cols {
		colsQuoted[i] = fmt.Sprintf("%q", c)
	}
	colList := strings.Join(colsQuoted, ", ")

	stmts := make([]string, 0, len(rows))
	for _, row := range rows {
		vals := make([]string, len(cols))
		for i, c := range cols {
			vals[i] = sqlLiteral(row[c])
		}
		stmts = append(stmts, fmt.Sprintf(
			"INSERT OR IGNORE INTO %q (%s) VALUES (%s)",
			table, colList, strings.Join(vals, ", "),
		))
	}
	return stmts, nil
}

// sqlLiteral converts a Go value to a SQL literal string.
func sqlLiteral(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "1"
		}
		return "0"
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case int64:
		return fmt.Sprintf("%d", val)
	case []byte:
		// BLOB as hex literal
		return fmt.Sprintf("X'%X'", val)
	default:
		s := fmt.Sprintf("%v", val)
		// Escape single quotes
		s = strings.ReplaceAll(s, "'", "''")
		return fmt.Sprintf("'%s'", s)
	}
}
