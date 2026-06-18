// Package advisor analyzes a SQLite database for performance problems that hurt
// in production — especially the ones AI-generated schemas ship by default:
// foreign keys with no index (a full scan on every join), redundant indexes,
// and, when queries are supplied, full table scans via EXPLAIN QUERY PLAN.
package advisor

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Finding is one performance issue with a concrete suggestion.
type Finding struct {
	Rule       string `json:"rule"`     // fk-no-index | redundant-index | full-scan
	Severity   string `json:"severity"` // warning | info
	Table      string `json:"table,omitempty"`
	Detail     string `json:"detail"`
	Suggestion string `json:"suggestion,omitempty"` // runnable SQL or guidance
}

// Report is the advisor's output for one database.
type Report struct {
	Path     string    `json:"path"`
	Findings []Finding `json:"findings"`
}

// Analyze inspects the schema for index problems and, for any supplied queries,
// flags full table scans. queries may be nil.
func Analyze(path string, queries []string) (*Report, error) {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()

	r := &Report{Path: path}

	tables, err := userTables(db)
	if err != nil {
		return nil, err
	}
	for _, t := range tables {
		idxs, err := indexColumns(db, t)
		if err != nil {
			return nil, err
		}
		r.Findings = append(r.Findings, fkFindings(db, t, idxs)...)
		r.Findings = append(r.Findings, redundantFindings(db, t, idxs)...)
	}

	for _, q := range queries {
		if f, ok := scanFinding(db, q); ok {
			r.Findings = append(r.Findings, f)
		}
	}

	return r, nil
}

// ── rule: foreign key with no covering index ────────────────────────────────

func fkFindings(db *sql.DB, table string, idxs []indexDef) []Finding {
	rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
	if err != nil {
		return nil
	}
	defer rows.Close()

	// Group FK columns by id, ordered by seq.
	type fk struct{ cols []string }
	fks := map[int]*fk{}
	var order []int
	for rows.Next() {
		var id, seq int
		var parent, from, to, onUpd, onDel, match sql.NullString
		if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpd, &onDel, &match); err != nil {
			return nil
		}
		if fks[id] == nil {
			fks[id] = &fk{}
			order = append(order, id)
		}
		fks[id].cols = append(fks[id].cols, from.String)
	}

	pk := pkColumns(db, table)
	var out []Finding
	for _, id := range order {
		cols := fks[id].cols
		if coveredBy(cols, idxs, pk) {
			continue
		}
		colList := strings.Join(cols, ", ")
		out = append(out, Finding{
			Rule:       "fk-no-index",
			Severity:   "warning",
			Table:      table,
			Detail:     fmt.Sprintf("foreign key on (%s) has no index — every join or cascade scans the whole table (SQLite does not auto-index FK columns)", colList),
			Suggestion: fmt.Sprintf("CREATE INDEX idx_%s_%s ON %q(%s);", table, strings.Join(cols, "_"), table, colList),
		})
	}
	return out
}

// coveredBy reports whether some index (or the primary key) has fkCols as a
// leading prefix.
func coveredBy(fkCols []string, idxs []indexDef, pk []string) bool {
	if isPrefix(fkCols, pk) {
		return true
	}
	for _, ix := range idxs {
		if isPrefix(fkCols, ix.cols) {
			return true
		}
	}
	return false
}

func isPrefix(want, have []string) bool {
	if len(want) == 0 || len(want) > len(have) {
		return false
	}
	for i := range want {
		if !strings.EqualFold(want[i], have[i]) {
			return false
		}
	}
	return true
}

// ── rule: redundant index ───────────────────────────────────────────────────

func redundantFindings(db *sql.DB, table string, idxs []indexDef) []Finding {
	var out []Finding
	for _, a := range idxs {
		if a.unique || a.origin != "c" {
			continue // keep unique/constraint indexes; only flag created ones
		}
		for _, b := range idxs {
			if a.name == b.name {
				continue
			}
			// a is redundant if its columns are a leading prefix of a longer index b.
			if len(a.cols) < len(b.cols) && isPrefix(a.cols, b.cols) {
				out = append(out, Finding{
					Rule:       "redundant-index",
					Severity:   "info",
					Table:      table,
					Detail:     fmt.Sprintf("index %s(%s) is a prefix of %s(%s) — likely redundant", a.name, strings.Join(a.cols, ", "), b.name, strings.Join(b.cols, ", ")),
					Suggestion: fmt.Sprintf("DROP INDEX %q;", a.name),
				})
				break
			}
		}
	}
	return out
}

// ── rule: full table scan from a query ──────────────────────────────────────

func scanFinding(db *sql.DB, query string) (Finding, bool) {
	rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
	if err != nil {
		return Finding{}, false
	}
	defer rows.Close()

	var scanned []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return Finding{}, false
		}
		// "SCAN <table>" is a full scan; "SEARCH ... USING INDEX" is fine.
		if strings.HasPrefix(detail, "SCAN ") && !strings.Contains(detail, "USING") {
			scanned = append(scanned, strings.TrimSpace(strings.TrimPrefix(detail, "SCAN")))
		}
	}
	if len(scanned) == 0 {
		return Finding{}, false
	}
	return Finding{
		Rule:       "full-scan",
		Severity:   "warning",
		Detail:     fmt.Sprintf("query does a full table scan of: %s", strings.Join(scanned, ", ")),
		Suggestion: fmt.Sprintf("add an index covering the WHERE/JOIN columns used in: %s", oneLine(query)),
	}, true
}

// ── SQLite metadata helpers ─────────────────────────────────────────────────

type indexDef struct {
	name   string
	unique bool
	origin string // c=created, u=unique constraint, pk=primary key
	cols   []string
}

func userTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func indexColumns(db *sql.DB, table string) ([]indexDef, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var idxs []indexDef
	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		idxs = append(idxs, indexDef{name: name, unique: unique == 1, origin: origin})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Fill columns for each index.
	for i := range idxs {
		idxs[i].cols = indexInfo(db, idxs[i].name)
	}
	return idxs, nil
}

func indexInfo(db *sql.DB, name string) []string {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_info(%q)", name))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var seqno, cid int
		var col sql.NullString
		if err := rows.Scan(&seqno, &cid, &col); err != nil {
			return cols
		}
		if col.Valid {
			cols = append(cols, col.String)
		}
	}
	return cols
}

// pkColumns returns the primary-key columns in order. A single INTEGER PRIMARY
// KEY is the rowid alias and is always indexed.
func pkColumns(db *sql.DB, table string) []string {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil
	}
	defer rows.Close()
	type pkcol struct {
		name string
		pos  int
	}
	var pks []pkcol
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil
		}
		if pk > 0 {
			pks = append(pks, pkcol{name: name, pos: pk})
		}
	}
	// order by pk position
	for i := 0; i < len(pks); i++ {
		for j := i + 1; j < len(pks); j++ {
			if pks[j].pos < pks[i].pos {
				pks[i], pks[j] = pks[j], pks[i]
			}
		}
	}
	out := make([]string, len(pks))
	for i, p := range pks {
		out[i] = p.name
	}
	return out
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		return s[:77] + "…"
	}
	return s
}
