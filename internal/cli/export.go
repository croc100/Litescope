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
		Short: "Export a table or query to CSV / TSV / JSON",
		Long: `Stream a whole table — or any read-only query — out of a SQLite database to
CSV, TSV, or JSON. The data-out half of Litescope: import a spreadsheet, fix it,
export it back.

The database is opened read-only. Output goes to stdout unless -o is given.

Examples:
  litescope export shop.db --table orders > orders.csv
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

			n, err := exporter.Export(db, q, format, out)
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
	cmd.Flags().StringVarP(&format, "format", "f", "csv", "output format: csv|tsv|json")
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
