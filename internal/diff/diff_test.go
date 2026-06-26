package diff

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func mk(t *testing.T, name, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return path
}

func dataFor(res *Result, table string) (DataDiff, bool) {
	for _, d := range res.Data {
		if d.Table == table {
			return d, true
		}
	}
	return DataDiff{}, false
}

func TestCompare_SchemaChanges(t *testing.T) {
	old := mk(t, "old.db", `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, legacy INTEGER);`)
	new := mk(t, "new.db", `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT);
		CREATE TABLE audit (id INTEGER PRIMARY KEY);`)

	res, err := Compare(old, new)
	if err != nil {
		t.Fatal(err)
	}
	var added, changed bool
	for _, td := range res.Schema {
		if td.Name == "audit" && td.Added {
			added = true
		}
		if td.Name == "users" && !td.Added && !td.Removed {
			changed = true
			if len(td.AddedColumns) != 1 || td.AddedColumns[0].Name != "email" {
				t.Errorf("expected added column email, got %+v", td.AddedColumns)
			}
			if len(td.RemovedColumns) != 1 || td.RemovedColumns[0].Name != "legacy" {
				t.Errorf("expected removed column legacy, got %+v", td.RemovedColumns)
			}
		}
	}
	if !added || !changed {
		t.Errorf("missing expected schema diffs: added=%v changed=%v", added, changed)
	}
}

// TestCompare_DataDeltaByPK verifies the ATTACH-based row comparison: inserted
// and deleted rows are counted across the two databases by primary key.
func TestCompare_DataDeltaByPK(t *testing.T) {
	old := mk(t, "old.db", `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);
		INSERT INTO t (id, v) VALUES (1,'a'), (2,'b'), (3,'c');`)
	new := mk(t, "new.db", `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);
		INSERT INTO t (id, v) VALUES (1,'a'), (3,'c'), (4,'d'), (5,'e');`)

	res, err := Compare(old, new)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := dataFor(res, "t")
	if !ok {
		t.Fatal("no data diff for table t")
	}
	if d.Added != 2 { // ids 4,5 are new
		t.Errorf("Added = %d, want 2", d.Added)
	}
	if d.Removed != 1 { // id 2 was deleted
		t.Errorf("Removed = %d, want 1", d.Removed)
	}
}

func TestCompare_AddedAndRemovedTablesData(t *testing.T) {
	old := mk(t, "old.db", `CREATE TABLE gone (id INTEGER PRIMARY KEY); INSERT INTO gone VALUES (1),(2);`)
	new := mk(t, "new.db", `CREATE TABLE fresh (id INTEGER PRIMARY KEY); INSERT INTO fresh VALUES (1),(2),(3);`)

	res, err := Compare(old, new)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := dataFor(res, "fresh"); !ok || d.Added != 3 {
		t.Errorf("fresh added = %+v, want Added=3", d)
	}
	if d, ok := dataFor(res, "gone"); !ok || d.Removed != 2 {
		t.Errorf("gone removed = %+v, want Removed=2", d)
	}
}
