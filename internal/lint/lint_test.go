package lint

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newDB(t *testing.T, stmts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return path
}

func rules(r *Report) map[string]int {
	m := map[string]int{}
	for _, f := range r.Findings {
		m[f.Rule]++
	}
	return m
}

func TestCleanSchemaHasNoWarnings(t *testing.T) {
	// INTEGER PK, typed columns, STRICT — should raise no warnings.
	path := newDB(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL) STRICT`)
	r, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Findings {
		if f.Severity == SevWarning {
			t.Errorf("unexpected warning: %s (%s)", f.Rule, f.Detail)
		}
	}
}

func TestDetectsAntiPatterns(t *testing.T) {
	path := newDB(t,
		`CREATE TABLE logs (msg, level TEXT)`,                               // no-primary-key + untyped-column + not-strict
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, e TEXT)`, // autoincrement + not-strict
		`CREATE TABLE sess (uuid TEXT PRIMARY KEY, d TEXT)`,                 // non-integer-pk + not-strict
	)
	r, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	got := rules(r)
	for _, want := range []string{"no-primary-key", "untyped-column", "not-strict", "autoincrement-overhead", "non-integer-pk"} {
		if got[want] == 0 {
			t.Errorf("expected rule %q to fire, findings: %+v", want, r.Findings)
		}
	}
}

func TestUntypedColumnIsWarning(t *testing.T) {
	path := newDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, x)`)
	r, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.Findings {
		if f.Rule == "untyped-column" {
			found = true
			if f.Severity != SevWarning {
				t.Errorf("untyped-column should be warning, got %s", f.Severity)
			}
		}
	}
	if !found {
		t.Error("expected untyped-column finding")
	}
}
