package exporter

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func seed(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE u (id INTEGER, name TEXT, score REAL);
		INSERT INTO u VALUES (1,'a',1.5),(2,'b',NULL),(3,'comma,name',3.0);`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestExportCSV(t *testing.T) {
	db := seed(t)
	var b strings.Builder
	n, err := Export(db, "SELECT id,name,score FROM u ORDER BY id", "csv", &b)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("rows=%d want 3", n)
	}
	out := b.String()
	if !strings.HasPrefix(out, "id,name,score\n") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "2,b,\n") { // NULL -> empty
		t.Errorf("NULL not empty: %q", out)
	}
	if !strings.Contains(out, `"comma,name"`) { // comma quoted
		t.Errorf("comma not quoted: %q", out)
	}
}

func TestExportTSV(t *testing.T) {
	db := seed(t)
	var b strings.Builder
	if _, err := Export(db, "SELECT id,name FROM u WHERE id=1", "tsv", &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "1\ta\n") {
		t.Errorf("tsv wrong: %q", b.String())
	}
}

func TestExportJSON(t *testing.T) {
	db := seed(t)
	var b strings.Builder
	if _, err := Export(db, "SELECT id,score FROM u ORDER BY id", "json", &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `"id":1`) || !strings.Contains(out, `"score":null`) {
		t.Errorf("json wrong: %q", out)
	}
}

func TestExportRejectsWrite(t *testing.T) {
	db := seed(t)
	var b strings.Builder
	if _, err := Export(db, "DELETE FROM u", "csv", &b); err == nil {
		t.Fatal("expected write to be rejected")
	}
}
