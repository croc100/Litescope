package cli

import (
	"fmt"
	"time"

	"github.com/croc100/litescope/internal/audit"
	"github.com/croc100/litescope/internal/snapshot"
	"github.com/spf13/cobra"
)

func cmdSnapshot() *cobra.Command {
	var label string
	var keep int

	cmd := &cobra.Command{
		Use:   "snapshot <db.sqlite>",
		Short: "Take a point-in-time backup of a local SQLite database",
		Long: `Create a consistent point-in-time snapshot of a local SQLite file.

Snapshots are VACUUM INTO copies — safe even while the database is in WAL mode —
stored in a sibling .litescope-snapshots/ directory next to the database. This
brings local and Turso SQLite the same "did you back up?" safety net that D1 has
through Time Travel.

  litescope snapshot ./app.db
  litescope snapshot ./app.db --label before-migration
  litescope snapshot ./app.db --keep 7          # retain only the 7 newest
  litescope snapshot list ./app.db
  litescope restore ./app.db                     # restore the newest snapshot

Each snapshot is integrity-checked after creation; a corrupt database is refused.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := args[0]
			snap, err := snapshot.Create(dbPath, snapshot.CreateOptions{Label: label, Keep: keep})
			if err != nil {
				audit.Record(audit.Entry{Action: "snapshot.create", Target: dbPath,
					Outcome: "error", Detail: err.Error()})
				return err
			}
			audit.Record(audit.Entry{Action: "snapshot.create", Target: dbPath,
				Summary: fmt.Sprintf("%s (%s)", snap.Path, humanBytes(snap.SizeBytes))})

			fmt.Printf("\n  %s  Snapshot created\n", styleOK.Render("✓"))
			fmt.Printf("       %s  %s\n", styleDim.Render("file:"), snap.Path)
			fmt.Printf("       %s  %s\n", styleDim.Render("size:"), humanBytes(snap.SizeBytes))
			if keep > 0 {
				fmt.Printf("       %s  keeping newest %d\n", styleDim.Render("retain:"), keep)
			}
			fmt.Printf("\n  %s  Restore: litescope restore %s\n\n", styleDim.Render("·"), dbPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&label, "label", "", "Optional label recorded in the snapshot name")
	cmd.Flags().IntVar(&keep, "keep", 0, "Retention: keep only the N newest snapshots (0 = keep all)")
	cmd.AddCommand(cmdSnapshotList())
	return cmd
}

func cmdSnapshotList() *cobra.Command {
	return &cobra.Command{
		Use:   "list <db.sqlite>",
		Short: "List point-in-time snapshots for a database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := args[0]
			snaps, err := snapshot.List(dbPath)
			if err != nil {
				return err
			}
			if len(snaps) == 0 {
				fmt.Printf("\n  %s\n", styleWarn.Render("No snapshots yet."))
				fmt.Printf("  %s  Create one: litescope snapshot %s\n\n", styleDim.Render("·"), dbPath)
				return nil
			}
			fmt.Printf("\n  Snapshots · %s\n", dbPath)
			fmt.Printf("  ─────────────────────────────────────────────────────────────\n")
			for i, s := range snaps {
				marker := "  "
				if i == 0 {
					marker = styleOK.Render("→ ")
				}
				label := ""
				if s.Label != "" {
					label = "  " + styleDim.Render("["+s.Label+"]")
				}
				fmt.Printf("  %s%s  %s  %s%s\n", marker,
					s.CreatedAt.Format("2006-01-02 15:04:05"),
					humanBytes(s.SizeBytes), styleDim.Render(s.Path), label)
			}
			fmt.Printf("\n  %s  newest. Restore: litescope restore %s\n\n", styleOK.Render("→"), dbPath)
			return nil
		},
	}
}

func cmdRestore() *cobra.Command {
	var from string
	var noSafety bool

	cmd := &cobra.Command{
		Use:   "restore <db.sqlite>",
		Short: "Restore a local database from a snapshot",
		Long: `Restore a local SQLite database from a point-in-time snapshot.

With no --from, the newest snapshot is restored. The snapshot is integrity-checked
before anything is overwritten, and the current database is itself snapshotted as
a "pre-restore" safety net first (disable with --no-safety-snapshot).

  litescope restore ./app.db                       # newest snapshot
  litescope restore ./app.db --from <snapshot.db>  # a specific snapshot
  litescope snapshot list ./app.db                 # see available snapshots`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath := args[0]

			snapPath := from
			if snapPath == "" {
				latest, ok, err := snapshot.Latest(dbPath)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("no snapshots found for %s — create one with 'litescope snapshot %s'", dbPath, dbPath)
				}
				snapPath = latest.Path
			}

			if err := snapshot.Restore(dbPath, snapPath, !noSafety); err != nil {
				audit.Record(audit.Entry{Action: "snapshot.restore", Target: dbPath,
					Outcome: "error", Detail: err.Error()})
				return err
			}
			audit.Record(audit.Entry{Action: "snapshot.restore", Target: dbPath,
				Summary: "restored from " + snapPath})

			fmt.Printf("\n  %s  Restored %s\n", styleOK.Render("✓"), dbPath)
			fmt.Printf("       %s  %s\n", styleDim.Render("from:"), snapPath)
			if !noSafety {
				fmt.Printf("       %s  a pre-restore snapshot was taken first\n", styleDim.Render("·"))
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Snapshot file to restore (default: newest)")
	cmd.Flags().BoolVar(&noSafety, "no-safety-snapshot", false, "Skip the pre-restore safety snapshot")
	return cmd
}

// agePhrase renders a rough "x ago" for a timestamp (used by health backup hint).
func agePhrase(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
