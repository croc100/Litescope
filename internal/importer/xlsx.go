package importer

// Minimal, dependency-free .xlsx reader. An .xlsx file is a ZIP of XML parts;
// we read the shared-string table and the first worksheet directly with the
// standard library so Litescope stays a single closed binary (no excelize).
//
// Scope: the data we need for import — cell text, by row/column, with shared
// strings, inline strings, booleans and numbers resolved. Dates are kept as
// their underlying serial number (no style-driven date formatting); rich text
// runs are concatenated. That is enough to turn a spreadsheet into a table.

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// ImportXLSX reads the first worksheet of an .xlsx stream and loads it into
// opt.Table, reusing the same type-inference and load path as CSV/JSON.
func ImportXLSX(db *sql.DB, r io.Reader, opt Options) (*Result, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read xlsx: %w", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open xlsx (not a valid .xlsx/zip): %w", err)
	}

	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}

	shared, err := readSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}

	sheetName := firstWorksheet(zr)
	if sheetName == "" {
		return nil, fmt.Errorf("no worksheet found in xlsx")
	}
	grid, err := readSheet(files[sheetName], shared)
	if err != nil {
		return nil, err
	}
	if len(grid) == 0 {
		return nil, fmt.Errorf("no rows in input")
	}

	var headers []string
	var rows [][]string
	if opt.HasHeader {
		headers = grid[0]
		rows = grid[1:]
	} else {
		headers = make([]string, len(grid[0]))
		for i := range headers {
			headers[i] = fmt.Sprintf("col%d", i+1)
		}
		rows = grid
	}
	headers = dedupeHeaders(headers)

	// Normalize ragged rows to the header width.
	for i := range rows {
		for len(rows[i]) < len(headers) {
			rows[i] = append(rows[i], "")
		}
		if len(rows[i]) > len(headers) {
			rows[i] = rows[i][:len(headers)]
		}
	}

	cols := inferColumns(headers, rows)
	return load(db, opt, cols, rows)
}

// firstWorksheet returns the path of the lowest-numbered worksheet part
// (xl/worksheets/sheet1.xml before sheet2.xml), which matches the first tab.
func firstWorksheet(zr *zip.Reader) string {
	var sheets []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheets = append(sheets, f.Name)
		}
	}
	if len(sheets) == 0 {
		return ""
	}
	sort.Slice(sheets, func(i, j int) bool {
		return sheetNum(sheets[i]) < sheetNum(sheets[j])
	})
	return sheets[0]
}

func sheetNum(name string) int {
	base := strings.TrimSuffix(strings.TrimPrefix(name, "xl/worksheets/sheet"), ".xml")
	n, err := strconv.Atoi(base)
	if err != nil {
		return 1 << 30 // unparseable names sort last
	}
	return n
}

// readSharedStrings parses xl/sharedStrings.xml into an indexable slice. Each
// <si> may hold a single <t> or several <r><t> runs (rich text); we concatenate.
func readSharedStrings(f *zip.File) ([]string, error) {
	if f == nil {
		return nil, nil // no shared strings table is valid (all-inline/numeric sheet)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open sharedStrings: %w", err)
	}
	defer rc.Close()

	var out []string
	dec := xml.NewDecoder(rc)
	var cur strings.Builder
	inSI, inT := false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse sharedStrings: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = true
			}
		case xml.CharData:
			if inSI && inT {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "si":
				inSI = false
				out = append(out, cur.String())
			}
		}
	}
	return out, nil
}

// readSheet parses a worksheet into a row-major string grid, placing each cell
// at the column implied by its A1-style reference so gaps become empty cells.
func readSheet(f *zip.File, shared []string) ([][]string, error) {
	if f == nil {
		return nil, fmt.Errorf("worksheet part missing")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open worksheet: %w", err)
	}
	defer rc.Close()

	dec := xml.NewDecoder(rc)
	var grid [][]string
	var row []string
	maxCols := 0

	var (
		cellType string // s | inlineStr | str | b | (empty = number)
		cellCol  int    // 0-based column of the current cell
		inV, inT bool
		val      strings.Builder
	)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse worksheet: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
			case "c":
				cellType = ""
				cellCol = -1
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "t":
						cellType = a.Value
					case "r":
						cellCol = colIndex(a.Value)
					}
				}
				val.Reset()
			case "v":
				inV = true
				val.Reset()
			case "t":
				inT = true
				val.Reset()
			}
		case xml.CharData:
			if inV || inT {
				val.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				text := val.String()
				if t.Name.Local == "v" {
					inV = false
				} else {
					inT = false
				}
				// Resolve only on the value-bearing element for this cell type.
				if (cellType == "s" && t.Name.Local == "v") ||
					(cellType != "s" && cellType != "inlineStr" && t.Name.Local == "v") ||
					(cellType == "inlineStr" && t.Name.Local == "t") {
					resolved := resolveCell(cellType, text, shared)
					if cellCol < 0 {
						cellCol = len(row) // no ref: append sequentially
					}
					for len(row) <= cellCol {
						row = append(row, "")
					}
					row[cellCol] = resolved
				}
			case "row":
				if len(row) > maxCols {
					maxCols = len(row)
				}
				grid = append(grid, row)
			}
		}
	}

	// Pad every row to the widest so the grid is rectangular.
	for i := range grid {
		for len(grid[i]) < maxCols {
			grid[i] = append(grid[i], "")
		}
	}
	return grid, nil
}

func resolveCell(cellType, text string, shared []string) string {
	switch cellType {
	case "s": // shared string: text is an index
		idx, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil && idx >= 0 && idx < len(shared) {
			return shared[idx]
		}
		return ""
	case "b": // boolean stored as 0/1
		if strings.TrimSpace(text) == "1" {
			return "1"
		}
		return "0"
	default: // inlineStr, str, or numeric — already literal
		return text
	}
}

// colIndex converts an A1-style cell reference to a 0-based column index
// ("A1"->0, "B2"->1, "AA10"->26). Returns -1 if no letters are present.
func colIndex(ref string) int {
	n := 0
	any := false
	for _, ch := range ref {
		if ch >= 'A' && ch <= 'Z' {
			n = n*26 + int(ch-'A'+1)
			any = true
		} else if ch >= 'a' && ch <= 'z' {
			n = n*26 + int(ch-'a'+1)
			any = true
		} else {
			break
		}
	}
	if !any {
		return -1
	}
	return n - 1
}
