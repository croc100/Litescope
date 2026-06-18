package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
)

func cmdFleet() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Manage many databases at once: discover, baseline, drift-check (Pro)",
		Long: `Fleet operates on every SQLite database in a Turso org or Cloudflare D1
account as a single unit.

  fleet discover     — list all databases and write a fleet config
  fleet snapshot     — capture baselines for the whole fleet in parallel
  fleet check        — detect schema drift across the whole fleet in parallel
  fleet fingerprint  — cluster the fleet by schema to reveal how many you run
  fleet converge     — bring every drifted database back to canonical
  fleet health       — triage operational faults (corruption, WAL bloat, bloat)
  fleet recover      — restore faulted databases from backups; quarantine the rest
  fleet migrate      — roll one migration out across the fleet, staged
  fleet status       — show the configured fleet

Fleet is a Pro feature.`,
	}
	cmd.AddCommand(cmdFleetDiscover())
	cmd.AddCommand(cmdFleetSnapshot())
	cmd.AddCommand(cmdFleetCheck())
	cmd.AddCommand(cmdFleetFingerprint())
	cmd.AddCommand(cmdFleetConverge())
	cmd.AddCommand(cmdFleetHealth())
	cmd.AddCommand(cmdFleetBlastRadius())
	cmd.AddCommand(cmdFleetRecover())
	cmd.AddCommand(cmdFleetStatus())
	cmd.AddCommand(cmdFleetMigrate())
	return cmd
}

// ── recover ─────────────────────────────────────────────────────────────────

func cmdFleetRecover() *cobra.Command {
	var configPath, tag, format, backupDir string
	var dryRun, deep, noQuarantine, yes bool

	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Restore faulted databases from backups; quarantine the unrecoverable",
		Long: `Close the incident loop. Triage the fleet and, for every critically faulted
database (corrupt or unreachable), restore it from its most recent backup —
the VACUUM INTO backup that 'migrate apply' already creates.

Backups are verified healthy before use; if a database has no healthy backup it
is quarantined and excluded from all future fleet operations until cleared.

Always dry-run first to see the plan:

  litescope fleet recover --dry-run
  litescope fleet recover --backup-dir ./backups --dry-run

Then recover for real:

  litescope fleet recover
  litescope fleet recover --no-quarantine    # restore only; never quarantine

Bloat/warning issues are not recovery targets — use 'fleet health' to find
VACUUM candidates. Recovery applies to local files; a faulted remote database
is reported as needing manual recovery.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			report := fleet.Recover(dbs, fleet.RecoverOptions{
				BackupDir:  backupDir,
				DryRun:     dryRun,
				Deep:       deep,
				Quarantine: !noQuarantine,
			})

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printRecoverReport(cfg, report)
			}

			// Persist quarantine decisions (unless dry-run or disabled).
			if !dryRun && !noQuarantine {
				names := report.Quarantine()
				if len(names) > 0 {
					if !yes && !confirm(fmt.Sprintf("Quarantine %d unrecoverable database(s) (excluded from future ops)?", len(names))) {
						fmt.Println("  Quarantine skipped.")
					} else if n := cfg.SetQuarantine(names, true); n > 0 {
						if err := cfg.Save(""); err != nil {
							return fmt.Errorf("saving quarantine state: %w", err)
						}
						fmt.Printf("  %s  Quarantined %d database(s) → %s\n\n", styleWarn.Render("!"), n, fleet.DefaultConfigFile)
					}
				}
			}

			_, _, failed, _ := report.Counts()
			if failed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only operate on databases with this tag")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "directory to search for backups (default: alongside each DB)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the recovery plan without restoring or quarantining")
	cmd.Flags().BoolVar(&deep, "deep", false, "use exhaustive integrity_check when assessing health")
	cmd.Flags().BoolVar(&noQuarantine, "no-quarantine", false, "restore only; never quarantine unrecoverable databases")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the quarantine confirmation prompt")
	return cmd
}

func printRecoverReport(cfg *fleet.Config, report *fleet.RecoverReport) {
	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	mode := "recovery"
	if report.DryRun {
		mode = "recovery (dry-run)"
	}
	fmt.Printf("\n  Fleet %s: %s · %d database(s)\n\n", mode, styleBold.Render(name), len(report.Results))

	width := 0
	for _, r := range report.Results {
		if len(r.Database) > width {
			width = len(r.Database)
		}
	}

	for _, r := range report.Results {
		var mark, state, detail string
		switch r.State {
		case fleet.RecoverRestored:
			mark = styleOK.Render("✓")
			state = styleOK.Render("restored")
			detail = styleDim.Render(r.Detail)
		case fleet.RecoverQuarantined:
			mark = styleWarn.Render("⚠")
			state = styleWarn.Render("quarantined")
			detail = styleWarn.Render(r.Detail)
		case fleet.RecoverFailed:
			mark = styleErr.Render("✗")
			state = styleErr.Render("failed")
			detail = styleErr.Render(recoverFailDetail(r))
		case fleet.RecoverRemote:
			mark = styleErr.Render("✗")
			state = styleErr.Render("remote")
			detail = styleErr.Render(r.Detail)
		default: // healthy
			continue // don't clutter the report with healthy DBs
		}
		fmt.Printf("  %s  %-*s  %-12s  %s\n", mark, width, r.Database, state, detail)
	}

	restored, quarantined, failed, healthy := report.Counts()
	fmt.Printf("\n  %s\n\n", summaryLine(len(report.Results),
		kv{"restored", restored, styleOK},
		kv{"quarantined", quarantined, styleWarn},
		kv{"failed", failed, styleErr},
		kv{"healthy", healthy, styleDim},
	))
}

func recoverFailDetail(r fleet.RecoverResult) string {
	if r.Detail != "" {
		return r.Detail
	}
	if r.Err != nil {
		return truncErr(r.Err)
	}
	return "recovery failed"
}

// ── health ──────────────────────────────────────────────────────────────────

func cmdFleetHealth() *cobra.Command {
	var configPath, tag, format, webhook string
	var deep, watch, autoRecover bool
	var interval time.Duration
	var concurrency int

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Triage operational faults across the whole fleet in parallel",
		Long: `The first command to run when the pager goes off. Inspects every database
for the faults that take down production SQLite at scale:

  corruption       PRAGMA quick_check (or integrity_check with --deep)
  WAL bloat        a -wal file growing unbounded — checkpoint starvation
  fragmentation    reclaimable freelist space — a VACUUM candidate
  reachability     databases that fail to open or connect

Results are sorted worst-first. Local files get the full inspection; remote
databases report reachability only.

  litescope fleet health
  litescope fleet health --deep                       # exhaustive integrity_check
  litescope fleet health --webhook $SLACK_URL         # one-shot alert for cron
  litescope fleet health --watch --interval 5m        # continuous monitoring
  litescope fleet health --watch --webhook $URL --recover  # detect → alert → auto-recover

Exit code is 1 when any database is in a warning or critical state — drop it
into a scheduled job (cron + --webhook) to get paged on fleet faults.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			// One scan: inspect, render, alert, and optionally auto-recover.
			runOnce := func() *fleet.HealthReport {
				report := fleet.Health(dbs, deep, concurrency)
				if format == "json" {
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					_ = enc.Encode(report)
				} else {
					printFleetHealth(cfg, report)
				}
				if webhook != "" && report.HasFaults() {
					if err := fleet.SendHealthAlert(webhook, report); err != nil {
						fmt.Printf("  %s  Webhook error: %v\n", styleWarn.Render("!"), err)
					} else {
						fmt.Printf("  %s  Alert sent to webhook\n", styleOK.Render("✓"))
					}
				}
				if autoRecover {
					if _, _, critical := report.Counts(); critical > 0 {
						rep := fleet.Recover(dbs, fleet.RecoverOptions{Quarantine: true})
						restored, quarantined, _, _ := rep.Counts()
						fmt.Printf("  %s  Auto-recover: %d restored, %d quarantined\n",
							styleOK.Render("↻"), restored, quarantined)
					}
				}
				return report
			}

			if !watch {
				report := runOnce()
				if report.HasFaults() {
					os.Exit(1)
				}
				return nil
			}

			// ── Continuous watch ──────────────────────────────────
			fmt.Printf("\n  %s  Watching %d database(s) · interval %s", styleOK.Render("◉"), len(dbs), interval)
			if webhook != "" {
				fmt.Printf(" · webhook on")
			}
			if autoRecover {
				fmt.Printf(" · auto-recover on")
			}
			fmt.Printf("\n  Press Ctrl+C to stop.\n")

			runOnce()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			for {
				select {
				case <-ticker.C:
					runOnce()
				case <-sig:
					fmt.Printf("\n  Stopped.\n\n")
					return nil
				}
			}
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only operate on databases with this tag")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().BoolVar(&deep, "deep", false, "use exhaustive integrity_check instead of quick_check")
	cmd.Flags().BoolVar(&watch, "watch", false, "run continuously on an interval")
	cmd.Flags().DurationVar(&interval, "interval", 5*time.Minute, "watch interval (e.g. 30s, 5m, 1h)")
	cmd.Flags().StringVar(&webhook, "webhook", "", "POST a fault alert to this URL (Slack-compatible)")
	cmd.Flags().BoolVar(&autoRecover, "recover", false, "auto-recover critically faulted databases from backup")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max parallel connections (default 8)")
	return cmd
}

// ── blast-radius ────────────────────────────────────────────────────────────

func cmdFleetBlastRadius() *cobra.Command {
	var configPath, format string
	var deep bool
	var concurrency int

	cmd := &cobra.Command{
		Use:   "blast-radius",
		Short: "Show which shared cohorts a fault threatens (corruption → who else is at risk)",
		Long: `Triage faults, then map each critically faulted database to the cohorts it
shares (Turso groups, regions — any tag). One corrupt database in a group often
means the whole group's shared infrastructure is suspect; this shows who else to
check before the next page.

  litescope fleet blast-radius

Exit code is 1 when any cohort is affected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, "")
			if err != nil {
				return err
			}
			report := fleet.Health(dbs, deep, concurrency)
			groups := fleet.BlastRadius(dbs, report)

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(groups)
			}

			name := cfg.Name
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Printf("\n  Blast radius: %s\n\n", styleBold.Render(name))
			if len(groups) == 0 {
				fmt.Printf("  %s  No faults — nothing at risk.\n\n", styleOK.Render("●"))
				return nil
			}
			for _, g := range groups {
				fmt.Printf("  %s  %s  %s\n",
					styleErr.Render("✗"),
					styleBold.Render(g.Tag),
					styleErr.Render(fmt.Sprintf("%d of %d faulted", len(g.Faulted), g.Total)))
				fmt.Printf("       %s %s\n", styleDim.Render("faulted:"), strings.Join(g.Faulted, ", "))
				if len(g.AtRisk) > 0 {
					fmt.Printf("       %s %s\n", styleWarn.Render("at risk:"), styleDim.Render(sampleMembers(g.AtRisk)))
				}
				fmt.Println()
			}
			os.Exit(1)
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().BoolVar(&deep, "deep", false, "use exhaustive integrity_check instead of quick_check")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max parallel connections (default 8)")
	return cmd
}

func printFleetHealth(cfg *fleet.Config, report *fleet.HealthReport) {
	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	ok, warning, critical := report.Counts()

	headline := styleOK.Render("all healthy")
	if critical > 0 || warning > 0 {
		var parts []string
		if critical > 0 {
			parts = append(parts, styleErr.Render(fmt.Sprintf("%d critical", critical)))
		}
		if warning > 0 {
			parts = append(parts, styleWarn.Render(fmt.Sprintf("%d warning", warning)))
		}
		headline = strings.Join(parts, styleDim.Render(" · "))
	}
	fmt.Printf("\n  Fleet: %s · %d database(s) · %s\n\n",
		styleBold.Render(name), len(report.Results), headline)

	width := 0
	for _, r := range report.Results {
		if len(r.Database) > width {
			width = len(r.Database)
		}
	}

	for _, r := range report.Results {
		rep := r.Report
		var mark, detail string
		switch rep.Severity {
		case health.SevCritical:
			mark = styleErr.Render("✗")
			detail = styleErr.Render(strings.Join(rep.Issues, "; "))
		case health.SevWarning:
			mark = styleWarn.Render("⚠")
			detail = styleWarn.Render(strings.Join(rep.Issues, "; "))
		default:
			mark = styleOK.Render("●")
			detail = styleDim.Render(healthOKDetail(rep))
		}
		fmt.Printf("  %s  %-*s  %s\n", mark, width, r.Database, detail)
	}

	fmt.Printf("\n  %s\n\n", summaryLine(len(report.Results),
		kv{"healthy", ok, styleOK},
		kv{"warning", warning, styleWarn},
		kv{"critical", critical, styleErr},
	))
}

func healthOKDetail(r *health.Report) string {
	if r.Remote {
		return "remote · reachable"
	}
	parts := []string{humanBytes(r.SizeBytes)}
	if r.WALBytes > 0 {
		parts = append(parts, "wal "+humanBytes(r.WALBytes))
	}
	if r.JournalMode != "" {
		parts = append(parts, strings.ToLower(r.JournalMode))
	}
	return strings.Join(parts, " · ")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}

// ── converge ────────────────────────────────────────────────────────────────────

func cmdFleetConverge() *cobra.Command {
	var (
		configPath, tag, format, backupDir, to string
		dryRun, noBackup, yes, force            bool
		canary, concurrency                     int
	)

	cmd := &cobra.Command{
		Use:   "converge",
		Short: "Bring every drifted database back to the canonical schema",
		Long: `Close the loop: fingerprint the fleet, then generate and apply the migration
that converges every drifted database onto one canonical schema.

The canonical schema is the largest cluster by default, or a reference you name
with --to (a local file or DSN). Each drifted cluster gets its own convergence
SQL — a missed migration is re-applied, a hotfix residue column is removed.

Always dry-run first — it validates the convergence against every database
(apply + rollback) without committing, so you see every failure at once:

  litescope fleet converge --dry-run
  litescope fleet converge --to canonical.db --dry-run

Then converge for real. Use --canary to fix the first N databases and stop:

  litescope fleet converge --canary 5
  litescope fleet converge                       # the whole fleet

Convergence that drops a column or table is destructive and refused unless
--force is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			var canonical *schema.Schema
			if to != "" {
				canonical, err = loadSchemaFromSource(to)
				if err != nil {
					return fmt.Errorf("loading canonical schema from %s: %w", to, err)
				}
			}

			plan, err := fleet.PlanConvergence(dbs, canonical, concurrency)
			if err != nil {
				return err
			}

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(plan); err != nil {
					return err
				}
				return nil
			}

			printConvergePlan(cfg, plan)

			if plan.TotalToConverge == 0 {
				return nil
			}
			if plan.HasDestructive() && !force {
				fmt.Printf("  %s  Convergence drops a column or table on some databases.\n", styleWarn.Render("!"))
				fmt.Printf("      Re-run with --force to proceed.\n\n")
				return fmt.Errorf("aborted: destructive convergence (use --force to override)")
			}

			if !dryRun && !yes {
				action := fmt.Sprintf("converge %d database(s) onto schema %s", plan.TotalToConverge, plan.CanonicalID)
				if canary > 0 {
					action = fmt.Sprintf("converge the first %d of %d database(s) onto schema %s", canary, plan.TotalToConverge, plan.CanonicalID)
				}
				if !confirm(fmt.Sprintf("About to %s. Continue?", action)) {
					fmt.Println("  Aborted.")
					return nil
				}
			}

			report := fleet.Converge(plan, fleet.RolloutOptions{
				DryRun:    dryRun,
				Canary:    canary,
				BackupDir: backupDir,
				NoBackup:  noBackup,
			})
			printRolloutReport(cfg, report)

			if _, failed, _ := report.Counts(); failed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only operate on databases with this tag")
	cmd.Flags().StringVar(&to, "to", "", "canonical schema source (local file or DSN); default: largest cluster")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json (plan only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate convergence against every database without committing")
	cmd.Flags().IntVar(&canary, "canary", 0, "converge the first N databases then stop")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "directory for local backups (default: alongside each DB)")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "skip local backups (not recommended)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "proceed even when convergence is destructive")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max parallel connections for fingerprinting (default 8)")
	return cmd
}

func loadSchemaFromSource(src string) (*schema.Schema, error) {
	conn, err := connector.Open(src)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return conn.Schema()
}

func printConvergePlan(cfg *fleet.Config, plan *fleet.ConvergePlan) {
	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Printf("\n  Convergence plan: %s → canonical schema %s\n\n",
		styleBold.Render(name), styleOK.Render(plan.CanonicalID))

	fmt.Printf("  %s  %d database(s) already canonical\n", styleOK.Render("✓"), plan.AlreadyOK)
	if len(plan.Unreachable) > 0 {
		fmt.Printf("  %s  %d database(s) unreachable — cannot converge\n",
			styleErr.Render("✗"), len(plan.Unreachable))
	}
	fmt.Println()

	if plan.TotalToConverge == 0 {
		if len(plan.Unreachable) == 0 {
			fmt.Printf("  %s  Fleet is already uniform — nothing to converge.\n\n", styleOK.Render("✓"))
		} else {
			fmt.Printf("  %s  No reachable database needs convergence.\n\n", styleOK.Render("✓"))
		}
		return
	}

	for _, cp := range plan.Clusters {
		marker := styleWarn.Render("▲")
		tag := ""
		if cp.Destructive {
			marker = styleErr.Render("✗")
			tag = "  " + styleErr.Render("[destructive]")
		}
		fmt.Printf("  %s  %s  %s%s\n",
			marker,
			styleBold.Render("schema "+cp.ClusterID),
			styleDim.Render(fmt.Sprintf("%d database(s) · %d statement(s) to reach canonical:", len(cp.Members), cp.Statements)),
			tag)
		for _, line := range fingerprintDriftLines(cp.Drift) {
			fmt.Printf("        %s\n", line)
		}
		fmt.Printf("        %s\n", styleDim.Render("e.g. "+sampleMembers(cp.MemberNames)))
		fmt.Println()
	}
}

// ── fingerprint ─────────────────────────────────────────────────────────────────

func cmdFleetFingerprint() *cobra.Command {
	var configPath, tag, format string
	var concurrency int

	cmd := &cobra.Command{
		Use:   "fingerprint",
		Short: "Cluster the fleet by schema — reveal how many distinct schemas you actually run",
		Long: `Read every database's live schema in parallel and group identical schemas
into clusters. You may think you run one schema; fingerprint shows the truth.

The largest cluster is treated as canonical. Every other cluster is reported
with a diff describing exactly how it drifted from canonical — a missed
migration, a hotfix residue column, a stale zombie database.

  litescope fleet fingerprint
  litescope fleet fingerprint --tag group:prod
  litescope fleet fingerprint --format json

Exit code is 1 when more than one distinct schema is found or any database is
unreachable — drop it into CI to enforce fleet uniformity.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			report := fleet.Fingerprint(dbs, concurrency)

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printFingerprintReport(cfg, report)
			}

			if len(report.Clusters) > 1 || len(report.Unreachable) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only operate on databases with this tag")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max parallel connections (default 8)")
	return cmd
}

func printFingerprintReport(cfg *fleet.Config, report *fleet.FingerprintReport) {
	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	distinct := len(report.Clusters)
	total := report.Total + len(report.Unreachable)

	fmt.Printf("\n  Fleet: %s · %d database(s) · %s\n\n",
		styleBold.Render(name), total,
		distinctSummary(distinct, len(report.Unreachable)))

	// Bar scale: longest bar = the largest cluster.
	const barWidth = 18
	maxCount := 0
	for _, c := range report.Clusters {
		if c.Count > maxCount {
			maxCount = c.Count
		}
	}

	for _, c := range report.Clusters {
		bar := renderBar(c.Count, maxCount, barWidth)
		label := fingerprintLabel(c)
		countStr := fmt.Sprintf("%6d", c.Count)

		var barStyled, labelStyled string
		if c.IsCanonical {
			barStyled = styleOK.Render(bar)
			labelStyled = styleOK.Render(label)
		} else {
			barStyled = styleWarn.Render(bar)
			labelStyled = styleWarn.Render(label)
		}
		fmt.Printf("  %s  %s  %s\n", barStyled, styleBold.Render(countStr), labelStyled)
	}

	if len(report.Unreachable) > 0 {
		bar := renderBar(len(report.Unreachable), maxCount, barWidth)
		fmt.Printf("  %s  %s  %s\n",
			styleErr.Render(bar),
			styleBold.Render(fmt.Sprintf("%6d", len(report.Unreachable))),
			styleErr.Render("unreachable / corrupted"))
	}

	fmt.Println()

	if distinct <= 1 && len(report.Unreachable) == 0 {
		fmt.Printf("  %s  Fleet is uniform — every database shares one schema.\n\n", styleOK.Render("✓"))
		return
	}

	// Detail: how each non-canonical cluster differs from canonical.
	for _, c := range report.Clusters {
		if c.IsCanonical {
			continue
		}
		fmt.Printf("  %s  %s  %s\n",
			styleWarn.Render("▲"),
			styleBold.Render("schema "+c.ID),
			styleDim.Render(fmt.Sprintf("%d database(s) · differs from canonical:", c.Count)))
		for _, line := range fingerprintDriftLines(c.Drift) {
			fmt.Printf("        %s\n", line)
		}
		fmt.Printf("        %s\n", styleDim.Render("e.g. "+sampleMembers(c.Members)))
		fmt.Println()
	}
}

func distinctSummary(distinct, unreachable int) string {
	if distinct <= 1 && unreachable == 0 {
		return styleOK.Render("1 schema")
	}
	s := styleWarn.Render(fmt.Sprintf("%d distinct schemas", distinct))
	if unreachable > 0 {
		s += styleDim.Render(" · ") + styleErr.Render(fmt.Sprintf("%d unreachable", unreachable))
	}
	return s
}

func fingerprintLabel(c fleet.FingerprintCluster) string {
	if c.IsCanonical {
		return fmt.Sprintf("schema %s  (canonical)", c.ID)
	}
	return fmt.Sprintf("schema %s", c.ID)
}

func renderBar(count, max, width int) string {
	if max <= 0 {
		return strings.Repeat("░", width)
	}
	filled := count * width / max
	if filled == 0 && count > 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func fingerprintDriftLines(drift []diff.TableDiff) []string {
	var lines []string
	for _, td := range drift {
		switch {
		case td.Added:
			// Present in this cluster, absent from canonical → canonical is missing it,
			// i.e. this cluster has an EXTRA table relative to canonical.
			lines = append(lines, fmt.Sprintf("%s extra table %s", styleOK.Render("+"), td.Name))
		case td.Removed:
			lines = append(lines, fmt.Sprintf("%s missing table %s", styleErr.Render("-"), td.Name))
		default:
			for _, c := range td.AddedColumns {
				lines = append(lines, fmt.Sprintf("%s %s.%s extra column", styleOK.Render("+"), td.Name, c.Name))
			}
			for _, c := range td.RemovedColumns {
				lines = append(lines, fmt.Sprintf("%s %s.%s missing column", styleErr.Render("-"), td.Name, c.Name))
			}
			for _, c := range td.ChangedColumns {
				lines = append(lines, fmt.Sprintf("%s %s.%s type %s→%s",
					styleWarn.Render("~"), td.Name, c.Name, c.Old.Type, c.New.Type))
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styleDim.Render("(index-only difference)"))
	}
	return lines
}

func sampleMembers(members []string) string {
	const max = 3
	if len(members) <= max {
		return strings.Join(members, ", ")
	}
	return strings.Join(members[:max], ", ") + fmt.Sprintf(", +%d more", len(members)-max)
}

// ── migrate ───────────────────────────────────────────────────────────────────

func cmdFleetMigrate() *cobra.Command {
	var (
		configPath, tag, format, backupDir string
		dryRun, noBackup, yes              bool
		canary                             int
	)

	cmd := &cobra.Command{
		Use:   "migrate <migration.sql>",
		Short: "Apply one migration across the whole fleet, staged, halting on failure",
		Long: `Roll a single migration out to every database in order.

The rollout is staged and fail-closed: databases are migrated one at a time, and
the first failure halts the rollout so a bad migration cannot cascade across the
fleet. Remaining databases are left untouched.

  Local files     full safety — integrity check, VACUUM INTO backup,
                  single transaction, FK verification, automatic rollback.
  Turso           transactional apply over the Hrana API (no local backup).
  D1              sequential apply; D1 has no rollback over HTTP.

Always dry-run first — it validates the migration against every database
(apply + rollback) and never halts early, so you see every failure at once:

  litescope fleet migrate migration.sql --dry-run

Then apply for real. Use --canary to apply to the first N databases and stop,
so you can verify before rolling out to the rest:

  litescope fleet migrate migration.sql --canary 5
  litescope fleet migrate migration.sql            # the whole fleet`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			sqlBytes, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading migration: %w", err)
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			// Destructive, multi-database operation — confirm unless dry-run/--yes.
			if !dryRun && !yes {
				action := fmt.Sprintf("apply this migration to %d database(s)", len(dbs))
				if canary > 0 {
					action = fmt.Sprintf("apply this migration to the first %d of %d database(s)", canary, len(dbs))
				}
				if !confirm(fmt.Sprintf("About to %s. Continue?", action)) {
					fmt.Println("  Aborted.")
					return nil
				}
			}

			report := fleet.Rollout(dbs, string(sqlBytes), fleet.RolloutOptions{
				DryRun:    dryRun,
				Canary:    canary,
				BackupDir: backupDir,
				NoBackup:  noBackup,
			})

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printRolloutReport(cfg, report)
			}

			if _, failed, _ := report.Counts(); failed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only migrate databases with this tag")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate against every database without committing")
	cmd.Flags().IntVar(&canary, "canary", 0, "apply to the first N databases then stop")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "directory for local backups (default: alongside each DB)")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "skip local backups (not recommended)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func printRolloutReport(cfg *fleet.Config, report *fleet.RolloutReport) {
	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	mode := "rollout"
	if report.DryRun {
		mode = "dry-run"
	}
	fmt.Printf("\n  Fleet %s: %s · %d database(s)\n\n", mode, styleBold.Render(name), len(report.Results))

	width := 0
	for _, r := range report.Results {
		if len(r.Database) > width {
			width = len(r.Database)
		}
	}

	for _, r := range report.Results {
		var mark, state, detail string
		switch r.State {
		case fleet.StateApplied:
			mark = styleOK.Render("✓")
			state = styleOK.Render("applied")
			detail = styleDim.Render(rolloutDetail(r))
		case fleet.StateDryRun:
			mark = styleOK.Render("✓")
			state = styleOK.Render("dry-run ok")
			detail = styleDim.Render(fmt.Sprintf("%d statements · %s", r.Executed, r.Provider))
		case fleet.StateFailed:
			mark = styleErr.Render("✗")
			state = styleErr.Render("failed")
			detail = styleErr.Render(truncErr(r.Err))
		case fleet.StateSkipped:
			mark = styleDim.Render("·")
			state = styleDim.Render("skipped")
			detail = styleDim.Render("rollout halted")
		case fleet.StateCanary:
			mark = styleDim.Render("·")
			state = styleDim.Render("held")
			detail = styleDim.Render("beyond canary limit")
		}
		fmt.Printf("  %s  %-*s  %-11s  %s\n", mark, width, r.Database, state, detail)
	}

	applied, failed, skipped := report.Counts()
	fmt.Printf("\n  %s\n", summaryLine(len(report.Results),
		kv{"applied", applied, styleOK},
		kv{"failed", failed, styleErr},
		kv{"held/skipped", skipped, styleDim},
	))
	if report.Halted {
		fmt.Printf("\n  %s  Rollout halted at the first failure — remaining databases untouched.\n",
			styleWarn.Render("!"))
	}
	fmt.Println()
}

func rolloutDetail(r fleet.RolloutResult) string {
	parts := []string{fmt.Sprintf("%d statements", r.Executed), r.Provider}
	if r.BackupPath != "" {
		parts = append(parts, "backup: "+r.BackupPath)
	}
	return strings.Join(parts, " · ")
}

// ── discover ──────────────────────────────────────────────────────────────────

func cmdFleetDiscover() *cobra.Command {
	var (
		org       string
		account   string
		token     string
		dbToken   string
		configOut string
		merge     bool
	)

	cmd := &cobra.Command{
		Use:   "discover <turso|d1>",
		Short: "Discover all databases in a Turso org or D1 account",
		Long: `Query the provider API for every database and write a fleet config.

Turso:
  litescope fleet discover turso --org my-org --token $TURSO_API_TOKEN \
    --db-token $TURSO_GROUP_TOKEN

Cloudflare D1:
  litescope fleet discover d1 --account $CF_ACCOUNT_ID --token $CF_API_TOKEN

By default this overwrites the config. Use --merge to update an existing one
(preserves baselines and tags for databases that are already listed).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			provider := strings.ToLower(args[0])

			if configOut == "" {
				configOut = fleet.DefaultConfigFile
			}

			var (
				dbs []fleet.Database
				err error
			)
			switch provider {
			case "turso":
				if org == "" || token == "" {
					return fmt.Errorf("turso discovery requires --org and --token")
				}
				dbs, err = fleet.DiscoverTurso(org, token, dbToken)
			case "d1":
				if account == "" || token == "" {
					return fmt.Errorf("d1 discovery requires --account and --token")
				}
				dbs, err = fleet.DiscoverD1(account, token)
			default:
				return fmt.Errorf("unknown provider %q (use 'turso' or 'd1')", provider)
			}
			if err != nil {
				return err
			}
			if len(dbs) == 0 {
				fmt.Printf("\n  %s  No databases found.\n\n", styleWarn.Render("!"))
				return nil
			}

			var cfg *fleet.Config
			if merge {
				if existing, lerr := fleet.Load(configOut); lerr == nil {
					cfg = existing
				}
			}
			if cfg == nil {
				cfg = &fleet.Config{Name: provider, Databases: dbs}
			} else {
				added, updated := cfg.Merge(dbs)
				fmt.Printf("\n  %s  Merged: %d added, %d updated\n", styleOK.Render("✓"), added, updated)
			}

			if err := cfg.Save(configOut); err != nil {
				return err
			}

			fmt.Printf("\n  %s  Discovered %d database(s) → %s\n", styleOK.Render("✓"), len(dbs), configOut)
			for _, db := range dbs {
				fmt.Printf("  %s  %s\n", styleDim.Render("·"), db.Name)
			}
			if dbToken == "" && provider == "turso" {
				fmt.Printf("\n  %s  Replace TOKEN in the config with a Turso auth token before running checks.\n",
					styleWarn.Render("!"))
			}
			fmt.Printf("\n  Next: %s\n\n", styleDim.Render("litescope fleet snapshot"))
			return nil
		},
	}

	cmd.Flags().StringVar(&org, "org", "", "Turso organization slug")
	cmd.Flags().StringVar(&account, "account", "", "Cloudflare account ID")
	cmd.Flags().StringVar(&token, "token", "", "provider API token (Turso platform token or Cloudflare API token)")
	cmd.Flags().StringVar(&dbToken, "db-token", "", "Turso database/group auth token applied to each DSN")
	cmd.Flags().StringVarP(&configOut, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().BoolVar(&merge, "merge", false, "merge into an existing config instead of overwriting")
	return cmd
}

// ── snapshot ──────────────────────────────────────────────────────────────────

func cmdFleetSnapshot() *cobra.Command {
	var configPath, tag string
	var concurrency int

	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture baselines for the whole fleet in parallel",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			fmt.Printf("\n  Capturing baselines for %d database(s)…\n\n", len(dbs))
			results := fleet.Snapshot(cfg, dbs, concurrency)

			ok, failed := 0, 0
			width := nameWidth(dbsNames(dbs))
			for _, r := range results {
				if r.Err != nil {
					failed++
					fmt.Printf("  %s  %-*s  %s\n", styleErr.Render("✗"), width, r.Database,
						styleErr.Render(truncErr(r.Err)))
					continue
				}
				ok++
				fmt.Printf("  %s  %-*s  %s\n", styleOK.Render("✓"), width, r.Database,
					styleDim.Render(fmt.Sprintf("%d tables → %s", r.Tables, r.Path)))
			}

			fmt.Printf("\n  %s\n\n", summaryLine(len(dbs),
				kv{"captured", ok, styleOK}, kv{"failed", failed, styleErr}))
			if failed > 0 {
				return fmt.Errorf("%d snapshot(s) failed", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only operate on databases with this tag")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max parallel connections (default 8)")
	return cmd
}

// ── check ─────────────────────────────────────────────────────────────────────

func cmdFleetCheck() *cobra.Command {
	var configPath, tag, format string
	var concurrency int

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Detect schema drift across the whole fleet in parallel",
		Long: `Compare every database's live schema against its baseline.

Exit code is 1 when any database has drifted or errored — drop it into CI.

  litescope fleet check
  litescope fleet check --tag group:prod
  litescope fleet check --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, tag)
			if err != nil {
				return err
			}

			report := fleet.Check(cfg, dbs, concurrency)

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printFleetReport(cfg, report)
			}

			if report.HasProblems() {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	cmd.Flags().StringVar(&tag, "tag", "", "only operate on databases with this tag")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "max parallel connections (default 8)")
	return cmd
}

// ── status ────────────────────────────────────────────────────────────────────

func cmdFleetStatus() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the configured fleet",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			if configPath == "" {
				configPath = fleet.DefaultConfigFile
			}
			cfg, err := fleet.Load(configPath)
			if err != nil {
				return err
			}

			name := cfg.Name
			if name == "" {
				name = "(unnamed)"
			}
			// Show every database, including quarantined ones (which operational
			// commands skip), so status reflects the true fleet.
			all := cfg.Databases
			quarantined := len(all) - len(cfg.Active())
			suffix := ""
			if quarantined > 0 {
				suffix = styleDim.Render(" · ") + styleWarn.Render(fmt.Sprintf("%d quarantined", quarantined))
			}
			fmt.Printf("\n  Fleet: %s · %d database(s)%s\n\n", styleBold.Render(name), len(all), suffix)

			width := nameWidth(dbsNames(all))
			for _, db := range all {
				mark := styleDim.Render("○")
				note := styleDim.Render("no baseline")
				if db.Quarantined {
					mark = styleWarn.Render("⚠")
					note = styleWarn.Render("quarantined")
				} else if _, err := os.Stat(cfg.BaselinePath(db)); err == nil {
					mark = styleOK.Render("●")
					note = styleDim.Render(cfg.BaselinePath(db))
				}
				tags := ""
				if len(db.Tags) > 0 {
					tags = "  " + styleDim.Render("["+strings.Join(db.Tags, ",")+"]")
				}
				fmt.Printf("  %s  %-*s  %s%s\n", mark, width, db.Name, note, tags)
			}
			fmt.Println()
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "fleet config path (default: litescope.fleet.yaml)")
	return cmd
}

// ── shared helpers ────────────────────────────────────────────────────────────

func loadFleet(configPath, tag string) (*fleet.Config, []fleet.Database, error) {
	if configPath == "" {
		configPath = fleet.DefaultConfigFile
	}
	cfg, err := fleet.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	dbs := cfg.Filter(tag)
	if len(dbs) == 0 {
		if tag != "" {
			return nil, nil, fmt.Errorf("no databases match tag %q", tag)
		}
		return nil, nil, fmt.Errorf("fleet config has no databases")
	}
	return cfg, dbs, nil
}

func printFleetReport(cfg *fleet.Config, report *fleet.FleetReport) {
	name := cfg.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Printf("\n  Fleet: %s · %d database(s)\n\n", styleBold.Render(name), len(report.Results))

	width := 0
	for _, r := range report.Results {
		if len(r.Database) > width {
			width = len(r.Database)
		}
	}

	for _, r := range report.Results {
		var mark, state, detail string
		switch r.State {
		case "ok":
			mark = styleOK.Render("●")
			state = styleOK.Render("ok")
			detail = styleDim.Render(fmt.Sprintf("%dms", r.Duration.Milliseconds()))
		case "drift":
			mark = styleWarn.Render("▲")
			state = styleWarn.Render("drift")
			detail = styleWarn.Render(driftSummary(r))
		case "no-baseline":
			mark = styleDim.Render("○")
			state = styleDim.Render("no baseline")
			detail = styleDim.Render("run: litescope fleet snapshot")
		case "error":
			mark = styleErr.Render("✗")
			state = styleErr.Render("error")
			detail = styleErr.Render(truncErr(r.Err))
		}
		fmt.Printf("  %s  %-*s  %-7s  %s\n", mark, width, r.Database, state, detail)
	}

	ok, drift, noBaseline, errCount := report.Counts()
	fmt.Printf("\n  %s\n\n", summaryLine(len(report.Results),
		kv{"ok", ok, styleOK},
		kv{"drift", drift, styleWarn},
		kv{"no baseline", noBaseline, styleDim},
		kv{"error", errCount, styleErr},
	))
}

func driftSummary(r fleet.CheckResult) string {
	if r.Drift == nil {
		return "drift"
	}
	added, removed, modified := 0, 0, 0
	for _, td := range r.Drift.Changes {
		switch {
		case td.Added:
			added++
		case td.Removed:
			removed++
		default:
			modified++
		}
	}
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d table", added))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("-%d table", removed))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("~%d table", modified))
	}
	if len(parts) == 0 {
		return "drift"
	}
	return strings.Join(parts, ", ")
}

type kv struct {
	label string
	count int
	style interface{ Render(...string) string }
}

func summaryLine(total int, parts ...kv) string {
	out := []string{fmt.Sprintf("%d databases", total)}
	for _, p := range parts {
		if p.count > 0 {
			out = append(out, p.style.Render(fmt.Sprintf("%d %s", p.count, p.label)))
		}
	}
	return strings.Join(out, styleDim.Render(" · "))
}

func nameWidth(names []string) int {
	w := 0
	for _, n := range names {
		if len(n) > w {
			w = len(n)
		}
	}
	return w
}

func dbsNames(dbs []fleet.Database) []string {
	out := make([]string, len(dbs))
	for i, db := range dbs {
		out[i] = db.Name
	}
	return out
}

// confirm prompts the user for a yes/no answer on stdin, defaulting to no.
// It returns true only on an explicit "y"/"yes". When stdin is not a terminal
// (e.g. CI without --yes), it returns false so destructive actions are refused.
func confirm(question string) bool {
	fmt.Printf("\n  %s [y/N] ", question)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func truncErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}
