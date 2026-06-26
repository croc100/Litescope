package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/audit"
	"github.com/croc100/litescope/internal/autopilot"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/spf13/cobra"
)

func cmdAutopilot() *cobra.Command {
	var (
		apply      bool
		aggressive bool
		noSnapshot bool
		queryFile  string
		format     string
		fleetPath  string
		tag        string
	)

	cmd := &cobra.Command{
		Use:   "autopilot <database.db>",
		Short: "Self-driving DBA: apply safe optimizations, explaining every step",
		Long: `Derive and apply safe maintenance/optimization actions for a SQLite database.

Autopilot runs ANALYZE and PRAGMA optimize, adds missing foreign-key indexes
(additive and safe), and — when fragmented — proposes VACUUM. Redundant-index
cleanup and VACUUM are "risky" and only run with --aggressive. Every real change
is preceded by an automatic snapshot, so a run is one 'litescope restore' away
from undo.

  litescope autopilot ./app.db                 # dry-run: show the plan
  litescope autopilot ./app.db --apply         # apply the safe actions
  litescope autopilot ./app.db --apply --aggressive
  litescope autopilot ./app.db --queries queries.sql   # EXPLAIN-based advice
  litescope autopilot --fleet litescope.fleet.yaml --apply

Dry-run by default — nothing changes until you pass --apply.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			queries, err := readQueries(queryFile)
			if err != nil {
				return err
			}
			opts := autopilot.RunOptions{Apply: apply, Aggressive: aggressive, NoSnapshot: noSnapshot}

			// Fleet mode.
			if fleetPath != "" {
				if len(args) > 0 {
					return fmt.Errorf("pass either a database path or --fleet, not both")
				}
				return runAutopilotFleet(fleetPath, tag, queries, opts, format)
			}

			if len(args) != 1 {
				return fmt.Errorf("a database path is required (or use --fleet)")
			}
			dbPath := args[0]

			plan, err := autopilot.BuildPlan(dbPath, queries)
			if err != nil {
				return err
			}
			res, err := autopilot.Run(dbPath, plan, opts)
			if err != nil {
				return err
			}
			if res.Applied {
				audit.Record(audit.Entry{Action: "autopilot.run", Target: dbPath,
					Summary: res.Summary()})
			}

			if format == "json" {
				return printJSON(res)
			}
			printAutopilot(res, apply)
			return nil
		},
	}

	cmd.Flags().BoolVar(&apply, "apply", false, "Apply the safe actions (default: dry-run)")
	cmd.Flags().BoolVar(&aggressive, "aggressive", false, "Also apply risky actions (VACUUM, drop redundant indexes)")
	cmd.Flags().BoolVar(&noSnapshot, "no-snapshot", false, "Skip the automatic pre-run snapshot (not recommended)")
	cmd.Flags().StringVar(&queryFile, "queries", "", "File of SQL queries (one per line/statement) for EXPLAIN-based advice")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().StringVar(&fleetPath, "fleet", "", "Run across every database in a fleet config")
	cmd.Flags().StringVar(&tag, "tag", "", "With --fleet, only databases matching this tag")
	return cmd
}

func runAutopilotFleet(configPath, tag string, queries []string, opts autopilot.RunOptions, format string) error {
	cfg, err := fleet.Load(configPath)
	if err != nil {
		return err
	}
	dbs := cfg.Filter(tag)
	if len(dbs) == 0 {
		return fmt.Errorf("no databases in %q (tag %q)", configPath, tag)
	}

	var results []*autopilot.Result
	for _, db := range dbs {
		if isRemoteDSN(db.DSN) {
			fmt.Printf("  %s  %s — skipped (autopilot is local SQLite only)\n", styleDim.Render("·"), db.Name)
			continue
		}
		plan, err := autopilot.BuildPlan(db.DSN, queries)
		if err != nil {
			fmt.Printf("  %s  %s — %v\n", styleErr.Render("✗"), db.Name, err)
			continue
		}
		res, err := autopilot.Run(db.DSN, plan, opts)
		if err != nil {
			fmt.Printf("  %s  %s — %v\n", styleErr.Render("✗"), db.Name, err)
			continue
		}
		if res.Applied {
			audit.Record(audit.Entry{Action: "autopilot.run", Target: db.DSN, Summary: res.Summary()})
		}
		results = append(results, res)
		if format != "json" {
			fmt.Printf("  %s  %s — %s\n", autopilotMark(res), db.Name, res.Summary())
		}
	}
	if format == "json" {
		return printJSON(results)
	}
	fmt.Println()
	return nil
}

func autopilotMark(r *autopilot.Result) string {
	if r.Counts["failed"] > 0 {
		return styleErr.Render("✗")
	}
	if r.Applied {
		return styleOK.Render("✓")
	}
	return styleDim.Render("·")
}

func printAutopilot(r *autopilot.Result, applied bool) {
	header := "Autopilot plan (dry-run)"
	if applied {
		header = "Autopilot run"
	}
	fmt.Printf("\n  %s · %s\n", header, styleDim.Render(r.Path))
	fmt.Printf("  ─────────────────────────────────────────────────────────────\n")

	if len(r.Actions) == 0 {
		fmt.Printf("  %s  %s\n\n", styleOK.Render("●"), r.Summary())
		return
	}

	for _, a := range r.Actions {
		var mark string
		switch a.Status {
		case "applied":
			mark = styleOK.Render("✓")
		case "failed":
			mark = styleErr.Render("✗")
		case "skipped":
			mark = styleWarn.Render("○")
		default: // proposed
			mark = styleDim.Render("·")
		}
		riskTag := ""
		if a.Risk == autopilot.RiskRisky {
			riskTag = "  " + styleWarn.Render("[risky]")
		}
		label := a.Kind
		if a.Table != "" {
			label += " " + a.Table
		}
		fmt.Printf("  %s  %s%s\n", mark, styleBold.Render(label), riskTag)
		fmt.Printf("       %s\n", styleDim.Render(a.Reason))
		if a.SQL != "" {
			fmt.Printf("       %s\n", a.SQL)
		}
		if a.Error != "" {
			fmt.Printf("       %s %s\n", styleErr.Render("error:"), a.Error)
		}
	}

	fmt.Printf("\n  %s\n", r.Summary())
	if r.Snapshot != "" {
		fmt.Printf("  %s  snapshot taken before run: %s\n", styleDim.Render("·"), r.Snapshot)
	}
	if !applied {
		fmt.Printf("  %s  Re-run with --apply to execute the safe actions.\n", styleDim.Render("·"))
	}
	fmt.Println()
}

func printJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// readQueries loads SQL statements from a file (empty path → nil).
func readQueries(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var queries []string
	var cur strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteByte(' ')
		if strings.HasSuffix(line, ";") {
			queries = append(queries, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		queries = append(queries, s)
	}
	return queries, sc.Err()
}
