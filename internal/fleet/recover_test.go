package fleet

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// makeBackup creates a healthy backup file for dbPath with a timestamped name
// matching migrate.Apply's convention, offset minutes into the past so ordering
// is deterministic.
func makeBackup(t *testing.T, dbPath string, minutesAgo int, ddl ...string) string {
	t.Helper()
	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath)
	stem := base[:len(base)-len(filepath.Ext(base))]
	ts := time.Now().Add(-time.Duration(minutesAgo) * time.Minute).Format("20060102-150405")
	bpath := filepath.Join(dir, fmt.Sprintf("%s.backup-%s.db", stem, ts))
	db, err := sql.Open("sqlite", bpath)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ddl {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("backup ddl %q: %v", s, err)
		}
	}
	db.Close()
	return bpath
}

func corrupt(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("SQLite format 3\x00 not a real database body, total garbage"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRecover_RestoresFromHealthyBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/tenant.db"
	corrupt(t, dbPath) // the live DB is broken
	makeBackup(t, dbPath, 1, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")

	dbs := []Database{{Name: "tenant", DSN: dbPath}}
	report := Recover(dbs, RecoverOptions{})

	if len(report.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(report.Results))
	}
	if report.Results[0].State != RecoverRestored {
		t.Fatalf("state = %q, want restored (detail: %s)", report.Results[0].State, report.Results[0].Detail)
	}
	// The DB must now pass an open + integrity check.
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var res string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&res); err != nil || res != "ok" {
		t.Errorf("restored DB not healthy: res=%q err=%v", res, err)
	}
}

func TestRecover_SkipsCorruptBackupUsesOlderHealthy(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/tenant.db"
	corrupt(t, dbPath)

	// Newest backup is corrupt; an older one is healthy. Recover must pick the
	// healthy older one, not the newest.
	healthyOld := makeBackup(t, dbPath, 10, "CREATE TABLE good (id INTEGER PRIMARY KEY)")
	newestCorrupt := makeBackup(t, dbPath, 1)
	corrupt(t, newestCorrupt)

	report := Recover([]Database{{Name: "tenant", DSN: dbPath}}, RecoverOptions{})
	r := report.Results[0]
	if r.State != RecoverRestored {
		t.Fatalf("state = %q, want restored", r.State)
	}
	if r.BackupPath != healthyOld {
		t.Errorf("restored from %q, want the healthy older backup %q", r.BackupPath, healthyOld)
	}
}

func TestRecover_QuarantinesWhenNoHealthyBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/tenant.db"
	corrupt(t, dbPath) // broken and no backups at all

	report := Recover([]Database{{Name: "tenant", DSN: dbPath}}, RecoverOptions{Quarantine: true})
	r := report.Results[0]
	if r.State != RecoverQuarantined {
		t.Fatalf("state = %q, want quarantined", r.State)
	}
	if got := report.Quarantine(); len(got) != 1 || got[0] != "tenant" {
		t.Errorf("Quarantine() = %v, want [tenant]", got)
	}
}

func TestRecover_HealthyDBIsNoOp(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/tenant.db"
	mkDB(t, dbPath, "CREATE TABLE t (id INTEGER PRIMARY KEY)")

	report := Recover([]Database{{Name: "tenant", DSN: dbPath}}, RecoverOptions{})
	if report.Results[0].State != RecoverHealthy {
		t.Errorf("healthy DB: state = %q, want healthy", report.Results[0].State)
	}
}

func TestRecover_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/tenant.db"
	corrupt(t, dbPath)
	makeBackup(t, dbPath, 1, "CREATE TABLE users (id INTEGER PRIMARY KEY)")

	report := Recover([]Database{{Name: "tenant", DSN: dbPath}}, RecoverOptions{DryRun: true})
	if report.Results[0].State != RecoverRestored {
		t.Errorf("dry-run state = %q, want restored (planned)", report.Results[0].State)
	}
	// DB must still be corrupt — dry-run writes nothing.
	db, _ := sql.Open("sqlite", dbPath+"?mode=ro")
	defer db.Close()
	var res string
	err := db.QueryRow("PRAGMA quick_check").Scan(&res)
	if err == nil && res == "ok" {
		t.Errorf("dry-run must not have restored the database")
	}
}

func TestConfig_QuarantineExcludedFromFilter(t *testing.T) {
	cfg := &Config{Databases: []Database{
		{Name: "a", DSN: "a.db"},
		{Name: "b", DSN: "b.db", Quarantined: true},
		{Name: "c", DSN: "c.db"},
	}}
	active := cfg.Filter("")
	if len(active) != 2 {
		t.Fatalf("Filter returned %d, want 2 (quarantined excluded)", len(active))
	}
	for _, db := range active {
		if db.Name == "b" {
			t.Errorf("quarantined db 'b' leaked into Filter result")
		}
	}
	if n := cfg.SetQuarantine([]string{"a"}, true); n != 1 {
		t.Errorf("SetQuarantine changed %d, want 1", n)
	}
	if len(cfg.Active()) != 1 {
		t.Errorf("after quarantining 'a', Active = %d, want 1", len(cfg.Active()))
	}
}
