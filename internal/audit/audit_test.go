package audit

import (
	"path/filepath"
	"testing"
)

func TestRecordAndRead(t *testing.T) {
	t.Setenv("LITESCOPE_AUDIT_LOG", filepath.Join(t.TempDir(), "audit.log"))
	t.Setenv("LITESCOPE_OPERATOR", "alice")

	for _, a := range []string{"migrate.apply", "fleet.converge", "sql.write"} {
		if err := Record(Entry{Action: a, Target: "app.db", Summary: a + " ran"}); err != nil {
			t.Fatal(err)
		}
	}

	// newest first, operator + outcome defaulted
	all, err := Read(0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 entries, got %d", len(all))
	}
	if all[0].Action != "sql.write" {
		t.Fatalf("expected newest first, got %s", all[0].Action)
	}
	if all[0].Operator != "alice" || all[0].Outcome != "ok" {
		t.Fatalf("defaults not applied: %+v", all[0])
	}

	// action filter
	only, _ := Read(0, "", "fleet.converge")
	if len(only) != 1 || only[0].Action != "fleet.converge" {
		t.Fatalf("action filter failed: %+v", only)
	}

	// limit
	lim, _ := Read(2, "", "")
	if len(lim) != 2 {
		t.Fatalf("limit failed: got %d", len(lim))
	}
}

func TestReadMissingLogIsEmpty(t *testing.T) {
	t.Setenv("LITESCOPE_AUDIT_LOG", filepath.Join(t.TempDir(), "nope.log"))
	all, err := Read(0, "", "")
	if err != nil || len(all) != 0 {
		t.Fatalf("missing log should be empty, got %v err=%v", all, err)
	}
}
