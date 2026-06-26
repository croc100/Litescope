package d1sync

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSqlLiteral(t *testing.T) {
	tests := []struct {
		in   interface{}
		want string
	}{
		{nil, "NULL"},
		{true, "1"},
		{false, "0"},
		{float64(42), "42"},
		{float64(3.14), "3.14"},
		{int64(99), "99"},
		{"hello", "'hello'"},
		{"it's", "'it''s'"},
	}
	for _, tc := range tests {
		got := sqlLiteral(tc.in)
		if got != tc.want {
			t.Errorf("sqlLiteral(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildInserts(t *testing.T) {
	rows := []map[string]interface{}{
		{"id": float64(1), "name": "alice"},
		{"id": float64(2), "name": "bob's"},
	}
	stmts, err := buildInserts("users", rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	for _, s := range stmts {
		if len(s) == 0 {
			t.Error("empty statement")
		}
	}
}

// TestLocalTableDDLs tests that we can read DDLs from a real SQLite file.
func TestLocalTableDDLs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, body TEXT)`); err != nil {
		t.Fatal(err)
	}

	tables, err := localTableDDLs(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
}

// TestInsertRows verifies that insertRows populates a local table correctly.
func TestInsertRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "insert_test.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT)`); err != nil {
		t.Fatal(err)
	}

	rows := []map[string]interface{}{
		{"id": float64(1), "label": "alpha"},
		{"id": float64(2), "label": "beta"},
	}
	if err := insertRows(db, "items", rows); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM items").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}
}

// TestPushPullRoundtrip creates a source SQLite file, pushes it to a second
// SQLite file using buildInserts + file Exec, then verifies the data.
// (We don't hit real D1 in unit tests; the round-trip exercises the
// localTableDDLs → buildInserts → Exec path end-to-end with local files.)
func TestPushPullRoundtrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	dst := filepath.Join(dir, "dst.db")

	// Build source.
	srcDB, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srcDB.Exec(`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, price REAL)`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := srcDB.Exec(`INSERT INTO products VALUES (?,?,?)`, i, fmt.Sprintf("item%d", i), float64(i)*1.5); err != nil {
			t.Fatal(err)
		}
	}
	srcDB.Close()

	// Read DDLs and rows.
	srcDB2, _ := sql.Open("sqlite", src)
	defer srcDB2.Close()
	tables, err := localTableDDLs(srcDB2)
	if err != nil {
		t.Fatal(err)
	}

	// Build destination.
	dstDB, _ := sql.Open("sqlite", dst)
	defer dstDB.Close()
	for _, td := range tables {
		if _, err := dstDB.Exec(td.DDL); err != nil {
			t.Fatal(err)
		}
		rows, err := localQueryRows(srcDB2, fmt.Sprintf("SELECT * FROM %q", td.Name))
		if err != nil {
			t.Fatal(err)
		}
		if err := insertRows(dstDB, td.Name, rows); err != nil {
			t.Fatal(err)
		}
	}

	// Verify.
	var count int
	dstDB.QueryRow("SELECT COUNT(*) FROM products").Scan(&count)
	if count != 5 {
		t.Fatalf("expected 5 rows in dst, got %d", count)
	}

	// Cleanup check.
	os.Remove(src)
	os.Remove(dst)
}
