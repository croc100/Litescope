package exporter

// Minimal, dependency-free .xlsx writer. We assemble the handful of ZIP parts a
// spreadsheet app needs to open the file, writing every cell as an inline string
// (t="inlineStr") to avoid a shared-string table. Numbers are emitted as numeric
// cells so Excel/Sheets treat them as numbers; everything else is text.

import (
	"archive/zip"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ExportXLSX runs query (read-only) and writes an .xlsx workbook to w with the
// columns as a header row. It returns the number of data rows written.
func ExportXLSX(db *sql.DB, query string, w io.Writer) (int64, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return 0, fmt.Errorf("empty query")
	}
	if !isReadOnly(q) {
		return 0, fmt.Errorf("only read queries (SELECT / WITH) can be exported")
	}

	rows, err := db.Query(q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)

	writeRow(&sheet, 1, stringCells(cols))

	var n int64
	for rows.Next() {
		raw := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		writeRow(&sheet, int(n)+2, valueCells(raw))
		n++
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	if err := writeWorkbookZip(w, sheet.String()); err != nil {
		return n, err
	}
	return n, nil
}

// cell is one spreadsheet cell: either an inline string or a numeric value.
type cell struct {
	text    string
	numeric bool
}

func stringCells(vals []string) []cell {
	out := make([]cell, len(vals))
	for i, v := range vals {
		out[i] = cell{text: v}
	}
	return out
}

// valueCells renders a scanned row, keeping integers/floats numeric and the
// rest as text. NULL becomes an empty string cell.
func valueCells(raw []interface{}) []cell {
	out := make([]cell, len(raw))
	for i, v := range raw {
		switch t := v.(type) {
		case nil:
			out[i] = cell{text: ""}
		case int64:
			out[i] = cell{text: strconv.FormatInt(t, 10), numeric: true}
		case float64:
			out[i] = cell{text: strconv.FormatFloat(t, 'f', -1, 64), numeric: true}
		default:
			out[i] = cell{text: cellString(v)}
		}
	}
	return out
}

func writeRow(b *strings.Builder, rowNum int, cells []cell) {
	fmt.Fprintf(b, `<row r="%d">`, rowNum)
	for i, c := range cells {
		ref := cellRef(i, rowNum)
		if c.numeric {
			fmt.Fprintf(b, `<c r="%s"><v>%s</v></c>`, ref, c.text)
		} else {
			fmt.Fprintf(b, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
				ref, xmlEscape(c.text))
		}
	}
	b.WriteString(`</row>`)
}

// writeWorkbookZip assembles the minimal set of OOXML parts around the sheet.
func writeWorkbookZip(w io.Writer, sheetXML string) error {
	zw := zip.NewWriter(w)
	parts := []struct{ name, body string }{
		{"[Content_Types].xml", contentTypesXML},
		{"_rels/.rels", relsXML},
		{"xl/workbook.xml", workbookXML},
		{"xl/_rels/workbook.xml.rels", workbookRelsXML},
		{"xl/worksheets/sheet1.xml", sheetXML},
	}
	for _, p := range parts {
		f, err := zw.Create(p.name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(f, p.body); err != nil {
			return err
		}
	}
	return zw.Close()
}

// cellRef builds an A1-style reference from a 0-based column and 1-based row.
func cellRef(col, row int) string {
	return colName(col) + strconv.Itoa(row)
}

func colName(col int) string {
	name := ""
	col++ // 1-based for the conversion
	for col > 0 {
		col--
		name = string(rune('A'+col%26)) + name
		col /= 26
	}
	return name
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

const contentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
	`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
	`<Default Extension="xml" ContentType="application/xml"/>` +
	`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
	`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
	`</Types>`

const relsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

const workbookXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
	`<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`

const workbookRelsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
	`</Relationships>`
