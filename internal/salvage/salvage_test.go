package salvage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func seedDB(t *testing.T, path string, rows int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, val INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE INDEX idx_items_val ON items(val)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := tx.Exec(`INSERT INTO items (name, val) VALUES (?, ?)`, "row", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverHealthyDatabase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "healthy.db")
	out := filepath.Join(dir, "recovered.db")
	seedDB(t, src, 200)

	res, err := Recover(src, out)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !res.OutputHealthy {
		t.Fatal("expected recovered database to pass quick_check")
	}
	if got := res.TotalCopied(); got != 200 {
		t.Fatalf("expected all 200 rows copied, got %d (lost %d)", got, res.TotalLost())
	}
	if res.TotalLost() != 0 {
		t.Fatalf("expected no rows lost on a healthy database, got %d", res.TotalLost())
	}

	// The recovered file should have the same row count via a fresh connection.
	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Fatalf("expected 200 rows in recovered db, got %d", n)
	}
}

func TestRecoverOutputMustNotExist(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	out := filepath.Join(dir, "out.db")
	seedDB(t, src, 5)
	if err := os.WriteFile(out, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(src, out); err == nil {
		t.Fatal("expected error when output already exists")
	}
}

func TestRecoverWithCorruption(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "corrupt.db")
	out := filepath.Join(dir, "recovered.db")
	seedDB(t, src, 2000)

	// Flip bytes throughout the back half of the file (past the schema
	// pages) to simulate scattered page corruption without destroying
	// sqlite_master itself.
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(data) / 2; i < len(data); i += 4096 {
		if i+16 < len(data) {
			for j := 0; j < 16; j++ {
				data[i+j] ^= 0xFF
			}
		}
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Recover(src, out)
	if err != nil {
		t.Fatalf("Recover should salvage what it can, not fail outright: %v", err)
	}
	if !res.OutputHealthy {
		t.Fatal("recovered database (freshly built) should itself pass quick_check")
	}
	if len(res.Tables) != 1 || res.Tables[0].Table != "items" {
		t.Fatalf("expected the items table to be recreated, got %+v", res.Tables)
	}
	// We can't assert exact counts — corruption placement is inherently
	// approximate — but the recovery should have made progress and stayed
	// within bounds of what existed.
	total := res.TotalCopied() + res.TotalLost()
	if total == 0 {
		t.Fatal("expected to account for some rows (copied or lost)")
	}
	if res.TotalCopied() > 2000 {
		t.Fatalf("copied more rows than existed: %d", res.TotalCopied())
	}
}
