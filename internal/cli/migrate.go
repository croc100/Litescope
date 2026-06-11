package cli

import (
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
)

func cmdMigrate() *cobra.Command {
	var output string
	var force bool

	cmd := &cobra.Command{
		Use:   "migrate <before.db> <after.db>",
		Short: "Generate and apply schema migrations",
		Long: `Generate SQLite migration SQL by diffing two databases.

Handles:
  - New tables     → CREATE TABLE
  - Removed tables → DROP TABLE (with warning)
  - Added columns  → ALTER TABLE ... ADD COLUMN
  - Removed columns / type changes → table rebuild pattern (CREATE + INSERT + DROP + RENAME)
  - Indexes        → CREATE INDEX / DROP INDEX

SQLite does not support DROP COLUMN or column type changes directly.
Litescope uses the standard rebuild pattern for those cases.

Destructive changes are analyzed against the source database so warnings
report the actual number of rows affected.

Examples:
  litescope migrate before.db after.db
  litescope migrate before.db after.db --output migration.sql
  litescope migrate before.db after.db --output migration.sql --force
  litescope migrate apply prod.db migration.sql --dry-run`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateGen(args[0], args[1], output, force)
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "write SQL to file instead of stdout")
	cmd.Flags().BoolVar(&force, "force", false, "write file even when destructive changes are present")

	cmd.AddCommand(cmdMigrateApply())
	return cmd
}

func runMigrateGen(oldPath, newPath, output string, force bool) error {
	d, err := diff.Compare(oldPath, newPath)
	if err != nil {
		return err
	}

	if len(d.Schema) == 0 {
		fmt.Println("  No schema changes detected. Nothing to migrate.")
		return nil
	}

	newSchema, err := schema.Load(newPath)
	if err != nil {
		return fmt.Errorf("loading new schema: %w", err)
	}

	migration := migrate.Generate(d, newSchema)
	sql := migration.SQL()

	// ── Risk analysis with measured blast radius ──────────────────────────
	risks, err := migrate.Analyze(d, oldPath)
	if err != nil {
		// Fall back to plain warnings when the source DB can't be read.
		risks = nil
	}

	if len(risks) > 0 {
		fmt.Fprintf(os.Stderr, "\n  %s  Destructive changes detected:\n", styleWarn.Render("!"))
		for _, r := range risks {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", styleDim.Render("·"), r)
		}
		fmt.Fprintln(os.Stderr)

		if !force && output != "" {
			fmt.Fprintf(os.Stderr, "  Use --force to write anyway.\n\n")
			return fmt.Errorf("aborted: destructive migration (use --force to override)")
		}
	} else if migration.HasWarnings() {
		fmt.Fprintf(os.Stderr, "\n  %s  Destructive changes detected:\n", styleWarn.Render("!"))
		for _, w := range migration.Warnings {
			fmt.Fprintf(os.Stderr, "  %s  %s\n", styleDim.Render("·"), w)
		}
		fmt.Fprintln(os.Stderr)

		if !force && output != "" {
			fmt.Fprintf(os.Stderr, "  Use --force to write anyway.\n\n")
			return fmt.Errorf("aborted: destructive migration (use --force to override)")
		}
	}

	// ── Output ────────────────────────────────────────────────────────────
	if output == "" {
		fmt.Print(sql)
		return nil
	}

	if err := os.WriteFile(output, []byte(sql), 0644); err != nil {
		return err
	}

	fmt.Printf("\n  %s  Migration written → %s\n", styleOK.Render("✓"), output)
	fmt.Printf("  %s  Statements: %d\n", styleDim.Render("·"), len(migration.Statements))
	if len(risks) > 0 {
		fmt.Printf("  %s  Risks:      %d (review before running)\n\n", styleWarn.Render("!"), len(risks))
	} else {
		fmt.Println()
	}
	return nil
}

func cmdMigrateApply() *cobra.Command {
	var dryRun bool
	var noBackup bool
	var backupDir string
	var verify string

	cmd := &cobra.Command{
		Use:   "apply <target.db> <migration.sql>",
		Short: "Apply a migration with backup, verification, and automatic rollback (Pro)",
		Long: `Apply migration SQL to a local SQLite database safely.

Safety sequence:
  1. Pre-flight integrity check — corrupt databases are refused
  2. Automatic backup via VACUUM INTO (point-in-time consistent)
  3. All statements run inside a single transaction
  4. Foreign key + integrity verification before commit
  5. Any failure rolls back; a failed commit restores the backup

Examples:
  litescope migrate apply prod.db migration.sql --dry-run
  litescope migrate apply prod.db migration.sql
  litescope migrate apply prod.db migration.sql --backup-dir ./backups
  litescope migrate apply prod.db migration.sql --verify staging.db`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}

			dbPath, sqlPath := args[0], args[1]

			sqlText, err := os.ReadFile(sqlPath)
			if err != nil {
				return fmt.Errorf("read migration: %w", err)
			}

			stmts := migrate.SplitStatements(string(sqlText))
			mode := "apply"
			if dryRun {
				mode = "dry-run"
			}
			fmt.Printf("\n  %s  %s → %s (%d statements)\n",
				styleDim.Render("·"), mode, dbPath, len(stmts))

			res, err := migrate.Apply(dbPath, string(sqlText), migrate.ApplyOptions{
				DryRun:    dryRun,
				NoBackup:  noBackup,
				BackupDir: backupDir,
			})
			if err != nil {
				if res != nil && res.Restored {
					fmt.Fprintf(os.Stderr, "\n  %s  Database restored from backup: %s\n",
						styleWarn.Render("!"), res.BackupPath)
				}
				return err
			}

			if res.BackupPath != "" {
				fmt.Printf("  %s  Backup:     %s\n", styleDim.Render("·"), res.BackupPath)
			}

			if dryRun {
				fmt.Printf("  %s  Dry run OK — %d statements executed and rolled back (%.0fms)\n",
					styleOK.Render("✓"), res.Executed, float64(res.Duration.Microseconds())/1000)
				fmt.Printf("  %s  Database unchanged. Run without --dry-run to apply.\n\n", styleDim.Render("·"))
				return nil
			}

			fmt.Printf("  %s  Applied %d statements (%.0fms)\n",
				styleOK.Render("✓"), res.Executed, float64(res.Duration.Microseconds())/1000)

			// ── Optional schema verification against a reference DB ──────
			if verify != "" {
				got, err := schema.Load(dbPath)
				if err != nil {
					return fmt.Errorf("verify: load %s: %w", dbPath, err)
				}
				want, err := schema.Load(verify)
				if err != nil {
					return fmt.Errorf("verify: load %s: %w", verify, err)
				}
				d := diff.CompareSchemas(got, want)
				if len(d.Schema) > 0 {
					fmt.Fprintf(os.Stderr, "\n  %s  Schema verification FAILED — %d table(s) differ from %s:\n",
						styleWarn.Render("!"), len(d.Schema), verify)
					for _, td := range d.Schema {
						fmt.Fprintf(os.Stderr, "  %s  %s\n", styleDim.Render("·"), td.Name)
					}
					fmt.Fprintln(os.Stderr)
					return fmt.Errorf("schema mismatch after migration")
				}
				fmt.Printf("  %s  Schema verified — matches %s\n", styleOK.Render("✓"), verify)
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "execute inside a transaction, then roll back")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "skip the automatic backup")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "directory for the automatic backup (default: alongside the database)")
	cmd.Flags().StringVar(&verify, "verify", "", "after applying, verify the schema matches this reference database")
	return cmd
}
