// Package exporter is the data-out half of Litescope: it streams the result of
// a read-only query (or a whole table) to CSV / TSV / JSON. Together with the
// importer it completes the spreadsheet round-trip — import, inspect/fix, export.
package exporter

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Export runs query against db (read-only) and writes the rows to w in the
// given format ("csv" | "tsv" | "json"). It returns the number of rows written.
func Export(db *sql.DB, query, format string, w io.Writer) (int64, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return 0, fmt.Errorf("empty query")
	}
	if !isReadOnly(q) {
		return 0, fmt.Errorf("only read queries (SELECT / WITH) can be exported")
	}

	rows, err := db.Query(q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	switch strings.ToLower(format) {
	case "json":
		return writeJSON(w, rows, cols)
	case "tsv":
		return writeDelimited(w, rows, cols, '\t')
	case "csv", "":
		return writeDelimited(w, rows, cols, ',')
	default:
		return 0, fmt.Errorf("unknown format %q (use csv, tsv, or json)", format)
	}
}

func writeDelimited(w io.Writer, rows *sql.Rows, cols []string, comma rune) (int64, error) {
	cw := csv.NewWriter(w)
	cw.Comma = comma
	if err := cw.Write(cols); err != nil {
		return 0, err
	}
	var n int64
	for rows.Next() {
		vals, err := scanStrings(rows, len(cols))
		if err != nil {
			return n, err
		}
		if err := cw.Write(vals); err != nil {
			return n, err
		}
		n++
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return n, err
	}
	return n, rows.Err()
}

func writeJSON(w io.Writer, rows *sql.Rows, cols []string) (int64, error) {
	if _, err := io.WriteString(w, "[\n"); err != nil {
		return 0, err
	}
	enc := json.NewEncoder(w)
	var n int64
	for rows.Next() {
		m, err := scanMap(rows, cols)
		if err != nil {
			return n, err
		}
		if n > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return n, err
			}
		}
		if err := enc.Encode(m); err != nil { // Encode appends a newline
			return n, err
		}
		n++
	}
	if _, err := io.WriteString(w, "]\n"); err != nil {
		return n, err
	}
	return n, rows.Err()
}

// scanStrings renders each column as a string; NULL becomes "".
func scanStrings(rows *sql.Rows, n int) ([]string, error) {
	raw := make([]interface{}, n)
	ptrs := make([]interface{}, n)
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]string, n)
	for i, v := range raw {
		out[i] = cellString(v)
	}
	return out, nil
}

// scanMap renders a row as an ordered-key map with native JSON types; NULL→null.
func scanMap(rows *sql.Rows, cols []string) (map[string]interface{}, error) {
	raw := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	m := make(map[string]interface{}, len(cols))
	for i, c := range cols {
		m[c] = cellValue(raw[i])
	}
	return m, nil
}

func cellString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", t)
	}
}

func cellValue(v interface{}) interface{} {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// isReadOnly allows only SELECT / WITH statements (defense in depth; the caller
// also opens the database read-only).
func isReadOnly(q string) bool {
	u := strings.ToUpper(strings.TrimSpace(q))
	return strings.HasPrefix(u, "SELECT") || strings.HasPrefix(u, "WITH")
}
