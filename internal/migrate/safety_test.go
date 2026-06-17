package migrate

import (
	"database/sql"
	"testing"
	"time"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
)

func TestEstimateRebuildLock(t *testing.T) {
	cases := []struct {
		name    string
		rows    int64
		indexes int
		want    time.Duration
	}{
		{"unknown rows", -1, 0, -1},
		{"empty table", 0, 0, 0},
		{"empty table with indexes", 0, 3, 0},
		{"5k no indexes", 5000, 0, 40 * time.Millisecond},   // 5 * 8ms
		{"5k two indexes", 5000, 2, 80 * time.Millisecond},  // 5 * 8 * (1 + 2*0.5)
		{"1M no indexes", 1_000_000, 0, 8000 * time.Millisecond},
		{"tiny rounds up to 1ms", 1, 0, 1 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateRebuildLock(c.rows, c.indexes)
			if got != c.want {
				t.Errorf("EstimateRebuildLock(%d, %d) = %v, want %v", c.rows, c.indexes, got, c.want)
			}
		})
	}
}

// buildDB creates a SQLite file at path and runs the given statements.
func buildDB(t *testing.T, path string, stmts ...string) {
	t.Helper()
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
}

// findOp returns the first operation whose table matches, or fails.
func findOp(t *testing.T, ops []Operation, table string) Operation {
	t.Helper()
	for _, op := range ops {
		if op.Table == table {
			return op
		}
	}
	t.Fatalf("no operation found for table %q (have %d ops)", table, len(ops))
	return Operation{}
}

func TestAnalyzeAll_Classification(t *testing.T) {
	dir := t.TempDir()
	old := dir + "/old.db"
	buildDB(t, old,
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, legacy INTEGER, kind TEXT)",
		"CREATE INDEX idx_users_name ON users(name)",
		"INSERT INTO users (name, legacy, kind) VALUES ('a', 1, 'x'), ('b', 2, 'y')",
		"CREATE TABLE sessions (id INTEGER PRIMARY KEY, tok TEXT)",
		"INSERT INTO sessions (tok) VALUES ('t1')",
		"CREATE TABLE addonly (id INTEGER PRIMARY KEY, a TEXT)",
		"INSERT INTO addonly (a) VALUES ('z')",
		"CREATE TABLE typed (id INTEGER PRIMARY KEY, val INTEGER)",
		"INSERT INTO typed (val) VALUES (1)",
	)

	col := func(name, typ string, notNull bool, def string) schema.Column {
		return schema.Column{Name: name, Type: typ, NotNull: notNull, Default: def}
	}

	d := &diff.Result{Schema: []diff.TableDiff{
		// New table → safe
		{Name: "audit", Added: true, AddedColumns: []schema.Column{col("id", "INTEGER", false, "")}},
		// Dropped table → destructive
		{Name: "sessions", Removed: true},
		// Drop column → destructive (rebuild + data loss)
		{Name: "users", RemovedColumns: []schema.Column{col("legacy", "INTEGER", false, "")}},
		// Pure ADD COLUMN → safe, in-place
		{Name: "addonly", AddedColumns: []schema.Column{col("b", "TEXT", false, "")}},
		// Type change only → risky (lock, but no proven data loss)
		{Name: "typed", ChangedColumns: []diff.ColumnChange{{
			Name: "val",
			Old:  &schema.Column{Name: "val", Type: "INTEGER"},
			New:  &schema.Column{Name: "val", Type: "TEXT"},
		}}},
	}}

	ops, err := AnalyzeAll(d, old)
	if err != nil {
		t.Fatal(err)
	}

	if got := findOp(t, ops, "audit").Kind; got != OpSafe {
		t.Errorf("audit (new table): kind = %v, want OpSafe", got)
	}
	if got := findOp(t, ops, "sessions").Kind; got != OpDestructive {
		t.Errorf("sessions (drop table): kind = %v, want OpDestructive", got)
	}

	users := findOp(t, ops, "users")
	if users.Kind != OpDestructive {
		t.Errorf("users (drop column): kind = %v, want OpDestructive", users.Kind)
	}
	if users.Rows != 2 {
		t.Errorf("users rows = %d, want 2", users.Rows)
	}
	// Has 1 index → lock estimate must be non-zero and reflect the index factor.
	if users.LockMs == 0 {
		t.Errorf("users rebuild should have a non-zero lock estimate, got 0")
	}

	if got := findOp(t, ops, "addonly").Kind; got != OpSafe {
		t.Errorf("addonly (add column): kind = %v, want OpSafe", got)
	}

	typed := findOp(t, ops, "typed")
	if typed.Kind != OpRisky {
		t.Errorf("typed (type change only): kind = %v, want OpRisky (must be reachable!)", typed.Kind)
	}
}

func TestAnalyzeAll_NotNullNoDefaultIsDestructive(t *testing.T) {
	dir := t.TempDir()
	old := dir + "/old.db"
	buildDB(t, old,
		"CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT)",
		"INSERT INTO t (a) VALUES ('x')",
	)
	d := &diff.Result{Schema: []diff.TableDiff{
		{Name: "t", AddedColumns: []schema.Column{{Name: "b", Type: "TEXT", NotNull: true}}},
	}}
	ops, err := AnalyzeAll(d, old)
	if err != nil {
		t.Fatal(err)
	}
	op := findOp(t, ops, "t")
	if op.Kind != OpDestructive {
		t.Errorf("ADD COLUMN NOT NULL without DEFAULT on non-empty table: kind = %v, want OpDestructive", op.Kind)
	}
}

func TestAnalyzeAll_NotNullNoDefaultOnEmptyTableIsSafe(t *testing.T) {
	dir := t.TempDir()
	old := dir + "/old.db"
	buildDB(t, old, "CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT)")
	d := &diff.Result{Schema: []diff.TableDiff{
		{Name: "t", AddedColumns: []schema.Column{{Name: "b", Type: "TEXT", NotNull: true}}},
	}}
	ops, err := AnalyzeAll(d, old)
	if err != nil {
		t.Fatal(err)
	}
	if op := findOp(t, ops, "t"); op.Kind != OpSafe {
		t.Errorf("NOT NULL no default on EMPTY table: kind = %v, want OpSafe", op.Kind)
	}
}
