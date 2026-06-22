package cli

import (
	"database/sql"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/dashboard"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/importer"
	"github.com/croc100/litescope/internal/license"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func cmdServe() *cobra.Command {
	var configPath string
	var tag string
	var addr string
	var deep bool
	var noOpen bool
	var importDir string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Launch the local fleet dashboard in a browser (free)",
		Long: `Serve a local, self-hosted web dashboard for your fleet — topology map,
worst-first health, and schema fingerprint, all in the browser.

It runs entirely on this machine: no cloud, no account, no telemetry. Free,
including self-hosting on your own server. (The hosted, multi-user, org-scoped
dashboard with SSO and time-series history is a separate Enterprise offering.)

Examples:
  litescope serve
  litescope serve --config litescope.fleet.yaml --addr 127.0.0.1:7575
  litescope serve --tag region=eu --deep`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			fullTotal := len(dbs)
			// Free sees a read-only preview of the fleet; Pro sees all of it.
			// The dashboard is read-only, so the preview cap applies the same way
			// it does to `fleet fingerprint` / `fleet health`.
			shown := freePreviewFleet(dbs)
			preview := !license.IsPro() && fullTotal > len(shown)

			// Databases added at runtime by dropping a file onto the dashboard.
			var mu sync.Mutex
			var imported []fleet.Database

			currentDBs := func() []fleet.Database {
				mu.Lock()
				defer mu.Unlock()
				all := make([]fleet.Database, 0, len(shown)+len(imported))
				all = append(all, shown...)
				all = append(all, imported...)
				return all
			}

			provider := func() (*dashboard.Overview, error) {
				dbList := currentDBs()
				health := fleet.Health(dbList, deep, 0)
				fp := fleet.Fingerprint(dbList, 0)
				name := cfg.Name
				if name == "" {
					name = "fleet"
				}
				return &dashboard.Overview{
					FleetName:   name,
					Total:       len(dbList),
					Preview:     preview,
					PreviewCap:  len(shown),
					FullTotal:   fullTotal + len(imported),
					Health:      health,
					Fingerprint: fp,
					GeneratedAt: time.Now().UTC(),
				}, nil
			}

			srv := dashboard.New(provider)
			srv.SetImportHandler(func(filename string, data io.Reader) (string, error) {
				name, dsn, table, err := importDropped(importDir, filename, data)
				if err != nil {
					return "", err
				}
				mu.Lock()
				defer mu.Unlock()
				// Replace any existing entry with the same name, else append.
				replaced := false
				for i := range imported {
					if imported[i].Name == name {
						imported[i].DSN = dsn
						replaced = true
						break
					}
				}
				if !replaced {
					imported = append(imported, fleet.Database{Name: name, DSN: dsn, Tags: []string{"imported"}})
				}
				return fmt.Sprintf("%s → table %s", filepath.Base(dsn), table), nil
			})
			url := "http://" + addr

			fmt.Printf("\n  %s  Litescope dashboard\n", styleOK.Render("◎"))
			fmt.Printf("  %s  Fleet:  %s (%d database(s))\n", styleDim.Render("·"), cfg.Name, fullTotal)
			if preview {
				fmt.Printf("  %s  %s\n", styleWarn.Render("!"),
					styleWarn.Render(fmt.Sprintf("Free preview — showing %d of %d. Full fleet with Pro.", len(shown), fullTotal)))
			}
			fmt.Printf("  %s  URL:    %s\n", styleDim.Render("·"), url)
			fmt.Printf("\n  Press Ctrl+C to stop.\n\n")

			if !noOpen {
				openBrowser(url)
			}

			return srv.ListenAndServe(addr)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config file (default litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only include databases matching tag (key=value or key)")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7575", "address to listen on")
	cmd.Flags().BoolVar(&deep, "deep", false, "run exhaustive integrity_check on each database")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open a browser automatically")
	cmd.Flags().StringVar(&importDir, "import-dir", ".", "directory for databases created by drag-drop import")
	return cmd
}

// importDropped ingests a file dropped on the dashboard into its own SQLite
// database under dir, returning the fleet display name, the database path, and
// the created table name.
func importDropped(dir, filename string, data io.Reader) (name, dsn, table string, err error) {
	format := detectFormat(filename)
	name = sanitizeTableName(filename)
	dsn = filepath.Join(dir, name+".db")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", "", "", err
	}
	defer db.Close()

	// A fresh drop replaces any prior import of the same name.
	opt := importer.Options{Table: name, Mode: importer.ModeReplace, HasHeader: true}
	var res *importer.Result
	switch format {
	case "tsv":
		opt.Delimiter = '\t'
		res, err = importer.ImportCSV(db, data, opt)
	case "json":
		res, err = importer.ImportJSON(db, data, opt)
	default:
		res, err = importer.ImportCSV(db, data, opt)
	}
	if err != nil {
		return "", "", "", err
	}
	return name, dsn, res.Table, nil
}

// openBrowser tries to open url in the default browser; failure is non-fatal.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	_ = exec.Command(cmd, args...).Start()
}
