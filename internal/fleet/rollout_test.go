package fleet

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// hasColumn reports whether table has the named column.
func hasColumn(t *testing.T, dbPath, table, col string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == col {
			return true
		}
	}
	return false
}

func threeLocalDBs(t *testing.T) []Database {
	t.Helper()
	ddl := `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);`
	return []Database{
		{Name: "a", DSN: makeDB(t, "a.db", ddl)},
		{Name: "b", DSN: makeDB(t, "b.db", ddl)},
		{Name: "c", DSN: makeDB(t, "c.db", ddl)},
	}
}

func TestRollout_AllApplied(t *testing.T) {
	dbs := threeLocalDBs(t)
	report := Rollout(dbs, `ALTER TABLE users ADD COLUMN email TEXT;`, RolloutOptions{NoBackup: true})

	applied, failed, skipped := report.Counts()
	if applied != 3 || failed != 0 || skipped != 0 {
		t.Fatalf("counts = %d/%d/%d, want 3/0/0", applied, failed, skipped)
	}
	if report.Halted {
		t.Error("should not be halted")
	}
	for _, db := range dbs {
		if !hasColumn(t, db.DSN, "users", "email") {
			t.Errorf("%s: email column not applied", db.Name)
		}
	}
}

func TestRollout_HaltsOnFailure(t *testing.T) {
	dbs := threeLocalDBs(t)
	// Break the middle database so the migration fails on it.
	db, _ := sql.Open("sqlite", dbs[1].DSN)
	db.Exec(`ALTER TABLE users ADD COLUMN email TEXT;`) // pre-existing column → ADD COLUMN fails
	db.Close()

	report := Rollout(dbs, `ALTER TABLE users ADD COLUMN email TEXT;`, RolloutOptions{NoBackup: true})

	if !report.Halted {
		t.Fatal("expected rollout to halt")
	}
	applied, failed, skipped := report.Counts()
	if applied != 1 || failed != 1 || skipped != 1 {
		t.Fatalf("counts = %d/%d/%d, want 1/1/1", applied, failed, skipped)
	}
	// States in order: a applied, b failed, c skipped.
	if report.Results[0].State != StateApplied {
		t.Errorf("a = %s, want applied", report.Results[0].State)
	}
	if report.Results[1].State != StateFailed {
		t.Errorf("b = %s, want failed", report.Results[1].State)
	}
	if report.Results[2].State != StateSkipped {
		t.Errorf("c = %s, want skipped", report.Results[2].State)
	}
	// The skipped database must be untouched.
	if hasColumn(t, dbs[2].DSN, "users", "email") {
		t.Error("c should not have been migrated after halt")
	}
}

func TestRollout_DryRunValidatesAllAndCommitsNothing(t *testing.T) {
	dbs := threeLocalDBs(t)
	// Break the middle one. Dry-run should still validate a and c, and NOT halt.
	db, _ := sql.Open("sqlite", dbs[1].DSN)
	db.Exec(`ALTER TABLE users ADD COLUMN email TEXT;`)
	db.Close()

	report := Rollout(dbs, `ALTER TABLE users ADD COLUMN email TEXT;`, RolloutOptions{DryRun: true})

	if report.Halted {
		t.Error("dry-run must never halt early")
	}
	applied, failed, _ := report.Counts()
	if applied != 2 || failed != 1 {
		t.Fatalf("dry-run counts = applied %d failed %d, want 2/1", applied, failed)
	}
	if report.Results[0].State != StateDryRun || report.Results[2].State != StateDryRun {
		t.Errorf("a/c states = %s/%s, want dry-run", report.Results[0].State, report.Results[2].State)
	}
	// Nothing committed: the healthy databases must NOT have the column.
	if hasColumn(t, dbs[0].DSN, "users", "email") {
		t.Error("dry-run committed changes to a")
	}
}

func TestRollout_Canary(t *testing.T) {
	dbs := threeLocalDBs(t)
	report := Rollout(dbs, `ALTER TABLE users ADD COLUMN email TEXT;`, RolloutOptions{Canary: 1, NoBackup: true})

	if report.Results[0].State != StateApplied {
		t.Errorf("first = %s, want applied", report.Results[0].State)
	}
	if report.Results[1].State != StateCanary || report.Results[2].State != StateCanary {
		t.Errorf("held states = %s/%s, want canary", report.Results[1].State, report.Results[2].State)
	}
	// Only the canary database was migrated.
	if !hasColumn(t, dbs[0].DSN, "users", "email") {
		t.Error("canary db a should be migrated")
	}
	if hasColumn(t, dbs[1].DSN, "users", "email") {
		t.Error("db b is beyond canary; must be untouched")
	}
}

func TestRollout_EmptyMigration(t *testing.T) {
	dbs := threeLocalDBs(t)
	report := Rollout(dbs, `-- just a comment, no statements`, RolloutOptions{NoBackup: true})
	if !report.Halted {
		t.Error("empty migration should halt as a failure")
	}
	if _, failed, _ := report.Counts(); failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

func TestProviderOf(t *testing.T) {
	cases := map[string]string{
		"prod.db":                  "local",
		"./data/x.db":              "local",
		"turso://tok@org/db":       "turso",
		"d1://tok@acct/uuid":       "d1",
	}
	for dsn, want := range cases {
		if got := providerOf(dsn); got != want {
			t.Errorf("providerOf(%q) = %q, want %q", dsn, got, want)
		}
	}
}
