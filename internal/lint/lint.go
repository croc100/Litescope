// Package lint flags SQLite schema *design* anti-patterns — the structural
// mistakes that ship by default, especially in hand-written or AI-generated
// schemas. It is the design counterpart to the advisor (which covers
// performance/index problems): lint never looks at data or queries, only at the
// shape of the schema, so it is fast and deterministic and safe for CI.
package lint

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// Severity of a lint finding.
type Severity string

const (
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

// Finding is one schema design issue.
type Finding struct {
	Rule       string   `json:"rule"`
	Severity   Severity `json:"severity"`
	Table      string   `json:"table,omitempty"`
	Detail     string   `json:"detail"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// Report is the lint output for one database.
type Report struct {
	Path     string    `json:"path"`
	Findings []Finding `json:"findings"`
}

type tableInfo struct {
	name    string
	sql     string
	cols    []colInfo
	hasPK   bool
	pkCount int
	pkType  string // declared type of the single-column PK, upper-cased
}

type colInfo struct {
	name    string
	typ     string
	notNull bool
	pk      int
}

var reAutoinc = regexp.MustCompile(`(?i)\bAUTOINCREMENT\b`)
var reStrict = regexp.MustCompile(`(?i)\bSTRICT\b`)

// Analyze opens a local SQLite file and returns design findings.
func Analyze(path string) (*Report, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()

	tables, err := loadTables(db)
	if err != nil {
		return nil, err
	}

	r := &Report{Path: path}
	for _, t := range tables {
		r.Findings = append(r.Findings, lintTable(t)...)
	}
	return r, nil
}

func loadTables(db *sql.DB) ([]tableInfo, error) {
	rows, err := db.Query(`SELECT name, COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []tableInfo
	for rows.Next() {
		var t tableInfo
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range tables {
		if err := loadColumns(db, &tables[i]); err != nil {
			return nil, err
		}
	}
	return tables, nil
}

func loadColumns(db *sql.DB, t *tableInfo) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", t.name))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		c := colInfo{name: name, typ: typ, notNull: notNull == 1, pk: pk}
		t.cols = append(t.cols, c)
		if pk > 0 {
			t.hasPK = true
			t.pkCount++
			t.pkType = strings.ToUpper(strings.TrimSpace(typ))
		}
	}
	return rows.Err()
}

func lintTable(t tableInfo) []Finding {
	var out []Finding

	// no-primary-key: a table without a PRIMARY KEY has no stable row identity;
	// UPDATE/DELETE of a specific row and replication both become unreliable.
	if !t.hasPK {
		out = append(out, Finding{
			Rule: "no-primary-key", Severity: SevWarning, Table: t.name,
			Detail:     "table has no PRIMARY KEY — rows have no stable identity",
			Suggestion: "add an INTEGER PRIMARY KEY column (becomes the rowid alias)",
		})
	}

	// non-integer-pk: a single-column PK that is not INTEGER does not alias the
	// rowid, so lookups carry an extra B-tree indirection.
	if t.hasPK && t.pkCount == 1 && t.pkType != "" && t.pkType != "INTEGER" {
		out = append(out, Finding{
			Rule: "non-integer-pk", Severity: SevInfo, Table: t.name,
			Detail:     fmt.Sprintf("single-column PRIMARY KEY is %s, not INTEGER — it does not alias rowid (extra indirection)", t.pkType),
			Suggestion: "use INTEGER PRIMARY KEY for the rowid alias, or declare WITHOUT ROWID if intentional",
		})
	}

	// autoincrement: AUTOINCREMENT adds a sqlite_sequence row and overhead, and
	// is almost never needed — plain INTEGER PRIMARY KEY already never reuses an
	// id within a session and monotonically increases in practice.
	if reAutoinc.MatchString(t.sql) {
		out = append(out, Finding{
			Rule: "autoincrement-overhead", Severity: SevInfo, Table: t.name,
			Detail:     "uses AUTOINCREMENT — adds overhead and a sqlite_sequence row, rarely needed",
			Suggestion: "drop AUTOINCREMENT unless you require ids to never be reused after deletion",
		})
	}

	// not-strict: without STRICT, declared column types are advisory affinities —
	// a TEXT column will silently accept an integer. STRICT (SQLite 3.37+)
	// enforces types and catches a whole class of data-corruption bugs.
	if t.sql != "" && !reStrict.MatchString(t.sql) {
		out = append(out, Finding{
			Rule: "not-strict", Severity: SevInfo, Table: t.name,
			Detail:     "not a STRICT table — column types are advisory, mismatched values are silently coerced",
			Suggestion: "declare the table STRICT (CREATE TABLE ... ) STRICT; (SQLite 3.37+)",
		})
	}

	// untyped-column: a column with no declared type gets BLOB affinity and
	// accepts anything — usually an oversight.
	for _, c := range t.cols {
		if strings.TrimSpace(c.typ) == "" {
			out = append(out, Finding{
				Rule: "untyped-column", Severity: SevWarning, Table: t.name,
				Detail:     fmt.Sprintf("column %q has no declared type (BLOB affinity — accepts any value)", c.name),
				Suggestion: fmt.Sprintf("give %q an explicit type (INTEGER, TEXT, REAL, ...)", c.name),
			})
		}
	}

	return out
}
