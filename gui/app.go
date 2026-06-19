package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

// OpenFleetConfig prompts for a litescope.fleet.yaml file and returns its path.
func (a *App) OpenFleetConfig() string {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "Fleet config", Pattern: "*.yaml;*.yml"},
		},
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
	Columns  []string        `json:"Columns"`
	Rows     [][]interface{} `json:"Rows"`
	Total    int64           `json:"Total"`
	RowIDs   []int64         `json:"RowIDs"`   // rowid per row, aligned with Rows (when HasRowID)
	HasRowID bool            `json:"HasRowID"` // false for WITHOUT ROWID tables — editing disabled
}

// quoteIdent wraps a SQLite identifier in double quotes, escaping any embedded
// quotes — the safe way to interpolate a column/table name we control.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// BrowseTable is QueryTable plus server-side sort, search, and a filtered count.
// orderBy must be one of the table's real columns (otherwise it's ignored, so a
// bad/crafted value can't inject SQL). search matches any column via LIKE and is
// always passed as a bound parameter. Total reflects the active filter.
func (a *App) BrowseTable(path, table string, limit, offset int, orderBy string, desc bool, search string) (*TableRows, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Real column names — used to validate orderBy and to build the search filter.
	cols, err := tableColumns(db, table)
	if err != nil {
		return nil, err
	}
	valid := make(map[string]bool, len(cols))
	for _, c := range cols {
		valid[c] = true
	}

	where := ""
	var args []interface{}
	if s := strings.TrimSpace(search); s != "" {
		parts := make([]string, 0, len(cols))
		for _, c := range cols {
			parts = append(parts, "CAST("+quoteIdent(c)+" AS TEXT) LIKE ? ESCAPE '\\'")
			args = append(args, "%"+likeEscape(s)+"%")
		}
		if len(parts) > 0 {
			where = " WHERE " + strings.Join(parts, " OR ")
		}
	}

	qt := quoteIdent(table)
	result := &TableRows{Columns: cols}
	db.QueryRow("SELECT COUNT(*) FROM "+qt+where, args...).Scan(&result.Total)

	order := ""
	if valid[orderBy] {
		dir := "ASC"
		if desc {
			dir = "DESC"
		}
		order = " ORDER BY " + quoteIdent(orderBy) + " " + dir
	}

	// Select rowid alongside the row so the UI can target edits/deletes. Tables
	// declared WITHOUT ROWID have no rowid; for those we fall back to read-only.
	result.HasRowID = tableHasRowID(db, qt)
	sel := "SELECT * FROM "
	if result.HasRowID {
		sel = "SELECT rowid, * FROM "
	}
	dataArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := db.Query(sel+qt+where+order+" LIMIT ? OFFSET ?", dataArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		if result.HasRowID {
			full, err := scanRowSlice(rows, len(cols)+1)
			if err != nil {
				continue
			}
			rid, _ := full[0].(int64)
			result.RowIDs = append(result.RowIDs, rid)
			result.Rows = append(result.Rows, full[1:])
		} else {
			row, err := scanRowSlice(rows, len(cols))
			if err != nil {
				continue
			}
			result.Rows = append(result.Rows, row)
		}
	}
	return result, rows.Err()
}

// tableHasRowID reports whether the table exposes a usable rowid (false for
// WITHOUT ROWID tables).
func tableHasRowID(db *sql.DB, quotedTable string) bool {
	var x interface{}
	err := db.QueryRow("SELECT rowid FROM " + quotedTable + " LIMIT 1").Scan(&x)
	if err == sql.ErrNoRows {
		return true // empty table, but rowid is supported
	}
	return err == nil
}

// UpdateCell sets one column of one row (identified by rowid). value is written
// as text; SQLite applies the column's affinity. isNull writes SQL NULL instead.
func (a *App) UpdateCell(path, table string, rowid int64, column, value string, isNull bool) error {
	if err := assertColumn(path, table, column); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	q := "UPDATE " + quoteIdent(table) + " SET " + quoteIdent(column) + " = ? WHERE rowid = ?"
	var arg interface{}
	if !isNull {
		arg = value
	}
	_, err = db.Exec(q, arg, rowid)
	return err
}

// DeleteRow removes the row with the given rowid.
func (a *App) DeleteRow(path, table string, rowid int64) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("DELETE FROM "+quoteIdent(table)+" WHERE rowid = ?", rowid)
	return err
}

// InsertRow appends a new row using each column's defaults and returns its
// rowid, so the UI can let the user fill it in. Fails if a NOT NULL column has
// no default (the error is surfaced to the user).
func (a *App) InsertRow(path, table string) (int64, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	r, err := db.Exec("INSERT INTO " + quoteIdent(table) + " DEFAULT VALUES")
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// assertColumn verifies column belongs to table — guards the UpdateCell SET
// identifier, which can't be a bound parameter.
func assertColumn(path, table, column string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	cols, err := tableColumns(db, table)
	if err != nil {
		return err
	}
	for _, c := range cols {
		if c == column {
			return nil
		}
	}
	return fmt.Errorf("unknown column %q", column)
}

// tableColumns returns the column names of a table in declared order.
func tableColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		cols = append(cols, n)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %q has no columns or does not exist", table)
	}
	return cols, rows.Err()
}

// likeEscape escapes LIKE wildcards so a user's literal % or _ matches itself.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// scanRowSlice scans the current row into a []interface{} with []byte→string.
func scanRowSlice(rows *sql.Rows, n int) ([]interface{}, error) {
	vals := make([]interface{}, n)
	ptrs := make([]interface{}, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := make([]interface{}, n)
	for i, v := range vals {
		if b, ok := v.([]byte); ok {
			row[i] = string(b)
		} else {
			row[i] = v
		}
	}
	return row, nil
}

// SQLResult is the outcome of a RunSQL call: a result set for queries, or an
// affected-row count for writes.
type SQLResult struct {
	Columns      []string        `json:"columns"`
	Rows         [][]interface{} `json:"rows"`
	RowsAffected int64           `json:"rowsAffected"`
	IsQuery      bool            `json:"isQuery"`
	Truncated    bool            `json:"truncated"`
	DurationMs   int64           `json:"durationMs"`
}

// sqlMaxRows caps how many rows a single query returns to the UI.
const sqlMaxRows = 5000

// RunSQL executes an arbitrary SQL statement. When write is false the database
// is opened read-only AND query_only is enforced, so any mutation fails at the
// engine level — we never rely on parsing the SQL to decide what's safe.
func (a *App) RunSQL(path, query string, write bool) (*SQLResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}

	dsn := path
	if !write {
		// file: URI with mode=ro opens the OS file read-only.
		dsn = "file:" + path + "?mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if !write {
		// Belt-and-suspenders: query_only rejects writes even via ATTACH.
		if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
			return nil, err
		}
	}

	start := time.Now()
	isQuery := isReadVerb(q)
	res := &SQLResult{IsQuery: isQuery}

	if isQuery {
		rows, err := db.Query(q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		res.Columns = cols
		for rows.Next() {
			if len(res.Rows) >= sqlMaxRows {
				res.Truncated = true
				break
			}
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
			res.Rows = append(res.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		r, err := db.Exec(q)
		if err != nil {
			return nil, err
		}
		res.RowsAffected, _ = r.RowsAffected()
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// isReadVerb reports whether a statement is a read (returns rows) vs a write.
func isReadVerb(q string) bool {
	// strip leading SQL line comments / whitespace
	for {
		q = strings.TrimSpace(q)
		if strings.HasPrefix(q, "--") {
			if i := strings.IndexByte(q, '\n'); i >= 0 {
				q = q[i+1:]
				continue
			}
		}
		break
	}
	up := strings.ToUpper(q)
	switch {
	case strings.HasPrefix(up, "SELECT"),
		strings.HasPrefix(up, "WITH"),
		strings.HasPrefix(up, "EXPLAIN"),
		strings.HasPrefix(up, "PRAGMA"),
		strings.HasPrefix(up, "VALUES"):
		return true
	default:
		return false
	}
}

// ExportResult reports how an export went.
type ExportResult struct {
	Path string `json:"path"`
	Rows int64  `json:"rows"`
}

// ExportSQL runs a read-only query and streams every row to destPath as CSV or
// JSON (format "csv" | "json"). Unlike RunSQL it is not capped — the full result
// is written. The source database is opened read-only.
func (a *App) ExportSQL(dbPath, query, destPath, format string) (*ExportResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("empty query")
	}
	if !isReadVerb(q) {
		return nil, fmt.Errorf("only read queries (SELECT/WITH/...) can be exported")
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()

	f, err := os.Create(destPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var count int64
	switch strings.ToLower(format) {
	case "json":
		if _, err := f.WriteString("[\n"); err != nil {
			return nil, err
		}
		enc := json.NewEncoder(f)
		for rows.Next() {
			m, err := scanRowMap(rows, cols)
			if err != nil {
				return nil, err
			}
			if count > 0 {
				if _, err := f.WriteString(",\n"); err != nil {
					return nil, err
				}
			}
			if err := enc.Encode(m); err != nil {
				return nil, err
			}
			count++
		}
		if _, err := f.WriteString("]\n"); err != nil {
			return nil, err
		}
	default: // csv
		w := csv.NewWriter(f)
		if err := w.Write(cols); err != nil {
			return nil, err
		}
		for rows.Next() {
			vals, err := scanRowStrings(rows, cols)
			if err != nil {
				return nil, err
			}
			if err := w.Write(vals); err != nil {
				return nil, err
			}
			count++
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &ExportResult{Path: destPath, Rows: count}, nil
}

// scanRowMap scans the current row into a column→value map (for JSON export).
func scanRowMap(rows *sql.Rows, cols []string) (map[string]interface{}, error) {
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	m := make(map[string]interface{}, len(cols))
	for i, c := range cols {
		if b, ok := vals[i].([]byte); ok {
			m[c] = string(b)
		} else {
			m[c] = vals[i]
		}
	}
	return m, nil
}

// scanRowStrings scans the current row into string cells (for CSV export).
func scanRowStrings(rows *sql.Rows, cols []string) ([]string, error) {
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make([]string, len(cols))
	for i := range vals {
		out[i] = cellToString(vals[i])
	}
	return out, nil
}

func cellToString(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
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

// FleetLoadConfig loads a local fleet config (litescope.fleet.yaml) and returns
// its active (non-quarantined) databases. Relative local-file DSNs are resolved
// against the config's directory so the fleet works regardless of the app's
// working directory; remote DSNs (turso://, d1://) are passed through unchanged.
func (a *App) FleetLoadConfig(path string) ([]FleetDBEntry, error) {
	cfg, err := fleet.Load(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	active := cfg.Active()
	out := make([]FleetDBEntry, len(active))
	for i, d := range active {
		dsn := d.DSN
		if isRemoteDSN(dsn) {
			// remote — leave as-is
		} else if !filepath.IsAbs(dsn) {
			dsn = filepath.Join(dir, dsn)
		}
		out[i] = FleetDBEntry{Name: d.Name, DSN: dsn, Tags: d.Tags}
	}
	return out, nil
}

func isRemoteDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "turso://") || strings.HasPrefix(dsn, "d1://")
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

// ── Fleet: fingerprint ──────────────────────────────────────────────────────

type FleetFingerprintCluster struct {
	ID          string   `json:"id"`
	Count       int      `json:"count"`
	IsCanonical bool     `json:"is_canonical"`
	Members     []string `json:"members"`
	Drift       []string `json:"drift,omitempty"` // human-readable "missing table x" lines vs canonical
}

type FleetFingerprintResult struct {
	Total       int                       `json:"total"`
	Distinct    int                       `json:"distinct"`
	Clusters    []FleetFingerprintCluster `json:"clusters"`
	Unreachable []string                  `json:"unreachable,omitempty"`
}

func (a *App) FleetFingerprint(entries []FleetDBEntry) FleetFingerprintResult {
	dbs := toFleetDatabases(entries)
	report := fleet.Fingerprint(dbs, 0)

	out := FleetFingerprintResult{Total: report.Total, Distinct: len(report.Clusters)}
	for _, c := range report.Clusters {
		out.Clusters = append(out.Clusters, FleetFingerprintCluster{
			ID:          c.ID,
			Count:       c.Count,
			IsCanonical: c.IsCanonical,
			Members:     c.Members,
			Drift:       driftToLines(c.Drift),
		})
	}
	for _, u := range report.Unreachable {
		out.Unreachable = append(out.Unreachable, u.Database)
	}
	return out
}

// ── Fleet: converge ─────────────────────────────────────────────────────────

type FleetConvergeCluster struct {
	ClusterID   string   `json:"cluster_id"`
	Count       int      `json:"count"`
	Statements  int      `json:"statements"`
	Destructive bool     `json:"destructive"`
	Drift       []string `json:"drift,omitempty"`
}

type FleetConvergeResult struct {
	CanonicalID     string                 `json:"canonical_id"`
	AlreadyOK       int                    `json:"already_ok"`
	TotalToConverge int                    `json:"total_to_converge"`
	Destructive     bool                   `json:"destructive"`
	Clusters        []FleetConvergeCluster `json:"clusters"`
	Applied         int                    `json:"applied"`  // populated when apply=true
	Failed          int                    `json:"failed"`   // populated when apply=true
	Mode            string                 `json:"mode"`     // "plan" | "dry-run" | "applied"
}

// FleetConverge plans (and optionally applies) convergence to the largest
// cluster. When apply is false, only the plan is returned. When apply is true,
// a destructive plan is refused unless force is set.
func (a *App) FleetConverge(entries []FleetDBEntry, apply, force bool) (FleetConvergeResult, error) {
	dbs := toFleetDatabases(entries)
	plan, err := fleet.PlanConvergence(dbs, nil, 0)
	if err != nil {
		return FleetConvergeResult{}, err
	}

	out := FleetConvergeResult{
		CanonicalID:     plan.CanonicalID,
		AlreadyOK:       plan.AlreadyOK,
		TotalToConverge: plan.TotalToConverge,
		Destructive:     plan.HasDestructive(),
		Mode:            "plan",
	}
	for _, c := range plan.Clusters {
		out.Clusters = append(out.Clusters, FleetConvergeCluster{
			ClusterID:   c.ClusterID,
			Count:       len(c.Members),
			Statements:  c.Statements,
			Destructive: c.Destructive,
			Drift:       driftToLines(c.Drift),
		})
	}

	if !apply || plan.TotalToConverge == 0 {
		return out, nil
	}
	if plan.HasDestructive() && !force {
		return out, fmt.Errorf("convergence is destructive — pass force to proceed")
	}

	report := fleet.Converge(plan, fleet.RolloutOptions{})
	applied, failed, _ := report.Counts()
	out.Applied = applied
	out.Failed = failed
	out.Mode = "applied"
	return out, nil
}

// ── Fleet: health ───────────────────────────────────────────────────────────

type FleetHealthResult struct {
	Database string   `json:"database"`
	Severity string   `json:"severity"` // "ok" | "warning" | "critical"
	Remote   bool     `json:"remote"`
	SizeMB   float64  `json:"size_mb"`
	WALMB    float64  `json:"wal_mb"`
	Issues   []string `json:"issues,omitempty"`
}

func (a *App) FleetHealth(entries []FleetDBEntry, deep bool) []FleetHealthResult {
	dbs := toFleetDatabases(entries)
	report := fleet.Health(dbs, deep, 0)
	out := make([]FleetHealthResult, len(report.Results))
	for i, r := range report.Results {
		out[i] = FleetHealthResult{
			Database: r.Database,
			Severity: r.Report.SeverityLabel,
			Remote:   r.Report.Remote,
			SizeMB:   float64(r.Report.SizeBytes) / (1024 * 1024),
			WALMB:    float64(r.Report.WALBytes) / (1024 * 1024),
			Issues:   r.Report.Issues,
		}
	}
	return out
}

// ── Fleet: recover ──────────────────────────────────────────────────────────

type FleetRecoverResult struct {
	Database string `json:"database"`
	State    string `json:"state"` // "healthy" | "restored" | "quarantined" | "failed" | "remote"
	Detail   string `json:"detail,omitempty"`
}

func (a *App) FleetRecover(entries []FleetDBEntry, dryRun bool) []FleetRecoverResult {
	dbs := toFleetDatabases(entries)
	report := fleet.Recover(dbs, fleet.RecoverOptions{DryRun: dryRun, Quarantine: true})
	out := make([]FleetRecoverResult, len(report.Results))
	for i, r := range report.Results {
		out[i] = FleetRecoverResult{
			Database: r.Database,
			State:    string(r.State),
			Detail:   r.Detail,
		}
	}
	return out
}

// ── Fleet: topology map ─────────────────────────────────────────────────────

type FleetTopologyNode struct {
	Name        string `json:"name"`
	ClusterID   string `json:"cluster_id"` // "" when unreachable / not fingerprinted
	IsCanonical bool   `json:"is_canonical"`
	Severity    string `json:"severity"` // ok | warning | critical
}

type FleetTopologyResult struct {
	Nodes    []FleetTopologyNode       `json:"nodes"`
	Clusters []FleetFingerprintCluster `json:"clusters"` // for the legend (id, count, is_canonical)
}

// FleetTopology builds a per-database map combining schema cluster (fingerprint)
// and operational health into one view — the data behind the topology map.
func (a *App) FleetTopology(entries []FleetDBEntry) FleetTopologyResult {
	dbs := toFleetDatabases(entries)
	fp := fleet.Fingerprint(dbs, 0)
	hr := fleet.Health(dbs, false, 0)

	clusterOf := map[string]string{}
	canonicalOf := map[string]bool{}
	for _, c := range fp.Clusters {
		for _, m := range c.Members {
			clusterOf[m] = c.ID
			canonicalOf[m] = c.IsCanonical
		}
	}
	sevOf := map[string]string{}
	for _, r := range hr.Results {
		sevOf[r.Database] = r.Report.SeverityLabel
	}

	nodes := make([]FleetTopologyNode, 0, len(dbs))
	for _, db := range dbs {
		nodes = append(nodes, FleetTopologyNode{
			Name:        db.Name,
			ClusterID:   clusterOf[db.Name],
			IsCanonical: canonicalOf[db.Name],
			Severity:    sevOf[db.Name],
		})
	}

	out := FleetTopologyResult{Nodes: nodes}
	for _, c := range fp.Clusters {
		out.Clusters = append(out.Clusters, FleetFingerprintCluster{
			ID: c.ID, Count: c.Count, IsCanonical: c.IsCanonical,
		})
	}
	return out
}

// driftToLines renders a schema diff as short human-readable lines, matching the
// CLI's "missing table / extra column" vocabulary.
func driftToLines(drift []diff.TableDiff) []string {
	var lines []string
	for _, td := range drift {
		switch {
		case td.Added:
			lines = append(lines, "+ extra table "+td.Name)
		case td.Removed:
			lines = append(lines, "- missing table "+td.Name)
		default:
			for _, c := range td.AddedColumns {
				lines = append(lines, fmt.Sprintf("+ %s.%s extra column", td.Name, c.Name))
			}
			for _, c := range td.RemovedColumns {
				lines = append(lines, fmt.Sprintf("- %s.%s missing column", td.Name, c.Name))
			}
			for _, c := range td.ChangedColumns {
				lines = append(lines, fmt.Sprintf("~ %s.%s type %s→%s", td.Name, c.Name, c.Old.Type, c.New.Type))
			}
		}
	}
	return lines
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
