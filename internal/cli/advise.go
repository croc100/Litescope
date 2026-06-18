package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/spf13/cobra"
)

func cmdAdvise() *cobra.Command {
	var format, query, sqlFile string

	cmd := &cobra.Command{
		Use:   "advise <database.db>",
		Short: "Recommend indexes and flag performance problems (free)",
		Long: `Analyze a SQLite database for the performance problems that ship by default —
especially in AI-generated schemas:

  fk-no-index      a foreign key with no index — every join scans the whole table
                   (SQLite, unlike MySQL, does not auto-index FK columns)
  redundant-index  an index whose columns are already a prefix of another
  full-scan        a supplied query that does a full table scan (EXPLAIN QUERY PLAN)

  litescope advise app.db
  litescope advise app.db --query "SELECT * FROM orders WHERE user_id = ?"
  litescope advise app.db --sql queries.sql
  litescope advise app.db --format json

Exit code is 1 when any warning is found.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var queries []string
			if query != "" {
				queries = append(queries, query)
			}
			if sqlFile != "" {
				b, err := os.ReadFile(sqlFile)
				if err != nil {
					return err
				}
				for _, q := range strings.Split(string(b), ";") {
					if strings.TrimSpace(q) != "" {
						queries = append(queries, q)
					}
				}
			}

			r, err := advisor.Analyze(args[0], queries)
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
				printAdvise(r)
			}

			for _, f := range r.Findings {
				if f.Severity == "warning" {
					os.Exit(1)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().StringVar(&query, "query", "", "analyze a single SQL query for full table scans")
	cmd.Flags().StringVar(&sqlFile, "sql", "", "analyze every query in this .sql file")
	return cmd
}

func printAdvise(r *advisor.Report) {
	if len(r.Findings) == 0 {
		fmt.Printf("\n  %s  No index or query problems found.\n\n", styleOK.Render("●"))
		return
	}

	warnings := 0
	for _, f := range r.Findings {
		if f.Severity == "warning" {
			warnings++
		}
	}
	fmt.Printf("\n  Advisor: %s · %d finding(s)\n\n", styleDim.Render(r.Path), len(r.Findings))

	for _, f := range r.Findings {
		mark := styleWarn.Render("⚠")
		if f.Severity == "info" {
			mark = styleDim.Render("·")
		}
		loc := f.Table
		if loc != "" {
			loc = " " + styleDim.Render(loc)
		}
		fmt.Printf("  %s  %s%s\n", mark, f.Detail, loc)
		if f.Suggestion != "" {
			fmt.Printf("       %s\n", styleOK.Render(f.Suggestion))
		}
	}
	fmt.Println()
}
