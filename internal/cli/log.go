package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/audit"
	"github.com/spf13/cobra"
)

func cmdLog() *cobra.Command {
	var limit int
	var target, action, format string

	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the audit log of operations that changed a database",
		Long: `Print the append-only audit log: every migration, fleet convergence/recovery,
and SQL/row write Litescope has performed, newest first — who ran it, when, the
target, and how it turned out.

The log is local-first (no server): it lives at ~/.litescope/audit.log. Set
LITESCOPE_OPERATOR to record a name other than your OS username.

  litescope log
  litescope log --limit 20
  litescope log --target prod.db
  litescope log --action migrate.apply
  litescope log --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := audit.Read(limit, target, action)
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			if len(entries) == 0 {
				fmt.Printf("\n  %s\n\n", styleDim.Render("No operations recorded yet."))
				return nil
			}
			fmt.Printf("\n  Audit log · %s\n\n", styleDim.Render(audit.LogPath()))
			for _, e := range entries {
				mark := styleOK.Render("✓")
				if e.Outcome != "ok" {
					mark = styleErr.Render("✗")
				}
				when := e.Time.Local().Format("2006-01-02 15:04:05")
				fmt.Printf("  %s  %s  %s  %s\n", mark, styleDim.Render(when),
					padRight(e.Action, 16), e.Summary)
				fmt.Printf("        %s  %s\n", styleDim.Render(e.Operator), styleDim.Render(e.Target))
				if e.Detail != "" && e.Outcome != "ok" {
					fmt.Printf("        %s\n", styleErr.Render(e.Detail))
				}
			}
			fmt.Printf("\n  %s\n\n", styleDim.Render(fmt.Sprintf("%d entr%s", len(entries), plural(len(entries)))))
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "max entries to show (0 = all)")
	cmd.Flags().StringVar(&target, "target", "", "filter by target substring (db path or fleet name)")
	cmd.Flags().StringVar(&action, "action", "", "filter by exact action (e.g. migrate.apply)")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	return cmd
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
