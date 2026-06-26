package locks_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/croc100/litescope/internal/locks"
)

func makeDB(t *testing.T, pragmas ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("exec %q: %v", p, err)
		}
	}
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return path
}

func TestDiagnoseLocal_DefaultMode(t *testing.T) {
	// Default SQLite: journal_mode=delete, busy_timeout=0 → two critical findings
	path := makeDB(t)
	r, err := locks.Diagnose(path)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if r.Provider != "local" {
		t.Errorf("provider: got %q, want \"local\"", r.Provider)
	}
	if r.Verdict != "critical" {
		t.Errorf("verdict: got %q, want \"critical\"", r.Verdict)
	}
	critCount := 0
	for _, f := range r.Findings {
		if f.Severity == "critical" {
			critCount++
		}
	}
	if critCount < 2 {
		t.Errorf("expected at least 2 critical findings, got %d", critCount)
	}
}

func TestDiagnoseLocal_WALWithTimeout(t *testing.T) {
	// WAL + busy_timeout: WAL is good, but busy_timeout is per-connection so still critical
	path := makeDB(t, "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000")
	r, err := locks.Diagnose(path)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	// Should still have busy-timeout-zero critical because it's per-connection
	found := false
	for _, f := range r.Findings {
		if f.Severity == "critical" && f.Rule == "busy-timeout-zero" {
			found = true
		}
		if f.Severity == "critical" && f.Rule == "journal-not-wal" {
			t.Errorf("should not have journal-not-wal critical when using WAL")
		}
	}
	if !found {
		t.Error("expected busy-timeout-zero critical finding")
	}
}

func TestDiagnoseLocal_MissingFile(t *testing.T) {
	_, err := locks.Diagnose("/nonexistent/path/to/db.sqlite")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestDiagnoseD1(t *testing.T) {
	r, err := locks.Diagnose("d1://some-db-id")
	if err != nil {
		t.Fatalf("unexpected error for D1 source: %v", err)
	}
	if r.Provider != "d1" {
		t.Errorf("provider: got %q, want \"d1\"", r.Provider)
	}
	if len(r.Findings) == 0 {
		t.Error("expected D1 findings, got none")
	}
}

func TestDiagnoseTurso(t *testing.T) {
	r, err := locks.Diagnose("turso://TOKEN@org/db")
	if err != nil {
		t.Fatalf("unexpected error for Turso source: %v", err)
	}
	if r.Provider != "turso" {
		t.Errorf("provider: got %q, want \"turso\"", r.Provider)
	}
}

func TestDiagnoseLocal_WALBloat(t *testing.T) {
	// Create a WAL file that looks large by faking it
	path := makeDB(t, "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000")
	walPath := path + "-wal"
	// Write a fake 110MB WAL file
	f, err := os.Create(walPath)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	if err := f.Truncate(110 * 1024 * 1024); err != nil {
		t.Fatalf("truncate wal: %v", err)
	}
	f.Close()

	r, err := locks.Diagnose(path)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	found := false
	for _, f := range r.Findings {
		if f.Rule == "wal-bloat" && f.Severity == "warning" {
			found = true
		}
	}
	if !found {
		t.Error("expected wal-bloat warning for 110MB WAL file")
	}
}
