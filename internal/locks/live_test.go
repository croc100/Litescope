package locks_test

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/croc100/litescope/internal/locks"
)

func TestProbe_Free(t *testing.T) {
	path := makeDB(t, "PRAGMA journal_mode=WAL")
	p, err := locks.Probe(path, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if p.State != locks.StateFree {
		t.Errorf("state: got %q, want %q (detail: %s)", p.State, locks.StateFree, p.Detail)
	}
}

func TestProbe_Locked(t *testing.T) {
	path := makeDB(t, "PRAGMA journal_mode=WAL")

	// Hold a write lock on a separate connection.
	holder, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open holder: %v", err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	conn, err := holder.Conn(t.Context())
	if err != nil {
		t.Fatalf("holder conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer conn.ExecContext(t.Context(), "ROLLBACK")

	p, err := locks.Probe(path, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if p.State != locks.StateLocked {
		t.Errorf("state: got %q, want %q (detail: %s)", p.State, locks.StateLocked, p.Detail)
	}
}

func TestProbe_Remote(t *testing.T) {
	if _, err := locks.Probe("d1://abc", time.Second); err == nil {
		t.Error("expected error probing a remote source")
	}
}
