package fleet

import (
	"testing"

	"github.com/croc100/litescope/internal/schema"
)

// buildConvergeFleet creates a fleet with a canonical majority and two drift
// clusters, returning the dbs slice and the canonical schema.
func buildConvergeFleet(t *testing.T, dir string) ([]Database, *schema.Schema) {
	t.Helper()
	canonicalDDL := []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)",
		"CREATE TABLE audit (id INTEGER PRIMARY KEY, act TEXT)",
	}
	// 3 canonical
	for _, n := range []string{"c1", "c2", "c3"} {
		mkDB(t, dir+"/"+n+".db", canonicalDDL...)
	}
	// 2 missing the audit table (a missed migration) — converge = CREATE TABLE (safe)
	for _, n := range []string{"m1", "m2"} {
		mkDB(t, dir+"/"+n+".db", "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)")
	}
	// 1 with an extra hotfix column — converge = rebuild dropping it (destructive)
	mkDB(t, dir+"/h1.db",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, temp_col TEXT)",
		"CREATE TABLE audit (id INTEGER PRIMARY KEY, act TEXT)")

	var dbs []Database
	for _, n := range []string{"c1", "c2", "c3", "m1", "m2", "h1"} {
		dbs = append(dbs, Database{Name: n, DSN: dir + "/" + n + ".db"})
	}
	canonical := loadSchema(t, dir+"/c1.db")
	return dbs, canonical
}

func TestPlanConvergence(t *testing.T) {
	dir := t.TempDir()
	dbs, canonical := buildConvergeFleet(t, dir)

	plan, err := PlanConvergence(dbs, canonical, 4)
	if err != nil {
		t.Fatal(err)
	}

	if plan.AlreadyOK != 3 {
		t.Errorf("AlreadyOK = %d, want 3", plan.AlreadyOK)
	}
	if plan.TotalToConverge != 3 {
		t.Errorf("TotalToConverge = %d, want 3 (2 missing-table + 1 hotfix)", plan.TotalToConverge)
	}
	if len(plan.Clusters) != 2 {
		t.Fatalf("clusters = %d, want 2", len(plan.Clusters))
	}

	// Find the missing-table cluster (safe) and the hotfix cluster (destructive).
	var sawSafe, sawDestructive bool
	for _, c := range plan.Clusters {
		if c.Statements == 0 {
			t.Errorf("cluster %s has no statements", c.ClusterID)
		}
		if c.Destructive {
			sawDestructive = true
		} else {
			sawSafe = true
		}
	}
	if !sawSafe {
		t.Errorf("expected a non-destructive cluster (missing table → CREATE TABLE)")
	}
	if !sawDestructive {
		t.Errorf("expected a destructive cluster (hotfix column drop)")
	}
	if !plan.HasDestructive() {
		t.Errorf("HasDestructive() = false, want true")
	}
}

func TestPlanConvergence_DefaultCanonicalIsLargest(t *testing.T) {
	dir := t.TempDir()
	dbs, _ := buildConvergeFleet(t, dir)

	// nil canonical → use the largest cluster (the 3 canonical DBs).
	plan, err := PlanConvergence(dbs, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AlreadyOK != 3 {
		t.Errorf("AlreadyOK = %d, want 3 (largest cluster is canonical)", plan.AlreadyOK)
	}
	if plan.TotalToConverge != 3 {
		t.Errorf("TotalToConverge = %d, want 3", plan.TotalToConverge)
	}
}

func TestConverge_DryRunValidatesAndCommitsNothing(t *testing.T) {
	dir := t.TempDir()
	dbs, canonical := buildConvergeFleet(t, dir)
	plan, err := PlanConvergence(dbs, canonical, 4)
	if err != nil {
		t.Fatal(err)
	}

	report := Converge(plan, RolloutOptions{DryRun: true})
	if !report.DryRun {
		t.Errorf("report.DryRun = false")
	}
	if report.Halted {
		t.Errorf("dry-run should never halt")
	}
	applied, failed, _ := report.Counts()
	if applied != 3 || failed != 0 {
		t.Errorf("dry-run: applied=%d failed=%d, want 3/0", applied, failed)
	}

	// Nothing should have changed: the drifted DBs still drift.
	after, err := PlanConvergence(dbs, canonical, 4)
	if err != nil {
		t.Fatal(err)
	}
	if after.TotalToConverge != 3 {
		t.Errorf("after dry-run TotalToConverge = %d, want 3 (no changes committed)", after.TotalToConverge)
	}
}

func TestConverge_RealApplyMakesFleetUniform(t *testing.T) {
	dir := t.TempDir()
	dbs, canonical := buildConvergeFleet(t, dir)
	plan, err := PlanConvergence(dbs, canonical, 4)
	if err != nil {
		t.Fatal(err)
	}

	report := Converge(plan, RolloutOptions{NoBackup: true})
	applied, failed, _ := report.Counts()
	if failed != 0 {
		t.Fatalf("convergence had %d failure(s): %+v", failed, report.Results)
	}
	if applied != 3 {
		t.Errorf("applied = %d, want 3", applied)
	}

	// The whole fleet must now be uniform.
	fp := Fingerprint(dbs, 4)
	if len(fp.Clusters) != 1 {
		t.Errorf("after convergence: %d clusters, want 1 (uniform)", len(fp.Clusters))
	}
}

func TestConverge_CanaryStopsEarly(t *testing.T) {
	dir := t.TempDir()
	dbs, canonical := buildConvergeFleet(t, dir)
	plan, err := PlanConvergence(dbs, canonical, 4)
	if err != nil {
		t.Fatal(err)
	}

	report := Converge(plan, RolloutOptions{Canary: 1, NoBackup: true})
	applied, _, held := report.Counts()
	if applied != 1 {
		t.Errorf("canary applied = %d, want 1", applied)
	}
	if held != 2 {
		t.Errorf("canary held = %d, want 2", held)
	}
}
