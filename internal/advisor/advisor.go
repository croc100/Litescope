// Package advisor analyzes a SQLite database for performance problems that hurt
// in production — especially the ones AI-generated schemas ship by default:
// foreign keys with no index (a full scan on every join), redundant indexes,
// and, when queries are supplied, full table scans via EXPLAIN QUERY PLAN.
package advisor

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// Finding is one performance issue with a concrete suggestion.
type Finding struct {
	Rule string `json:"rule"` // fk-no-index | redundant-index | full-scan | expr-index |
	// partial-index | pragma-tuning
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
		r.Findings = append(r.Findings, scanFindings(db, q)...)
		r.Findings = append(r.Findings, exprFindings(db, q)...)
		r.Findings = append(r.Findings, partialFindings(db, q)...)
	}

	r.Findings = append(r.Findings, pragmaFindings(db, path)...)

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

// scanFindings runs EXPLAIN QUERY PLAN for one query and emits a full-scan
// finding per table the planner scans without an index. When the predicate
// columns for that table can be inferred from the query, the suggestion is a
// runnable CREATE INDEX statement; otherwise it falls back to guidance.
func scanFindings(db *sql.DB, query string) []Finding {
	rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var scanned []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return nil
		}
		// "SCAN <table>" is a full scan; "SEARCH ... USING INDEX" is fine.
		if strings.HasPrefix(detail, "SCAN ") && !strings.Contains(detail, "USING") {
			scanned = append(scanned, scanTable(detail))
		}
	}
	if err := rows.Err(); err != nil || len(scanned) == 0 {
		return nil
	}

	var out []Finding
	for _, table := range scanned {
		f := Finding{
			Rule:     "full-scan",
			Severity: "warning",
			Table:    table,
			Detail:   fmt.Sprintf("query does a full table scan of %q: %s", table, oneLine(query)),
		}
		if cols := inferIndexColumns(db, table, query); len(cols) > 0 {
			covering := append([]string{}, cols...)
			extra := selectColumns(db, table, query, cols)
			covering = append(covering, extra...)
			f.Suggestion = fmt.Sprintf("CREATE INDEX idx_%s_%s ON %q(%s);",
				table, strings.Join(covering, "_"), table, strings.Join(covering, ", "))
			if len(extra) > 0 {
				f.Detail += fmt.Sprintf(" — widening the index with %s makes it covering (no row lookup needed)", strings.Join(extra, ", "))
			}
		} else {
			f.Suggestion = "add an index covering the WHERE/JOIN columns used in this query"
		}
		out = append(out, f)
	}
	return out
}

// scanTable extracts the table name from an EXPLAIN QUERY PLAN "SCAN" line,
// dropping any trailing "AS alias" or "USING ..." qualifiers.
func scanTable(detail string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(detail, "SCAN"))
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return rest
	}
	return fields[0]
}

// identTok matches a column/table identifier, optionally table-qualified.
var identTok = regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*\.)?([A-Za-z_][A-Za-z0-9_]*)\s*(=|<|>|<=|>=|<>|!=|\bIN\b|\bLIKE\b|\bBETWEEN\b|\bIS\b)`)

// inferIndexColumns parses the query's WHERE / JOIN ... ON predicates and
// returns the columns of `table` that are filtered on, equality columns first
// (the leftmost-prefix rule for composite indexes). It is deliberately
// conservative: it only proposes columns that actually exist on the table and
// are already not the leftmost column of an existing index.
func inferIndexColumns(db *sql.DB, table, query string) []string {
	cols := tableColumnSet(db, table)
	if len(cols) == 0 {
		return nil
	}
	covered := indexedLeftmost(db, table)

	region := predicateRegion(query)
	var eq, rng []string
	seen := map[string]bool{}
	for _, m := range identTok.FindAllStringSubmatch(region, -1) {
		col := m[1]
		op := strings.ToUpper(strings.TrimSpace(m[2]))
		if !cols[col] || seen[col] || covered[strings.ToLower(col)] {
			continue
		}
		seen[col] = true
		if op == "=" || op == "IN" || op == "IS" {
			eq = append(eq, col)
		} else {
			rng = append(rng, col)
		}
	}
	// Equality columns lead; a single range column can follow usefully.
	out := eq
	if len(rng) > 0 {
		out = append(out, rng[0])
	}
	return out
}

// predicateRegion returns the slice of the query that carries filter
// predicates: everything from the first WHERE / ON keyword onward, stopped
// before clauses that don't constrain rows (GROUP/ORDER/LIMIT).
func predicateRegion(query string) string {
	q := " " + strings.Join(strings.Fields(query), " ") + " "
	lower := strings.ToLower(q)
	start := len(q)
	for _, kw := range []string{" where ", " on "} {
		if i := strings.Index(lower, kw); i >= 0 && i < start {
			start = i
		}
	}
	if start == len(q) {
		return ""
	}
	region := q[start:]
	rl := strings.ToLower(region)
	for _, kw := range []string{" group by ", " order by ", " limit ", " having "} {
		if i := strings.Index(rl, kw); i >= 0 {
			region = region[:i]
			rl = rl[:i]
		}
	}
	return region
}

// tableColumnSet returns the set of column names on a table.
func tableColumnSet(db *sql.DB, table string) map[string]bool {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return out
		}
		out[name] = true
	}
	return out
}

// indexedLeftmost returns the set of columns (lowercased) that are already the
// leftmost column of some index or the primary key — adding another index led
// by them would be redundant.
func indexedLeftmost(db *sql.DB, table string) map[string]bool {
	out := map[string]bool{}
	if pks := pkColumns(db, table); len(pks) > 0 {
		out[strings.ToLower(pks[0])] = true
	}
	idxs, err := indexColumns(db, table)
	if err != nil {
		return out
	}
	for _, ix := range idxs {
		if len(ix.cols) > 0 {
			out[strings.ToLower(ix.cols[0])] = true
		}
	}
	return out
}

// selectColumns extracts plain identifier columns from the query's SELECT
// list (skipping "*", expressions, and aliases) that belong to table and
// aren't already in lead, so they can be appended to a query-derived index to
// make it covering. Deliberately conservative: a select list with more than 6
// items or anything that isn't a simple (optionally qualified) identifier
// yields no suggestion, since guessing wrong there is worse than staying
// silent.
func selectColumns(db *sql.DB, table, query string, lead []string) []string {
	cols := tableColumnSet(db, table)
	if len(cols) == 0 {
		return nil
	}
	q := " " + strings.Join(strings.Fields(query), " ") + " "
	lower := strings.ToLower(q)
	si := strings.Index(lower, "select ")
	fi := strings.Index(lower, " from ")
	if si < 0 || fi < 0 || fi <= si {
		return nil
	}
	list := q[si+len("select ") : fi]
	if strings.Contains(list, "(") || strings.TrimSpace(list) == "*" {
		return nil // aggregates/expressions/star — too risky to guess
	}
	parts := strings.Split(list, ",")
	if len(parts) > 6 {
		return nil
	}
	leadSet := map[string]bool{}
	for _, c := range lead {
		leadSet[strings.ToLower(c)] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if !identOnly.MatchString(name) || !cols[name] {
			return nil // anything unexpected in the list — bail out entirely
		}
		key := strings.ToLower(name)
		if leadSet[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

var identOnly = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ── rule: expression index ──────────────────────────────────────────────────

// exprPred matches a single-argument function call compared against a value,
// e.g. lower(email) = ? or date(created_at) >= ?.
var exprPred = regexp.MustCompile(`(?i)\b([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*(=|<|>|<=|>=|<>|!=|\bIN\b|\bLIKE\b)`)

// exprFindings flags predicates that filter on a function of a column
// (lower(email) = ..., date(created_at) = ...). SQLite can't use a plain
// index for these — only an expression index on the same function call.
func exprFindings(db *sql.DB, query string) []Finding {
	tables, err := userTables(db)
	if err != nil {
		return nil
	}
	region := predicateRegion(query)
	var out []Finding
	seen := map[string]bool{}
	for _, m := range exprPred.FindAllStringSubmatch(region, -1) {
		fn, col := strings.ToLower(m[1]), m[2]
		for _, table := range tables {
			if !tableColumnSet(db, table)[col] {
				continue
			}
			key := table + "." + fn + "(" + strings.ToLower(col) + ")"
			if seen[key] || hasExprIndex(db, table, fn, col) {
				continue
			}
			seen[key] = true
			out = append(out, Finding{
				Rule:     "expr-index",
				Severity: "warning",
				Table:    table,
				Detail: fmt.Sprintf("query filters on %s(%s) — a plain index on %s can't be used for this predicate",
					fn, col, col),
				Suggestion: fmt.Sprintf("CREATE INDEX idx_%s_%s_%s ON %q(%s(%s));", table, fn, col, table, fn, col),
			})
			break
		}
	}
	return out
}

// hasExprIndex reports whether table already has an index whose leading term
// is exactly fn(col). PRAGMA index_info reports a NULL column name for
// expression terms, so exact-expression matching isn't available via pragmas
// alone; this checks sqlite_master's stored index SQL as a best effort.
func hasExprIndex(db *sql.DB, table, fn, col string) bool {
	rows, err := db.Query(`SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	needle := strings.ToLower(fn) + "(" + strings.ToLower(col) + ")"
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(strings.ReplaceAll(s, " ", "")), strings.ReplaceAll(needle, " ", "")) {
			return true
		}
	}
	return false
}

// ── rule: partial index ─────────────────────────────────────────────────────

// eqLiteral matches "col = literal" where literal is a quoted string or a
// number, so selectivity can be measured directly against the table.
var eqLiteral = regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*\.)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*('(?:[^']|'')*'|-?\d+(?:\.\d+)?)`)

// partialFindings flags equality filters on a literal value that only match a
// small slice of the table (e.g. status = 'archived' on a mostly-active
// table). A partial index restricted to that value is far smaller and
// cheaper to maintain than a full index over the whole column.
func partialFindings(db *sql.DB, query string) []Finding {
	tables, err := userTables(db)
	if err != nil {
		return nil
	}
	region := predicateRegion(query)
	var out []Finding
	seen := map[string]bool{}
	for _, m := range eqLiteral.FindAllStringSubmatch(region, -1) {
		col, lit := m[1], m[2]
		for _, table := range tables {
			cols := tableColumnSet(db, table)
			if !cols[col] {
				continue
			}
			key := table + "." + col + "=" + lit
			if seen[key] {
				continue
			}
			seen[key] = true
			total := scalarCount(db, fmt.Sprintf("SELECT COUNT(*) FROM %q", table))
			if total < 1000 {
				continue // too small to matter
			}
			matched := scalarCount(db, fmt.Sprintf("SELECT COUNT(*) FROM %q WHERE %q = %s", table, col, lit))
			if matched <= 0 {
				continue
			}
			ratio := float64(matched) / float64(total)
			if ratio > 0.2 {
				continue // not selective enough to justify a partial index
			}
			out = append(out, Finding{
				Rule:     "partial-index",
				Severity: "info",
				Table:    table,
				Detail: fmt.Sprintf("%q = %s matches %.1f%% of rows in %q — a full index over the column indexes the other %.0f%% for nothing",
					col, lit, ratio*100, table, (1-ratio)*100),
				Suggestion: fmt.Sprintf("CREATE INDEX idx_%s_%s_partial ON %q(%s) WHERE %s = %s;", table, col, table, col, col, lit),
			})
			break
		}
	}
	return out
}

func scalarCount(db *sql.DB, query string) int64 {
	var n int64
	if err := db.QueryRow(query).Scan(&n); err != nil {
		return -1
	}
	return n
}

// ── rule: PRAGMA tuning ──────────────────────────────────────────────────────

// pragmaFindings recommends cache_size/mmap_size tuning for databases large
// enough that SQLite's tiny default cache (2MB) causes repeated disk reads
// for pages that would otherwise stay hot in memory. These are guidance-only:
// cache_size and mmap_size are per-connection PRAGMAs, not stored in the
// database file, so there's no SQL for autopilot to apply — the caller's
// connection setup needs them.
func pragmaFindings(db *sql.DB, path string) []Finding {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	const largeDB = 256 * 1024 * 1024 // 256MB
	if info.Size() < largeDB {
		return nil
	}

	var cacheSize int64
	_ = db.QueryRow("PRAGMA cache_size").Scan(&cacheSize)
	// Positive cache_size is a page count at the default page_size (usually
	// 4096B); negative is a KB budget. Either way, SQLite's built-in default
	// (-2000, i.e. ~2MB) is far too small for a multi-hundred-MB database.
	if cacheSize != -2000 && cacheSize != 2000 {
		return nil // already tuned away from the default
	}

	sizeMB := info.Size() / (1024 * 1024)
	return []Finding{{
		Rule:     "pragma-tuning",
		Severity: "info",
		Detail: fmt.Sprintf("database is %dMB but the connection is running SQLite's default ~2MB page cache — "+
			"most reads miss the cache and hit disk", sizeMB),
		Suggestion: "PRAGMA cache_size = -64000; -- 64MB, set per connection at startup\n" +
			"PRAGMA mmap_size = 268435456; -- let the OS page cache serve reads beyond that",
	}}
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
