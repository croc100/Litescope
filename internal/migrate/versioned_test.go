package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newDB(t *testing.T, stmts ...string) string {
	t.Helper()
	path := t.TempDir() + "/db.sqlite"
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

func TestNew_SequencesAndSlugs(t *testing.T) {
	dir := t.TempDir()
	p1, err := New(dir, "Add Users Table", "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p1) != "0001_add_users_table.sql" {
		t.Errorf("first migration name = %s", filepath.Base(p1))
	}
	p2, _ := New(dir, "add index!!", "CREATE INDEX i ON t(a);")
	if filepath.Base(p2) != "0002_add_index.sql" {
		t.Errorf("second migration name = %s", filepath.Base(p2))
	}
	files, _ := LoadDir(dir)
	if len(files) != 2 {
		t.Fatalf("LoadDir = %d files, want 2", len(files))
	}
}

func TestStatusAndUp(t *testing.T) {
	dir := t.TempDir()
	New(dir, "create_users", "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);")
	New(dir, "add_email", "ALTER TABLE users ADD COLUMN email TEXT;")

	db := newDB(t) // empty database

	st, err := GetStatus(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 2 || len(st.Applied) != 0 {
		t.Fatalf("before up: applied=%d pending=%d", len(st.Applied), len(st.Pending))
	}

	res, err := Up(db, dir, ApplyOptions{NoBackup: true})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(res.Applied) != 2 {
		t.Errorf("applied %d, want 2", len(res.Applied))
	}

	st, _ = GetStatus(db, dir)
	if len(st.Applied) != 2 || len(st.Pending) != 0 {
		t.Errorf("after up: applied=%d pending=%d", len(st.Applied), len(st.Pending))
	}

	// Idempotent: a second up does nothing.
	res2, err := Up(db, dir, ApplyOptions{NoBackup: true})
	if err != nil || len(res2.Applied) != 0 {
		t.Errorf("second up should be a no-op, got applied=%d err=%v", len(res2.Applied), err)
	}

	// The schema change actually landed.
	conn, _ := sql.Open("sqlite", db)
	defer conn.Close()
	if _, err := conn.Exec("INSERT INTO users (name, email) VALUES ('a','b')"); err != nil {
		t.Errorf("migrated schema missing expected columns: %v", err)
	}
}

func TestUp_DetectsHistoryDrift(t *testing.T) {
	dir := t.TempDir()
	p, _ := New(dir, "create_t", "CREATE TABLE t (id INTEGER PRIMARY KEY);")
	db := newDB(t)
	if _, err := Up(db, dir, ApplyOptions{NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	// Edit the already-applied file.
	if err := os.WriteFile(p, []byte("CREATE TABLE t (id INTEGER PRIMARY KEY, tampered TEXT);"), 0644); err != nil {
		t.Fatal(err)
	}
	st, _ := GetStatus(db, dir)
	if len(st.Drifted) != 1 {
		t.Errorf("expected 1 drifted version, got %v", st.Drifted)
	}
	if _, err := Up(db, dir, ApplyOptions{NoBackup: true}); err == nil {
		t.Errorf("up should refuse when history drifted")
	}
}

func TestUp_DryRunDoesNotRecord(t *testing.T) {
	dir := t.TempDir()
	New(dir, "create_t", "CREATE TABLE t (id INTEGER PRIMARY KEY);")
	db := newDB(t)
	if _, err := Up(db, dir, ApplyOptions{DryRun: true, NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	// Dry-run rolled back: still pending, table absent.
	st, _ := GetStatus(db, dir)
	if len(st.Pending) != 1 {
		t.Errorf("dry-run should leave migration pending, got applied=%d", len(st.Applied))
	}
}

func TestUp_StopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	New(dir, "ok", "CREATE TABLE a (id INTEGER PRIMARY KEY);")
	New(dir, "bad", "CREATE TABLE a (id INTEGER PRIMARY KEY);") // duplicate table → fails
	New(dir, "never", "CREATE TABLE c (id INTEGER PRIMARY KEY);")
	db := newDB(t)
	res, err := Up(db, dir, ApplyOptions{NoBackup: true})
	if err == nil {
		t.Fatal("expected failure on duplicate table")
	}
	if len(res.Applied) != 1 {
		t.Errorf("only the first migration should have applied, got %d", len(res.Applied))
	}
	st, _ := GetStatus(db, dir)
	if len(st.Applied) != 1 {
		t.Errorf("after halt: applied=%d, want 1", len(st.Applied))
	}
}
