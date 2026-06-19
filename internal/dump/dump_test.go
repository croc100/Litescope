package dump

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func makeDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT, author_id INTEGER REFERENCES authors(id), price REAL, cover BLOB);
		CREATE INDEX idx_books_author ON books(author_id);
		INSERT INTO authors VALUES (1, 'O''Brien'), (2, 'Diaz');
		INSERT INTO books VALUES (1, 'It''s here', 1, 9.99, x'deadbeef'), (2, NULL, 2, NULL, NULL);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func dumpString(t *testing.T, path string, opts Options) string {
	t.Helper()
	var b strings.Builder
	if err := Dump(path, &b, opts); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	return b.String()
}

func TestDump_RoundTrip(t *testing.T) {
	src := dumpString(t, makeDB(t), Options{})

	for _, want := range []string{
		"BEGIN TRANSACTION;",
		"CREATE TABLE authors",
		`INSERT INTO "authors" VALUES(1,'O''Brien');`,
		`INSERT INTO "books" VALUES(1,'It''s here',1,9.99,X'DEADBEEF');`,
		`INSERT INTO "books" VALUES(2,NULL,2,NULL,NULL);`,
		"CREATE INDEX idx_books_author",
		"COMMIT;",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("dump missing %q\n---\n%s", want, src)
		}
	}

	// Replaying the dump into a fresh database must reproduce the rows.
	dst := filepath.Join(t.TempDir(), "restored.db")
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(src); err != nil {
		t.Fatalf("replay dump: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM books").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("restored books count = %d, want 2", n)
	}
}

func TestDump_SchemaOnly(t *testing.T) {
	out := dumpString(t, makeDB(t), Options{SchemaOnly: true})
	if strings.Contains(out, "INSERT INTO") {
		t.Errorf("schema-only dump contains INSERT:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE authors") {
		t.Errorf("schema-only dump missing CREATE:\n%s", out)
	}
}

func TestDump_DataOnly(t *testing.T) {
	out := dumpString(t, makeDB(t), Options{DataOnly: true})
	if strings.Contains(out, "CREATE ") {
		t.Errorf("data-only dump contains CREATE:\n%s", out)
	}
	if !strings.Contains(out, "INSERT INTO") {
		t.Errorf("data-only dump missing INSERT:\n%s", out)
	}
}

func TestDump_TableFilter(t *testing.T) {
	out := dumpString(t, makeDB(t), Options{Table: "authors"})
	if strings.Contains(out, "books") {
		t.Errorf("table filter leaked books:\n%s", out)
	}
	if !strings.Contains(out, "CREATE TABLE authors") {
		t.Errorf("table filter missing authors:\n%s", out)
	}
}

func TestDump_UnknownTable(t *testing.T) {
	var b strings.Builder
	if err := Dump(makeDB(t), &b, Options{Table: "nope"}); err == nil {
		t.Error("expected error for unknown table")
	}
}
