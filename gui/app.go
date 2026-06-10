package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/schema"
	_ "modernc.org/sqlite"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx context.Context
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) OpenFile() string {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "SQLite databases", Pattern: "*.db;*.sqlite;*.sqlite3"},
		},
	})
	if err != nil {
		return ""
	}
	return path
}

func (a *App) Diff(oldPath, newPath string) (*diff.Result, error) {
	return diff.Compare(oldPath, newPath)
}

func (a *App) Schema(path string) (*schema.Schema, error) {
	return schema.Load(path)
}

// TableRows holds paginated query results.
type TableRows struct {
	Columns []string        `json:"Columns"`
	Rows    [][]interface{} `json:"Rows"`
	Total   int64           `json:"Total"`
}

// QueryTable returns paginated rows from a table.
func (a *App) QueryTable(path, table string, limit, offset int) (*TableRows, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var total int64
	db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", table)).Scan(&total)

	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %q LIMIT %d OFFSET %d", table, limit, offset))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	result := &TableRows{Columns: cols, Total: total}

	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		row := make([]interface{}, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return result, rows.Err()
}

// DiffedRow represents a single row difference.
type DiffedRow struct {
	Status string                 `json:"Status"` // added | removed | changed
	PK     interface{}            `json:"PK"`
	Old    map[string]interface{} `json:"Old"`
	New    map[string]interface{} `json:"New"`
}

// TableDiffRows returns the actual changed rows between two databases for a table.
func (a *App) TableDiffRows(oldPath, newPath, table, pkCol string, limit int) ([]DiffedRow, error) {
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

	oldRows, err := fetchRowMap(oldDB, table, pkCol, limit)
	if err != nil {
		return nil, err
	}
	newRows, err := fetchRowMap(newDB, table, pkCol, limit)
	if err != nil {
		return nil, err
	}

	var result []DiffedRow

	for pk, newRow := range newRows {
		if oldRow, exists := oldRows[pk]; !exists {
			result = append(result, DiffedRow{Status: "added", PK: pk, New: newRow})
		} else if !rowsEqual(oldRow, newRow) {
			result = append(result, DiffedRow{Status: "changed", PK: pk, Old: oldRow, New: newRow})
		}
	}
	for pk, oldRow := range oldRows {
		if _, exists := newRows[pk]; !exists {
			result = append(result, DiffedRow{Status: "removed", PK: pk, Old: oldRow})
		}
	}

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func fetchRowMap(db *sql.DB, table, pkCol string, limit int) (map[interface{}]map[string]interface{}, error) {
	rows, err := db.Query(fmt.Sprintf("SELECT * FROM %q LIMIT %d", table, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	result := make(map[interface{}]map[string]interface{})

	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		rowMap := make(map[string]interface{}, len(cols))
		var pk interface{}
		for i, col := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			rowMap[col] = v
			if col == pkCol {
				pk = v
			}
		}
		if pk != nil {
			result[pk] = rowMap
		}
	}
	return result, rows.Err()
}

func rowsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", va) != fmt.Sprintf("%v", vb) {
			return false
		}
	}
	return true
}
