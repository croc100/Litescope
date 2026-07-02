package safewrite

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, active INTEGER);
		INSERT INTO users (name, active) VALUES ('a', 1), ('b', 1), ('c', 0);`); err != nil {
		t.Fatal(err)
	}
	return path
}

func count(t *testing.T, path, q string) int64 {
	t.Helper()
	db, _ := sql.Open("sqlite", path)
	defer db.Close()
	var n int64
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDryRunMeasuresWithoutApplying(t *testing.T) {
	path := newDB(t)
	res, err := PlanLocal(path, `UPDATE users SET active = 0 WHERE active = 1;`, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Applied {
		t.Fatalf("dry-run should be OK and not applied: %+v", res)
	}
	if res.RowsAffected != 2 {
		t.Errorf("expected 2 rows affected, got %d", res.RowsAffected)
	}
	if len(res.Preview) != 1 || res.Preview[0].Kind != "update" {
		t.Errorf("bad preview: %+v", res.Preview)
	}
	// Database must be unchanged.
	if n := count(t, path, `SELECT COUNT(*) FROM users WHERE active = 1`); n != 2 {
		t.Errorf("dry-run mutated the database: active=1 count is %d, want 2", n)
	}
}

func TestApplyCommitsAndSnapshots(t *testing.T) {
	path := newDB(t)
	res, err := PlanLocal(path, `DELETE FROM users WHERE active = 0;`, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatalf("expected applied: %+v", res)
	}
	if res.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", res.RowsAffected)
	}
	if res.BackupPath == "" {
		t.Error("apply must take a snapshot")
	}
	if n := count(t, path, `SELECT COUNT(*) FROM users`); n != 2 {
		t.Errorf("expected 2 rows after delete, got %d", n)
	}
}

func TestDryRunInvalidSQLDoesNotApply(t *testing.T) {
	path := newDB(t)
	res, err := PlanLocal(path, `UPDATE nonexistent SET x = 1;`, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Applied {
		t.Fatalf("invalid SQL should fail cleanly: %+v", res)
	}
	if res.Error == "" {
		t.Error("expected error message")
	}
}

func TestMultiStatementImpact(t *testing.T) {
	path := newDB(t)
	res, err := PlanLocal(path,
		`UPDATE users SET name = 'x' WHERE active = 1; DELETE FROM users WHERE active = 0;`, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Preview) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(res.Preview))
	}
	if res.RowsAffected != 3 { // 2 updated + 1 deleted
		t.Errorf("expected total 3 rows affected, got %d", res.RowsAffected)
	}
}

func TestEmptySQL(t *testing.T) {
	path := newDB(t)
	res, err := PlanLocal(path, `-- just a comment`, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Errorf("empty SQL should not be OK: %+v", res)
	}
}

func TestD1RewindTokenRoundTrip(t *testing.T) {
	tok := EncodeD1RewindToken("bm-0123", "db-uuid-1")
	bm, err := DecodeD1RewindToken(tok, "db-uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	if bm != "bm-0123" {
		t.Errorf("bookmark = %q, want bm-0123", bm)
	}
}

func TestD1RewindTokenRejectsWrongDatabase(t *testing.T) {
	tok := EncodeD1RewindToken("bm-0123", "db-uuid-1")
	if _, err := DecodeD1RewindToken(tok, "db-uuid-2"); err == nil {
		t.Fatal("expected error decoding token minted for a different database")
	}
}

func TestD1RewindTokenRejectsLocalToken(t *testing.T) {
	tok := EncodeRewindToken("/tmp/snap.db", "/tmp/app.db")
	if _, err := DecodeD1RewindToken(tok, "db-uuid-1"); err == nil {
		t.Fatal("expected error decoding a local token as D1")
	}
}

func TestLocalDecodeRejectsD1Token(t *testing.T) {
	tok := EncodeD1RewindToken("bm-0123", "db-uuid-1")
	if _, err := DecodeRewindToken(tok, "/tmp/app.db"); err == nil {
		t.Fatal("expected error decoding a D1 token as local")
	}
}
