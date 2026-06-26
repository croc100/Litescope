package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/spf13/cobra"
)

func cmdRewind() *cobra.Command {
	var to string

	cmd := &cobra.Command{
		Use:   "rewind <d1-source>",
		Short: "D1 Time Travel: restore or explore restore points",
		Long: `Use Cloudflare D1 Time Travel to restore a database to any point within the
last 30 days without needing a local snapshot.

  export CLOUDFLARE_API_TOKEN=...
  export CLOUDFLARE_ACCOUNT_ID=...

  litescope rewind d1://DB_ID --to "2h ago"
  litescope rewind d1://DB_ID --to "yesterday"
  litescope rewind d1://DB_ID --to "2024-01-15T10:30:00Z"
  litescope rewind list d1://DB_ID

Accepted time formats: "30m ago", "2h ago", "3d ago", "yesterday", RFC 3339.

⚠  Restore is destructive — the database is overwritten with the historical
   snapshot. Use 'litescope rewind list' to see available restore points
   before committing.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			if !strings.HasPrefix(src, "d1://") {
				return fmt.Errorf("rewind only supports D1 databases (d1://DB_ID); got %q", src)
			}
			if to == "" {
				return fmt.Errorf("--to is required (e.g. --to \"2h ago\"); run 'litescope rewind list %s' to see options", src)
			}

			ts, err := parseRewindTime(to)
			if err != nil {
				return fmt.Errorf("--to %q: %w", to, err)
			}

			_, accountID, databaseID, err := connector.ParseD1DSN(src)
			if err != nil {
				return err
			}

			fmt.Printf("\n  Rewinding D1 database %s to %s …\n\n", databaseID, ts.UTC().Format(time.RFC3339))

			result, err := connector.D1TimeTravel(accountID, databaseID, ts)
			if err != nil {
				return fmt.Errorf("time travel failed: %w", err)
			}

			fmt.Printf("  %s  Restored to bookmark: %s\n", styleOK.Render("✓"), result.Bookmark)
			fmt.Printf("       Timestamp:           %s\n\n", result.Timestamp)
			return nil
		},
	}

	cmd.Flags().StringVar(&to, "to", "", `Point in time: "2h ago", "yesterday", "3d ago", or RFC 3339`)
	cmd.AddCommand(cmdRewindList())
	return cmd
}

func cmdRewindList() *cobra.Command {
	var migrationsDir string

	return &cobra.Command{
		Use:   "list <d1-source>",
		Short: "Show available Time Travel restore points for a D1 database",
		Long: `Display the Time Travel window and suggested restore points for a D1 database.

D1 Time Travel preserves 30 days of continuous history — you can restore to
any second within that window. This command shows:

  • The available window (now − 30 days)
  • Suggested restore points at common intervals
  • Migration file timestamps from your local migrations/ directory (if present)

Use the displayed timestamps with 'litescope rewind <source> --to <time>'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			if !strings.HasPrefix(src, "d1://") {
				return fmt.Errorf("rewind list only supports D1 databases (d1://DB_ID); got %q", src)
			}

			now := time.Now().UTC()
			oldest := now.Add(-30 * 24 * time.Hour)

			fmt.Printf("\n  D1 Time Travel — %s\n", src)
			fmt.Printf("  ─────────────────────────────────────────────────────────────\n")
			fmt.Printf("  Available window: %s  →  now\n\n", oldest.Format("2006-01-02 15:04 UTC"))

			// Suggested restore points.
			points := []struct {
				label string
				t     time.Time
			}{
				{"15 minutes ago", now.Add(-15 * time.Minute)},
				{"1 hour ago", now.Add(-time.Hour)},
				{"6 hours ago", now.Add(-6 * time.Hour)},
				{"1 day ago", now.Add(-24 * time.Hour)},
				{"3 days ago", now.Add(-3 * 24 * time.Hour)},
				{"7 days ago", now.Add(-7 * 24 * time.Hour)},
				{"14 days ago", now.Add(-14 * 24 * time.Hour)},
				{"30 days ago", now.Add(-30 * 24 * time.Hour)},
			}

			fmt.Printf("  Suggested restore points:\n")
			for _, p := range points {
				if p.t.Before(oldest) {
					break
				}
				fmt.Printf("    %-20s  litescope rewind %s --to %q\n",
					p.label, src, p.t.UTC().Format(time.RFC3339))
			}

			// Migration file timestamps.
			dir := migrationsDir
			if dir == "" {
				dir = "migrations"
			}
			migrations := findMigrationTimestamps(dir, oldest)
			if len(migrations) > 0 {
				fmt.Printf("\n  Local migration files (in %s/):\n", dir)
				for _, m := range migrations {
					fmt.Printf("    %-40s  %s\n", m.name,
						fmt.Sprintf("--to %q  (just before this migration)", m.t.Add(-time.Second).UTC().Format(time.RFC3339)))
				}
				fmt.Printf("\n  Tip: rewind to just BEFORE a migration timestamp to undo it.\n")
			}

			fmt.Printf("\n  %s  Restore: litescope rewind %s --to \"<timestamp>\"\n", styleDim.Render("·"), src)
			fmt.Printf("  %s  Bisect:  litescope bisect %s --good \"<known-good>\" --bad now --check \"<sql>\" --expect \"<value>\"\n\n",
				styleDim.Render("·"), src)
			return nil
		},
	}
}

// findMigrationTimestamps scans a migrations directory for versioned SQL files
// and returns them sorted newest-first, filtered to within the Time Travel window.
func findMigrationTimestamps(dir string, oldest time.Time) []struct {
	name string
	t    time.Time
} {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []struct {
		name string
		t    time.Time
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		modTime := info.ModTime().UTC()
		if modTime.Before(oldest) {
			continue
		}
		out = append(out, struct {
			name string
			t    time.Time
		}{filepath.Base(e.Name()), modTime})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t.After(out[j].t) })
	return out
}

// parseRewindTime converts human-readable strings to a time.Time.
func parseRewindTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	now := time.Now().UTC()

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}

	if s == "yesterday" {
		return now.Add(-24 * time.Hour), nil
	}
	if s == "now" {
		return now, nil
	}

	s2 := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), " ago"), "ago")
	s2 = strings.TrimSpace(s2)

	for _, unit := range []struct {
		suffix string
		factor time.Duration
	}{
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	} {
		if strings.HasSuffix(s2, unit.suffix) {
			numStr := strings.TrimSuffix(s2, unit.suffix)
			var n int
			if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil {
				break
			}
			return now.Add(-time.Duration(n) * unit.factor), nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized time format; use \"2h ago\", \"3d ago\", \"yesterday\", RFC 3339, or \"now\"")
}
