package cli

import (
	"fmt"

	"github.com/croc100/litescope/internal/d1sync"
	"github.com/spf13/cobra"
)

func cmdD1() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "d1",
		Short: "D1 data operations: pull, push (sync between D1 and local SQLite)",
		Long: `Commands for syncing data between a Cloudflare D1 database and a local
SQLite file. Credentials are read from environment variables:

  export CLOUDFLARE_API_TOKEN=...
  export CLOUDFLARE_ACCOUNT_ID=...`,
	}
	cmd.AddCommand(cmdD1Pull())
	cmd.AddCommand(cmdD1Push())
	return cmd
}

func cmdD1Pull() *cobra.Command {
	var batchSize int

	cmd := &cobra.Command{
		Use:   "pull <d1-source> <local-file>",
		Short: "Copy a D1 database to a local SQLite file",
		Long: `Download the full schema and data from a Cloudflare D1 database into a
local SQLite file. Useful for local development, inspection, or backup.

  export CLOUDFLARE_API_TOKEN=...
  export CLOUDFLARE_ACCOUNT_ID=...

  litescope d1 pull d1://DB_UUID ./local.db
  litescope d1 pull d1://DB_UUID ./snapshot.db

The local file is created or overwritten. All user tables are copied;
Cloudflare-internal tables (_cf_*) are skipped.

After pulling you can use any litescope command on the local file:

  litescope doctor ./local.db
  litescope diff ./local.db ./other.db`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			d1DSN, localPath := args[0], args[1]
			if len(d1DSN) < 5 || d1DSN[:5] != "d1://" {
				return fmt.Errorf("first argument must be a D1 DSN (d1://DB_UUID); got %q", d1DSN)
			}

			fmt.Printf("\n  Pulling %s → %s\n\n", d1DSN, localPath)

			opts := d1sync.PullOptions{
				BatchSize: batchSize,
				ProgressFn: func(table string, rows int) {
					fmt.Printf("  %s  %-30s  %d rows\n", styleOK.Render("✓"), table, rows)
				},
			}
			if err := d1sync.Pull(d1DSN, localPath, opts); err != nil {
				return err
			}
			fmt.Printf("\n  %s  Saved to %s\n\n", styleOK.Render("✓"), localPath)
			return nil
		},
	}

	cmd.Flags().IntVar(&batchSize, "batch-size", 500, "Rows per SELECT page")
	return cmd
}

func cmdD1Push() *cobra.Command {
	var batchSize int
	var dropExisting bool

	cmd := &cobra.Command{
		Use:   "push <local-file> <d1-source>",
		Short: "Copy a local SQLite file to a D1 database",
		Long: `Upload the full schema and data from a local SQLite file into a Cloudflare
D1 database. Useful for seeding a fresh D1 database or restoring from a local
snapshot.

  export CLOUDFLARE_API_TOKEN=...
  export CLOUDFLARE_ACCOUNT_ID=...

  litescope d1 push ./seed.db d1://DB_UUID
  litescope d1 push ./local.db d1://DB_UUID --drop-existing

By default tables that already exist in D1 are left untouched (INSERT OR
IGNORE for rows). Use --drop-existing to DROP and recreate each table before
inserting — this gives a clean overwrite.

⚠  --drop-existing is destructive. All existing data in the target D1
   database is lost. Consider 'litescope rewind' to save a restore point first.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			localPath, d1DSN := args[0], args[1]
			if len(d1DSN) < 5 || d1DSN[:5] != "d1://" {
				return fmt.Errorf("second argument must be a D1 DSN (d1://DB_UUID); got %q", d1DSN)
			}

			if dropExisting {
				fmt.Printf("\n  %s  --drop-existing: existing D1 tables will be dropped\n", styleWarn.Render("!"))
			}
			fmt.Printf("\n  Pushing %s → %s\n\n", localPath, d1DSN)

			opts := d1sync.PushOptions{
				BatchSize:    batchSize,
				DropExisting: dropExisting,
				ProgressFn: func(table string, rows int) {
					fmt.Printf("  %s  %-30s  %d rows\n", styleOK.Render("✓"), table, rows)
				},
			}
			if err := d1sync.Push(localPath, d1DSN, opts); err != nil {
				return err
			}
			fmt.Printf("\n  %s  Pushed to %s\n\n", styleOK.Render("✓"), d1DSN)
			return nil
		},
	}

	cmd.Flags().IntVar(&batchSize, "batch-size", 50, "INSERT rows per D1 API call")
	cmd.Flags().BoolVar(&dropExisting, "drop-existing", false, "DROP tables in D1 before inserting (clean overwrite)")
	return cmd
}
