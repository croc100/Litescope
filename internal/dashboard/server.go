// Package dashboard serves a local, self-hosted web view of a Litescope fleet.
//
// It is intentionally dependency-free: the frontend is a single embedded HTML
// file (no build step, no node_modules) and the server is the Go standard
// library. It runs on the operator's own machine or server — no cloud, no
// outbound telemetry. The hosted, multi-user, org-scoped dashboard is a
// separate Enterprise offering; this one is free.
package dashboard

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/croc100/litescope/internal/fleet"
)

//go:embed assets/*
var assets embed.FS

// Overview is the payload the dashboard renders. It is recomputed on each
// request so the browser always reflects the current state of the fleet.
type Overview struct {
	FleetName   string                   `json:"fleet_name"`
	Total       int                      `json:"total"`
	Preview     bool                     `json:"preview"`     // true when a Free preview cap is in effect
	PreviewCap  int                      `json:"preview_cap"` // databases shown under the cap
	FullTotal   int                      `json:"full_total"`  // databases the fleet actually contains
	Health      *fleet.HealthReport      `json:"health"`
	Fingerprint *fleet.FingerprintReport `json:"fingerprint"`
	GeneratedAt time.Time                `json:"generated_at"`
}

// Provider computes a fresh Overview. The CLI supplies this so the dashboard
// package stays decoupled from license gating and config loading.
type Provider func() (*Overview, error)

// ImportFn ingests an uploaded data file (CSV/TSV/JSON) and returns a short
// human summary (e.g. the new table name). The CLI supplies it so this package
// stays decoupled from the importer; when nil, drag-drop import is disabled.
type ImportFn func(filename string, data io.Reader) (summary string, err error)

// TableInfo describes one table available for browsing in a database.
type TableInfo struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

// QueryResult is the outcome of a read-only SQL query against one database.
type QueryResult struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	Truncated  bool     `json:"truncated"`
	DurationMs int64    `json:"duration_ms"`
}

// BrowseResult is one page of a table, with server-side sorting and the total
// row count so the dashboard can paginate.
type BrowseResult struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Total   int64    `json:"total"`
	Offset  int      `json:"offset"`
	Limit   int      `json:"limit"`
	OrderBy string   `json:"order_by,omitempty"`
	Dir     string   `json:"dir,omitempty"`
}

// SchemaColumn is one column in an ERD table node. Drift, when set, marks how
// the column deviates from the fleet's canonical schema: "added" (present here,
// absent in canonical), "changed" (type/not-null differs), or "missing"
// (present in canonical, absent here).
type SchemaColumn struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	PK    bool   `json:"pk"`
	FK    bool   `json:"fk"`
	Drift string `json:"drift,omitempty"`
}

// SchemaEdge is a foreign-key relationship between two tables.
type SchemaEdge struct {
	From   string `json:"from"`   // table holding the foreign key
	To     string `json:"to"`     // referenced table
	Column string `json:"column"` // column in From that references To
}

// SchemaGraph is the entity-relationship graph of one database, rendered as an
// interactive ERD in the dashboard.
type SchemaGraph struct {
	Tables      []SchemaTable      `json:"tables"`
	Edges       []SchemaEdge       `json:"edges"`
	Fingerprint *SchemaFingerprint `json:"fingerprint,omitempty"` // fleet drift overlay
}

// SchemaTable is one entity (table) in the ERD. Drift "added" marks a table
// present here but absent from canonical; Ghost marks a table present in
// canonical but missing here (rendered as a placeholder).
type SchemaTable struct {
	Name    string         `json:"name"`
	Columns []SchemaColumn `json:"columns"`
	Drift   string         `json:"drift,omitempty"`
	Ghost   bool           `json:"ghost,omitempty"`
}

// SchemaFingerprint places one database's ERD in the context of the whole fleet:
// which schema cluster it belongs to and how far it has drifted from canonical.
type SchemaFingerprint struct {
	ClusterID    string `json:"cluster_id"`
	IsCanonical  bool   `json:"is_canonical"`
	CanonicalID  string `json:"canonical_id"`
	ClusterCount int    `json:"cluster_count"` // databases sharing this exact schema
	FleetTotal   int    `json:"fleet_total"`   // databases fingerprinted
	DriftTables  int    `json:"drift_tables"`  // tables differing from canonical
	DriftColumns int    `json:"drift_columns"` // columns differing from canonical
}

// SchemaFn returns the ERD graph of the named database. The CLI supplies it so
// this package stays decoupled from schema loading; when nil, the ERD is
// disabled.
type SchemaFn func(dbName string) (*SchemaGraph, error)

// DiffColumnChange records a column whose definition changed between two
// databases.
type DiffColumnChange struct {
	Name    string `json:"name"`
	OldType string `json:"old_type,omitempty"`
	NewType string `json:"new_type,omitempty"`
}

// DiffTable is one table's worth of schema change between the old and new
// database. Status is "added", "removed", or "changed".
type DiffTable struct {
	Name           string             `json:"name"`
	Status         string             `json:"status"`
	AddedColumns   []string           `json:"added_columns,omitempty"`
	RemovedColumns []string           `json:"removed_columns,omitempty"`
	ChangedColumns []DiffColumnChange `json:"changed_columns,omitempty"`
	AddedIndexes   []string           `json:"added_indexes,omitempty"`
	RemovedIndexes []string           `json:"removed_indexes,omitempty"`
}

// DiffData is the row-count delta for one table between the two databases.
type DiffData struct {
	Table   string `json:"table"`
	Added   int64  `json:"added"`
	Removed int64  `json:"removed"`
	Changed int64  `json:"changed"`
}

// DiffResult is the full comparison rendered in the dashboard's diff panel —
// "see what changes before you apply it".
type DiffResult struct {
	Old         string      `json:"old"`
	New         string      `json:"new"`
	Schema      []DiffTable `json:"schema"`
	Data        []DiffData  `json:"data"`
	Identical   bool        `json:"identical"`
	DataSkipped bool        `json:"data_skipped"` // true for remote sources (schema-only)
}

// DiffFn compares two databases by fleet name (old → new) and returns the
// curated diff. The CLI supplies it so this package stays decoupled from DSN
// resolution and the diff engine; when nil, the diff panel is disabled.
type DiffFn func(oldDB, newDB string) (*DiffResult, error)

// LockFinding is one static lock-configuration issue (mirrors locks.Finding).
type LockFinding struct {
	Severity string `json:"severity"` // "ok" | "warning" | "critical"
	Rule     string `json:"rule"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
	Fix      string `json:"fix"`
}

// LockHolder is a process holding the database file open (mirrors locks.Holder).
type LockHolder struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Access  string `json:"access"`
}

// LockReport is the lock doctor's verdict for one database — static PRAGMA
// diagnosis plus, for local files, a live probe of who holds the lock right now.
type LockReport struct {
	Source     string            `json:"source"`
	Provider   string            `json:"provider"` // "local" | "d1" | "turso"
	Verdict    string            `json:"verdict"`  // "ok" | "attention" | "critical"
	Pragmas    map[string]string `json:"pragmas,omitempty"`
	WALBytes   int64             `json:"wal_bytes"`
	Findings   []LockFinding     `json:"findings"`
	LiveState  string            `json:"live_state,omitempty"` // "free" | "locked" | "readable" | "error"
	LiveDetail string            `json:"live_detail,omitempty"`
	WaitMS     int64             `json:"wait_ms,omitempty"`
	Holders    []LockHolder      `json:"holders,omitempty"`
	Hint       string            `json:"hint,omitempty"`
}

// LocksFn diagnoses lock health for the named database. The CLI supplies it so
// this package stays decoupled from DSN resolution; when nil, the panel hides.
type LocksFn func(dbName string) (*LockReport, error)

// AutopilotAction is one optimization step autopilot would take (mirrors
// autopilot.Action). Risk is "safe" (auto-applied) or "risky" (needs review).
type AutopilotAction struct {
	Kind   string `json:"kind"` // analyze | optimize | vacuum | create-index | drop-index
	Risk   string `json:"risk"` // safe | risky
	Table  string `json:"table,omitempty"`
	SQL    string `json:"sql,omitempty"` // the statement autopilot would run (empty = guidance only)
	Reason string `json:"reason"`        // plain-language explanation
}

// AutopilotPlan is the DBA self-driving moat's verdict for one database: the set
// of maintenance and indexing actions it would take, surfaced read-only so the
// operator can see the plan before applying it from the CLI. Queries records how
// many observed SQL console queries fed the EXPLAIN-based index advice.
type AutopilotPlan struct {
	Source  string            `json:"source"`
	Actions []AutopilotAction `json:"actions"`
	Safe    int               `json:"safe"`    // count of safe (auto-applied) actions
	Risky   int               `json:"risky"`   // count of risky (review-first) actions
	Queries int               `json:"queries"` // observed queries fed to the advisor
}

// AutopilotFn builds the autopilot plan for the named database. The CLI supplies
// it so this package stays decoupled from DSN resolution; when nil, the panel
// hides.
type AutopilotFn func(dbName string) (*AutopilotPlan, error)

// SnapshotInfo is one stored point-in-time backup (mirrors snapshot.Snapshot).
type SnapshotInfo struct {
	Path      string    `json:"path"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

// BackupReport is the snapshot history for one database — the file-superpower
// moat (point-in-time backups) surfaced in the dashboard.
type BackupReport struct {
	Source    string         `json:"source"`
	Snapshots []SnapshotInfo `json:"snapshots"`
}

// SnapshotsFn lists the snapshots of the named database. The CLI supplies it so
// this package stays decoupled from DSN resolution; when nil, the panel hides.
type SnapshotsFn func(dbName string) (*BackupReport, error)

// CreateSnapshotFn takes a new snapshot of the named database with an optional
// label and returns the created snapshot.
type CreateSnapshotFn func(dbName, label string) (*SnapshotInfo, error)

// RestoreSnapshotFn overwrites the named database with one of its snapshots
// (identified by path). A safety snapshot of the current state is taken first.
type RestoreSnapshotFn func(dbName, snapshotPath string) error

// TimeTravelInfo describes the Cloudflare D1 Time Travel window for one
// database — the file-superpower moat's D1-native counterpart to local
// snapshots (D1 keeps 30 days of continuous history, no local backup needed).
type TimeTravelInfo struct {
	Source string    `json:"source"`
	Oldest time.Time `json:"oldest"`
	Now    time.Time `json:"now"`
}

// TimeTravelFn reports the Time Travel window for the named database. The CLI
// supplies it and rejects non-D1 sources; when nil, the panel hides.
type TimeTravelFn func(dbName string) (*TimeTravelInfo, error)

// RewindResult is the outcome of a Time Travel restore (mirrors
// connector.D1TimeTravelResult, kept local so this package stays decoupled
// from the D1 connector).
type RewindResult struct {
	Bookmark  string `json:"bookmark"`
	Timestamp string `json:"timestamp"`
}

// RewindFn restores the named database to the given point in time (RFC 3339 or
// a human form like "2h ago"). Destructive — the CLI supplies it only for D1
// sources, which is why it lives alongside TimeTravelFn rather than the local
// backup panel's RestoreSnapshotFn.
type RewindFn func(dbName, to string) (*RewindResult, error)

// TablesFn lists the browsable tables of the named database. The CLI supplies it
// so this package stays decoupled from DSN resolution; when nil, the data
// browser is disabled.
type TablesFn func(dbName string) ([]TableInfo, error)

// QueryFn runs a read-only SQL query against the named database. Read-only
// safety is enforced by the CLI at the engine level (mode=ro + query_only).
type QueryFn func(dbName, sql string) (*QueryResult, error)

// BrowseFn returns one paginated, optionally sorted page of a table. The CLI
// supplies it; it validates the table and sort column to prevent injection.
type BrowseFn func(dbName, table, orderBy, dir string, limit, offset int) (*BrowseResult, error)

// Server serves the embedded dashboard and its JSON API.
type Server struct {
	provider         Provider
	importFn         ImportFn
	tablesFn         TablesFn
	queryFn          QueryFn
	browseFn         BrowseFn
	schemaFn         SchemaFn
	diffFn           DiffFn
	locksFn          LocksFn
	autopilotFn      AutopilotFn
	snapshotsFn      SnapshotsFn
	createSnapshotFn CreateSnapshotFn
	restoreFn        RestoreSnapshotFn
	timeTravelFn     TimeTravelFn
	rewindFn         RewindFn
	history          *History
}

// New builds a dashboard server backed by the given provider.
func New(provider Provider) *Server {
	return &Server{provider: provider}
}

// SetImportHandler enables drag-drop import in the dashboard.
func (s *Server) SetImportHandler(fn ImportFn) { s.importFn = fn }

// SetDataBrowser enables the read-only data browser and SQL console.
func (s *Server) SetDataBrowser(tables TablesFn, query QueryFn) {
	s.tablesFn = tables
	s.queryFn = query
}

// SetTableBrowser enables paginated, sortable table browsing.
func (s *Server) SetTableBrowser(fn BrowseFn) { s.browseFn = fn }

// SetSchemaProvider enables the interactive ERD.
func (s *Server) SetSchemaProvider(fn SchemaFn) { s.schemaFn = fn }

// SetDiffProvider enables the visual schema/data diff panel.
func (s *Server) SetDiffProvider(fn DiffFn) { s.diffFn = fn }

// SetLockDoctor enables the lock doctor panel.
func (s *Server) SetLockDoctor(fn LocksFn) { s.locksFn = fn }

// SetAutopilot enables the DBA autopilot panel (read-only plan preview).
func (s *Server) SetAutopilot(fn AutopilotFn) { s.autopilotFn = fn }

// SetBackup enables the snapshot/restore (backup) panel. list is required;
// create and restore enable the respective actions when non-nil.
func (s *Server) SetBackup(list SnapshotsFn, create CreateSnapshotFn, restore RestoreSnapshotFn) {
	s.snapshotsFn = list
	s.createSnapshotFn = create
	s.restoreFn = restore
}

// SetTimeTravel enables the D1 Time Travel panel (window info + restore).
// info is required; rewind enables the restore action when non-nil.
func (s *Server) SetTimeTravel(info TimeTravelFn, rewind RewindFn) {
	s.timeTravelFn = info
	s.rewindFn = rewind
}

// SetHistory enables the fleet-health timeline, persisting a snapshot on each
// overview request to the given store.
func (s *Server) SetHistory(h *History) { s.history = h }

// Handler returns the HTTP handler (useful for tests and custom hosting).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// embed.FS is built at compile time; a failure here is a programmer error.
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		ov, err := s.provider()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if s.history != nil {
			_ = s.history.Record(ov) // best-effort; history must never break the view
		}
		writeJSON(w, http.StatusOK, ov)
	})

	// Returns the fleet-health timeline for the trend view. The optional `since`
	// query parameter (unix milliseconds) bounds how far back to read.
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		if s.history == nil {
			writeJSON(w, http.StatusOK, []Sample{})
			return
		}
		var since int64
		if v := r.URL.Query().Get("since"); v != "" {
			since, _ = strconv.ParseInt(v, 10, 64)
		}
		samples, err := s.history.Series(since)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if samples == nil {
			samples = []Sample{}
		}
		writeJSON(w, http.StatusOK, samples)
	})

	// Advertises which optional features are available to the frontend.
	mux.HandleFunc("/api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{
			"import":             s.importFn != nil,
			"data":               s.tablesFn != nil && s.queryFn != nil,
			"browse":             s.browseFn != nil,
			"schema":             s.schemaFn != nil,
			"diff":               s.diffFn != nil,
			"locks":              s.locksFn != nil,
			"autopilot":          s.autopilotFn != nil,
			"backup":             s.snapshotsFn != nil,
			"backup_create":      s.createSnapshotFn != nil,
			"backup_restore":     s.restoreFn != nil,
			"timetravel":         s.timeTravelFn != nil,
			"timetravel_restore": s.rewindFn != nil,
		})
	})

	// Builds the DBA autopilot plan (read-only) for one database.
	mux.HandleFunc("/api/autopilot", func(w http.ResponseWriter, r *http.Request) {
		if s.autopilotFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "autopilot is disabled"})
			return
		}
		res, err := s.autopilotFn(r.URL.Query().Get("db"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Lists the snapshots (point-in-time backups) of one database.
	mux.HandleFunc("/api/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if s.snapshotsFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "backups are disabled"})
			return
		}
		res, err := s.snapshotsFn(r.URL.Query().Get("db"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Takes a new snapshot of one database.
	mux.HandleFunc("/api/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if s.createSnapshotFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "snapshot creation is disabled"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST to create a snapshot"})
			return
		}
		var req struct {
			DB    string `json:"db"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		snap, err := s.createSnapshotFn(req.DB, req.Label)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, snap)
	})

	// Restores one database from a chosen snapshot (a safety snapshot of the
	// current state is taken first by the CLI-supplied handler).
	mux.HandleFunc("/api/restore", func(w http.ResponseWriter, r *http.Request) {
		if s.restoreFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "restore is disabled"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST to restore"})
			return
		}
		var req struct {
			DB   string `json:"db"`
			Path string `json:"path"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.restoreFn(req.DB, req.Path); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	// Reports the D1 Time Travel window for one database.
	mux.HandleFunc("/api/timetravel", func(w http.ResponseWriter, r *http.Request) {
		if s.timeTravelFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "time travel is disabled"})
			return
		}
		res, err := s.timeTravelFn(r.URL.Query().Get("db"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Restores one D1 database to a point in time via Cloudflare Time Travel.
	mux.HandleFunc("/api/rewind", func(w http.ResponseWriter, r *http.Request) {
		if s.rewindFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "rewind is disabled"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST to rewind"})
			return
		}
		var req struct {
			DB string `json:"db"`
			To string `json:"to"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		res, err := s.rewindFn(req.DB, req.To)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Diagnoses lock health (static PRAGMA + live probe) for one database.
	mux.HandleFunc("/api/locks", func(w http.ResponseWriter, r *http.Request) {
		if s.locksFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "lock doctor is disabled"})
			return
		}
		res, err := s.locksFn(r.URL.Query().Get("db"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if s.history != nil && res.LiveState != "" {
			var holders []LockHolderInfo
			for _, hd := range res.Holders {
				holders = append(holders, LockHolderInfo{PID: hd.PID, Command: hd.Command})
			}
			_ = s.history.RecordLockEvent(res.Source, res.LiveState, res.WaitMS, holders, res.LiveDetail)
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Returns the per-database lock-contention timeline (event-driven captures
	// from live probes, not polling) for the lock doctor's trend view. The
	// optional `since` query parameter (unix milliseconds) bounds how far back.
	mux.HandleFunc("/api/locks/history", func(w http.ResponseWriter, r *http.Request) {
		if s.history == nil {
			writeJSON(w, http.StatusOK, []LockEvent{})
			return
		}
		var since int64
		if v := r.URL.Query().Get("since"); v != "" {
			since, _ = strconv.ParseInt(v, 10, 64)
		}
		events, err := s.history.LockSeries(r.URL.Query().Get("db"), since)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if events == nil {
			events = []LockEvent{}
		}
		writeJSON(w, http.StatusOK, events)
	})

	// Compares two databases (old → new) and returns the curated schema/data diff.
	mux.HandleFunc("/api/diff", func(w http.ResponseWriter, r *http.Request) {
		if s.diffFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "diff is disabled"})
			return
		}
		q := r.URL.Query()
		res, err := s.diffFn(q.Get("old"), q.Get("new"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Returns the ERD graph (tables, columns, foreign keys) of a database.
	mux.HandleFunc("/api/schema", func(w http.ResponseWriter, r *http.Request) {
		if s.schemaFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "ERD is disabled"})
			return
		}
		g, err := s.schemaFn(r.URL.Query().Get("db"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, g)
	})

	// Lists the browsable tables of a database (read-only).
	mux.HandleFunc("/api/tables", func(w http.ResponseWriter, r *http.Request) {
		if s.tablesFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "data browser is disabled"})
			return
		}
		tables, err := s.tablesFn(r.URL.Query().Get("db"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tables": tables})
	})

	// Runs a read-only SQL query against a database. Writes are rejected at the
	// engine level by the CLI-supplied handler, never by parsing the SQL.
	mux.HandleFunc("/api/query", func(w http.ResponseWriter, r *http.Request) {
		if s.queryFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "data browser is disabled"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST a query"})
			return
		}
		var req struct {
			DB  string `json:"db"`
			SQL string `json:"sql"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		res, err := s.queryFn(req.DB, req.SQL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	// Returns one paginated, optionally sorted page of a table.
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		if s.browseFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "table browser is disabled"})
			return
		}
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		res, err := s.browseFn(q.Get("db"), q.Get("table"), q.Get("order"), q.Get("dir"), limit, offset)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("/api/import", func(w http.ResponseWriter, r *http.Request) {
		if s.importFn == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "import is disabled"})
			return
		}
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST a file"})
			return
		}
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file uploaded"})
			return
		}
		defer file.Close()
		summary, err := s.importFn(hdr.Filename, file)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true", "summary": summary})
	})

	return mux
}

// ListenAndServe starts the dashboard on addr (e.g. "127.0.0.1:7575").
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
	}
}
