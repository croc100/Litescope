package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/lint"
	"github.com/spf13/cobra"
)

func cmdLint() *cobra.Command {
	var format string
	var strict bool

	cmd := &cobra.Command{
		Use:   "lint <database.db>",
		Short: "Flag SQLite schema design anti-patterns (free)",
		Long: `Check a schema for the structural mistakes that ship by default —
especially in hand-written or AI-generated schemas:

  no-primary-key          a table with no PRIMARY KEY (no stable row identity)
  untyped-column          a column with no declared type (BLOB affinity)
  not-strict              a non-STRICT table (types are advisory, silently coerced)
  autoincrement-overhead  AUTOINCREMENT where plain INTEGER PRIMARY KEY would do
  non-integer-pk          a single-column PK that does not alias the rowid

  litescope lint app.db
  litescope lint app.db --format json
  litescope lint app.db --strict        # exit 1 on info findings too

lint looks only at the schema shape — never data or queries — so it is fast and
safe in CI. By default it exits 1 only on warnings; --strict also fails on info.

For index/performance problems use 'litescope advise'; for both plus health, 'doctor'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := lint.Analyze(args[0])
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
				printLint(r)
			}

			for _, f := range r.Findings {
				if f.Severity == lint.SevWarning || (strict && f.Severity == lint.SevInfo) {
					os.Exit(1)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit 1 on info findings as well as warnings")
	return cmd
}

func printLint(r *lint.Report) {
	if len(r.Findings) == 0 {
		fmt.Printf("\n  %s  No schema design problems found.\n\n", styleOK.Render("●"))
		return
	}

	warnings := 0
	for _, f := range r.Findings {
		if f.Severity == lint.SevWarning {
			warnings++
		}
	}
	fmt.Printf("\n  Lint: %s · %d finding(s), %d warning(s)\n\n", styleDim.Render(r.Path), len(r.Findings), warnings)

	for _, f := range r.Findings {
		mark := styleWarn.Render("⚠")
		if f.Severity == lint.SevInfo {
			mark = styleDim.Render("·")
		}
		loc := ""
		if f.Table != "" {
			loc = " " + styleDim.Render(f.Table)
		}
		fmt.Printf("  %s  %s%s  %s\n", mark, f.Detail, loc, styleDim.Render("["+f.Rule+"]"))
		if f.Suggestion != "" {
			fmt.Printf("       %s\n", styleOK.Render(f.Suggestion))
		}
	}
	fmt.Println()
}
