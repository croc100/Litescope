package schema

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// FromSQL materializes a declarative schema (a body of CREATE statements) into a
// throwaway database and returns the resulting Schema. This lets a checked-in
// schema.sql be compared against a live database with the normal diff engine.
func FromSQL(sqlText string) (*Schema, error) {
	f, err := os.CreateTemp("", "litescope-schema-*.db")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(sqlText); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying declarative schema: %w", err)
	}
	db.Close()
	return Load(path)
}

type Column struct {
	Name    string
	Type    string
	NotNull bool
	Default string
	PK      int
}

type Index struct {
	Name   string
	Table  string
	Unique bool
	SQL    string
}

// ForeignKey describes a single-column relationship from this table to another.
type ForeignKey struct {
	From  string // column in this table
	Table string // referenced table
	To    string // referenced column
}

type Table struct {
	Name        string
	Columns     []Column
	Indexes     []Index
	ForeignKeys []ForeignKey
}

type Schema struct {
	Tables []Table
}

func Load(path string) (*Schema, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()

	tables, err := loadTables(db)
	if err != nil {
		return nil, err
	}

	return &Schema{Tables: tables}, nil
}

func loadTables(db *sql.DB) ([]Table, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		t, err := loadTable(db, name)
		if err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func loadTable(db *sql.DB, name string) (Table, error) {
	t := Table{Name: name}

	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", name))
	if err != nil {
		return t, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var col Column
		var notNull int
		var defaultVal sql.NullString
		if err := rows.Scan(&cid, &col.Name, &col.Type, &notNull, &defaultVal, &col.PK); err != nil {
			return t, err
		}
		col.NotNull = notNull == 1
		if defaultVal.Valid {
			col.Default = defaultVal.String
		}
		t.Columns = append(t.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return t, err
	}

	idxRows, err := db.Query(fmt.Sprintf("PRAGMA index_list(%q)", name))
	if err != nil {
		return t, err
	}
	defer idxRows.Close()

	for idxRows.Next() {
		var seq, partial int
		var origin string
		var idx Index
		var unique int
		if err := idxRows.Scan(&seq, &idx.Name, &unique, &origin, &partial); err != nil {
			return t, err
		}
		idx.Table = name
		idx.Unique = unique == 1
		t.Indexes = append(t.Indexes, idx)
	}

	if err := idxRows.Err(); err != nil {
		return t, err
	}

	fkRows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", name))
	if err != nil {
		return t, err
	}
	defer fkRows.Close()

	for fkRows.Next() {
		var id, seq int
		var onUpdate, onDelete, match string
		var fk ForeignKey
		if err := fkRows.Scan(&id, &seq, &fk.Table, &fk.From, &fk.To, &onUpdate, &onDelete, &match); err != nil {
			return t, err
		}
		t.ForeignKeys = append(t.ForeignKeys, fk)
	}

	return t, fkRows.Err()
}

func (s *Schema) TableMap() map[string]Table {
	m := make(map[string]Table, len(s.Tables))
	for _, t := range s.Tables {
		m[t.Name] = t
	}
	return m
}

// Mermaid renders the schema as a Mermaid erDiagram, suitable for pasting into
// a README or any Mermaid-aware renderer. Foreign keys become relationships.
func (s *Schema) Mermaid() string {
	var b strings.Builder
	b.WriteString("erDiagram\n")

	for _, t := range s.Tables {
		// Columns referenced by a foreign key, for the FK marker.
		fkCols := make(map[string]bool, len(t.ForeignKeys))
		for _, fk := range t.ForeignKeys {
			fkCols[fk.From] = true
		}

		fmt.Fprintf(&b, "    %s {\n", mermaidName(t.Name))
		for _, c := range t.Columns {
			typ := c.Type
			if typ == "" {
				typ = "any"
			}
			var keys []string
			if c.PK > 0 {
				keys = append(keys, "PK")
			}
			if fkCols[c.Name] {
				keys = append(keys, "FK")
			}
			line := fmt.Sprintf("        %s %s", mermaidName(typ), mermaidName(c.Name))
			if len(keys) > 0 {
				line += " " + strings.Join(keys, ",")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("    }\n")
	}

	for _, t := range s.Tables {
		for _, fk := range t.ForeignKeys {
			// child }o--|| parent : "fk_col"
			fmt.Fprintf(&b, "    %s }o--|| %s : %q\n",
				mermaidName(t.Name), mermaidName(fk.Table), fk.From)
		}
	}

	return b.String()
}

// mermaidName sanitizes an identifier so it is a valid Mermaid token: Mermaid
// entity and attribute names must be alphanumeric or underscore.
func mermaidName(s string) string {
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (s *Schema) String() string {
	var b strings.Builder
	for _, t := range s.Tables {
		fmt.Fprintf(&b, "table %s\n", t.Name)
		for _, c := range t.Columns {
			fmt.Fprintf(&b, "  %s %s\n", c.Name, c.Type)
		}
		for _, idx := range t.Indexes {
			u := ""
			if idx.Unique {
				u = " UNIQUE"
			}
			fmt.Fprintf(&b, "  index %s%s\n", idx.Name, u)
		}
	}
	return b.String()
}
