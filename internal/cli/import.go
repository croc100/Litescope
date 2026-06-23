package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/croc100/litescope/internal/importer"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func cmdImport() *cobra.Command {
	var toPath string
	var table string
	var format string
	var replace bool
	var appendMode bool
	var noHeader bool
	var delimiter string

	cmd := &cobra.Command{
		Use:   "import <file.csv|tsv|json|xlsx>",
		Short: "Import a CSV / TSV / JSON / Excel file into a SQLite table (with type inference)",
		Long: `Turn a spreadsheet or data file into a real SQLite database — type-inferred,
header-aware, one command.

Format is detected from the extension (.csv / .tsv / .json / .xlsx) unless
--format is given. The destination database defaults to <file>.db and the table
to the file's name; both can be overridden. Excel imports read the first sheet.

Examples:
  litescope import sales.csv                       # -> sales.db, table "sales"
  litescope import budget.xlsx                      # first sheet -> budget.db
  litescope import sales.csv --to shop.db --table orders
  litescope import data.tsv --append
  litescope import records.json --to app.db --replace`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

			f, err := os.Open(input)
			if err != nil {
				return fmt.Errorf("open input: %w", err)
			}
			defer f.Close()

			if format == "" || format == "auto" {
				format = detectFormat(input)
			}
			if toPath == "" {
				toPath = strings.TrimSuffix(input, filepath.Ext(input)) + ".db"
			}
			if table == "" {
				table = sanitizeTableName(input)
			}

			if replace && appendMode {
				return fmt.Errorf("--replace and --append are mutually exclusive")
			}
			mode := importer.ModeCreate
			switch {
			case replace:
				mode = importer.ModeReplace
			case appendMode:
				mode = importer.ModeAppend
			}

			opt := importer.Options{
				Table:     table,
				Mode:      mode,
				HasHeader: !noHeader,
			}
			if format == "tsv" {
				opt.Delimiter = '\t'
			}
			if d := parseDelimiter(delimiter); d != 0 {
				opt.Delimiter = d // explicit override wins
			}

			db, err := sql.Open("sqlite", toPath)
			if err != nil {
				return err
			}
			defer db.Close()

			var res *importer.Result
			switch format {
			case "csv", "tsv":
				res, err = importer.ImportCSV(db, f, opt)
			case "json":
				res, err = importer.ImportJSON(db, f, opt)
			case "xlsx":
				res, err = importer.ImportXLSX(db, f, opt)
			default:
				return fmt.Errorf("unknown format %q (use csv, tsv, json, or xlsx)", format)
			}
			if err != nil {
				return err
			}

			fmt.Printf("\n  %s  Imported %s\n", styleOK.Render("◎"),
				styleBold.Render(fmt.Sprintf("%d row(s)", res.Rows)))
			fmt.Printf("  %s  Into:   %s  table %s\n", styleDim.Render("·"), toPath, styleBold.Render(res.Table))
			fmt.Printf("  %s  Columns: %s\n", styleDim.Render("·"), formatColumns(res.Columns))
			fmt.Printf("\n  Next: litescope doctor %s\n\n", toPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&toPath, "to", "", "destination database file (default <file>.db)")
	cmd.Flags().StringVar(&table, "table", "", "destination table name (default from filename)")
	cmd.Flags().StringVarP(&format, "format", "f", "auto", "input format: auto|csv|tsv|json|xlsx")
	cmd.Flags().BoolVar(&replace, "replace", false, "drop and recreate the table if it exists")
	cmd.Flags().BoolVar(&appendMode, "append", false, "append into an existing table")
	cmd.Flags().BoolVar(&noHeader, "no-header", false, "CSV/TSV has no header row (columns named col1, col2, …)")
	cmd.Flags().StringVar(&delimiter, "delimiter", "", "CSV delimiter override (e.g. ';')")
	return cmd
}

func detectFormat(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsv", ".tab":
		return "tsv"
	case ".json":
		return "json"
	case ".xlsx":
		return "xlsx"
	default:
		return "csv"
	}
}

func parseDelimiter(s string) rune {
	if s == "" {
		return 0
	}
	r := []rune(s)
	return r[0]
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// sanitizeTableName derives a safe table name from a file path.
func sanitizeTableName(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = nonIdent.ReplaceAllString(base, "_")
	base = strings.Trim(base, "_")
	if base == "" || (base[0] >= '0' && base[0] <= '9') {
		base = "t_" + base
	}
	return base
}

func formatColumns(cols []importer.Column) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = fmt.Sprintf("%s %s", c.Name, styleDim.Render(c.Type))
	}
	return strings.Join(parts, ", ")
}
