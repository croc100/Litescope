package cli

import (
	"bufio"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/dump"
	"github.com/spf13/cobra"
)

func cmdDump() *cobra.Command {
	var (
		schemaOnly bool
		dataOnly   bool
		table      string
		out        string
	)

	cmd := &cobra.Command{
		Use:   "dump <database.db>",
		Short: "Dump a SQLite database as portable SQL (free)",
		Long: `Write a database out as a standalone .sql file — schema plus data — that
recreates it when replayed into sqlite3 or 'litescope migrate'. Equivalent to
the sqlite3 shell's ".dump", with the same ordering and quoting.

  litescope dump app.db                  # full dump to stdout
  litescope dump app.db -o backup.sql    # ... to a file
  litescope dump app.db --schema-only    # DDL only (CREATE statements)
  litescope dump app.db --data-only      # INSERTs only
  litescope dump app.db --table users    # one table and its data

dump works on local files (remote sources expose schema only).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if schemaOnly && dataOnly {
				return fmt.Errorf("--schema-only and --data-only are mutually exclusive")
			}
			if !isLocalFile(args[0]) {
				return fmt.Errorf("dump requires a local database file; got remote source %q", args[0])
			}

			w := cmd.OutOrStdout()
			if out != "" {
				f, err := os.Create(out)
				if err != nil {
					return err
				}
				defer f.Close()
				bw := bufio.NewWriter(f)
				defer bw.Flush()
				w = bw
			}

			return dump.Dump(args[0], w, dump.Options{
				SchemaOnly: schemaOnly,
				DataOnly:   dataOnly,
				Table:      table,
			})
		},
	}

	cmd.Flags().BoolVar(&schemaOnly, "schema-only", false, "dump DDL only (no INSERTs)")
	cmd.Flags().BoolVar(&dataOnly, "data-only", false, "dump data only (no CREATE statements)")
	cmd.Flags().StringVar(&table, "table", "", "dump only this table")
	cmd.Flags().StringVarP(&out, "out", "o", "", "write to a file instead of stdout")
	return cmd
}
