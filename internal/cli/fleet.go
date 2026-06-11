package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/license"
	"github.com/spf13/cobra"
)

func cmdFleet() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Manage many databases at once: discover, baseline, drift-check (Cloud)",
		Long: `Fleet operates on every SQLite database in a Turso org or Cloudflare D1
account as a single unit.

  fleet discover  — list all databases and write a fleet config
  fleet snapshot  — capture baselines for the whole fleet in parallel
  fleet check     — detect schema drift across the whole fleet in parallel
  fleet status    — show the configured fleet

Fleet is a Cloud feature ($49/mo).`,
	}
	cmd.AddCommand(cmdFleetDiscover())
	cmd.AddCommand(cmdFleetSnapshot())
	cmd.AddCommand(cmdFleetCheck())
	cmd.AddCommand(cmdFleetStatus())
	cmd.AddCommand(cmdFleetMigrate())
	return cmd
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
			if err := license.RequireCloud(); err != nil {
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
			if err := license.RequireCloud(); err != nil {
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
			if err := license.RequireCloud(); err != nil {
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
			if err := license.RequireCloud(); err != nil {
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
			if err := license.RequireCloud(); err != nil {
				return err
			}
			cfg, dbs, err := loadFleet(configPath, "")
			if err != nil {
				return err
			}

			name := cfg.Name
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Printf("\n  Fleet: %s · %d database(s)\n\n", styleBold.Render(name), len(dbs))

			width := nameWidth(dbsNames(dbs))
			for _, db := range dbs {
				baseline := cfg.BaselinePath(db)
				mark := styleDim.Render("○")
				note := styleDim.Render("no baseline")
				if _, err := os.Stat(baseline); err == nil {
					mark = styleOK.Render("●")
					note = styleDim.Render(baseline)
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
