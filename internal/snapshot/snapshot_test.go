package snapshot

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newDB(t *testing.T, rows int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := db.Exec(`INSERT INTO t (v) VALUES ('x')`); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func count(t *testing.T, path string) int64 {
	t.Helper()
	db, _ := sql.Open("sqlite", path)
	defer db.Close()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateAndList(t *testing.T) {
	path := newDB(t, 3)
	snap, err := Create(path, CreateOptions{Label: "before migration"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snap.Path); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}
	if snap.Label != "before-migration" {
		t.Errorf("label = %q, want before-migration", snap.Label)
	}
	if count(t, snap.Path) != 3 {
		t.Error("snapshot does not contain original rows")
	}

	snaps, err := List(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
}

func TestRestore(t *testing.T) {
	path := newDB(t, 2)
	if _, err := Create(path, CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	// Mutate after the snapshot.
	db, _ := sql.Open("sqlite", path)
	db.Exec(`INSERT INTO t (v) VALUES ('y'), ('z')`)
	db.Close()
	if count(t, path) != 4 {
		t.Fatal("setup: expected 4 rows after insert")
	}

	latest, ok, err := Latest(path)
	if err != nil || !ok {
		t.Fatalf("latest: ok=%v err=%v", ok, err)
	}
	if err := Restore(path, latest.Path, true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, path); n != 2 {
		t.Errorf("after restore expected 2 rows, got %d", n)
	}
	// Safety-net snapshot of the pre-restore state should now exist too.
	snaps, _ := List(path)
	if len(snaps) < 2 {
		t.Errorf("expected a pre-restore safety snapshot, have %d", len(snaps))
	}
}

func TestRetention(t *testing.T) {
	path := newDB(t, 1)
	for i := 1; i <= 4; i++ {
		// Distinct past timestamps so ordering is stable.
		mustCreateAt(t, path, time.Now().Add(-time.Duration(i)*time.Second))
	}
	if _, err := Prune(path, 2); err != nil {
		t.Fatal(err)
	}
	snaps, _ := List(path)
	if len(snaps) != 2 {
		t.Errorf("retention: expected 2 snapshots kept, got %d", len(snaps))
	}
}

func TestKeepOnCreate(t *testing.T) {
	path := newDB(t, 1)
	for i := 1; i <= 3; i++ {
		mustCreateAt(t, path, time.Now().Add(-time.Duration(i)*time.Second))
	}
	// One more with Keep=1 should leave only the newest (real now > all faked past).
	s, err := Create(path, CreateOptions{Keep: 1})
	if err != nil {
		t.Fatal(err)
	}
	snaps, _ := List(path)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot after keep=1, got %d", len(snaps))
	}
	if snaps[0].Path != s.Path {
		t.Error("kept snapshot is not the newest")
	}
}

func TestRefuseMissing(t *testing.T) {
	if _, err := Create(filepath.Join(t.TempDir(), "nope.db"), CreateOptions{}); err == nil {
		t.Error("expected error for missing database")
	}
}

// mustCreateAt creates a snapshot then renames it to a controlled timestamp so
// tests can exercise ordering/retention without sleeping a full second each time.
func mustCreateAt(t *testing.T, dbPath string, ts time.Time) *Snapshot {
	t.Helper()
	s, err := Create(dbPath, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(Dir(dbPath), base(dbPath)+"__"+ts.Format(tsLayout)+".db")
	if newPath != s.Path {
		os.Rename(s.Path, newPath)
		s.Path = newPath
		s.CreatedAt = ts
	}
	return s
}
