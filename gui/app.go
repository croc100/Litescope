package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/croc100/litescope/internal/check"
	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/monitor"
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

func (a *App) SaveFile(defaultName string) string {
	path, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: defaultName,
	})
	if err != nil {
		return ""
	}
	return path
}

// ── Diff ──────────────────────────────────────────────────────────────────────

func (a *App) Diff(oldPath, newPath string) (*diff.Result, error) {
	return diff.Compare(oldPath, newPath)
}

// ── Schema / Explorer ─────────────────────────────────────────────────────────

func (a *App) Schema(path string) (*schema.Schema, error) {
	return schema.Load(path)
}

type TableRows struct {
	Columns []string        `json:"Columns"`
	Rows    [][]interface{} `json:"Rows"`
	Total   int64           `json:"Total"`
}

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

type DiffedRow struct {
	Status string                 `json:"Status"`
	PK     interface{}            `json:"PK"`
	Old    map[string]interface{} `json:"Old"`
	New    map[string]interface{} `json:"New"`
}

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

// ── Check ─────────────────────────────────────────────────────────────────────

func (a *App) Check(backupPath, referencePath string, withData bool) (*check.Result, error) {
	return check.Check(backupPath, referencePath, withData)
}

// ── Migrate ───────────────────────────────────────────────────────────────────

type MigratePreview struct {
	SQL      string   `json:"SQL"`
	Warnings []string `json:"Warnings"`
}

func (a *App) MigrateGenerate(oldPath, newPath string) (*MigratePreview, error) {
	d, err := diff.Compare(oldPath, newPath)
	if err != nil {
		return nil, err
	}
	newSchema, err := schema.Load(newPath)
	if err != nil {
		return nil, err
	}
	m := migrate.Generate(d, newSchema)
	return &MigratePreview{
		SQL:      m.SQL(),
		Warnings: m.Warnings,
	}, nil
}

type MigrateApplyResult struct {
	Executed   int    `json:"Executed"`
	BackupPath string `json:"BackupPath"`
	DryRun     bool   `json:"DryRun"`
	DurationMs int64  `json:"DurationMs"`
}

func (a *App) MigrateApply(dbPath, migrationSQL string, dryRun bool) (*MigrateApplyResult, error) {
	res, err := migrate.Apply(dbPath, migrationSQL, migrate.ApplyOptions{
		DryRun:    dryRun,
		BackupDir: filepath.Dir(dbPath),
	})
	if err != nil {
		return nil, err
	}
	return &MigrateApplyResult{
		Executed:   res.Executed,
		BackupPath: res.BackupPath,
		DryRun:     res.DryRun,
		DurationMs: res.Duration.Milliseconds(),
	}, nil
}

// ── Monitor ───────────────────────────────────────────────────────────────────

type SnapshotInfo struct {
	Source     string `json:"Source"`
	CapturedAt string `json:"CapturedAt"`
	TableCount int    `json:"TableCount"`
}

func (a *App) MonitorSnapshot(dbPath, outputPath string) (*SnapshotInfo, error) {
	conn, err := connector.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	s, err := conn.Schema()
	if err != nil {
		return nil, err
	}

	snap := &monitor.Snapshot{
		Source:     dbPath,
		CapturedAt: time.Now().UTC(),
		Schema:     s,
	}
	if err := monitor.Save(outputPath, snap); err != nil {
		return nil, err
	}

	tableCount := 0
	if s != nil {
		tableCount = len(s.Tables)
	}
	return &SnapshotInfo{
		Source:     dbPath,
		CapturedAt: snap.CapturedAt.Format(time.RFC3339),
		TableCount: tableCount,
	}, nil
}

func (a *App) MonitorCheck(dbPath, baselinePath string) (*monitor.DriftResult, error) {
	snap, err := monitor.Load(baselinePath)
	if err != nil {
		return nil, err
	}

	conn, err := connector.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	current, err := conn.Schema()
	if err != nil {
		return nil, err
	}

	result := monitor.Check(dbPath, snap, current)
	return result, nil
}

func (a *App) MonitorLoadHistory(reportPath string) ([]monitor.HistoryEntry, error) {
	return monitor.LoadHistory(reportPath)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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
