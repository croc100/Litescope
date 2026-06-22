package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/croc100/litescope/internal/dashboard"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/license"
	"github.com/spf13/cobra"
)

func cmdServe() *cobra.Command {
	var configPath string
	var tag string
	var addr string
	var deep bool
	var noOpen bool

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

			provider := func() (*dashboard.Overview, error) {
				health := fleet.Health(shown, deep, 0)
				fp := fleet.Fingerprint(shown, 0)
				name := cfg.Name
				if name == "" {
					name = "fleet"
				}
				return &dashboard.Overview{
					FleetName:   name,
					Total:       len(shown),
					Preview:     preview,
					PreviewCap:  len(shown),
					FullTotal:   fullTotal,
					Health:      health,
					Fingerprint: fp,
					GeneratedAt: time.Now().UTC(),
				}, nil
			}

			srv := dashboard.New(provider)
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
	return cmd
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
