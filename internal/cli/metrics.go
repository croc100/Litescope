package cli

import (
	"fmt"
	"net/http"

	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/metrics"
	"github.com/spf13/cobra"
)

func cmdMetrics() *cobra.Command {
	var configPath string
	var tag string
	var deep bool
	var serve bool
	var addr string
	var noFingerprint bool

	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Export fleet health as Prometheus / OpenMetrics text",
		Long: `Render fleet operational health and schema-drift state as Prometheus text
exposition — so Litescope drops straight into Grafana / Alertmanager.

By default it prints one snapshot to stdout (ideal for a node_exporter textfile
collector or a Pushgateway). With --serve it runs a /metrics endpoint that
re-inspects the fleet on every scrape, the standard Prometheus exporter model.

Examples:
  litescope metrics > /var/lib/node_exporter/litescope.prom
  litescope metrics --config litescope.fleet.yaml --tag region=eu
  litescope metrics --serve --addr 127.0.0.1:9105`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			collect := func() (string, error) {
				_, dbs, err := loadFleet(configPath, tag)
				if err != nil {
					return "", err
				}
				// Free sees the read-only fleet preview, same cap as serve/health.
				shown := freePreviewFleet(dbs)
				fr := fleet.Health(shown, deep, 0)
				var fp *fleet.FingerprintReport
				if !noFingerprint {
					fp = fleet.Fingerprint(shown, 0)
				}
				return metrics.Render(fr, fp), nil
			}

			if serve {
				http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
					out, err := collect()
					if err != nil {
						http.Error(w, "# collect error: "+err.Error(), http.StatusInternalServerError)
						return
					}
					w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
					_, _ = w.Write([]byte(out))
				})
				http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(`<html><head><title>Litescope metrics</title></head>` +
						`<body><h1>Litescope</h1><p><a href="/metrics">Metrics</a></p></body></html>`))
				})
				if !license.IsPro() {
					fmt.Printf("  %s  %s\n", styleWarn.Render("!"),
						styleWarn.Render("Free preview — metrics cover up to 10 databases. Full fleet with Pro."))
				}
				fmt.Printf("  %s  Litescope exporter on http://%s/metrics\n", styleOK.Render("◎"), addr)
				return http.ListenAndServe(addr, nil)
			}

			out, err := collect()
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config file (default litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only include databases matching tag (key=value or key)")
	cmd.Flags().BoolVar(&deep, "deep", false, "run exhaustive integrity_check on each database")
	cmd.Flags().BoolVar(&serve, "serve", false, "run a /metrics HTTP endpoint instead of printing once")
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:9105", "address for --serve")
	cmd.Flags().BoolVar(&noFingerprint, "no-fingerprint", false, "skip schema fingerprint metrics (faster)")
	return cmd
}
