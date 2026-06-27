package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/croc100/litescope/internal/audit"
	"github.com/croc100/litescope/internal/fleet"
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
	cmd.AddCommand(cmdSnapshotSchedule())
	return cmd
}

// cmdSnapshotSchedule runs unattended snapshots on a fixed interval with
// retention — the local/Turso parity for D1 Time Travel's automatic backups.
// It snapshots a single database or every local database in a fleet config,
// and is meant to be wrapped in a systemd timer / cron entry (or just left
// running). Retention defaults on so the snapshot directory never grows
// unbounded.
func cmdSnapshotSchedule() *cobra.Command {
	var (
		interval  time.Duration
		keep      int
		label     string
		fleetPath string
		tag       string
		once      bool
	)

	cmd := &cobra.Command{
		Use:   "schedule [db.sqlite]",
		Short: "Take snapshots unattended on an interval, with retention",
		Long: `Run point-in-time snapshots on a schedule so local (and Turso) SQLite gets
the same unattended backup safety net D1 has through Time Travel.

Each tick snapshots the database and then prunes to the newest --keep copies, so
the .litescope-snapshots/ directory never grows without bound. Run it against one
database or every local database in a fleet config.

  litescope snapshot schedule ./app.db --interval 1h --keep 24
  litescope snapshot schedule --fleet litescope.fleet.yaml --interval 6h --keep 7
  litescope snapshot schedule ./app.db --once          # one tick, for cron/systemd

For an unattended host, pair --once with a systemd timer or cron entry, or leave
the daemon running and let --interval drive the cadence.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if fleetPath == "" && len(args) != 1 {
				return fmt.Errorf("a database path is required (or use --fleet)")
			}
			if fleetPath != "" && len(args) > 0 {
				return fmt.Errorf("pass either a database path or --fleet, not both")
			}

			targets, err := scheduleTargets(fleetPath, tag, args)
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				return fmt.Errorf("no local databases to snapshot")
			}

			runTick := func() {
				for _, path := range targets {
					snap, err := snapshot.Create(path, snapshot.CreateOptions{Label: label, Keep: keep})
					ts := time.Now().Format("15:04:05")
					if err != nil {
						fmt.Printf("  %s  %s  %s — %v\n", styleErr.Render("✗"), styleDim.Render(ts), path, err)
						audit.Record(audit.Entry{Action: "snapshot.schedule", Target: path,
							Outcome: "error", Detail: err.Error()})
						continue
					}
					fmt.Printf("  %s  %s  %s  %s\n", styleOK.Render("✓"), styleDim.Render(ts),
						path, styleDim.Render(humanBytes(snap.SizeBytes)))
					audit.Record(audit.Entry{Action: "snapshot.schedule", Target: path,
						Summary: fmt.Sprintf("%s (%s)", snap.Path, humanBytes(snap.SizeBytes))})
				}
			}

			if once {
				runTick()
				return nil
			}

			fmt.Printf("\n  %s  Scheduling snapshots\n", styleOK.Render("◉"))
			fmt.Printf("  %s  Targets:  %d database(s)\n", styleDim.Render("·"), len(targets))
			fmt.Printf("  %s  Interval: %s\n", styleDim.Render("·"), interval)
			if keep > 0 {
				fmt.Printf("  %s  Retain:   newest %d per database\n", styleDim.Render("·"), keep)
			}
			fmt.Printf("\n  Press Ctrl+C to stop.\n\n")

			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

			runTick()
			for {
				select {
				case <-ticker.C:
					runTick()
				case <-sig:
					fmt.Printf("\n  Stopped.\n\n")
					return nil
				}
			}
		},
	}

	cmd.Flags().DurationVarP(&interval, "interval", "i", 1*time.Hour, "snapshot interval (e.g. 30m, 1h, 6h)")
	cmd.Flags().IntVar(&keep, "keep", 24, "Retention: keep only the N newest snapshots per database (0 = keep all)")
	cmd.Flags().StringVar(&label, "label", "scheduled", "Label recorded in each snapshot name")
	cmd.Flags().StringVar(&fleetPath, "fleet", "", "Snapshot every local database in a fleet config")
	cmd.Flags().StringVar(&tag, "tag", "", "With --fleet, only databases matching this tag")
	cmd.Flags().BoolVar(&once, "once", false, "Take a single snapshot and exit (for cron/systemd)")
	return cmd
}

// scheduleTargets resolves the local database paths to snapshot, from either a
// single path argument or a fleet config (remote DSNs are skipped — snapshots
// are local-file VACUUM INTO copies).
func scheduleTargets(fleetPath, tag string, args []string) ([]string, error) {
	if fleetPath == "" {
		return []string{args[0]}, nil
	}
	cfg, err := fleet.Load(fleetPath)
	if err != nil {
		return nil, err
	}
	var targets []string
	for _, db := range cfg.Filter(tag) {
		if isRemoteDSN(db.DSN) {
			fmt.Printf("  %s  %s — skipped (snapshots are local SQLite only)\n", styleDim.Render("·"), db.Name)
			continue
		}
		targets = append(targets, db.DSN)
	}
	return targets, nil
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
