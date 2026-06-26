package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/health"
	"github.com/spf13/cobra"
)

func cmdHealth() *cobra.Command {
	var format string
	var deep bool

	cmd := &cobra.Command{
		Use:   "health <database.db>",
		Short: "Inspect a database for operational faults (free)",
		Long: `Check a SQLite database for the faults that hurt in production:

  corruption       PRAGMA quick_check (or integrity_check with --deep)
  WAL bloat        a -wal file growing unbounded — checkpoint starvation
  fragmentation    reclaimable freelist space — a VACUUM candidate

  litescope health app.db
  litescope health app.db --deep
  litescope health app.db --format json

Exit code is 1 when the database is in a warning or critical state.

Triage an entire Turso/D1 fleet at once with: litescope fleet health (Pro)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := health.Inspect(args[0], deep)

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(r); err != nil {
					return err
				}
			} else {
				printHealth(r)
			}

			if r.Severity != health.SevOK {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().BoolVar(&deep, "deep", false, "use exhaustive integrity_check instead of quick_check")
	return cmd
}

func printHealth(r *health.Report) {
	var mark, label string
	switch r.Severity {
	case health.SevCritical:
		mark, label = styleErr.Render("✗"), styleErr.Render("CRITICAL")
	case health.SevWarning:
		mark, label = styleWarn.Render("⚠"), styleWarn.Render("WARNING")
	default:
		mark, label = styleOK.Render("●"), styleOK.Render("HEALTHY")
	}

	fmt.Printf("\n  %s  %s  %s\n\n", mark, label, styleDim.Render(r.Path))

	row := func(k, v string) { fmt.Printf("  %s  %s\n", styleDim.Render(fmt.Sprintf("%-14s", k)), v) }

	if r.Reachable {
		integ := styleOK.Render("ok")
		if !r.IntegrityOK {
			integ = styleErr.Render("FAILED")
		}
		row("integrity", integ)
		row("size", humanBytes(r.SizeBytes))
		if r.JournalMode != "" {
			row("journal mode", strings.ToLower(r.JournalMode))
		}
		walStr := humanBytes(r.WALBytes)
		if r.WALBytes >= health.WALBloatBytes {
			walStr = styleWarn.Render(walStr + "  (bloated)")
		}
		row("wal", walStr)
		fragStr := fmt.Sprintf("%.1f%%", r.FragmentationPct())
		if r.FragmentationPct() >= health.FragmentationRatio*100 {
			fragStr = styleWarn.Render(fragStr + "  (VACUUM candidate)")
		}
		row("fragmentation", fragStr)

		if r.HasBackup && r.LastBackupAt != nil {
			row("backup", fmt.Sprintf("%s  (%s, %d kept)",
				styleOK.Render("yes"), agePhrase(*r.LastBackupAt), r.SnapshotCount))
		} else {
			row("backup", styleWarn.Render("none — run 'litescope snapshot "+r.Path+"'"))
		}
	}

	if len(r.Issues) > 0 {
		fmt.Println()
		for _, iss := range r.Issues {
			fmt.Printf("  %s  %s\n", styleWarn.Render("→"), iss)
		}
	}
	fmt.Println()
}
