// Package dump produces a portable SQL dump of a local SQLite database —
// the schema as CREATE statements plus the data as INSERTs — equivalent to the
// sqlite3 shell's ".dump" command. The output is a standalone .sql file that
// recreates the database when fed back into sqlite3 (or litescope migrate).
package dump

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	_ "modernc.org/sqlite"
)

// Options controls what the dump includes.
type Options struct {
	// SchemaOnly emits only DDL (CREATE statements), no INSERTs.
	SchemaOnly bool
	// DataOnly emits only INSERTs, no schema.
	DataOnly bool
	// Table, when non-empty, restricts the dump to a single table (and its data).
	Table string
}

// object is a row from sqlite_master.
type object struct {
	objType string // "table", "index", "trigger", "view"
	name    string
	tblName string
	sql     string
}

// Dump writes a SQL dump of the database at path to w.
func Dump(path string, w io.Writer, opts Options) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()

	objs, err := loadObjects(db, opts.Table)
	if err != nil {
		return err
	}

	bw := &errWriter{w: w}
	bw.writeString("PRAGMA foreign_keys=OFF;\n")
	bw.writeString("BEGIN TRANSACTION;\n")

	// Tables first (schema + data), then indexes/triggers/views so that data
	// loads before secondary objects reference it — mirrors sqlite3 .dump.
	for _, o := range objs {
		if o.objType != "table" {
			continue
		}
		if !opts.DataOnly && o.sql != "" {
			bw.writeString(o.sql)
			bw.writeString(";\n")
		}
		if !opts.SchemaOnly {
			if err := dumpTableData(db, o.name, bw); err != nil {
				return err
			}
		}
	}

	if !opts.DataOnly {
		for _, o := range objs {
			if o.objType == "table" || o.sql == "" {
				continue
			}
			bw.writeString(o.sql)
			bw.writeString(";\n")
		}
	}

	bw.writeString("COMMIT;\n")
	return bw.err
}

func loadObjects(db *sql.DB, table string) ([]object, error) {
	q := `SELECT type, name, tbl_name, COALESCE(sql, '')
	      FROM sqlite_master
	      WHERE name NOT LIKE 'sqlite_%'`
	args := []any{}
	if table != "" {
		q += ` AND (name = ? OR tbl_name = ?)`
		args = append(args, table, table)
	}
	// Tables ordered first, then the rest; stable by name within a type group.
	q += ` ORDER BY (type='table') DESC, name`

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objs []object
	for rows.Next() {
		var o object
		if err := rows.Scan(&o.objType, &o.name, &o.tblName, &o.sql); err != nil {
			return nil, err
		}
		objs = append(objs, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if table != "" && len(objs) == 0 {
		return nil, fmt.Errorf("no such table: %s", table)
	}
	return objs, nil
}

func dumpTableData(db *sql.DB, table string, bw *errWriter) error {
	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %s", quoteIdent(table)))
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	prefix := fmt.Sprintf("INSERT INTO %s VALUES(", quoteIdent(table))
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		bw.writeString(prefix)
		for i, v := range vals {
			if i > 0 {
				bw.writeString(",")
			}
			bw.writeString(sqlLiteral(v))
		}
		bw.writeString(");\n")
	}
	return rows.Err()
}

// sqlLiteral renders a scanned value as a SQLite SQL literal.
func sqlLiteral(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%g", t)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case []byte:
		return "X'" + hexEncode(t) + "'"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	default:
		return "'" + strings.ReplaceAll(fmt.Sprintf("%v", t), "'", "''") + "'"
	}
}

const hexDigits = "0123456789ABCDEF"

func hexEncode(b []byte) string {
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hexDigits[c>>4]
		out[i*2+1] = hexDigits[c&0x0f]
	}
	return string(out)
}

// quoteIdent double-quotes an identifier for use in a query.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// errWriter accumulates the first write error so callers can write a sequence of
// strings and check once at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) writeString(s string) {
	if e.err != nil {
		return
	}
	_, e.err = io.WriteString(e.w, s)
}
