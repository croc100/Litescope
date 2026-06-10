package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/monitor"
	"github.com/spf13/cobra"
)

func cmdMonitor() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "monitor",
		Short: "Track schema drift in production databases",
		Long: `Monitor detects unexpected schema changes in your production databases.

  Free:  snapshot, check (one-shot)
  Pro:   watch (continuous), webhook alerts
  Cloud: hosted monitoring, dashboard, team alerts`,
	}
	cmd.AddCommand(cmdMonitorSnapshot())
	cmd.AddCommand(cmdMonitorCheck())
	cmd.AddCommand(cmdMonitorWatch())
	return cmd
}

// ── snapshot ──────────────────────────────────────────────────────────────────
// FREE: capture current schema as baseline

func cmdMonitorSnapshot() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "snapshot <source>",
		Short: "Capture current schema as a baseline [free]",
		Long: `Save the current schema of a database as a baseline snapshot.
Run this once after a confirmed-good deployment, then use 'monitor check' to detect drift.

Examples:
  litescope monitor snapshot production.db --output baseline.json
  litescope monitor snapshot turso://TOKEN@ORG/prod --output baseline.json
  litescope monitor snapshot d1://TOKEN@ACCOUNT/DB --output baseline.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn := args[0]

			conn, err := connector.Open(dsn)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", dsn, err)
			}
			defer conn.Close()

			s, err := conn.Schema()
			if err != nil {
				return fmt.Errorf("loading schema: %w", err)
			}

			snap := &monitor.Snapshot{
				Source:     dsn,
				CapturedAt: time.Now().UTC(),
				Schema:     s,
			}

			if err := monitor.Save(output, snap); err != nil {
				return err
			}

			tableCount := 0
			if s != nil {
				tableCount = len(s.Tables)
			}
			fmt.Printf("\n  %s  Snapshot saved → %s\n", styleOK.Render("✓"), output)
			fmt.Printf("  %s  Source:  %s\n", styleDim.Render("·"), dsn)
			fmt.Printf("  %s  Tables:  %d\n", styleDim.Render("·"), tableCount)
			fmt.Printf("  %s  Time:    %s\n\n", styleDim.Render("·"), snap.CapturedAt.Format(time.RFC3339))
			fmt.Printf("  Run checks with:\n")
			fmt.Printf("  %s\n\n", styleDim.Render(
				fmt.Sprintf("litescope monitor check %s --baseline %s", dsn, output),
			))
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "baseline.json", "output snapshot file path")
	return cmd
}

// ── check ─────────────────────────────────────────────────────────────────────
// FREE: one-shot drift check

func cmdMonitorCheck() *cobra.Command {
	var baseline string
	var format string

	cmd := &cobra.Command{
		Use:   "check <source>",
		Short: "Check for schema drift against a baseline [free]",
		Long: `Compare the current schema against a saved baseline snapshot.
Exits 0 if no drift, exits 1 if drift is detected.

Examples:
  litescope monitor check production.db --baseline baseline.json
  litescope monitor check turso://TOKEN@ORG/prod --baseline baseline.json --format json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn := args[0]

			snap, err := monitor.Load(baseline)
			if err != nil {
				return err
			}

			conn, err := connector.Open(dsn)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", dsn, err)
			}
			defer conn.Close()

			current, err := conn.Schema()
			if err != nil {
				return fmt.Errorf("loading schema: %w", err)
			}

			result := monitor.Check(dsn, snap, current)

			switch format {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			default:
				printDriftTerminal(result)
			}

			if result.HasDrift {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&baseline, "baseline", "b", "baseline.json", "baseline snapshot file")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json")
	_ = cmd.MarkFlagRequired("baseline")
	return cmd
}

// ── watch ─────────────────────────────────────────────────────────────────────
// PRO: continuous drift detection with alerts

func cmdMonitorWatch() *cobra.Command {
	var baseline string
	var interval time.Duration
	var webhook string
	var format string

	cmd := &cobra.Command{
		Use:   "watch <source>",
		Short: "Continuously monitor for schema drift [Pro]",
		Long: `Run drift checks on a schedule and alert on changes. Requires Pro license.

Examples:
  litescope monitor watch turso://TOKEN@ORG/prod --baseline baseline.json --interval 1h
  litescope monitor watch d1://TOKEN@ACC/prod --baseline baseline.json --webhook https://hooks.slack.com/...

  Set license: export LITESCOPE_LICENSE=lsc_pro_<key>
  Get license: https://litescope.dev/pricing`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// ── License gate ──────────────────────────────────────
			if err := license.RequirePro(); err != nil {
				return err
			}

			dsn := args[0]

			snap, err := monitor.Load(baseline)
			if err != nil {
				return err
			}

			fmt.Printf("\n  %s  Watching %s\n", styleOK.Render("◉"), dsn)
			fmt.Printf("  %s  Baseline: %s (captured %s)\n",
				styleDim.Render("·"), baseline,
				snap.CapturedAt.Format("2006-01-02 15:04:05 UTC"))
			fmt.Printf("  %s  Interval: %s\n", styleDim.Render("·"), interval)
			if webhook != "" {
				fmt.Printf("  %s  Webhook:  %s\n", styleDim.Render("·"), webhook)
			}
			fmt.Printf("\n  Press Ctrl+C to stop.\n\n")

			// ── Watch loop ────────────────────────────────────────
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

			runCheck := func() {
				conn, err := connector.Open(dsn)
				if err != nil {
					fmt.Printf("  %s  Connection error: %v\n", styleErr.Render("✗"), err)
					return
				}
				defer conn.Close()

				current, err := conn.Schema()
				if err != nil {
					fmt.Printf("  %s  Schema error: %v\n", styleErr.Render("✗"), err)
					return
				}

				result := monitor.Check(dsn, snap, current)

				if format == "json" {
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					_ = enc.Encode(result)
				} else {
					printDriftTerminal(result)
				}

				if result.HasDrift && webhook != "" {
					if err := monitor.Alert(webhook, result); err != nil {
						fmt.Printf("  %s  Webhook error: %v\n", styleWarn.Render("!"), err)
					} else {
						fmt.Printf("  %s  Alert sent to webhook\n", styleOK.Render("✓"))
					}
				}
			}

			// Run immediately, then on ticker
			runCheck()

			for {
				select {
				case <-ticker.C:
					runCheck()
				case <-sig:
					fmt.Printf("\n  Stopped.\n\n")
					return nil
				}
			}
		},
	}

	cmd.Flags().StringVarP(&baseline, "baseline", "b", "baseline.json", "baseline snapshot file")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 1*time.Hour, "check interval (e.g. 30m, 1h, 6h)")
	cmd.Flags().StringVar(&webhook, "webhook", "", "webhook URL for drift alerts (Slack, Discord, custom)")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json")
	_ = cmd.MarkFlagRequired("baseline")
	return cmd
}

// ── Terminal output ───────────────────────────────────────────────────────────

func printDriftTerminal(r *monitor.DriftResult) {
	ts := r.CheckedAt.Format("2006-01-02 15:04:05 UTC")

	if !r.HasDrift {
		fmt.Printf("  %s  %s  %s\n",
			styleOK.Render("✓"),
			styleDim.Render(ts),
			styleOK.Render("No drift detected"),
		)
		return
	}

	fmt.Printf("\n  %s  %s  %s\n",
		styleErr.Render("⚠"),
		styleDim.Render(ts),
		styleErr.Render(fmt.Sprintf("Drift detected — %d change(s)", len(r.Changes))),
	)
	fmt.Printf("  %s  Baseline: %s\n\n",
		styleDim.Render("·"),
		r.BaselineAt.Format("2006-01-02 15:04:05 UTC"),
	)

	for _, td := range r.Changes {
		switch {
		case td.Added:
			fmt.Printf("  %s  table %s %s\n",
				styleOK.Render("+"), td.Name, styleDim.Render("added"))
		case td.Removed:
			fmt.Printf("  %s  table %s %s\n",
				styleErr.Render("-"), td.Name, styleDim.Render("removed"))
		default:
			fmt.Printf("  %s  table %s %s\n",
				styleWarn.Render("~"), td.Name, styleDim.Render("modified"))
			for _, c := range td.AddedColumns {
				fmt.Printf("       %s  column %s %s\n",
					styleOK.Render("+"), c.Name, styleDim.Render("added"))
			}
			for _, c := range td.RemovedColumns {
				fmt.Printf("       %s  column %s %s\n",
					styleErr.Render("-"), c.Name, styleDim.Render("removed"))
			}
			for _, c := range td.ChangedColumns {
				fmt.Printf("       %s  column %s  %s → %s\n",
					styleWarn.Render("~"), c.Name, c.Old.Type, c.New.Type)
			}
			for _, ix := range td.AddedIndexes {
				fmt.Printf("       %s  index %s %s\n",
					styleOK.Render("+"), ix.Name, styleDim.Render("added"))
			}
			for _, ix := range td.RemovedIndexes {
				fmt.Printf("       %s  index %s %s\n",
					styleErr.Render("-"), ix.Name, styleDim.Render("removed"))
			}
		}
	}
	fmt.Println()
}
