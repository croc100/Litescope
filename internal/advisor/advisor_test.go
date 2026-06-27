package advisor

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func build(t *testing.T, stmts ...string) string {
	t.Helper()
	path := t.TempDir() + "/a.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	db.Close()
	return path
}

func rules(r *Report) map[string]int {
	m := map[string]int{}
	for _, f := range r.Findings {
		m[f.Rule]++
	}
	return m
}

func TestAnalyze_FKWithoutIndex(t *testing.T) {
	path := build(t,
		"CREATE TABLE users (id INTEGER PRIMARY KEY)",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id))",
	)
	r, err := Analyze(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rules(r)["fk-no-index"] != 1 {
		t.Errorf("expected 1 fk-no-index finding, got %+v", r.Findings)
	}
	// The suggestion must be a runnable CREATE INDEX on the FK column.
	var found bool
	for _, f := range r.Findings {
		if f.Rule == "fk-no-index" && f.Suggestion != "" && f.Table == "orders" {
			found = true
		}
	}
	if !found {
		t.Errorf("fk-no-index finding missing suggestion/table")
	}
}

func TestAnalyze_FKIndexed_NoFinding(t *testing.T) {
	path := build(t,
		"CREATE TABLE users (id INTEGER PRIMARY KEY)",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id))",
		"CREATE INDEX idx_orders_user_id ON orders(user_id)",
	)
	r, _ := Analyze(path, nil)
	if rules(r)["fk-no-index"] != 0 {
		t.Errorf("indexed FK should not be flagged, got %+v", r.Findings)
	}
}

func TestAnalyze_FKCoveredByPKPrefix_NoFinding(t *testing.T) {
	// post_id is the leading column of the composite PK → already indexed.
	path := build(t,
		"CREATE TABLE posts (id INTEGER PRIMARY KEY)",
		"CREATE TABLE tags (post_id INTEGER REFERENCES posts(id), name TEXT, PRIMARY KEY(post_id, name))",
	)
	r, _ := Analyze(path, nil)
	if rules(r)["fk-no-index"] != 0 {
		t.Errorf("FK covered by PK prefix should not be flagged, got %+v", r.Findings)
	}
}

func TestAnalyze_RedundantIndex(t *testing.T) {
	path := build(t,
		"CREATE TABLE t (a INTEGER, b INTEGER, c INTEGER)",
		"CREATE INDEX idx_a ON t(a)",       // prefix of idx_ab → redundant
		"CREATE INDEX idx_ab ON t(a, b)",
	)
	r, _ := Analyze(path, nil)
	if rules(r)["redundant-index"] != 1 {
		t.Errorf("expected 1 redundant-index finding, got %+v", r.Findings)
	}
}

func TestAnalyze_UniqueIndexNotFlaggedRedundant(t *testing.T) {
	path := build(t,
		"CREATE TABLE t (a INTEGER, b INTEGER)",
		"CREATE UNIQUE INDEX idx_a ON t(a)",  // unique enforces a constraint — keep it
		"CREATE INDEX idx_ab ON t(a, b)",
	)
	r, _ := Analyze(path, nil)
	if rules(r)["redundant-index"] != 0 {
		t.Errorf("unique index must not be flagged redundant, got %+v", r.Findings)
	}
}

func TestAnalyze_FullScanQuery(t *testing.T) {
	path := build(t,
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, total INTEGER)",
	)
	r, _ := Analyze(path, []string{"SELECT * FROM orders WHERE total > 100"})
	if rules(r)["full-scan"] != 1 {
		t.Errorf("expected a full-scan finding, got %+v", r.Findings)
	}
}

func TestAnalyze_FullScanInfersRunnableIndex(t *testing.T) {
	path := build(t,
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER, total INTEGER, status TEXT)",
	)
	r, _ := Analyze(path, []string{
		"SELECT * FROM orders WHERE status = 'paid' AND total > 100",
	})
	var got *Finding
	for i := range r.Findings {
		if r.Findings[i].Rule == "full-scan" {
			got = &r.Findings[i]
		}
	}
	if got == nil {
		t.Fatalf("expected a full-scan finding, got %+v", r.Findings)
	}
	if got.Table != "orders" {
		t.Errorf("expected table=orders, got %q", got.Table)
	}
	// Equality column (status) must lead the composite index; the range
	// column (total) follows.
	want := `CREATE INDEX idx_orders_status_total ON "orders"(status, total);`
	if got.Suggestion != want {
		t.Errorf("inferred index mismatch:\n got: %s\nwant: %s", got.Suggestion, want)
	}
}

func TestAnalyze_FullScanSkipsAlreadyIndexedColumn(t *testing.T) {
	path := build(t,
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT)",
		"CREATE INDEX idx_status ON orders(status)",
	)
	// status is indexed but the planner may still scan for a non-sargable
	// predicate; the inference must not re-propose a status-led index.
	r, _ := Analyze(path, []string{"SELECT * FROM orders WHERE status LIKE '%x%'"})
	for _, f := range r.Findings {
		if f.Rule == "full-scan" && strings.HasPrefix(f.Suggestion, "CREATE INDEX") {
			t.Errorf("should not propose an index led by an already-indexed column: %s", f.Suggestion)
		}
	}
}

func TestAnalyze_IndexedQuery_NoScan(t *testing.T) {
	path := build(t,
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER)",
		"CREATE INDEX idx_user ON orders(user_id)",
	)
	r, _ := Analyze(path, []string{"SELECT * FROM orders WHERE user_id = 5"})
	if rules(r)["full-scan"] != 0 {
		t.Errorf("indexed query should not be a full scan, got %+v", r.Findings)
	}
}

func TestAnalyze_CleanDB(t *testing.T) {
	path := build(t, "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	r, _ := Analyze(path, nil)
	if len(r.Findings) != 0 {
		t.Errorf("clean DB should yield no findings, got %+v", r.Findings)
	}
}
