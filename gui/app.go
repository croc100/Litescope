package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/check"
	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/monitor"
	"github.com/croc100/litescope/internal/schema"
	_ "modernc.org/sqlite"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx context.Context

	watchMu      sync.Mutex
	watchCancel  context.CancelFunc
	watchRunning bool
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

// MonitorWatchStart begins polling dbPath against baselinePath every intervalSec seconds.
// Emits "monitor:event" events with WatchEvent payloads. Stops any existing watch first.
// webhookURL is optional — if non-empty, drift events are POSTed to Slack/Discord.
func (a *App) MonitorWatchStart(dbPath, baselinePath string, intervalSec int, webhookURL string) {
	a.watchMu.Lock()
	if a.watchCancel != nil {
		a.watchCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.watchCancel = cancel
	a.watchRunning = true
	a.watchMu.Unlock()

	if intervalSec < 5 {
		intervalSec = 30
	}

	go func() {
		ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
		defer ticker.Stop()

		runCheck := func() {
			snap, err := monitor.Load(baselinePath)
			if err != nil {
				wailsRuntime.EventsEmit(a.ctx, "monitor:event", WatchEvent{
					At: time.Now().Format(time.RFC3339), Kind: "error", Message: "baseline not found: " + err.Error(),
				})
				return
			}
			conn, err := connector.Open(dbPath)
			if err != nil {
				wailsRuntime.EventsEmit(a.ctx, "monitor:event", WatchEvent{
					At: time.Now().Format(time.RFC3339), Kind: "error", Message: err.Error(),
				})
				return
			}
			defer conn.Close()
			current, err := conn.Schema()
			if err != nil {
				wailsRuntime.EventsEmit(a.ctx, "monitor:event", WatchEvent{
					At: time.Now().Format(time.RFC3339), Kind: "error", Message: err.Error(),
				})
				return
			}
			result := monitor.Check(dbPath, snap, current)
			if result.HasDrift {
				msg := fmt.Sprintf("%d change(s) detected", len(result.Changes))
				wailsRuntime.EventsEmit(a.ctx, "monitor:event", WatchEvent{
					At: time.Now().Format(time.RFC3339), Kind: "drift",
					Message: msg, Changes: len(result.Changes),
				})
				if webhookURL != "" {
					sendWebhookAlert(webhookURL, dbPath, msg)
				}
			} else {
				wailsRuntime.EventsEmit(a.ctx, "monitor:event", WatchEvent{
					At: time.Now().Format(time.RFC3339), Kind: "ok", Message: "no drift",
				})
			}
		}

		runCheck()
		for {
			select {
			case <-ctx.Done():
				a.watchMu.Lock()
				a.watchRunning = false
				a.watchMu.Unlock()
				return
			case <-ticker.C:
				runCheck()
			}
		}
	}()
}

func (a *App) MonitorWatchStop() {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()
	if a.watchCancel != nil {
		a.watchCancel()
		a.watchCancel = nil
	}
	a.watchRunning = false
}

func (a *App) MonitorWatchIsRunning() bool {
	a.watchMu.Lock()
	defer a.watchMu.Unlock()
	return a.watchRunning
}

type WatchEvent struct {
	At      string `json:"at"`
	Kind    string `json:"kind"` // "ok" | "drift" | "error"
	Message string `json:"message"`
	Changes int    `json:"changes,omitempty"`
}

// ── Fleet ─────────────────────────────────────────────────────────────────────

type FleetDBEntry struct {
	Name string   `json:"name"`
	DSN  string   `json:"dsn"`
	Tags []string `json:"tags,omitempty"`
}

type FleetCheckResult struct {
	Database   string `json:"database"`
	State      string `json:"state"` // "ok" | "drift" | "no-baseline" | "error"
	Error      string `json:"error,omitempty"`
	Changes    int    `json:"changes"`
	DurationMs int64  `json:"duration_ms"`
}

type FleetSnapshotResult struct {
	Database string `json:"database"`
	Tables   int    `json:"tables"`
	Error    string `json:"error,omitempty"`
}

func (a *App) FleetDiscover(provider, orgOrAccount, platformToken, dbToken string) ([]FleetDBEntry, error) {
	var dbs []fleet.Database
	var err error
	switch provider {
	case "turso":
		dbs, err = fleet.DiscoverTurso(orgOrAccount, platformToken, dbToken)
	case "d1":
		dbs, err = fleet.DiscoverD1(orgOrAccount, platformToken)
	default:
		return nil, fmt.Errorf("unknown provider %q (use turso or d1)", provider)
	}
	if err != nil {
		return nil, err
	}
	out := make([]FleetDBEntry, len(dbs))
	for i, d := range dbs {
		out[i] = FleetDBEntry{Name: d.Name, DSN: d.DSN, Tags: d.Tags}
	}
	return out, nil
}

func (a *App) FleetSnapshot(entries []FleetDBEntry) []FleetSnapshotResult {
	baseDir := fleetBaselineDir()
	cfg := &fleet.Config{BaselinesDir: baseDir}
	dbs := toFleetDatabases(entries)
	raw := fleet.Snapshot(cfg, dbs, 0)
	out := make([]FleetSnapshotResult, len(raw))
	for i, r := range raw {
		res := FleetSnapshotResult{Database: r.Database, Tables: r.Tables}
		if r.Err != nil {
			res.Error = r.Err.Error()
		}
		out[i] = res
	}
	return out
}

func (a *App) FleetCheck(entries []FleetDBEntry) []FleetCheckResult {
	baseDir := fleetBaselineDir()
	cfg := &fleet.Config{BaselinesDir: baseDir}
	dbs := toFleetDatabases(entries)
	report := fleet.Check(cfg, dbs, 0)
	out := make([]FleetCheckResult, len(report.Results))
	for i, r := range report.Results {
		res := FleetCheckResult{
			Database:   r.Database,
			State:      r.State,
			Error:      r.Error,
			DurationMs: r.Duration.Milliseconds(),
		}
		if r.Drift != nil {
			res.Changes = len(r.Drift.Changes)
		}
		out[i] = res
	}
	return out
}

func fleetBaselineDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".litescope", "fleet", "baselines")
}

func toFleetDatabases(entries []FleetDBEntry) []fleet.Database {
	dbs := make([]fleet.Database, len(entries))
	for i, e := range entries {
		dbs[i] = fleet.Database{Name: e.Name, DSN: e.DSN, Tags: e.Tags}
	}
	return dbs
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func sendWebhookAlert(webhookURL, dbPath, message string) {
	payload := fmt.Sprintf(`{"text":"🚨 *Litescope drift detected*\nDatabase: %s\n%s"}`, dbPath, message)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", strings.NewReader(payload))
	if err == nil {
		resp.Body.Close()
	}
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
