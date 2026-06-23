package cli

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/exporter"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func cmdExport() *cobra.Command {
	var table string
	var query string
	var format string
	var outPath string

	cmd := &cobra.Command{
		Use:   "export <db>",
		Short: "Export a table or query to CSV / TSV / JSON / Excel",
		Long: `Stream a whole table — or any read-only query — out of a SQLite database to
CSV, TSV, JSON, or Excel (.xlsx). The data-out half of Litescope: import a
spreadsheet, fix it, export it back.

The database is opened read-only. Output goes to stdout unless -o is given; when
-o ends in a known extension the format is inferred. Excel output is binary and
requires -o.

Examples:
  litescope export shop.db --table orders > orders.csv
  litescope export shop.db --table orders -o orders.xlsx
  litescope export shop.db --table orders --format json -o orders.json
  litescope export shop.db --query "SELECT city, COUNT(*) FROM users GROUP BY city"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := args[0]

			if (table == "") == (query == "") {
				return fmt.Errorf("provide exactly one of --table or --query")
			}
			q := query
			if table != "" {
				q = `SELECT * FROM "` + sqlEscapeIdent(table) + `"`
			}

			// Infer format from the output extension when -o is given and the
			// format flag is left at its default.
			if outPath != "" && !cmd.Flags().Changed("format") {
				if f := detectFormat(outPath); f != "" {
					format = f
				}
			}
			if format == "xlsx" && outPath == "" {
				return fmt.Errorf("Excel output is binary — provide -o <file.xlsx>")
			}

			db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
			if err != nil {
				return err
			}
			defer db.Close()

			var out *os.File
			if outPath == "" {
				out = os.Stdout
			} else {
				out, err = os.Create(outPath)
				if err != nil {
					return err
				}
				defer out.Close()
			}

			var n int64
			if format == "xlsx" {
				n, err = exporter.ExportXLSX(db, q, out)
			} else {
				n, err = exporter.Export(db, q, format, out)
			}
			if err != nil {
				return err
			}

			if outPath != "" {
				fmt.Fprintf(os.Stderr, "\n  %s  Exported %s to %s\n\n",
					styleOK.Render("◎"), styleBold.Render(fmt.Sprintf("%d row(s)", n)), outPath)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&table, "table", "", "export this entire table")
	cmd.Flags().StringVar(&query, "query", "", "export the result of a read-only SQL query")
	cmd.Flags().StringVarP(&format, "format", "f", "csv", "output format: csv|tsv|json|xlsx")
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to this file (default stdout)")
	return cmd
}

// sqlEscapeIdent escapes a double-quoted SQLite identifier.
func sqlEscapeIdent(s string) string {
	out := make([]rune, 0, len(s)+2)
	for _, r := range s {
		if r == '"' {
			out = append(out, '"')
		}
		out = append(out, r)
	}
	return string(out)
}
