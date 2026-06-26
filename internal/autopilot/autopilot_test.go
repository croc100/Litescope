package autopilot

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/croc100/litescope/internal/snapshot"

	_ "modernc.org/sqlite"
)

// newDB creates a schema with a foreign key that has no covering index, so the
// advisor produces a safe create-index action.
func newDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE books (
			id INTEGER PRIMARY KEY,
			author_id INTEGER REFERENCES authors(id),
			title TEXT
		);
		INSERT INTO authors (name) VALUES ('a');
		INSERT INTO books (author_id, title) VALUES (1, 'x');`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func indexExists(t *testing.T, path, name string) bool {
	t.Helper()
	db, _ := sql.Open("sqlite", path)
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n)
	return n > 0
}

func find(plan *Plan, kind string) (Action, bool) {
	for _, a := range plan.Actions {
		if a.Kind == kind {
			return a, true
		}
	}
	return Action{}, false
}

func TestBuildPlanIncludesMaintenanceAndIndex(t *testing.T) {
	path := newDB(t)
	plan, err := BuildPlan(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := find(plan, "analyze"); !ok {
		t.Error("expected an analyze action")
	}
	if _, ok := find(plan, "optimize"); !ok {
		t.Error("expected an optimize action")
	}
	ci, ok := find(plan, "create-index")
	if !ok {
		t.Fatal("expected a create-index action for the unindexed FK")
	}
	if ci.Risk != RiskSafe {
		t.Errorf("FK index should be safe, got %q", ci.Risk)
	}
}

func TestDryRunAppliesNothing(t *testing.T) {
	path := newDB(t)
	plan, _ := BuildPlan(path, nil)
	res, err := Run(path, plan, RunOptions{Apply: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied {
		t.Error("dry-run must not apply")
	}
	for _, a := range res.Actions {
		if a.Status == "applied" {
			t.Errorf("dry-run applied %q", a.Kind)
		}
	}
	if res.Snapshot != "" {
		t.Error("dry-run must not snapshot")
	}
}

func TestApplyRunsSafeActionsAndSnapshots(t *testing.T) {
	path := newDB(t)
	plan, _ := BuildPlan(path, nil)
	res, err := Run(path, plan, RunOptions{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied {
		t.Fatal("expected applied actions")
	}
	if res.Snapshot == "" {
		t.Error("apply must take a pre-run snapshot")
	}
	// The FK index should now exist.
	if !indexExists(t, path, "idx_books_author_id") {
		t.Error("create-index action did not run")
	}
	// Snapshot must be listable.
	snaps, _ := snapshot.List(path)
	if len(snaps) == 0 {
		t.Error("no snapshot recorded")
	}
}

func TestRiskyNeedsAggressive(t *testing.T) {
	path := newDB(t)
	// Create a redundant index: idx_a(author_id) is a prefix of idx_ab(author_id,title).
	db, _ := sql.Open("sqlite", path)
	db.Exec(`CREATE INDEX idx_a ON books(author_id)`)
	db.Exec(`CREATE INDEX idx_ab ON books(author_id, title)`)
	db.Close()

	plan, _ := BuildPlan(path, nil)
	drop, ok := find(plan, "drop-index")
	if !ok {
		t.Fatal("expected a drop-index action for the redundant index")
	}
	if drop.Risk != RiskRisky {
		t.Errorf("drop-index should be risky, got %q", drop.Risk)
	}

	// Without aggressive: skipped.
	res, _ := Run(path, plan, RunOptions{Apply: true})
	for _, a := range res.Actions {
		if a.Kind == "drop-index" && a.Status != "skipped" {
			t.Errorf("risky action should be skipped without --aggressive, got %q", a.Status)
		}
	}
	if !indexExists(t, path, "idx_a") {
		t.Error("redundant index dropped without --aggressive")
	}

	// With aggressive: applied.
	plan2, _ := BuildPlan(path, nil)
	res2, _ := Run(path, plan2, RunOptions{Apply: true, Aggressive: true})
	var dropped bool
	for _, a := range res2.Actions {
		if a.Kind == "drop-index" && a.Status == "applied" {
			dropped = true
		}
	}
	if !dropped {
		t.Error("expected redundant index to be dropped with --aggressive")
	}
	if indexExists(t, path, "idx_a") {
		t.Error("redundant index still present after aggressive run")
	}
}
