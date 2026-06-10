package validate

import (
	"testing"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
)

// ── Validate ──────────────────────────────────────────────────────────────────

func TestValidate_Passes(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{
				Name:         "users",
				AddedColumns: []schema.Column{{Name: "verified_at", Type: "TEXT"}},
			},
		},
	}
	spec := &Spec{
		Schema: SchemaSpec{
			ModifiedTables: []TableChange{
				{
					Table:        "users",
					AddedColumns: []ColumnSpec{{Name: "verified_at", Type: "TEXT"}},
				},
			},
		},
	}

	r := Validate(d, spec)

	if !r.Passed {
		t.Fatalf("expected Passed=true, got false; unexpected=%v missing=%v", r.Unexpected, r.Missing)
	}
	if len(r.Confirmed) != 1 {
		t.Errorf("expected 1 confirmed change, got %d", len(r.Confirmed))
	}
}

func TestValidate_UnexpectedChange(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{Name: "users", AddedColumns: []schema.Column{{Name: "verified_at", Type: "TEXT"}}},
			{Name: "sessions", Removed: true},
		},
	}
	spec := &Spec{
		Schema: SchemaSpec{
			ModifiedTables: []TableChange{
				{Table: "users", AddedColumns: []ColumnSpec{{Name: "verified_at"}}},
			},
		},
	}

	r := Validate(d, spec)

	if r.Passed {
		t.Fatal("expected Passed=false due to unexpected table removal")
	}
	if len(r.Unexpected) != 1 || r.Unexpected[0].Table != "sessions" {
		t.Errorf("expected sessions in unexpected, got %v", r.Unexpected)
	}
}

func TestValidate_MissingChange(t *testing.T) {
	d := &diff.Result{Schema: []diff.TableDiff{}}
	spec := &Spec{
		Schema: SchemaSpec{
			AddedTables: []string{"audit_logs"},
		},
	}

	r := Validate(d, spec)

	if r.Passed {
		t.Fatal("expected Passed=false due to missing table addition")
	}
	if len(r.Missing) != 1 || r.Missing[0].Table != "audit_logs" {
		t.Errorf("expected audit_logs in missing, got %v", r.Missing)
	}
}

func TestValidate_NoChanges(t *testing.T) {
	d := &diff.Result{}
	spec := &Spec{}

	r := Validate(d, spec)

	if !r.Passed {
		t.Fatal("empty diff against empty spec should pass")
	}
}

func TestValidate_TableAddedAndRemoved(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{Name: "new_table", Added: true},
			{Name: "old_table", Removed: true},
		},
	}
	spec := &Spec{
		Schema: SchemaSpec{
			AddedTables:   []string{"new_table"},
			RemovedTables: []string{"old_table"},
		},
	}

	r := Validate(d, spec)

	if !r.Passed {
		t.Fatalf("expected pass, unexpected=%v missing=%v", r.Unexpected, r.Missing)
	}
	if len(r.Confirmed) != 2 {
		t.Errorf("expected 2 confirmed, got %d", len(r.Confirmed))
	}
}

func TestValidate_IndexChanges(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{
				Name:           "users",
				AddedIndexes:   []schema.Index{{Name: "idx_users_email"}},
				RemovedIndexes: []schema.Index{{Name: "idx_users_old"}},
			},
		},
	}
	spec := &Spec{
		Schema: SchemaSpec{
			ModifiedTables: []TableChange{
				{
					Table:          "users",
					AddedIndexes:   []IndexSpec{{Name: "idx_users_email"}},
					RemovedIndexes: []IndexSpec{{Name: "idx_users_old"}},
				},
			},
		},
	}

	r := Validate(d, spec)

	if !r.Passed {
		t.Fatalf("expected pass, unexpected=%v missing=%v", r.Unexpected, r.Missing)
	}
}

// ── SpecFromDiff (init) ────────────────────────────────────────────────────────

func TestSpecFromDiff_RoundTrip(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{Name: "logs", Added: true},
			{Name: "users", AddedColumns: []schema.Column{{Name: "bio", Type: "TEXT"}}},
		},
	}

	spec := SpecFromDiff(d, "test migration")
	r := Validate(d, spec)

	if !r.Passed {
		t.Fatalf("SpecFromDiff round-trip failed: unexpected=%v missing=%v", r.Unexpected, r.Missing)
	}
}
