package importer

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestImportCSV_TypesAndNulls(t *testing.T) {
	db := openTestDB(t)
	csv := "id,name,price,note\n1,apple,1.50,fresh\n2,banana,0.75,\n3,cherry,3,ripe\n"
	res, err := ImportCSV(db, strings.NewReader(csv), Options{Table: "fruit", HasHeader: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 3 {
		t.Fatalf("rows = %d, want 3", res.Rows)
	}

	want := map[string]string{"id": "INTEGER", "name": "TEXT", "price": "REAL", "note": "TEXT"}
	for _, c := range res.Columns {
		if want[c.Name] != c.Type {
			t.Errorf("col %s type = %s, want %s", c.Name, c.Type, want[c.Name])
		}
	}

	// Empty cell should be NULL, not "".
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fruit WHERE note IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("NULL notes = %d, want 1", n)
	}

	var price float64
	if err := db.QueryRow(`SELECT price FROM fruit WHERE id=1`).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if price != 1.50 {
		t.Errorf("price = %v, want 1.5", price)
	}
}

func TestImportCSV_NoHeader(t *testing.T) {
	db := openTestDB(t)
	res, err := ImportCSV(db, strings.NewReader("a,1\nb,2\n"), Options{Table: "t", HasHeader: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Columns[0].Name != "col1" || res.Columns[1].Name != "col2" {
		t.Errorf("columns = %+v, want col1/col2", res.Columns)
	}
	if res.Rows != 2 {
		t.Errorf("rows = %d, want 2", res.Rows)
	}
}

func TestImportCSV_ModeCreateConflict(t *testing.T) {
	db := openTestDB(t)
	csv := "x\n1\n"
	if _, err := ImportCSV(db, strings.NewReader(csv), Options{Table: "t", HasHeader: true}); err != nil {
		t.Fatal(err)
	}
	_, err := ImportCSV(db, strings.NewReader(csv), Options{Table: "t", HasHeader: true})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}

func TestImportCSV_Append(t *testing.T) {
	db := openTestDB(t)
	csv1 := "x\n1\n"
	csv2 := "x\n2\n3\n"
	if _, err := ImportCSV(db, strings.NewReader(csv1), Options{Table: "t", HasHeader: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportCSV(db, strings.NewReader(csv2), Options{Table: "t", Mode: ModeAppend, HasHeader: true}); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow("SELECT COUNT(*) FROM t").Scan(&n)
	if n != 3 {
		t.Errorf("rows after append = %d, want 3", n)
	}
}

func TestImportCSV_Replace(t *testing.T) {
	db := openTestDB(t)
	if _, err := ImportCSV(db, strings.NewReader("x\n1\n"), Options{Table: "t", HasHeader: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportCSV(db, strings.NewReader("y\n9\n"), Options{Table: "t", Mode: ModeReplace, HasHeader: true}); err != nil {
		t.Fatal(err)
	}
	var col string
	db.QueryRow("SELECT name FROM pragma_table_info('t') LIMIT 1").Scan(&col)
	if col != "y" {
		t.Errorf("after replace column = %q, want y", col)
	}
}

func TestImportJSON(t *testing.T) {
	db := openTestDB(t)
	js := `[{"id":1,"name":"a","active":true},{"id":2,"name":"b","tags":["x","y"]}]`
	res, err := ImportJSON(db, strings.NewReader(js), Options{Table: "j"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 2 {
		t.Fatalf("rows = %d, want 2", res.Rows)
	}
	// bool -> stored as 1/0 in an INTEGER column.
	var active int
	if err := db.QueryRow(`SELECT active FROM j WHERE id=1`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Errorf("active = %d, want 1", active)
	}
	// nested array kept as JSON text.
	var tags sql.NullString
	if err := db.QueryRow(`SELECT tags FROM j WHERE id=2`).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if !tags.Valid || !strings.Contains(tags.String, "x") {
		t.Errorf("tags = %v, want JSON array text", tags)
	}
}

func TestDedupeHeaders(t *testing.T) {
	got := dedupeHeaders([]string{"a", "a", "", "a"})
	want := []string{"a", "a_2", "col3", "a_3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupe[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
