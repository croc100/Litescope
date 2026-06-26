package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/spf13/cobra"
)

func cmdRewind() *cobra.Command {
	var to string

	cmd := &cobra.Command{
		Use:   "rewind <source>",
		Short: "Restore a D1 database to a past point in time (Time Travel)",
		Long: `Use Cloudflare D1 Time Travel to restore a database to any point within the
last 30 days without needing a local snapshot.

The source must be a D1 DSN. Credentials are read from environment variables:

  export CLOUDFLARE_API_TOKEN=...
  export CLOUDFLARE_ACCOUNT_ID=...
  litescope rewind d1://DB_ID --to "2h ago"
  litescope rewind d1://DB_ID --to "yesterday"
  litescope rewind d1://DB_ID --to "2024-01-15T10:30:00Z"

Relative durations accepted: "30m ago", "2h ago", "3d ago", "yesterday".
RFC 3339 timestamps are also accepted for precision restores.

⚠  This is a destructive operation — the database is overwritten with the
   historical snapshot. There is no undo. Cloudflare keeps a short safety
   window but you should capture a snapshot with 'litescope dump' first.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			if !strings.HasPrefix(src, "d1://") {
				return fmt.Errorf("rewind only supports D1 databases (d1://DB_ID); got %q", src)
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

	cmd.Flags().StringVar(&to, "to", "", `Point in time: "2h ago", "yesterday", "3d ago", or RFC 3339 (required)`)
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// parseRewindTime converts human-readable strings to a time.Time.
func parseRewindTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	now := time.Now().UTC()

	// RFC 3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t, nil
	}

	if s == "yesterday" {
		return now.Add(-24 * time.Hour), nil
	}

	// "<number><unit> ago"
	s = strings.TrimSuffix(s, " ago")
	s = strings.TrimSuffix(s, "ago")
	s = strings.TrimSpace(s)

	for _, unit := range []struct {
		suffix string
		factor time.Duration
	}{
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	} {
		if strings.HasSuffix(s, unit.suffix) {
			numStr := strings.TrimSuffix(s, unit.suffix)
			var n int
			if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil {
				break
			}
			return now.Add(-time.Duration(n) * unit.factor), nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognized time format; use \"2h ago\", \"3d ago\", \"yesterday\", or RFC 3339")
}
