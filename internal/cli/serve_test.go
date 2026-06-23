package cli

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/croc100/litescope/internal/fleet"
	_ "modernc.org/sqlite"
)

func makeServeTestDB(t *testing.T) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE widgets(id INTEGER PRIMARY KEY, name TEXT);
		INSERT INTO widgets(name) VALUES('a'),('b'),('c');
		CREATE TABLE meta(k TEXT);`); err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestListTables(t *testing.T) {
	dsn := makeServeTestDB(t)
	tables, err := listTables(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 2 {
		t.Fatalf("want 2 tables, got %d: %+v", len(tables), tables)
	}
	// Ordered by name: meta, widgets.
	if tables[0].Name != "meta" || tables[1].Name != "widgets" {
		t.Fatalf("unexpected order: %+v", tables)
	}
	if tables[1].Rows != 3 {
		t.Fatalf("widgets row count = %d, want 3", tables[1].Rows)
	}
}

func TestRunReadOnlyQuery_Select(t *testing.T) {
	dsn := makeServeTestDB(t)
	res, err := runReadOnlyQuery(dsn, "SELECT name FROM widgets ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "name" {
		t.Fatalf("columns = %+v", res.Columns)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}
}

func TestRunReadOnlyQuery_BlocksWrite(t *testing.T) {
	dsn := makeServeTestDB(t)
	if _, err := runReadOnlyQuery(dsn, "DELETE FROM widgets"); err == nil {
		t.Fatal("expected write to be rejected on a read-only connection")
	}
	// The data must be untouched.
	res, err := runReadOnlyQuery(dsn, "SELECT count(*) FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Rows[0][0]; got != int64(3) {
		t.Fatalf("row count after blocked delete = %v, want 3", got)
	}
}

func TestRunReadOnlyQuery_RejectsRemote(t *testing.T) {
	if _, err := runReadOnlyQuery("turso://tok@org/db", "SELECT 1"); err == nil {
		t.Fatal("expected remote DSN to be rejected")
	}
	if _, err := listTables("d1://tok@acct/dbid"); err == nil {
		t.Fatal("expected remote DSN to be rejected")
	}
}

func TestRunReadOnlyQuery_EmptyRejected(t *testing.T) {
	dsn := makeServeTestDB(t)
	if _, err := runReadOnlyQuery(dsn, "   "); err == nil {
		t.Fatal("expected empty query to be rejected")
	}
}

func TestBrowseTable_PaginateAndSort(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "b.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE nums(id INTEGER PRIMARY KEY, v INTEGER);`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 250; i++ {
		if _, err := db.Exec(`INSERT INTO nums(v) VALUES(?)`, 1000-i); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	// First page, default order (insertion).
	r, err := browseTable(dsn, "nums", "", "", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 250 {
		t.Fatalf("total = %d, want 250", r.Total)
	}
	if len(r.Rows) != 100 {
		t.Fatalf("page rows = %d, want 100", len(r.Rows))
	}

	// Third page should hold the remaining 50.
	r, err = browseTable(dsn, "nums", "", "", 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 50 {
		t.Fatalf("last page rows = %d, want 50", len(r.Rows))
	}

	// Sort ascending by v: smallest v (=750) is row with id 250.
	r, err = browseTable(dsn, "nums", "v", "asc", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Rows[0][1]; got != int64(750) {
		t.Fatalf("min v = %v, want 750", got)
	}
	if r.OrderBy != "v" || r.Dir != "asc" {
		t.Fatalf("order metadata = %q/%q", r.OrderBy, r.Dir)
	}

	// Limit is capped at the max.
	r, err = browseTable(dsn, "nums", "", "", 99999, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Limit != serveBrowseMaxLimit {
		t.Fatalf("limit = %d, want cap %d", r.Limit, serveBrowseMaxLimit)
	}
}

func TestBrowseTable_RejectsInjection(t *testing.T) {
	dsn := makeServeTestDB(t)
	if _, err := browseTable(dsn, "widgets", "name); DROP TABLE widgets;--", "asc", 10, 0); err == nil {
		t.Fatal("expected unknown sort column to be rejected")
	}
	if _, err := browseTable(dsn, "no_such_table", "", "", 10, 0); err == nil {
		t.Fatal("expected unknown table to be rejected")
	}
}

func TestSchemaGraph(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "erd.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT);
		CREATE TABLE orders(id INTEGER PRIMARY KEY, user_id INTEGER REFERENCES users(id), total REAL);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	g, err := schemaGraph(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Tables) != 2 {
		t.Fatalf("want 2 tables, got %d", len(g.Tables))
	}
	if len(g.Edges) != 1 {
		t.Fatalf("want 1 edge, got %d: %+v", len(g.Edges), g.Edges)
	}
	e := g.Edges[0]
	if e.From != "orders" || e.To != "users" || e.Column != "user_id" {
		t.Fatalf("unexpected edge: %+v", e)
	}
	// The PK and FK flags must be set on the right columns.
	var orders *struct {
		pk, fk bool
	}
	for _, tb := range g.Tables {
		if tb.Name != "orders" {
			continue
		}
		orders = &struct{ pk, fk bool }{}
		for _, c := range tb.Columns {
			if c.Name == "id" && c.PK {
				orders.pk = true
			}
			if c.Name == "user_id" && c.FK {
				orders.fk = true
			}
		}
	}
	if orders == nil || !orders.pk || !orders.fk {
		t.Fatalf("orders PK/FK flags wrong: %+v", orders)
	}
}

func TestSchemaGraph_RejectsRemote(t *testing.T) {
	if _, err := schemaGraph("turso://tok@org/db"); err == nil {
		t.Fatal("expected remote DSN to be rejected")
	}
}

// fpTestDB creates a database with the given DDL and returns its DSN.
func fpTestDB(t *testing.T, ddl string) string {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "fp.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestAnnotateSchemaFingerprint_Canonical(t *testing.T) {
	canon := `CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT);`
	a := fpTestDB(t, canon)
	b := fpTestDB(t, canon)
	dbs := []fleet.Database{{Name: "a", DSN: a}, {Name: "b", DSN: b}}
	fp := fleet.Fingerprint(dbs, 0)

	g, err := schemaGraph(a)
	if err != nil {
		t.Fatal(err)
	}
	annotateSchemaFingerprint(g, "a", fp)
	if g.Fingerprint == nil || !g.Fingerprint.IsCanonical {
		t.Fatalf("expected canonical fingerprint, got %+v", g.Fingerprint)
	}
	if g.Fingerprint.ClusterCount != 2 || g.Fingerprint.FleetTotal != 2 {
		t.Fatalf("cluster/fleet counts wrong: %+v", g.Fingerprint)
	}
}

func TestAnnotateSchemaFingerprint_Drift(t *testing.T) {
	// Two canonical DBs form the reference; the drifted one adds a column, an
	// extra table, and is missing a column that canonical has.
	canon := `CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT, country TEXT);`
	drifted := `CREATE TABLE users(id INTEGER PRIMARY KEY, email TEXT, phone TEXT);
		CREATE TABLE audit(id INTEGER PRIMARY KEY, msg TEXT);`
	c1 := fpTestDB(t, canon)
	c2 := fpTestDB(t, canon)
	d := fpTestDB(t, drifted)
	dbs := []fleet.Database{{Name: "c1", DSN: c1}, {Name: "c2", DSN: c2}, {Name: "d", DSN: d}}
	fp := fleet.Fingerprint(dbs, 0)

	g, err := schemaGraph(d)
	if err != nil {
		t.Fatal(err)
	}
	annotateSchemaFingerprint(g, "d", fp)
	if g.Fingerprint == nil || g.Fingerprint.IsCanonical {
		t.Fatalf("expected drift fingerprint, got %+v", g.Fingerprint)
	}

	var ghost, extra bool
	var added, missing bool
	for _, tb := range g.Tables {
		if tb.Ghost {
			ghost = true // not expected here (no whole table is missing)
		}
		if tb.Name == "audit" && tb.Drift == "added" {
			extra = true
		}
		if tb.Name == "users" {
			for _, c := range tb.Columns {
				if c.Name == "phone" && c.Drift == "added" {
					added = true
				}
				if c.Name == "country" && c.Drift == "missing" {
					missing = true
				}
			}
		}
	}
	if ghost {
		t.Fatal("did not expect a ghost table")
	}
	if !extra {
		t.Fatal("expected audit table marked as extra (drift=added)")
	}
	if !added {
		t.Fatal("expected users.phone marked drift=added")
	}
	if !missing {
		t.Fatal("expected users.country marked drift=missing (present in canonical)")
	}
	if g.Fingerprint.DriftColumns < 2 {
		t.Fatalf("expected >=2 drift columns, got %d", g.Fingerprint.DriftColumns)
	}
}
