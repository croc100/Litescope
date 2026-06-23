package exporter

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openExportDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE people (id INTEGER, name TEXT, score REAL, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO people VALUES (1,'Alice',9.5,'a,b'),(2,'Bob & Co',3,NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestExportXLSX_ValidAndContents(t *testing.T) {
	db := openExportDB(t)
	var buf bytes.Buffer
	n, err := ExportXLSX(db, "SELECT * FROM people ORDER BY id", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}

	parts := map[string]bool{}
	var sheet string
	for _, f := range zr.File {
		parts[f.Name] = true
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			sheet = string(b)
		}
	}
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml",
		"xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"} {
		if !parts[want] {
			t.Errorf("missing part %s", want)
		}
	}

	// Numbers stay numeric; the ampersand must be XML-escaped.
	if !strings.Contains(sheet, `<c r="A2"><v>1</v></c>`) {
		t.Errorf("expected numeric id cell, sheet=%s", sheet)
	}
	if !strings.Contains(sheet, "Bob &amp; Co") {
		t.Errorf("ampersand not escaped, sheet=%s", sheet)
	}
}

func TestExportXLSX_RejectsWrite(t *testing.T) {
	db := openExportDB(t)
	var buf bytes.Buffer
	if _, err := ExportXLSX(db, "DELETE FROM people", &buf); err == nil {
		t.Fatal("expected read-only rejection")
	}
}

func TestColName(t *testing.T) {
	cases := map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB"}
	for col, want := range cases {
		if got := colName(col); got != want {
			t.Errorf("colName(%d) = %s, want %s", col, got, want)
		}
	}
}
