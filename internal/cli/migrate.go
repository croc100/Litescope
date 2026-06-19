package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/audit"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/policy"
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
	cmd.AddCommand(cmdMigratePlan())
	cmd.AddCommand(cmdMigrateNew())
	cmd.AddCommand(cmdMigrateStatus())
	cmd.AddCommand(cmdMigrateUp())
	return cmd
}

// ── declarative schema-as-code ──────────────────────────────────────────────

func cmdMigratePlan() *cobra.Command {
	var schemaFile, output string
	cmd := &cobra.Command{
		Use:   "plan <db> --schema <schema.sql>",
		Short: "Plan the migration to bring a database to a declarative schema (free)",
		Long: `Declarative schema-as-code: keep your desired schema as a checked-in .sql file
of CREATE statements, and plan the migration that brings a live database to match
it. Shows the SQL and a blast-radius report; does not apply anything.

  litescope migrate plan prod.db --schema schema.sql
  litescope migrate plan prod.db --schema schema.sql -o migration.sql

Pair with the versioned workflow:
  litescope migrate new sync --from prod.db --schema schema.sql`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if schemaFile == "" {
				return fmt.Errorf("--schema is required")
			}
			sqlText, err := os.ReadFile(schemaFile)
			if err != nil {
				return err
			}
			live, err := schema.Load(args[0])
			if err != nil {
				return err
			}
			desired, err := schema.FromSQL(string(sqlText))
			if err != nil {
				return err
			}

			d := diff.CompareSchemas(live, desired)
			if len(d.Schema) == 0 {
				fmt.Printf("\n  %s  %s already matches %s — nothing to do.\n\n",
					styleOK.Render("✓"), args[0], schemaFile)
				return nil
			}

			m := migrate.Generate(d, desired)
			if ops, err := migrate.AnalyzeAll(d, args[0]); err == nil {
				printBlastRadius(ops)
			}

			if output != "" {
				if err := os.WriteFile(output, []byte(m.SQL()), 0644); err != nil {
					return err
				}
				fmt.Printf("  %s  Migration written → %s\n\n", styleOK.Render("✓"), output)
				return nil
			}
			fmt.Print(m.SQL())
			return nil
		},
	}
	cmd.Flags().StringVar(&schemaFile, "schema", "", "declarative schema file (CREATE statements)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write SQL to file instead of stdout")
	return cmd
}

// ── versioned migration workflow ────────────────────────────────────────────

const defaultMigrationsDir = "migrations"

func cmdMigrateNew() *cobra.Command {
	var dir, from, to, schemaFile string
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Create the next versioned migration file (free)",
		Long: `Create a numbered migration file in the migrations directory.

  litescope migrate new add_users_table
  litescope migrate new add_index  --from prod.db --to desired.db
  litescope migrate new sync       --from prod.db --schema schema.sql

With --from + --to (a database) or --from + --schema (a declarative .sql file),
the migration is pre-filled with the generated SQL and a blast-radius report is
printed. Otherwise an empty template is created.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := ""
			if from != "" || to != "" || schemaFile != "" {
				if from == "" {
					return fmt.Errorf("--from is required with --to/--schema")
				}
				if (to == "") == (schemaFile == "") {
					return fmt.Errorf("give exactly one of --to or --schema")
				}
				var desired *schema.Schema
				var err error
				if to != "" {
					desired, err = schema.Load(to)
				} else {
					var sqlText []byte
					if sqlText, err = os.ReadFile(schemaFile); err == nil {
						desired, err = schema.FromSQL(string(sqlText))
					}
				}
				if err != nil {
					return err
				}
				live, err := schema.Load(from)
				if err != nil {
					return err
				}
				d := diff.CompareSchemas(live, desired)
				body = migrate.Generate(d, desired).SQL()
				if ops, err := migrate.AnalyzeAll(d, from); err == nil {
					printBlastRadius(ops)
				}
			}
			path, err := migrate.New(dir, args[0], body)
			if err != nil {
				return err
			}
			fmt.Printf("\n  %s  Created %s\n\n", styleOK.Render("✓"), path)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", defaultMigrationsDir, "migrations directory")
	cmd.Flags().StringVar(&from, "from", "", "generate SQL from this source database")
	cmd.Flags().StringVar(&to, "to", "", "...to this target database's schema")
	cmd.Flags().StringVar(&schemaFile, "schema", "", "...to this declarative schema file (instead of --to)")
	return cmd
}

func cmdMigrateStatus() *cobra.Command {
	var dir, format string
	cmd := &cobra.Command{
		Use:   "status <db>",
		Short: "Show applied and pending versioned migrations (free)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := migrate.GetStatus(args[0], dir)
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}
			fmt.Printf("\n  Migrations: %s\n\n", styleDim.Render(dir))
			for _, a := range st.Applied {
				fmt.Printf("  %s  %s_%s  %s\n", styleOK.Render("✓"), a.Version, a.Name, styleDim.Render(a.AppliedAt))
			}
			for _, p := range st.Pending {
				fmt.Printf("  %s  %s_%s  %s\n", styleDim.Render("○"), p.Version, p.Name, styleDim.Render("pending"))
			}
			if len(st.Drifted) > 0 {
				fmt.Printf("\n  %s  history drift — file changed after apply: %s\n",
					styleErr.Render("✗"), strings.Join(st.Drifted, ", "))
			}
			fmt.Printf("\n  %s\n\n", styleDim.Render(fmt.Sprintf("%d applied · %d pending", len(st.Applied), len(st.Pending))))
			if len(st.Drifted) > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", defaultMigrationsDir, "migrations directory")
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	return cmd
}

func cmdMigrateUp() *cobra.Command {
	var dir, backupDir string
	var dryRun, noBackup bool
	cmd := &cobra.Command{
		Use:   "up <db>",
		Short: "Apply all pending versioned migrations in order (Pro)",
		Long: `Apply every pending migration in order, each through the safe pipeline:
pre-flight integrity check, VACUUM INTO backup, single transaction, foreign-key
verification, and rollback on failure. Each applied migration is recorded in the
litescope_schema_migrations table. Stops at the first failure.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := license.RequirePro(); err != nil {
				return err
			}
			res, err := migrate.Up(args[0], dir, migrate.ApplyOptions{
				DryRun:    dryRun,
				BackupDir: backupDir,
				NoBackup:  noBackup,
			})
			if err != nil {
				if res != nil && len(res.Applied) > 0 {
					fmt.Printf("\n  %s  Applied %d before failure: %s\n", styleWarn.Render("!"), len(res.Applied), strings.Join(res.Applied, ", "))
				}
				return err
			}
			if len(res.Applied) == 0 {
				fmt.Printf("\n  %s  Up to date — no pending migrations.\n\n", styleOK.Render("✓"))
				return nil
			}
			verb := "Applied"
			if dryRun {
				verb = "Validated (dry-run)"
			}
			fmt.Printf("\n  %s  %s %d migration(s): %s\n\n", styleOK.Render("✓"), verb, len(res.Applied), strings.Join(res.Applied, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", defaultMigrationsDir, "migrations directory")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate each migration (apply + rollback) without committing")
	cmd.Flags().BoolVar(&noBackup, "no-backup", false, "skip the automatic per-migration backup")
	cmd.Flags().StringVar(&backupDir, "backup-dir", "", "directory for backups (default: alongside the database)")
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

	// ── Blast-radius analysis ─────────────────────────────────────────────
	ops, err := migrate.AnalyzeAll(d, oldPath)
	if err != nil {
		ops = nil
	}

	printBlastRadius(ops)

	hasDestructive := false
	for _, op := range ops {
		if op.Kind == migrate.OpDestructive {
			hasDestructive = true
			break
		}
	}

	if hasDestructive && !force && output != "" {
		fmt.Fprintf(os.Stderr, "  Use --force to write anyway.\n\n")
		return fmt.Errorf("aborted: destructive migration (use --force to override)")
	}

	// ── Output ────────────────────────────────────────────────────────────
	if output == "" {
		fmt.Print(sql)
		return nil
	}

	if err := os.WriteFile(output, []byte(sql), 0644); err != nil {
		return err
	}

	risky, destructive := 0, 0
	for _, op := range ops {
		switch op.Kind {
		case migrate.OpRisky:
			risky++
		case migrate.OpDestructive:
			destructive++
		}
	}

	fmt.Printf("\n  %s  Migration written → %s\n", styleOK.Render("✓"), output)
	fmt.Printf("  %s  Statements:  %d\n", styleDim.Render("·"), len(migration.Statements))
	if destructive > 0 {
		fmt.Printf("  %s  Destructive: %d  (review before running)\n", styleWarn.Render("!"), destructive)
	}
	if risky > 0 {
		fmt.Printf("  %s  Rebuild:     %d  (check estimated lock times above)\n", styleWarn.Render("⚠"), risky)
	}
	fmt.Println()
	return nil
}

func printBlastRadius(ops []migrate.Operation) {
	if len(ops) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "\n  Blast radius analysis")
	fmt.Fprintln(os.Stderr, "  ─────────────────────────────────────────────────────────────")
	for _, op := range ops {
		var iconStyle string
		switch op.Kind {
		case migrate.OpSafe:
			iconStyle = styleOK.Render(op.Icon)
		case migrate.OpRisky:
			iconStyle = styleWarn.Render(op.Icon)
		case migrate.OpDestructive:
			iconStyle = styleErr.Render(op.Icon)
		}
		fmt.Fprintf(os.Stderr, "  %s  %-50s  %s\n", iconStyle, op.Headline, styleDim.Render(op.Detail))
	}
	fmt.Fprintln(os.Stderr, "  ─────────────────────────────────────────────────────────────")
	fmt.Fprintln(os.Stderr)
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

			if !dryRun {
				if pol, _ := policy.Load(); pol != nil {
					if perr := pol.Allow(dbPath); perr != nil {
						audit.Record(audit.Entry{Action: "migrate.apply", Target: dbPath,
							Summary: fmt.Sprintf("%d statements", len(stmts)), Outcome: "blocked", Detail: perr.Error()})
						return perr
					}
				}
			}

			res, err := migrate.Apply(dbPath, string(sqlText), migrate.ApplyOptions{
				DryRun:    dryRun,
				NoBackup:  noBackup,
				BackupDir: backupDir,
			})
			if err != nil {
				if !dryRun {
					detail := err.Error()
					if res != nil && res.Restored {
						detail += " (restored from backup)"
					}
					audit.Record(audit.Entry{Action: "migrate.apply", Target: dbPath,
						Summary: fmt.Sprintf("%d statements", len(stmts)), Outcome: "error", Detail: detail})
				}
				if res != nil && res.Restored {
					fmt.Fprintf(os.Stderr, "\n  %s  Database restored from backup: %s\n",
						styleWarn.Render("!"), res.BackupPath)
				}
				return err
			}

			if !dryRun {
				audit.Record(audit.Entry{Action: "migrate.apply", Target: dbPath,
					Summary: fmt.Sprintf("%d statements applied", res.Executed), Detail: res.BackupPath})
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
