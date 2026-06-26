package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/locks"
	"github.com/spf13/cobra"
)

func cmdLocks() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "locks <database>",
		Short: "Diagnose \"database is locked\" / SQLITE_BUSY problems",
		Long: `Diagnose the SQLite locking faults behind "database is locked" and writer
starvation — the single most common SQLite production failure:

  journal mode      DELETE/TRUNCATE block all readers during a write; WAL doesn't
  busy_timeout      defaults to 0 — any contention fails immediately as SQLITE_BUSY
  locking_mode      EXCLUSIVE locks out every other process
  WAL bloat         a stalled checkpoint starves readers

Each finding ships with the exact PRAGMA or DSN change to apply.

  litescope locks app.db
  litescope locks app.db --format json
  litescope locks d1://DB_ID         # provider-specific D1 guidance
  litescope locks turso://TOKEN@ORG/DB

Exit code is 1 when the verdict is attention or critical.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := locks.Diagnose(args[0])
			if err != nil {
				return err
			}

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(r); err != nil {
					return err
				}
			} else {
				printLocks(r)
			}

			if r.Verdict != locks.SeverityOK {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	return cmd
}

func printLocks(r *locks.Report) {
	var mark, label string
	switch r.Verdict {
	case "critical":
		mark, label = styleErr.Render("✗"), styleErr.Render("CRITICAL")
	case "attention":
		mark, label = styleWarn.Render("⚠"), styleWarn.Render("ATTENTION")
	default:
		mark, label = styleOK.Render("●"), styleOK.Render("OK")
	}

	fmt.Printf("\n  %s  %s  %s  %s\n",
		mark, label, styleDim.Render(r.Source), styleDim.Render("("+r.Provider+")"))

	if len(r.Pragmas) > 0 {
		fmt.Println()
		for _, k := range []string{"journal_mode", "wal_autocheckpoint", "locking_mode", "synchronous"} {
			if v, ok := r.Pragmas[k]; ok && v != "" {
				fmt.Printf("  %s  %s\n", styleDim.Render(fmt.Sprintf("%-20s", k)), v)
			}
		}
	}

	fmt.Println()
	for _, f := range r.Findings {
		var icon string
		switch f.Severity {
		case locks.SeverityCritical:
			icon = styleErr.Render("✗")
		case locks.SeverityWarning:
			icon = styleWarn.Render("⚠")
		default:
			icon = styleOK.Render("●")
		}
		fmt.Printf("  %s  %s\n", icon, styleBold.Render(f.Summary))
		if f.Detail != "" {
			for _, line := range wrapLines(f.Detail, 72) {
				fmt.Printf("      %s\n", styleDim.Render(line))
			}
		}
		if f.Fix != "" {
			for _, line := range strings.Split(f.Fix, "\n") {
				fmt.Printf("      %s\n", styleOK.Render(line))
			}
		}
		fmt.Println()
	}
}

// wrapLines breaks text into lines no longer than width, on word boundaries.
func wrapLines(text string, width int) []string {
	words := strings.Fields(text)
	var lines []string
	var cur string
	for _, w := range words {
		if cur == "" {
			cur = w
		} else if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
