package schema

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

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

type Table struct {
	Name    string
	Columns []Column
	Indexes []Index
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

	return t, idxRows.Err()
}

func (s *Schema) TableMap() map[string]Table {
	m := make(map[string]Table, len(s.Tables))
	for _, t := range s.Tables {
		m[t.Name] = t
	}
	return m
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
