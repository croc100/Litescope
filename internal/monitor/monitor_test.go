package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/croc100/litescope/internal/schema"
)

func sampleSchema(tables ...string) *schema.Schema {
	s := &schema.Schema{}
	for _, name := range tables {
		s.Tables = append(s.Tables, schema.Table{
			Name:    name,
			Columns: []schema.Column{{Name: "id", Type: "INTEGER", PK: 1}},
		})
	}
	return s
}

// ── Snapshot ──────────────────────────────────────────────────────────────────

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")

	orig := &Snapshot{
		Source:     "test.db",
		CapturedAt: time.Now().UTC().Truncate(time.Second),
		Schema:     sampleSchema("users", "posts"),
	}

	if err := Save(path, orig); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Source != orig.Source {
		t.Errorf("Source: got %q, want %q", loaded.Source, orig.Source)
	}
	if loaded.Version != snapshotVersion {
		t.Errorf("Version: got %d, want %d", loaded.Version, snapshotVersion)
	}
	if len(loaded.Schema.Tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(loaded.Schema.Tables))
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{invalid json"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── Check / DriftResult ───────────────────────────────────────────────────────

func TestCheck_NoDrift(t *testing.T) {
	snap := &Snapshot{
		Source:     "prod.db",
		CapturedAt: time.Now().Add(-1 * time.Hour),
		Schema:     sampleSchema("users"),
	}
	current := sampleSchema("users")

	r := Check("prod.db", snap, current)

	if r.HasDrift {
		t.Errorf("expected no drift, got changes: %v", r.Changes)
	}
	if r.Source != "prod.db" {
		t.Errorf("wrong source: %q", r.Source)
	}
}

func TestCheck_TableAdded(t *testing.T) {
	snap := &Snapshot{
		Source:     "prod.db",
		CapturedAt: time.Now().Add(-1 * time.Hour),
		Schema:     sampleSchema("users"),
	}
	current := sampleSchema("users", "audit_logs") // audit_logs is new

	r := Check("prod.db", snap, current)

	if !r.HasDrift {
		t.Error("expected drift for added table")
	}
	if len(r.Changes) != 1 || r.Changes[0].Name != "audit_logs" {
		t.Errorf("unexpected changes: %+v", r.Changes)
	}
}

func TestCheck_TableRemoved(t *testing.T) {
	snap := &Snapshot{
		Source:     "prod.db",
		CapturedAt: time.Now().Add(-1 * time.Hour),
		Schema:     sampleSchema("users", "sessions"),
	}
	current := sampleSchema("users") // sessions removed

	r := Check("prod.db", snap, current)

	if !r.HasDrift {
		t.Error("expected drift for removed table")
	}
}

func TestCheck_TimestampsSet(t *testing.T) {
	before := time.Now()
	snap := &Snapshot{CapturedAt: before.Add(-1 * time.Hour), Schema: sampleSchema()}
	r := Check("x.db", snap, sampleSchema())

	if r.CheckedAt.Before(before) {
		t.Error("CheckedAt should be set to approximately now")
	}
	if r.BaselineAt != snap.CapturedAt {
		t.Error("BaselineAt should match snapshot CapturedAt")
	}
}
