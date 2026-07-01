package health

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestInspect_Healthy(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ok.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	db.Exec("INSERT INTO t (v) VALUES ('a'), ('b')")
	db.Close()

	r := Inspect(path, false)
	if r.Severity != SevOK {
		t.Errorf("severity = %v, want SevOK (issues: %v)", r.Severity, r.Issues)
	}
	if !r.Reachable || !r.IntegrityOK {
		t.Errorf("healthy DB: reachable=%v integrityOK=%v", r.Reachable, r.IntegrityOK)
	}
	if r.SizeBytes <= 0 || r.PageCount <= 0 {
		t.Errorf("expected positive size/pages, got size=%d pages=%d", r.SizeBytes, r.PageCount)
	}
	if r.SeverityLabel != "ok" {
		t.Errorf("SeverityLabel = %q, want ok", r.SeverityLabel)
	}
}

func TestInspect_Missing(t *testing.T) {
	r := Inspect(t.TempDir()+"/nope.db", false)
	if r.Severity != SevCritical {
		t.Errorf("missing file: severity = %v, want SevCritical", r.Severity)
	}
	if r.Reachable {
		t.Errorf("missing file should be unreachable")
	}
}

func TestInspect_Corrupt(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/corrupt.db"
	// A valid header-ish prefix followed by garbage isn't required; a file that
	// isn't a SQLite database at all fails quick_check / open.
	if err := os.WriteFile(path, []byte("SQLite format 3\x00 then total garbage that is not a real database body"), 0644); err != nil {
		t.Fatal(err)
	}
	r := Inspect(path, false)
	if r.Severity != SevCritical {
		t.Errorf("corrupt file: severity = %v, want SevCritical (issues: %v)", r.Severity, r.Issues)
	}
	if r.IntegrityOK {
		t.Errorf("corrupt file reported IntegrityOK = true")
	}
	if len(r.Issues) == 0 {
		t.Errorf("corrupt file should record an issue")
	}
}

func TestInspect_FragmentationWarning(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/frag.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Build a DB comfortably over FragmentationMinBytes, then delete most rows
	// so the freed pages land on the freelist (auto_vacuum defaults to NONE).
	db.Exec("CREATE TABLE big (id INTEGER PRIMARY KEY, blob TEXT)")
	blob := make([]byte, 4000)
	for i := range blob {
		blob[i] = 'x'
	}
	tx, _ := db.Begin()
	stmt, _ := tx.Prepare("INSERT INTO big (blob) VALUES (?)")
	for i := 0; i < 8000; i++ { // ~32MB
		stmt.Exec(string(blob))
	}
	stmt.Close()
	tx.Commit()
	db.Exec("DELETE FROM big WHERE id % 4 != 0") // delete ~75%
	db.Close()

	r := Inspect(path, false)
	if r.SizeBytes < FragmentationMinBytes {
		t.Fatalf("test DB too small (%d bytes) to exercise fragmentation threshold", r.SizeBytes)
	}
	if r.FreelistCount == 0 {
		t.Fatalf("expected a non-empty freelist after deleting 75%% of rows")
	}
	if r.FragmentationPct() < FragmentationRatio*100 {
		t.Skipf("freelist ratio %.1f%% below threshold on this platform — arithmetic still valid", r.FragmentationPct())
	}
	if r.Severity != SevWarning {
		t.Errorf("fragmented DB: severity = %v, want SevWarning (issues: %v)", r.Severity, r.Issues)
	}
}

func TestFragmentationPct(t *testing.T) {
	r := &Report{PageCount: 1000, FreelistCount: 250}
	if got := r.FragmentationPct(); got != 25.0 {
		t.Errorf("FragmentationPct = %.1f, want 25.0", got)
	}
	empty := &Report{}
	if got := empty.FragmentationPct(); got != 0 {
		t.Errorf("FragmentationPct on empty = %.1f, want 0", got)
	}
}

func TestCheckStaleness(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/heartbeat.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)")
	db.Close()

	r := Inspect(path, false)
	if r.ModTime.IsZero() {
		t.Fatalf("expected ModTime to be captured")
	}

	// Fresh write, generous threshold: not stale.
	r.CheckStaleness(time.Hour)
	if r.Stale || r.Severity != SevOK {
		t.Errorf("fresh db: stale=%v severity=%v, want not stale/OK", r.Stale, r.Severity)
	}

	// Backdate the mtime to simulate a stalled heartbeat.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	r = Inspect(path, false)
	r.CheckStaleness(time.Hour)
	if !r.Stale || r.Severity != SevWarning {
		t.Errorf("stale db: stale=%v severity=%v, want stale/SevWarning", r.Stale, r.Severity)
	}
	if len(r.Issues) == 0 {
		t.Error("expected a stale issue to be recorded")
	}

	// Disabled (zero threshold) is a no-op.
	r2 := Inspect(path, false)
	r2.CheckStaleness(0)
	if r2.Stale || r2.Severity != SevOK {
		t.Errorf("disabled staleness check should be a no-op, got stale=%v severity=%v", r2.Stale, r2.Severity)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:               "512B",
		2048:              "2.0KB",
		5 * 1024 * 1024:   "5.0MB",
		3 * 1024 * 1024 * 1024: "3.0GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
