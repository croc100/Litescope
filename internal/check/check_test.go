package check

import (
	"os"
	"path/filepath"
	"testing"

	"database/sql"
	_ "modernc.org/sqlite"
)

// makeDB creates a temporary SQLite file with the given SQL and returns its path.
func makeDB(t *testing.T, ddl string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	db, err := sql.Open("sqlite", f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("setup DDL: %v", err)
	}
	return f.Name()
}

func TestCheck_IntegrityOK(t *testing.T) {
	path := makeDB(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)

	r, err := Check(path, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IntegrityOK {
		t.Errorf("expected IntegrityOK=true, errors: %v", r.IntegrityErrors)
	}
	if !r.Passed {
		t.Error("expected Passed=true")
	}
}

func TestCheck_SchemaMatch(t *testing.T) {
	ddl := `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`
	ref := makeDB(t, ddl)
	bak := makeDB(t, ddl)

	r, err := Check(bak, ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SchemaOK == nil || !*r.SchemaOK {
		t.Errorf("expected SchemaOK=true, diff: %+v", r.SchemaDiff)
	}
	if !r.Passed {
		t.Error("expected Passed=true")
	}
}

func TestCheck_SchemaMismatch(t *testing.T) {
	ref := makeDB(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
	bak := makeDB(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, extra TEXT)`)

	r, err := Check(bak, ref, false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SchemaOK == nil || *r.SchemaOK {
		t.Error("expected SchemaOK=false due to extra column")
	}
	if r.Passed {
		t.Error("expected Passed=false")
	}
}

func TestCheck_DataRowCounts(t *testing.T) {
	ddl := `CREATE TABLE items (id INTEGER PRIMARY KEY, val TEXT)`
	ref := makeDB(t, ddl)
	bak := makeDB(t, ddl)

	// Insert same rows in both
	for _, path := range []string{ref, bak} {
		db, _ := sql.Open("sqlite", path)
		db.Exec(`INSERT INTO items VALUES (1,'a'),(2,'b')`)
		db.Close()
	}

	r, err := Check(bak, ref, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.DataOK == nil || !*r.DataOK {
		t.Errorf("expected DataOK=true, tables: %+v", r.Tables)
	}
}

func TestCheck_DataRowMismatch(t *testing.T) {
	ddl := `CREATE TABLE items (id INTEGER PRIMARY KEY, val TEXT)`
	ref := makeDB(t, ddl)
	bak := makeDB(t, ddl)

	refDB, _ := sql.Open("sqlite", ref)
	refDB.Exec(`INSERT INTO items VALUES (1,'a'),(2,'b'),(3,'c')`)
	refDB.Close()

	bakDB, _ := sql.Open("sqlite", bak)
	bakDB.Exec(`INSERT INTO items VALUES (1,'a')`)
	bakDB.Close()

	r, err := Check(bak, ref, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.DataOK == nil || *r.DataOK {
		t.Error("expected DataOK=false due to row count mismatch")
	}
	if r.Passed {
		t.Error("expected Passed=false")
	}
}

func TestCheck_NoReference(t *testing.T) {
	path := makeDB(t, `CREATE TABLE x (id INTEGER)`)

	r, err := Check(path, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.SchemaOK != nil {
		t.Error("SchemaOK should be nil when no reference given")
	}
}

func TestCheck_FileNotFound(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does_not_exist.db")
	_, err := Check(missing, "", false)
	if err == nil {
		t.Error("expected error for missing file")
	}
}
