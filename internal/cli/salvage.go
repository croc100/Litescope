package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/croc100/litescope/internal/audit"
	"github.com/croc100/litescope/internal/salvage"
	"github.com/spf13/cobra"
)

func cmdSalvage() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "salvage <corrupt.db>",
		Short: "Recover readable rows from a corrupt SQLite database into a fresh file",
		Long: `Best-effort recovery of a SQLite database that fails integrity_check and has
no healthy backup to restore from.

This does not modify the corrupt file — it replays the schema (from
sqlite_master) into a brand-new database, then copies every row it can still
read out of each table, skipping over the exact rowids that live on corrupt
pages instead of giving up on the whole table. This is litescope's pure-Go
answer to the official sqlite3 shell's ".recover" command (which needs cgo
and isn't available through this project's driver).

  litescope salvage ./corrupt.db                      # writes ./corrupt.recovered.db
  litescope salvage ./corrupt.db --output ./fixed.db

Run 'litescope health ./corrupt.db --deep' first to confirm it's actually
corrupt, and 'litescope fleet recover' if you have a healthy backup instead —
salvage is the fallback when no backup exists.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			out := output
			if out == "" {
				out = defaultSalvageOutput(src)
			}

			res, err := salvage.Recover(src, out)
			if err != nil {
				audit.Record(audit.Entry{Action: "salvage.recover", Target: src,
					Outcome: "error", Detail: err.Error()})
				return err
			}
			audit.Record(audit.Entry{Action: "salvage.recover", Target: src,
				Summary: fmt.Sprintf("%s -> %s: %d copied, %d lost", src, out, res.TotalCopied(), res.TotalLost())})

			mark := styleOK.Render("✓")
			if !res.OutputHealthy {
				mark = styleErr.Render("✗")
			}
			fmt.Printf("\n  %s  Salvage complete\n", mark)
			fmt.Printf("       %s  %s\n", styleDim.Render("output:"), res.Output)
			for _, t := range res.Tables {
				if t.Error != "" {
					fmt.Printf("       %s  %s — %s\n", styleErr.Render("✗"), t.Table, t.Error)
					continue
				}
				line := fmt.Sprintf("%s: %d rows copied", t.Table, t.RowsCopied)
				if t.RowsLost > 0 {
					line += styleWarn.Render(fmt.Sprintf(", %d lost", t.RowsLost))
				}
				fmt.Printf("       %s  %s\n", styleOK.Render("·"), line)
			}
			for _, s := range res.SchemaLost {
				fmt.Printf("       %s  schema object lost: %s\n", styleWarn.Render("○"), s)
			}
			fmt.Printf("\n  %s  %d rows copied, %d rows lost across %d table(s)\n",
				styleDim.Render("total:"), res.TotalCopied(), res.TotalLost(), len(res.Tables))
			if !res.OutputHealthy {
				fmt.Printf("  %s  recovered database still fails quick_check — salvage was incomplete\n", styleErr.Render("!"))
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVar(&output, "output", "", "Path for the recovered database (default: <name>.recovered.db)")
	return cmd
}

// defaultSalvageOutput derives "<name>.recovered.db" from a source path,
// preserving the source's directory and extension.
func defaultSalvageOutput(src string) string {
	ext := filepath.Ext(src)
	base := strings.TrimSuffix(src, ext)
	return base + ".recovered" + ext
}
