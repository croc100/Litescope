package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/croc100/litescope/internal/check"
	"github.com/spf13/cobra"
)

func cmdCheck() *cobra.Command {
	var reference string
	var withData bool
	var format string

	cmd := &cobra.Command{
		Use:   "check <backup.db>",
		Short: "Verify backup database integrity and schema consistency",
		Long: `Runs three levels of verification:
  1. File integrity   — PRAGMA integrity_check (always)
  2. Schema match     — compare against --against reference (if provided)
  3. Row count match  — compare row counts per table (if --data)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := check.Check(args[0], reference, withData)
			if err != nil {
				return err
			}

			switch format {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			default:
				printCheckTerminal(result)
			}

			if !result.Passed {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&reference, "against", "a", "", "reference (production) DB to compare schema and row counts against")
	cmd.Flags().BoolVar(&withData, "data", false, "also compare row counts per table")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json")
	return cmd
}

func printCheckTerminal(r *check.Result) {
	fmt.Printf("\n  %s\n\n", styleDim.Render(r.Path))

	// 1. Integrity
	if r.IntegrityOK {
		fmt.Printf("  %s  File integrity       %s\n",
			styleOK.Render("✓"),
			styleDim.Render("PRAGMA integrity_check passed"))
	} else {
		fmt.Printf("  %s  File integrity       %s\n",
			styleErr.Render("✗"),
			styleErr.Render("CORRUPTED"))
		for _, e := range r.IntegrityErrors {
			fmt.Printf("       %s\n", styleErr.Render(e))
		}
	}

	// 2. Schema
	if r.SchemaOK != nil {
		if *r.SchemaOK {
			fmt.Printf("  %s  Schema match         %s\n",
				styleOK.Render("✓"),
				styleDim.Render("identical to reference"))
		} else {
			fmt.Printf("  %s  Schema match         %s\n",
				styleErr.Render("✗"),
				styleErr.Render("schema drift detected"))
			if r.SchemaDiff != nil {
				for _, td := range r.SchemaDiff.Schema {
					if td.Added {
						fmt.Printf("       %s  %s %s\n",
							styleErr.Render("+"), td.Name,
							styleDim.Render("(in backup, not in reference)"))
					}
					if td.Removed {
						fmt.Printf("       %s  %s %s\n",
							styleErr.Render("-"), td.Name,
							styleDim.Render("(in reference, not in backup)"))
					}
					for _, c := range td.AddedColumns {
						fmt.Printf("       %s  %s.%s %s\n",
							styleErr.Render("+"), td.Name, c.Name,
							styleDim.Render("(extra column in backup)"))
					}
					for _, c := range td.RemovedColumns {
						fmt.Printf("       %s  %s.%s %s\n",
							styleErr.Render("-"), td.Name, c.Name,
							styleDim.Render("(missing column in backup)"))
					}
				}
			}
		}
	}

	// 3. Row counts
	if r.DataOK != nil {
		if *r.DataOK {
			fmt.Printf("  %s  Row counts           %s\n",
				styleOK.Render("✓"),
				styleDim.Render("all tables match"))
		} else {
			fmt.Printf("  %s  Row counts           %s\n",
				styleWarn.Render("!"),
				styleWarn.Render("mismatch detected"))
		}
	}

	if len(r.Tables) > 0 {
		fmt.Println()
		fmt.Printf("  %s\n", styleDim.Render("Table row counts:"))
		for _, t := range r.Tables {
			var line string
			if t.RowsMatch != nil {
				if *t.RowsMatch {
					line = fmt.Sprintf("  %s  %-30s %d rows",
						styleOK.Render("✓"), t.Name, t.BackupRows)
				} else {
					line = fmt.Sprintf("  %s  %-30s backup=%d  ref=%d",
						styleWarn.Render("!"), t.Name, t.BackupRows, t.RefRows)
				}
			} else {
				line = fmt.Sprintf("     %-30s %d rows", t.Name, t.BackupRows)
			}
			fmt.Println(line)
		}
	}

	fmt.Println()
	if r.Passed {
		fmt.Println(styleBold.Render(styleOK.Render("  Check passed")))
	} else {
		fmt.Println(styleBold.Render(styleErr.Render("  Check failed")))
	}
	fmt.Println()
}

// Re-use styles from validate.go (same package)
var _ = lipgloss.NewStyle()
