package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func makeDB(t *testing.T, rows int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.db")
	db, _ := sql.Open("sqlite", p)
	defer db.Close()
	db.Exec("CREATE TABLE events(id INTEGER PRIMARY KEY, k TEXT)")
	tx, _ := db.Begin()
	st, _ := tx.Prepare("INSERT INTO events(k) VALUES(?)")
	for i := 0; i < rows; i++ {
		st.Exec("v")
	}
	tx.Commit()
	return p
}

func TestRunSQL_ReadQuery(t *testing.T) {
	a := &App{}
	p := makeDB(t, 100)
	r, err := a.RunSQL(p, "SELECT count(*) AS n FROM events", false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsQuery || len(r.Rows) != 1 || r.Columns[0] != "n" {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestRunSQL_ReadOnlyBlocksWrite(t *testing.T) {
	a := &App{}
	p := makeDB(t, 10)
	_, err := a.RunSQL(p, "DELETE FROM events", false)
	if err == nil {
		t.Fatal("expected read-only mode to reject DELETE, got nil error")
	}
	// confirm nothing was deleted
	r, _ := a.RunSQL(p, "SELECT count(*) AS n FROM events", false)
	if got := r.Rows[0][0]; toInt(got) != 10 {
		t.Fatalf("rows changed under read-only: %v", got)
	}
}

func TestRunSQL_WriteMode(t *testing.T) {
	a := &App{}
	p := makeDB(t, 10)
	r, err := a.RunSQL(p, "DELETE FROM events WHERE id <= 4", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.IsQuery || r.RowsAffected != 4 {
		t.Fatalf("expected 4 affected, got %+v", r)
	}
}

func TestRunSQL_LargeTruncates(t *testing.T) {
	a := &App{}
	p := makeDB(t, 6000)
	r, err := a.RunSQL(p, "SELECT * FROM events", false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || len(r.Rows) != sqlMaxRows {
		t.Fatalf("expected truncation at %d, got rows=%d truncated=%v", sqlMaxRows, len(r.Rows), r.Truncated)
	}
}

func toInt(v interface{}) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	}
	return -1
}

func TestExportSQL_CSV(t *testing.T) {
	a := &App{}
	p := makeDB(t, 3)
	dest := filepath.Join(t.TempDir(), "out.csv")
	r, err := a.ExportSQL(p, "SELECT id, k FROM events ORDER BY id", dest, "csv")
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != 3 {
		t.Fatalf("want 3 rows, got %d", r.Rows)
	}
	b, _ := os.ReadFile(dest)
	got := string(b)
	if want := "id,k\n1,v\n2,v\n3,v\n"; got != want {
		t.Fatalf("csv mismatch:\n%q\nwant\n%q", got, want)
	}
}

func TestExportSQL_JSON(t *testing.T) {
	a := &App{}
	p := makeDB(t, 2)
	dest := filepath.Join(t.TempDir(), "out.json")
	r, err := a.ExportSQL(p, "SELECT id FROM events ORDER BY id", dest, "json")
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != 2 {
		t.Fatalf("want 2 rows, got %d", r.Rows)
	}
	b, _ := os.ReadFile(dest)
	var arr []map[string]interface{}
	if err := json.Unmarshal(b, &arr); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, b)
	}
	if len(arr) != 2 {
		t.Fatalf("want 2 objects, got %d", len(arr))
	}
}

func TestExportSQL_RejectsWrite(t *testing.T) {
	a := &App{}
	p := makeDB(t, 1)
	dest := filepath.Join(t.TempDir(), "x.csv")
	if _, err := a.ExportSQL(p, "DELETE FROM events", dest, "csv"); err == nil {
		t.Fatal("expected non-read query to be rejected")
	}
}
