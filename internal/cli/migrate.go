package cli

import (
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
)

func cmdMigrate() *cobra.Command {
	var output string
	var force bool

	cmd := &cobra.Command{
		Use:   "migrate <before.db> <after.db>",
		Short: "Generate migration SQL from schema diff",
		Long: `Generate SQLite migration SQL by diffing two databases.

Handles:
  - New tables     → CREATE TABLE
  - Removed tables → DROP TABLE (with warning)
  - Added columns  → ALTER TABLE ... ADD COLUMN
  - Removed columns / type changes → table rebuild pattern (CREATE + INSERT + DROP + RENAME)
  - Indexes        → CREATE INDEX / DROP INDEX

SQLite does not support DROP COLUMN or column type changes directly.
Litescope uses the standard rebuild pattern for those cases.

Examples:
  litescope migrate before.db after.db
  litescope migrate before.db after.db --output migration.sql
  litescope migrate before.db after.db --output migration.sql --force`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldPath, newPath := args[0], args[1]

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

			// ── Warnings ──────────────────────────────────────────────────────
			if migration.HasWarnings() {
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

			// ── Output ────────────────────────────────────────────────────────
			if output == "" {
				fmt.Print(sql)
				return nil
			}

			if err := os.WriteFile(output, []byte(sql), 0644); err != nil {
				return err
			}

			fmt.Printf("\n  %s  Migration written → %s\n", styleOK.Render("✓"), output)
			fmt.Printf("  %s  Statements: %d\n", styleDim.Render("·"), len(migration.Statements))
			if migration.HasWarnings() {
				fmt.Printf("  %s  Warnings:   %d (review before running)\n\n",
					styleWarn.Render("!"), len(migration.Warnings))
			} else {
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "write SQL to file instead of stdout")
	cmd.Flags().BoolVar(&force, "force", false, "write file even when destructive changes are present")
	return cmd
}
