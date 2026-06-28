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
	Preview     bool                     `json:"preview"`      // true when a Free preview cap is in effect
	PreviewCap  int                      `json:"preview_cap"`  // databases shown under the cap
	FullTotal   int                      `json:"full_total"`   // databases the fleet actually contains
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
	Old        string      `json:"old"`
	New        string      `json:"new"`
	Schema     []DiffTable `json:"schema"`
	Data       []DiffData  `json:"data"`
	Identical  bool        `json:"identical"`
	DataSkipped bool       `json:"data_skipped"` // true for remote sources (schema-only)
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
	Source    string            `json:"source"`
	Provider  string            `json:"provider"` // "local" | "d1" | "turso"
	Verdict   string            `json:"verdict"`  // "ok" | "attention" | "critical"
	Pragmas   map[string]string `json:"pragmas,omitempty"`
	WALBytes  int64             `json:"wal_bytes"`
	Findings  []LockFinding     `json:"findings"`
	LiveState string            `json:"live_state,omitempty"` // "free" | "locked" | "readable" | "error"
	LiveDetail string           `json:"live_detail,omitempty"`
	WaitMS    int64             `json:"wait_ms,omitempty"`
	Holders   []LockHolder      `json:"holders,omitempty"`
	Hint      string            `json:"hint,omitempty"`
}

// LocksFn diagnoses lock health for the named database. The CLI supplies it so
// this package stays decoupled from DSN resolution; when nil, the panel hides.
type LocksFn func(dbName string) (*LockReport, error)

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
	provider Provider
	importFn ImportFn
	tablesFn TablesFn
	queryFn  QueryFn
	browseFn BrowseFn
	schemaFn SchemaFn
	diffFn   DiffFn
	locksFn  LocksFn
	history  *History
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
			"import": s.importFn != nil,
			"data":   s.tablesFn != nil && s.queryFn != nil,
			"browse": s.browseFn != nil,
			"schema": s.schemaFn != nil,
			"diff":   s.diffFn != nil,
			"locks":  s.locksFn != nil,
		})
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
		writeJSON(w, http.StatusOK, res)
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
