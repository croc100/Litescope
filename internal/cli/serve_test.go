package cli

import (
	"database/sql"
	"path/filepath"
	"testing"

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
