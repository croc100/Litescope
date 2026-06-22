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

// Server serves the embedded dashboard and its JSON API.
type Server struct {
	provider Provider
}

// New builds a dashboard server backed by the given provider.
func New(provider Provider) *Server {
	return &Server{provider: provider}
}

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
