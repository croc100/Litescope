package fleet

import (
	"database/sql"
	"os"
	"testing"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
	_ "modernc.org/sqlite"
)

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

func mkDB(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
}

func loadSchema(t *testing.T, path string) *schema.Schema {
	t.Helper()
	s, err := schema.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The defining invariant: two schemas share a fingerprint if and only if the
// diff engine reports no drift between them.
func TestHash_MatchesDriftSemantics(t *testing.T) {
	dir := t.TempDir()

	// a and b: identical schema, but columns declared in a DIFFERENT order and
	// a different table order. Must hash the same (order-independent).
	mkDB(t, dir+"/a.db",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)",
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT)",
		"CREATE INDEX idx_email ON users(email)",
	)
	mkDB(t, dir+"/b.db",
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT)",
		"CREATE TABLE users (email TEXT, id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE INDEX idx_email ON users(email)",
	)
	// c: one extra column → must hash differently AND show drift.
	mkDB(t, dir+"/c.db",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, verified INTEGER)",
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT)",
		"CREATE INDEX idx_email ON users(email)",
	)

	sa, sb, sc := loadSchema(t, dir+"/a.db"), loadSchema(t, dir+"/b.db"), loadSchema(t, dir+"/c.db")

	if Hash(sa) != Hash(sb) {
		t.Errorf("a and b are the same schema (reordered) but hashes differ")
	}
	if d := diff.CompareSchemas(sa, sb); len(d.Schema) != 0 {
		t.Errorf("a and b should have no drift, got %d changes", len(d.Schema))
	}

	if Hash(sa) == Hash(sc) {
		t.Errorf("a and c differ by a column but hashes match")
	}
	if d := diff.CompareSchemas(sa, sc); len(d.Schema) == 0 {
		t.Errorf("a and c should drift but diff found nothing")
	}
}

func TestFingerprint_Clustering(t *testing.T) {
	dir := t.TempDir()
	canonical := "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)"
	missingTable := canonical // same users, but no audit table — we'll add audit to canonical group

	// Canonical group: 3 DBs with users + audit.
	for _, n := range []string{"t1", "t2", "t3"} {
		mkDB(t, dir+"/"+n+".db", canonical, "CREATE TABLE audit (id INTEGER PRIMARY KEY, act TEXT)")
	}
	// Drift group: 2 DBs missing the audit table.
	for _, n := range []string{"t4", "t5"} {
		mkDB(t, dir+"/"+n+".db", missingTable)
	}
	// Hotfix group: 1 DB with an extra column.
	mkDB(t, dir+"/t6.db",
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT, temp_col TEXT)",
		"CREATE TABLE audit (id INTEGER PRIMARY KEY, act TEXT)")

	var dbs []Database
	for _, n := range []string{"t1", "t2", "t3", "t4", "t5", "t6"} {
		dbs = append(dbs, Database{Name: n, DSN: dir + "/" + n + ".db"})
	}

	report := Fingerprint(dbs, 4)

	if report.Total != 6 {
		t.Errorf("total = %d, want 6", report.Total)
	}
	if len(report.Unreachable) != 0 {
		t.Errorf("unreachable = %d, want 0", len(report.Unreachable))
	}
	if len(report.Clusters) != 3 {
		t.Fatalf("clusters = %d, want 3", len(report.Clusters))
	}

	// Largest cluster (3) must be canonical and first.
	if !report.Clusters[0].IsCanonical {
		t.Errorf("first cluster should be canonical")
	}
	if report.Clusters[0].Count != 3 {
		t.Errorf("canonical count = %d, want 3", report.Clusters[0].Count)
	}
	if report.Clusters[0].Drift != nil {
		t.Errorf("canonical cluster should carry no drift")
	}

	// Non-canonical clusters carry a non-empty drift vs canonical.
	for _, c := range report.Clusters[1:] {
		if c.IsCanonical {
			t.Errorf("only the first cluster may be canonical")
		}
		if len(c.Drift) == 0 {
			t.Errorf("cluster %s should report drift from canonical", c.ID)
		}
	}
}

func TestFingerprint_UnreachableBucket(t *testing.T) {
	dir := t.TempDir()
	mkDB(t, dir+"/good.db", "CREATE TABLE t (id INTEGER PRIMARY KEY)")

	// A corrupt file (not a valid SQLite database) fails on schema read →
	// unreachable bucket, not a schema cluster. (A merely *empty* DB would be a
	// valid empty-schema cluster, which is correct drift signal, not an error.)
	corrupt := dir + "/corrupt.db"
	if err := writeFile(corrupt, []byte("this is not a sqlite database at all, just garbage bytes")); err != nil {
		t.Fatal(err)
	}

	dbs := []Database{
		{Name: "good", DSN: dir + "/good.db"},
		{Name: "bad", DSN: corrupt},
	}
	report := Fingerprint(dbs, 2)

	if len(report.Unreachable) != 1 || report.Unreachable[0].Database != "bad" {
		t.Errorf("expected 'bad' in unreachable bucket, got %+v", report.Unreachable)
	}
	if report.Total != 1 {
		t.Errorf("total = %d, want 1 (only the good DB)", report.Total)
	}
}

func TestFingerprint_UniformFleet(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		mkDB(t, dir+"/"+n+".db", "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)")
	}
	var dbs []Database
	for _, n := range []string{"a", "b", "c"} {
		dbs = append(dbs, Database{Name: n, DSN: dir + "/" + n + ".db"})
	}
	report := Fingerprint(dbs, 2)
	if len(report.Clusters) != 1 {
		t.Errorf("uniform fleet should have 1 cluster, got %d", len(report.Clusters))
	}
	if !report.Clusters[0].IsCanonical {
		t.Errorf("the single cluster should be canonical")
	}
}
