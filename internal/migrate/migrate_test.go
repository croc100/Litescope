package migrate

import (
	"strings"
	"testing"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
)

func TestGenerate_AddTable(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{{Name: "logs", Added: true}},
	}
	ns := &schema.Schema{
		Tables: []schema.Table{
			{Name: "logs", Columns: []schema.Column{
				{Name: "id", Type: "INTEGER", PK: 1},
				{Name: "message", Type: "TEXT"},
			}},
		},
	}

	m := Generate(d, ns)

	if m.HasWarnings() {
		t.Errorf("unexpected warnings: %v", m.Warnings)
	}
	sql := m.SQL()
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Errorf("expected CREATE TABLE, got:\n%s", sql)
	}
	if !strings.Contains(sql, `"logs"`) {
		t.Errorf("expected table name 'logs', got:\n%s", sql)
	}
}

func TestGenerate_DropTable(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{{Name: "sessions", Removed: true}},
	}

	m := Generate(d, &schema.Schema{})

	if !m.HasWarnings() {
		t.Error("expected warning for DROP TABLE")
	}
	sql := m.SQL()
	if !strings.Contains(sql, "DROP TABLE") {
		t.Errorf("expected DROP TABLE, got:\n%s", sql)
	}
}

func TestGenerate_AddColumn(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{
				Name:         "users",
				AddedColumns: []schema.Column{{Name: "verified_at", Type: "TEXT"}},
			},
		},
	}

	m := Generate(d, &schema.Schema{
		Tables: []schema.Table{
			{Name: "users", Columns: []schema.Column{
				{Name: "id", Type: "INTEGER", PK: 1},
				{Name: "name", Type: "TEXT"},
				{Name: "verified_at", Type: "TEXT"},
			}},
		},
	})

	if m.HasWarnings() {
		t.Errorf("ADD COLUMN should not produce warnings, got: %v", m.Warnings)
	}
	sql := m.SQL()
	if !strings.Contains(sql, "ALTER TABLE") || !strings.Contains(sql, "ADD COLUMN") {
		t.Errorf("expected ALTER TABLE ... ADD COLUMN, got:\n%s", sql)
	}
	if !strings.Contains(sql, `"verified_at"`) {
		t.Errorf("expected column name in SQL, got:\n%s", sql)
	}
}

func TestGenerate_RemoveColumn_TriggersRebuild(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{
				Name:           "users",
				RemovedColumns: []schema.Column{{Name: "legacy_id", Type: "INTEGER"}},
			},
		},
	}
	ns := &schema.Schema{
		Tables: []schema.Table{
			{Name: "users", Columns: []schema.Column{
				{Name: "id", Type: "INTEGER", PK: 1},
				{Name: "name", Type: "TEXT"},
			}},
		},
	}

	m := Generate(d, ns)

	if !m.HasWarnings() {
		t.Error("expected warning for column removal (data loss)")
	}
	sql := m.SQL()
	// rebuild pattern: CREATE tmp → INSERT → DROP → RENAME
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("expected CREATE TABLE (rebuild pattern)")
	}
	if !strings.Contains(sql, "INSERT INTO") {
		t.Error("expected INSERT INTO (rebuild pattern)")
	}
	if !strings.Contains(sql, "DROP TABLE") {
		t.Error("expected DROP TABLE (rebuild pattern)")
	}
	if !strings.Contains(sql, "RENAME TO") {
		t.Error("expected RENAME TO (rebuild pattern)")
	}
}

func TestGenerate_AddIndex(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{
				Name:         "users",
				AddedIndexes: []schema.Index{{Name: "idx_email", SQL: "CREATE INDEX idx_email ON users(email)"}},
			},
		},
	}

	m := Generate(d, &schema.Schema{
		Tables: []schema.Table{{Name: "users", Columns: []schema.Column{{Name: "id", Type: "INTEGER", PK: 1}}}},
	})

	sql := m.SQL()
	if !strings.Contains(sql, "CREATE INDEX idx_email") {
		t.Errorf("expected index creation, got:\n%s", sql)
	}
}

func TestGenerate_RemoveIndex(t *testing.T) {
	d := &diff.Result{
		Schema: []diff.TableDiff{
			{
				Name:           "users",
				RemovedIndexes: []schema.Index{{Name: "idx_old"}},
			},
		},
	}

	m := Generate(d, &schema.Schema{
		Tables: []schema.Table{{Name: "users"}},
	})

	sql := m.SQL()
	if !strings.Contains(sql, "DROP INDEX") {
		t.Errorf("expected DROP INDEX, got:\n%s", sql)
	}
}

func TestGenerate_NoChanges(t *testing.T) {
	m := Generate(&diff.Result{}, &schema.Schema{})

	if len(m.Statements) != 0 {
		t.Errorf("expected 0 statements for empty diff, got %d", len(m.Statements))
	}
	if m.SQL() != "" {
		t.Errorf("expected empty SQL for empty diff")
	}
}

func TestQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"users", `"users"`},
		{"my table", `"my table"`},
		{`weird"name`, `"weird""name"`},
	}
	for _, tc := range cases {
		if got := quote(tc.in); got != tc.want {
			t.Errorf("quote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
