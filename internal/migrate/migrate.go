// Package migrate generates SQLite migration SQL from a diff result.
// SQLite's ALTER TABLE is limited: only ADD COLUMN and RENAME are supported.
// Removed columns and type changes require a table-rebuild pattern.
package migrate

import (
	"fmt"
	"strings"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
)

type Statement struct {
	SQL     string
	Comment string // explains what the statement does or why
	Warning string // set when destructive or lossy
}

type Migration struct {
	Statements []Statement
	Warnings   []string // summary of destructive operations
}

func (m *Migration) HasWarnings() bool { return len(m.Warnings) > 0 }

func (m *Migration) SQL() string {
	var b strings.Builder
	for i, s := range m.Statements {
		if i > 0 {
			b.WriteString("\n")
		}
		if s.Comment != "" {
			b.WriteString("-- ")
			b.WriteString(s.Comment)
			b.WriteString("\n")
		}
		if s.Warning != "" {
			b.WriteString("-- WARNING: ")
			b.WriteString(s.Warning)
			b.WriteString("\n")
		}
		b.WriteString(s.SQL)
		b.WriteString(";")
		b.WriteString("\n")
	}
	return b.String()
}

// Generate produces migration SQL for the given diff result.
// It handles all schema changes, annotating destructive operations with warnings.
func Generate(d *diff.Result, newSchema *schema.Schema) *Migration {
	m := &Migration{}

	// Build a lookup of new table definitions for rebuild patterns.
	newTables := map[string]*schema.Table{}
	if newSchema != nil {
		for i := range newSchema.Tables {
			t := &newSchema.Tables[i]
			newTables[t.Name] = t
		}
	}

	for _, td := range d.Schema {
		switch {
		case td.Added:
			m.addTable(td, newTables)
		case td.Removed:
			m.dropTable(td)
		default:
			m.alterTable(td, newTables)
		}
	}

	return m
}

// ── Table-level operations ────────────────────────────────────────────────────

func (m *Migration) addTable(td diff.TableDiff, newTables map[string]*schema.Table) {
	t, ok := newTables[td.Name]
	if !ok {
		m.add(Statement{
			Comment: fmt.Sprintf("table %s: added (definition not available)", td.Name),
			SQL:     fmt.Sprintf("-- CREATE TABLE %s (...) -- run litescope schema to get the full definition", td.Name),
		})
		return
	}

	m.add(Statement{
		Comment: fmt.Sprintf("new table: %s", td.Name),
		SQL:     createTableSQL(t),
	})

	for _, ix := range t.Indexes {
		if ix.SQL != "" {
			m.add(Statement{SQL: ix.SQL})
		}
	}
}

func (m *Migration) dropTable(td diff.TableDiff) {
	warn := fmt.Sprintf("drops table %s and ALL its data permanently", td.Name)
	m.Warnings = append(m.Warnings, warn)
	m.add(Statement{
		Comment: fmt.Sprintf("removed table: %s", td.Name),
		Warning: warn,
		SQL:     fmt.Sprintf("DROP TABLE IF EXISTS %s", quote(td.Name)),
	})
}

// ── Column / index alterations ────────────────────────────────────────────────

func (m *Migration) alterTable(td diff.TableDiff, newTables map[string]*schema.Table) {
	needsRebuild := len(td.RemovedColumns) > 0 || len(td.ChangedColumns) > 0

	if needsRebuild {
		m.rebuildTable(td, newTables)
	} else {
		// Simple ADD COLUMN — no rebuild needed.
		for _, c := range td.AddedColumns {
			m.add(Statement{
				Comment: fmt.Sprintf("table %s: add column %s", td.Name, c.Name),
				SQL:     fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quote(td.Name), columnDef(c)),
			})
		}
	}

	// Indexes can always be dropped/created independently.
	for _, ix := range td.RemovedIndexes {
		m.add(Statement{
			Comment: fmt.Sprintf("table %s: remove index %s", td.Name, ix.Name),
			SQL:     fmt.Sprintf("DROP INDEX IF EXISTS %s", quote(ix.Name)),
		})
	}
	for _, ix := range td.AddedIndexes {
		if ix.SQL != "" {
			m.add(Statement{
				Comment: fmt.Sprintf("table %s: add index %s", td.Name, ix.Name),
				SQL:     ix.SQL,
			})
		}
	}
}

// rebuildTable emits the SQLite table-rebuild pattern:
// 1. CREATE new_table
// 2. INSERT INTO new_table SELECT cols FROM old_table
// 3. DROP old_table
// 4. ALTER TABLE new_table RENAME TO old_table
func (m *Migration) rebuildTable(td diff.TableDiff, newTables map[string]*schema.Table) {
	t, ok := newTables[td.Name]
	if !ok {
		warn := fmt.Sprintf("table %s needs rebuild but new definition is unavailable — skipped", td.Name)
		m.Warnings = append(m.Warnings, warn)
		m.add(Statement{Warning: warn, SQL: fmt.Sprintf("-- REBUILD %s -- definition not available", td.Name)})
		return
	}

	tmp := td.Name + "_new"

	// Columns that survive (exist in both old and new).
	var keepCols []string
	removed := map[string]bool{}
	for _, c := range td.RemovedColumns {
		removed[c.Name] = true
	}
	for _, c := range t.Columns {
		if !removed[c.Name] {
			keepCols = append(keepCols, quote(c.Name))
		}
	}
	// Also include added columns (they'll get their DEFAULT on INSERT).
	var newColNames []string
	for _, c := range t.Columns {
		newColNames = append(newColNames, quote(c.Name))
	}

	// Describe what this rebuild does.
	var desc []string
	for _, c := range td.RemovedColumns {
		w := fmt.Sprintf("drops column %s.%s and its data", td.Name, c.Name)
		m.Warnings = append(m.Warnings, w)
		desc = append(desc, fmt.Sprintf("remove column %s", c.Name))
	}
	for _, c := range td.ChangedColumns {
		desc = append(desc, fmt.Sprintf("change %s type %s→%s", c.Name, c.Old.Type, c.New.Type))
	}
	for _, c := range td.AddedColumns {
		desc = append(desc, fmt.Sprintf("add column %s", c.Name))
	}

	m.add(Statement{
		Comment: fmt.Sprintf("table %s: rebuild to %s (SQLite does not support DROP COLUMN or type change directly)",
			td.Name, strings.Join(desc, ", ")),
		SQL: fmt.Sprintf("CREATE TABLE %s %s", quote(tmp), tableBody(t)),
	})

	// INSERT surviving columns + defaults for new ones.
	selectCols := strings.Join(keepCols, ", ")
	insertCols := selectCols
	if len(td.AddedColumns) > 0 {
		// new columns get their DEFAULT; omit from SELECT, let SQLite fill them.
		insertCols = strings.Join(newColNames, ", ")
		// Build a SELECT that has NULLs (or DEFAULT expressions) for added cols.
		selectParts := make([]string, 0, len(t.Columns))
		addedSet := map[string]schema.Column{}
		for _, c := range td.AddedColumns {
			addedSet[c.Name] = c
		}
		for _, c := range t.Columns {
			if ac, isNew := addedSet[c.Name]; isNew {
				if ac.Default != "" {
					selectParts = append(selectParts, ac.Default)
				} else {
					selectParts = append(selectParts, "NULL")
				}
			} else if !removed[c.Name] {
				selectParts = append(selectParts, quote(c.Name))
			}
		}
		selectCols = strings.Join(selectParts, ", ")
	}

	m.add(Statement{
		SQL: fmt.Sprintf("INSERT INTO %s (%s)\n  SELECT %s FROM %s",
			quote(tmp), insertCols, selectCols, quote(td.Name)),
	})
	m.add(Statement{SQL: fmt.Sprintf("DROP TABLE %s", quote(td.Name))})
	m.add(Statement{SQL: fmt.Sprintf("ALTER TABLE %s RENAME TO %s", quote(tmp), quote(td.Name))})
}

// ── SQL helpers ───────────────────────────────────────────────────────────────

func createTableSQL(t *schema.Table) string {
	return fmt.Sprintf("CREATE TABLE %s %s", quote(t.Name), tableBody(t))
}

func tableBody(t *schema.Table) string {
	defs := make([]string, 0, len(t.Columns))
	for _, c := range t.Columns {
		defs = append(defs, "  "+columnDef(c))
	}
	return fmt.Sprintf("(\n%s\n)", strings.Join(defs, ",\n"))
}

func columnDef(c schema.Column) string {
	var b strings.Builder
	b.WriteString(quote(c.Name))
	if c.Type != "" {
		b.WriteString(" ")
		b.WriteString(c.Type)
	}
	if c.NotNull {
		b.WriteString(" NOT NULL")
	}
	if c.Default != "" {
		b.WriteString(" DEFAULT ")
		b.WriteString(c.Default)
	}
	if c.PK > 0 {
		b.WriteString(" PRIMARY KEY")
	}
	return b.String()
}

func quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (m *Migration) add(s Statement) {
	m.Statements = append(m.Statements, s)
}
