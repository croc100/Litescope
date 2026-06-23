package importer

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildXLSX assembles a minimal .xlsx in memory from the given part bodies so we
// can exercise the reader against the shared-string layout real Excel produces.
func buildXLSX(t *testing.T, sharedStrings, sheet string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parts := map[string]string{
		"xl/worksheets/sheet1.xml": sheet,
	}
	if sharedStrings != "" {
		parts["xl/sharedStrings.xml"] = sharedStrings
	}
	for name, body := range parts {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportXLSX_SharedStringsAndTypes(t *testing.T) {
	db := openTestDB(t)

	// Shared string table: indices 0..3 used by t="s" cells.
	shared := `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<si><t>id</t></si><si><t>name</t></si><si><t>note</t></si><si><t>apple</t></si>` +
		`</sst>`
	// Header row uses shared strings; data rows mix numbers, shared strings and a gap.
	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c></row>` +
		`<row r="2"><c r="A2"><v>1</v></c><c r="B2" t="s"><v>3</v></c><c r="C2" t="inlineStr"><is><t>fresh</t></is></c></row>` +
		`<row r="3"><c r="A3"><v>2</v></c><c r="C3" t="inlineStr"><is><t>ripe</t></is></c></row>` + // B3 missing -> gap
		`</sheetData></worksheet>`

	data := buildXLSX(t, shared, sheet)
	res, err := ImportXLSX(db, bytes.NewReader(data), Options{Table: "fruit", HasHeader: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 2 {
		t.Fatalf("rows = %d, want 2", res.Rows)
	}

	want := map[string]string{"id": "INTEGER", "name": "TEXT", "note": "TEXT"}
	for _, c := range res.Columns {
		if want[c.Name] != c.Type {
			t.Errorf("col %s type = %s, want %s", c.Name, c.Type, want[c.Name])
		}
	}

	// The gap at B3 must land as NULL, and shared/inline strings must resolve.
	var name1 string
	if err := db.QueryRow(`SELECT name FROM fruit WHERE id=1`).Scan(&name1); err != nil {
		t.Fatal(err)
	}
	if name1 != "apple" {
		t.Errorf("id=1 name = %q, want apple", name1)
	}
	var nameNull bool
	if err := db.QueryRow(`SELECT name IS NULL FROM fruit WHERE id=2`).Scan(&nameNull); err != nil {
		t.Fatal(err)
	}
	if !nameNull {
		t.Errorf("id=2 name should be NULL (gap cell)")
	}
}

func TestImportXLSX_NoHeader(t *testing.T) {
	db := openTestDB(t)
	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1"><v>10</v></c><c r="B1" t="inlineStr"><is><t>x</t></is></c></row>` +
		`<row r="2"><c r="A2"><v>20</v></c><c r="B2" t="inlineStr"><is><t>y</t></is></c></row>` +
		`</sheetData></worksheet>`
	data := buildXLSX(t, "", sheet)
	res, err := ImportXLSX(db, bytes.NewReader(data), Options{Table: "t", HasHeader: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != 2 {
		t.Fatalf("rows = %d, want 2", res.Rows)
	}
	if res.Columns[0].Name != "col1" || res.Columns[1].Name != "col2" {
		t.Errorf("synthetic headers = %v, want col1/col2", res.Columns)
	}
}

func TestImportXLSX_NotAZip(t *testing.T) {
	db := openTestDB(t)
	_, err := ImportXLSX(db, bytes.NewReader([]byte("plain text, not xlsx")), Options{Table: "t", HasHeader: true})
	if err == nil {
		t.Fatal("expected error for non-xlsx input")
	}
}

func TestColIndex(t *testing.T) {
	cases := map[string]int{"A1": 0, "B2": 1, "Z9": 25, "AA10": 26, "AB1": 27, "": -1, "5": -1}
	for ref, want := range cases {
		if got := colIndex(ref); got != want {
			t.Errorf("colIndex(%q) = %d, want %d", ref, got, want)
		}
	}
}
