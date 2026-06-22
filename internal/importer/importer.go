// Package importer ingests tabular data (CSV / TSV / JSON) into a SQLite table,
// inferring column types from the data. It is the data-in half of Litescope:
// drag a spreadsheet, get a real database you can then doctor / lint / diff.
package importer

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Mode controls how an import interacts with an existing table.
type Mode int

const (
	// ModeCreate creates the table and fails if it already exists.
	ModeCreate Mode = iota
	// ModeReplace drops any existing table and recreates it.
	ModeReplace
	// ModeAppend inserts into an existing table (created if absent).
	ModeAppend
)

// Options configures an import.
type Options struct {
	Table     string // destination table name (required)
	Mode      Mode
	HasHeader bool // CSV/TSV: first row holds column names
	Delimiter rune // CSV/TSV delimiter; 0 means comma
}

// Column is an inferred destination column.
type Column struct {
	Name string
	Type string // INTEGER | REAL | TEXT
}

// Result summarizes a completed import.
type Result struct {
	Table   string
	Columns []Column
	Rows    int
}

// ImportCSV reads delimited rows from r and loads them into opt.Table.
func ImportCSV(db *sql.DB, r io.Reader, opt Options) (*Result, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	if opt.Delimiter != 0 {
		cr.Comma = opt.Delimiter
	}

	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no rows in input")
	}

	var headers []string
	var rows [][]string
	if opt.HasHeader {
		headers = records[0]
		rows = records[1:]
	} else {
		headers = make([]string, len(records[0]))
		for i := range headers {
			headers[i] = fmt.Sprintf("col%d", i+1)
		}
		rows = records
	}
	headers = dedupeHeaders(headers)

	// Normalize ragged rows to the header width.
	for i := range rows {
		for len(rows[i]) < len(headers) {
			rows[i] = append(rows[i], "")
		}
		if len(rows[i]) > len(headers) {
			rows[i] = rows[i][:len(headers)]
		}
	}

	cols := inferColumns(headers, rows)
	return load(db, opt, cols, rows)
}

// ImportJSON reads a JSON array of objects from r and loads them into opt.Table.
func ImportJSON(db *sql.DB, r io.Reader, opt Options) (*Result, error) {
	var raw []map[string]json.RawMessage
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse JSON (expected an array of objects): %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("no objects in input")
	}

	// Column order = first appearance across all objects.
	var headers []string
	seen := map[string]bool{}
	for _, obj := range raw {
		for k := range obj {
			if !seen[k] {
				seen[k] = true
				headers = append(headers, k)
			}
		}
	}
	// json maps are unordered; sort for determinism so re-imports are stable.
	stableSort(headers)
	headers = dedupeHeaders(headers)

	rows := make([][]string, len(raw))
	for i, obj := range raw {
		row := make([]string, len(headers))
		for j, h := range headers {
			row[j] = jsonScalar(obj[h])
		}
		rows[i] = row
	}

	cols := inferColumns(headers, rows)
	return load(db, opt, cols, rows)
}

// load creates the table (per mode) and inserts rows in one transaction.
func load(db *sql.DB, opt Options, cols []Column, rows [][]string) (*Result, error) {
	if opt.Table == "" {
		return nil, fmt.Errorf("destination table name is required")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	switch opt.Mode {
	case ModeReplace:
		if _, err := tx.Exec("DROP TABLE IF EXISTS " + quoteIdent(opt.Table)); err != nil {
			return nil, err
		}
		if err := createTable(tx, opt.Table, cols); err != nil {
			return nil, err
		}
	case ModeAppend:
		if err := createTableIfNotExists(tx, opt.Table, cols); err != nil {
			return nil, err
		}
	default: // ModeCreate
		if exists, err := tableExists(tx, opt.Table); err != nil {
			return nil, err
		} else if exists {
			return nil, fmt.Errorf("table %q already exists (use --replace or --append)", opt.Table)
		}
		if err := createTable(tx, opt.Table, cols); err != nil {
			return nil, err
		}
	}

	insSQL := insertStmt(opt.Table, cols)
	stmt, err := tx.Prepare(insSQL)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	for _, row := range rows {
		args := make([]interface{}, len(cols))
		for i := range cols {
			args[i] = coerce(row[i], cols[i].Type)
		}
		if _, err := stmt.Exec(args...); err != nil {
			return nil, fmt.Errorf("insert row: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Result{Table: opt.Table, Columns: cols, Rows: len(rows)}, nil
}

// inferColumns determines each column's SQLite type by scanning every value.
func inferColumns(headers []string, rows [][]string) []Column {
	cols := make([]Column, len(headers))
	for i, h := range headers {
		allInt, allReal, anyValue := true, true, false
		for _, row := range rows {
			v := strings.TrimSpace(row[i])
			if v == "" {
				continue // empty => NULL, doesn't constrain the type
			}
			anyValue = true
			if allInt {
				if _, err := strconv.ParseInt(v, 10, 64); err != nil {
					allInt = false
				}
			}
			if allReal {
				if _, err := strconv.ParseFloat(v, 64); err != nil {
					allReal = false
				}
			}
		}
		typ := "TEXT"
		switch {
		case anyValue && allInt:
			typ = "INTEGER"
		case anyValue && allReal:
			typ = "REAL"
		}
		cols[i] = Column{Name: h, Type: typ}
	}
	return cols
}

// coerce converts a string cell to the typed value to bind (or nil for NULL).
func coerce(v, typ string) interface{} {
	s := strings.TrimSpace(v)
	if s == "" {
		return nil
	}
	switch typ {
	case "INTEGER":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case "REAL":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return v
}

func createTable(tx *sql.Tx, table string, cols []Column) error {
	_, err := tx.Exec(createSQL(table, cols))
	return err
}

func createTableIfNotExists(tx *sql.Tx, table string, cols []Column) error {
	exists, err := tableExists(tx, table)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return createTable(tx, table, cols)
}

func createSQL(table string, cols []Column) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fmt.Sprintf("  %s %s", quoteIdent(c.Name), c.Type)
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n)", quoteIdent(table), strings.Join(parts, ",\n"))
}

func insertStmt(table string, cols []Column) string {
	names := make([]string, len(cols))
	ph := make([]string, len(cols))
	for i, c := range cols {
		names[i] = quoteIdent(c.Name)
		ph[i] = "?"
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(table), strings.Join(names, ", "), strings.Join(ph, ", "))
}

func tableExists(tx *sql.Tx, table string) (bool, error) {
	var name string
	err := tx.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
	).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// quoteIdent safely quotes a SQLite identifier.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// dedupeHeaders ensures non-empty, unique column names.
func dedupeHeaders(headers []string) []string {
	out := make([]string, len(headers))
	seen := map[string]int{}
	for i, h := range headers {
		name := strings.TrimSpace(h)
		if name == "" {
			name = fmt.Sprintf("col%d", i+1)
		}
		if n, ok := seen[name]; ok {
			seen[name] = n + 1
			name = fmt.Sprintf("%s_%d", name, n+1)
		} else {
			seen[name] = 1
		}
		out[i] = name
	}
	return out
}

// jsonScalar renders a JSON value as the string cell the inference path expects.
// Objects/arrays are kept as compact JSON text (TEXT column).
func jsonScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		if t {
			return "1"
		}
		return "0"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case string:
		return t
	default:
		return string(raw) // object/array: keep raw JSON
	}
}

func stableSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
