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

// SchemaColumn is one column in an ERD table node.
type SchemaColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
	PK   bool   `json:"pk"`
	FK   bool   `json:"fk"`
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
	Tables []SchemaTable `json:"tables"`
	Edges  []SchemaEdge  `json:"edges"`
}

// SchemaTable is one entity (table) in the ERD.
type SchemaTable struct {
	Name    string         `json:"name"`
	Columns []SchemaColumn `json:"columns"`
}

// SchemaFn returns the ERD graph of the named database. The CLI supplies it so
// this package stays decoupled from schema loading; when nil, the ERD is
// disabled.
type SchemaFn func(dbName string) (*SchemaGraph, error)

// TablesFn lists the browsable tables of the named database. The CLI supplies it
// so this package stays decoupled from DSN resolution; when nil, the data
// browser is disabled.
type TablesFn func(dbName string) ([]TableInfo, error)

// QueryFn runs a read-only SQL query against the named database. Read-only
// safety is enforced by the CLI at the engine level (mode=ro + query_only).
type QueryFn func(dbName, sql string) (*QueryResult, error)

// Server serves the embedded dashboard and its JSON API.
type Server struct {
	provider Provider
	importFn ImportFn
	tablesFn TablesFn
	queryFn  QueryFn
	schemaFn SchemaFn
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

// SetSchemaProvider enables the interactive ERD.
func (s *Server) SetSchemaProvider(fn SchemaFn) { s.schemaFn = fn }

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
		writeJSON(w, http.StatusOK, ov)
	})

	// Advertises which optional features are available to the frontend.
	mux.HandleFunc("/api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{
			"import": s.importFn != nil,
			"data":   s.tablesFn != nil && s.queryFn != nil,
			"schema": s.schemaFn != nil,
		})
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
