package diff

import (
	"database/sql"
	"fmt"

	"github.com/croc100/litescope/internal/schema"
	_ "modernc.org/sqlite"
)

type ColumnChange struct {
	Name string
	Old  *schema.Column
	New  *schema.Column
}

type TableDiff struct {
	Name          string
	Added         bool
	Removed       bool
	AddedColumns  []schema.Column
	RemovedColumns []schema.Column
	ChangedColumns []ColumnChange
	AddedIndexes  []schema.Index
	RemovedIndexes []schema.Index
}

type DataDiff struct {
	Table   string
	Added   int64
	Removed int64
	Changed int64
}

type Result struct {
	Schema []TableDiff
	Data   []DataDiff
}

func Compare(oldPath, newPath string) (*Result, error) {
	oldSchema, err := schema.Load(oldPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", oldPath, err)
	}
	newSchema, err := schema.Load(newPath)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", newPath, err)
	}

	schemaDiffs := diffSchema(oldSchema, newSchema)

	dataDiffs, err := diffData(oldPath, newPath, oldSchema, newSchema)
	if err != nil {
		return nil, err
	}

	return &Result{Schema: schemaDiffs, Data: dataDiffs}, nil
}

// CompareSchemas compares two schemas loaded via connectors.
// Data diff is skipped for remote sources.
func CompareSchemas(oldSchema, newSchema *schema.Schema) *Result {
	return &Result{Schema: diffSchema(oldSchema, newSchema)}
}

func diffSchema(old, new *schema.Schema) []TableDiff {
	oldMap := old.TableMap()
	newMap := new.TableMap()
	var diffs []TableDiff

	for name, newTable := range newMap {
		oldTable, exists := oldMap[name]
		if !exists {
			diffs = append(diffs, TableDiff{Name: name, Added: true, AddedColumns: newTable.Columns})
			continue
		}
		td := diffTable(name, oldTable, newTable)
		if td != nil {
			diffs = append(diffs, *td)
		}
	}

	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			t := oldMap[name]
			diffs = append(diffs, TableDiff{Name: name, Removed: true, RemovedColumns: t.Columns})
		}
	}

	return diffs
}

func diffTable(name string, old, new schema.Table) *TableDiff {
	td := &TableDiff{Name: name}

	oldCols := columnMap(old.Columns)
	newCols := columnMap(new.Columns)

	for colName, newCol := range newCols {
		if oldCol, exists := oldCols[colName]; !exists {
			td.AddedColumns = append(td.AddedColumns, newCol)
		} else if oldCol.Type != newCol.Type || oldCol.NotNull != newCol.NotNull {
			old := oldCol
			new := newCol
			td.ChangedColumns = append(td.ChangedColumns, ColumnChange{Name: colName, Old: &old, New: &new})
		}
	}
	for colName, oldCol := range oldCols {
		if _, exists := newCols[colName]; !exists {
			td.RemovedColumns = append(td.RemovedColumns, oldCol)
		}
	}

	oldIdx := indexMap(old.Indexes)
	newIdx := indexMap(new.Indexes)
	for idxName, idx := range newIdx {
		if _, exists := oldIdx[idxName]; !exists {
			td.AddedIndexes = append(td.AddedIndexes, idx)
		}
	}
	for idxName, idx := range oldIdx {
		if _, exists := newIdx[idxName]; !exists {
			td.RemovedIndexes = append(td.RemovedIndexes, idx)
		}
	}

	if len(td.AddedColumns)+len(td.RemovedColumns)+len(td.ChangedColumns)+len(td.AddedIndexes)+len(td.RemovedIndexes) == 0 {
		return nil
	}
	return td
}

func diffData(oldPath, newPath string, oldSchema, newSchema *schema.Schema) ([]DataDiff, error) {
	oldDB, err := sql.Open("sqlite", oldPath)
	if err != nil {
		return nil, err
	}
	defer oldDB.Close()

	newDB, err := sql.Open("sqlite", newPath)
	if err != nil {
		return nil, err
	}
	defer newDB.Close()

	oldMap := oldSchema.TableMap()
	newMap := newSchema.TableMap()

	var diffs []DataDiff
	for name, newTable := range newMap {
		oldTable, exists := oldMap[name]
		if !exists {
			var count int64
			newDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", name)).Scan(&count)
			diffs = append(diffs, DataDiff{Table: name, Added: count})
			continue
		}
		dd, err := diffTableData(oldDB, newDB, oldTable, newTable)
		if err != nil {
			continue
		}
		if dd.Added+dd.Removed+dd.Changed > 0 {
			diffs = append(diffs, dd)
		}
	}

	for name := range oldMap {
		if _, exists := newMap[name]; !exists {
			var count int64
			oldDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", name)).Scan(&count)
			diffs = append(diffs, DataDiff{Table: name, Removed: count})
		}
	}

	return diffs, nil
}

func diffTableData(oldDB, newDB *sql.DB, old, new schema.Table) (DataDiff, error) {
	dd := DataDiff{Table: old.Name}

	pk := pkColumn(new.Columns)
	if pk == "" {
		var oldCount, newCount int64
		oldDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", old.Name)).Scan(&oldCount)
		newDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", new.Name)).Scan(&newCount)
		diff := newCount - oldCount
		if diff > 0 {
			dd.Added = diff
		} else {
			dd.Removed = -diff
		}
		return dd, nil
	}

	var oldCount, newCount int64
	oldDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", old.Name)).Scan(&oldCount)
	newDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", new.Name)).Scan(&newCount)

	newDB.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM %q WHERE %q NOT IN (SELECT %q FROM main.%q)",
		new.Name, pk, pk, old.Name,
	)).Scan(&dd.Added)

	newDB.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM %q WHERE %q NOT IN (SELECT %q FROM main.%q)",
		old.Name, pk, pk, new.Name,
	)).Scan(&dd.Removed)

	_ = oldCount
	_ = newCount
	return dd, nil
}

func pkColumn(cols []schema.Column) string {
	for _, c := range cols {
		if c.PK == 1 {
			return c.Name
		}
	}
	return ""
}

func columnMap(cols []schema.Column) map[string]schema.Column {
	m := make(map[string]schema.Column, len(cols))
	for _, c := range cols {
		m[c.Name] = c
	}
	return m
}

func indexMap(idxs []schema.Index) map[string]schema.Index {
	m := make(map[string]schema.Index, len(idxs))
	for _, i := range idxs {
		m[i.Name] = i
	}
	return m
}
