// Package salvage recovers readable rows from a SQLite database that fails
// integrity_check and has no healthy backup to restore from — the case
// fleet.Recover can't handle. It mirrors the official sqlite3 shell's
// `.recover` command in pure Go: replay the schema from sqlite_master into a
// fresh database, then copy every row that can still be read out of each
// table, skipping over the specific rowids that live on corrupt pages
// instead of failing the whole table.
//
// modernc.org/sqlite (this project's driver) is cgo-free and doesn't expose
// SQLite's internal sqlite3_recover API or raw b-tree page access, so this
// package can't do the byte-level freelist walk the real .recover does.
// Instead it exploits the fact that a rowid-range query
// (`WHERE rowid BETWEEN a AND b`) only touches the b-tree pages covering that
// range: a range that errors gets bisected until the unreadable rowid(s) are
// isolated to single rows, and everything else in the table is still copied.
package salvage

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// TableResult is the salvage outcome for one table.
type TableResult struct {
	Table      string `json:"table"`
	RowsCopied int64  `json:"rows_copied"`
	RowsLost   int64  `json:"rows_lost"`       // rowids that couldn't be read (corrupt page)
	Error      string `json:"error,omitempty"` // fatal for this table (e.g. schema unreadable)
}

// Result is the full salvage outcome.
type Result struct {
	Source        string        `json:"source"`
	Output        string        `json:"output"`
	Tables        []TableResult `json:"tables"`
	SchemaLost    []string      `json:"schema_lost,omitempty"` // indexes/views/triggers that failed to replay
	OutputHealthy bool          `json:"output_healthy"`        // integrity_check on the recovered file
}

// TotalCopied sums rows_copied across all tables.
func (r *Result) TotalCopied() int64 {
	var n int64
	for _, t := range r.Tables {
		n += t.RowsCopied
	}
	return n
}

// TotalLost sums rows_lost across all tables.
func (r *Result) TotalLost() int64 {
	var n int64
	for _, t := range r.Tables {
		n += t.RowsLost
	}
	return n
}

// Recover reads what it can out of a corrupt database at srcPath and writes a
// fresh, healthy database to outPath. outPath must not already exist. Tables
// are recreated from sqlite_master's stored SQL and populated by rowid-range
// bisection; indexes/views/triggers are replayed afterward on a best-effort
// basis and reported in SchemaLost if they fail.
func Recover(srcPath, outPath string) (*Result, error) {
	if _, err := os.Stat(outPath); err == nil {
		return nil, fmt.Errorf("output already exists: %s", outPath)
	}

	src, err := sql.Open("sqlite", srcPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open source %s: %w", srcPath, err)
	}
	defer src.Close()

	schema, err := readSchema(src)
	if err != nil {
		return nil, fmt.Errorf("source schema unreadable, cannot salvage: %w", err)
	}

	dst, err := sql.Open("sqlite", outPath)
	if err != nil {
		return nil, fmt.Errorf("create output %s: %w", outPath, err)
	}
	defer dst.Close()
	if _, err := dst.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		return nil, err
	}

	res := &Result{Source: srcPath, Output: outPath}

	for _, obj := range schema {
		if obj.typ != "table" {
			continue
		}
		if _, err := dst.Exec(obj.sql); err != nil {
			res.Tables = append(res.Tables, TableResult{Table: obj.name, Error: "schema replay failed: " + err.Error()})
			continue
		}
		res.Tables = append(res.Tables, salvageTable(src, dst, obj.name))
	}

	for _, obj := range schema {
		if obj.typ == "table" {
			continue
		}
		if _, err := dst.Exec(obj.sql); err != nil {
			res.SchemaLost = append(res.SchemaLost, fmt.Sprintf("%s %s: %v", obj.typ, obj.name, err))
		}
	}

	var quick string
	if err := dst.QueryRow("PRAGMA quick_check").Scan(&quick); err == nil {
		res.OutputHealthy = quick == "ok"
	}

	return res, nil
}

type schemaObj struct {
	typ, name, sql string
}

// readSchema pulls every non-null CREATE statement from sqlite_master, tables
// first so views/triggers/indexes replay after the tables they depend on
// exist.
func readSchema(db *sql.DB) ([]schemaObj, error) {
	rows, err := db.Query(`SELECT type, name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
		ORDER BY (type != 'table'), rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []schemaObj
	for rows.Next() {
		var o schemaObj
		if err := rows.Scan(&o.typ, &o.name, &o.sql); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// salvageTable copies every row of table it can read from src into dst. It
// tries a rowid-bounded range scan first (works for ordinary rowid tables and
// isolates corrupt pages to their exact rowids); if the table has no rowid
// range (WITHOUT ROWID, or empty), it falls back to one unbounded scan.
func salvageTable(src, dst *sql.DB, table string) TableResult {
	res := TableResult{Table: table}

	cols, err := columnNames(src, table)
	if err != nil {
		res.Error = "columns unreadable: " + err.Error()
		return res
	}

	var lo, hi sql.NullInt64
	rangeErr := src.QueryRow(fmt.Sprintf("SELECT MIN(rowid), MAX(rowid) FROM %q", table)).Scan(&lo, &hi)
	if rangeErr != nil || !lo.Valid {
		n, err := copyRange(src, dst, table, cols, "")
		res.RowsCopied = n
		if err != nil {
			res.RowsLost = 1 // at least one row in this table is unreadable; exact count unknown without rowid access
		}
		return res
	}

	copied, lost := bisectCopy(src, dst, table, cols, lo.Int64, hi.Int64)
	res.RowsCopied, res.RowsLost = copied, lost
	return res
}

func columnNames(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("no columns found for %q", table)
	}
	return cols, nil
}

// bisectCopy copies rows with rowid in [lo, hi]. A range that fails to scan
// is split in half and retried; a single unreadable rowid is counted as lost.
// Ranges are re-tried with INSERT OR IGNORE so a partially-successful range
// that later gets bisected doesn't double-count already-copied rows.
func bisectCopy(src, dst *sql.DB, table string, cols []string, lo, hi int64) (copied, lost int64) {
	if lo > hi {
		return 0, 0
	}
	where := fmt.Sprintf("rowid BETWEEN %d AND %d", lo, hi)
	n, err := copyRange(src, dst, table, cols, where)
	if err == nil {
		return n, 0
	}
	if lo == hi {
		return 0, 1
	}
	mid := lo + (hi-lo)/2
	c1, l1 := bisectCopy(src, dst, table, cols, lo, mid)
	c2, l2 := bisectCopy(src, dst, table, cols, mid+1, hi)
	return c1 + c2, l1 + l2
}

// copyRange reads rowid+cols from src under the given WHERE clause (empty =
// no filter) and inserts them into dst with INSERT OR IGNORE, so retried
// ranges from bisection are idempotent. Returns the number of rows inserted
// and the first error encountered reading the result set, if any — rows read
// before the error are still copied.
func copyRange(src, dst *sql.DB, table string, cols []string, where string) (int64, error) {
	query := fmt.Sprintf("SELECT rowid, %s FROM %q", quoteList(cols), table)
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := src.Query(query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	placeholders := make([]string, len(cols)+1)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insert := fmt.Sprintf("INSERT OR IGNORE INTO %q (rowid, %s) VALUES (%s)",
		table, quoteList(cols), strings.Join(placeholders, ", "))

	dest := make([]interface{}, len(cols)+1)
	vals := make([]interface{}, len(cols)+1)
	for i := range dest {
		dest[i] = &vals[i]
	}

	var n int64
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return n, err
		}
		if _, err := dst.Exec(insert, vals...); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func quoteList(cols []string) string {
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = fmt.Sprintf("%q", c)
	}
	return strings.Join(quoted, ", ")
}
