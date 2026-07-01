package mcp

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestSplitStaleAfter(t *testing.T) {
	cases := []struct {
		in      string
		wantSrc string
		wantDur time.Duration
	}{
		{"app.db", "app.db", 0},
		{"app.db?stale_after=1h", "app.db", time.Hour},
		{"app.db?stale_after=90s", "app.db", 90 * time.Second},
		{"app.db?stale_after=bogus", "app.db", 0},
		{"", "", 0},
	}
	for _, c := range cases {
		src, dur := splitStaleAfter(c.in)
		if src != c.wantSrc || dur != c.wantDur {
			t.Errorf("splitStaleAfter(%q) = (%q, %v), want (%q, %v)", c.in, src, dur, c.wantSrc, c.wantDur)
		}
	}
}

func TestLiveSignature_HealthChangesOnlyOnSeverityShift(t *testing.T) {
	path := t.TempDir() + "/live.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)")
	db.Close()

	uri := healthScheme + path
	sig1, ok := liveSignature(uri)
	if !ok {
		t.Fatalf("expected liveSignature to handle a local health URI")
	}

	// A plain write shouldn't change the severity/verdict signature.
	db2, _ := sql.Open("sqlite", path)
	db2.Exec("INSERT INTO t DEFAULT VALUES")
	db2.Close()
	sig2, _ := liveSignature(uri)
	if sig1 != sig2 {
		t.Errorf("signature changed on an ordinary write with no severity shift: %q -> %q", sig1, sig2)
	}

	// Backdating the file past stale_after should flip the signature (severity ok -> warning).
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	staleURI := healthScheme + path + "?stale_after=1h"
	sig3, ok := liveSignature(staleURI)
	if !ok {
		t.Fatalf("expected liveSignature to handle a stale_after health URI")
	}
	if sig3 == sig1 {
		t.Errorf("expected signature to change once the heartbeat goes stale, got same: %q", sig3)
	}
}

func TestLiveSignature_Locks(t *testing.T) {
	path := t.TempDir() + "/locks.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)")
	db.Close()

	sig, ok := liveSignature(locksScheme + path)
	if !ok || sig == "" {
		t.Errorf("expected a non-empty locks signature, got (%q, %v)", sig, ok)
	}
}

func TestLiveSignature_RemoteAndUnknownAreUnhandled(t *testing.T) {
	if _, ok := liveSignature(healthScheme + "d1://fake"); ok {
		t.Error("expected remote health URI to be unhandled by liveSignature")
	}
	if _, ok := liveSignature(schemaScheme + "app.db"); ok {
		t.Error("expected schema URI to be unhandled by liveSignature (uses mtime instead)")
	}
}
