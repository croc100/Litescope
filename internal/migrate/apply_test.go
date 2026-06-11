package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return path
}

func queryInt(t *testing.T, path, q string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApply_Basic(t *testing.T) {
	path := newTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO users (name) VALUES ('a'), ('b');
	`)

	res, err := Apply(path, `ALTER TABLE users ADD COLUMN email TEXT;`, ApplyOptions{NoBackup: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Executed != 1 {
		t.Errorf("executed = %d, want 1", res.Executed)
	}

	if n := queryInt(t, path, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='email'`); n != 1 {
		t.Error("email column not added")
	}
}

func TestApply_DryRunLeavesDBUnchanged(t *testing.T) {
	path := newTestDB(t, `CREATE TABLE users (id INTEGER PRIMARY KEY);`)

	res, err := Apply(path, `ALTER TABLE users ADD COLUMN email TEXT;`, ApplyOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Executed != 1 {
		t.Errorf("res = %+v", res)
	}
	if res.BackupPath != "" {
		t.Error("dry run must not create a backup")
	}

	if n := queryInt(t, path, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='email'`); n != 0 {
		t.Error("dry run modified the database")
	}
}

func TestApply_FailureRollsBack(t *testing.T) {
	path := newTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO users (name) VALUES ('a');
	`)

	// Second statement fails — first must be rolled back.
	sqlText := `
		ALTER TABLE users ADD COLUMN email TEXT;
		ALTER TABLE nonexistent ADD COLUMN x TEXT;
	`
	_, err := Apply(path, sqlText, ApplyOptions{NoBackup: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("error should mention rollback: %v", err)
	}

	if n := queryInt(t, path, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='email'`); n != 0 {
		t.Error("failed migration left partial changes")
	}
}

func TestApply_BackupCreated(t *testing.T) {
	path := newTestDB(t, `
		CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);
		INSERT INTO t (v) VALUES ('keep');
	`)
	backupDir := t.TempDir()

	res, err := Apply(path, `ALTER TABLE t ADD COLUMN extra TEXT;`, ApplyOptions{BackupDir: backupDir})
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath == "" {
		t.Fatal("no backup path")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// Backup must be a valid DB containing the pre-migration state.
	if n := queryInt(t, res.BackupPath, `SELECT COUNT(*) FROM t`); n != 1 {
		t.Error("backup does not contain original data")
	}
	if n := queryInt(t, res.BackupPath, `SELECT COUNT(*) FROM pragma_table_info('t') WHERE name='extra'`); n != 0 {
		t.Error("backup contains post-migration schema")
	}
}

func TestApply_ForeignKeyViolationRollsBack(t *testing.T) {
	path := newTestDB(t, `
		CREATE TABLE parents (id INTEGER PRIMARY KEY);
		CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id));
		INSERT INTO parents (id) VALUES (1);
		INSERT INTO children (id, parent_id) VALUES (1, 1);
	`)

	// Dropping parents orphans children.parent_id → FK check must fail and roll back.
	_, err := Apply(path, `DROP TABLE parents;`, ApplyOptions{NoBackup: true})
	if err == nil {
		t.Fatal("expected foreign key failure")
	}
	if !strings.Contains(err.Error(), "foreign key") {
		t.Errorf("expected FK error, got: %v", err)
	}

	if n := queryInt(t, path, `SELECT COUNT(*) FROM parents`); n != 1 {
		t.Error("rollback failed: parents table missing or empty")
	}
}

func TestApply_MissingDB(t *testing.T) {
	_, err := Apply("/nonexistent/path.db", "SELECT 1;", ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

func TestApply_EmptyMigration(t *testing.T) {
	path := newTestDB(t, `CREATE TABLE t (id INTEGER);`)
	_, err := Apply(path, "-- only comments\n", ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "no executable statements") {
		t.Errorf("expected empty-migration error, got: %v", err)
	}
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"two simple", "SELECT 1; SELECT 2;", 2},
		{"no trailing semicolon", "SELECT 1", 1},
		{"semicolon in string", `INSERT INTO t VALUES ('a;b'); SELECT 1;`, 2},
		{"escaped quote in string", `INSERT INTO t VALUES ('it''s; fine'); SELECT 1;`, 2},
		{"semicolon in comment", "-- note; not a split\nSELECT 1;", 1},
		{"comment only", "-- nothing here\n", 0},
		{"empty", "", 0},
		{"quoted identifier", `CREATE TABLE "a;b" (id INTEGER); SELECT 1;`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitStatements(tc.in)
			if len(got) != tc.want {
				t.Errorf("got %d statements %q, want %d", len(got), got, tc.want)
			}
		})
	}
}

func TestApply_GeneratedRebuildMigration(t *testing.T) {
	// End-to-end: generate a rebuild migration via Generate(), apply it,
	// verify data survives the rebuild.
	before := newTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, legacy TEXT);
		INSERT INTO users (name, legacy) VALUES ('alice', 'x'), ('bob', 'y');
	`)
	after := newTestDB(t, `
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
	`)

	d, gerr := diff.Compare(before, after)
	if gerr != nil {
		t.Fatal(gerr)
	}
	newSchema, serr := schema.Load(after)
	if serr != nil {
		t.Fatal(serr)
	}

	m := Generate(d, newSchema)
	res, err := Apply(before, m.SQL(), ApplyOptions{NoBackup: true})
	if err != nil {
		t.Fatalf("apply generated migration: %v", err)
	}
	if res.Executed == 0 {
		t.Fatal("nothing executed")
	}

	if n := queryInt(t, before, `SELECT COUNT(*) FROM users`); n != 2 {
		t.Errorf("rows after rebuild = %d, want 2", n)
	}
	if n := queryInt(t, before, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name='legacy'`); n != 0 {
		t.Error("legacy column still present after rebuild")
	}
}
