package cli

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/spf13/cobra"
)

func cmdBisect() *cobra.Command {
	var (
		good, bad string
		checkSQL  string
		expect    string
		minWindow time.Duration
	)

	cmd := &cobra.Command{
		Use:   "bisect <d1-source>",
		Short: "Binary-search D1 Time Travel to find the commit that broke something",
		Long: `Binary-search across Cloudflare D1 Time Travel snapshots to pinpoint the
exact moment a condition changed — e.g. the migration that nuked a column.

How it works:
  1. Restore the database to the --good timestamp; verify the condition passes.
  2. Restore to the --bad timestamp; verify the condition fails.
  3. Binary-search the interval, restoring to midpoints until the window is
     smaller than --min-window (default 1 minute).
  4. Report the exact boundary timestamp.
  5. Leave the database at the --bad state so you can inspect it, then rewind
     manually if needed.

⚠  Each bisect step calls the D1 Time Travel restore API — the database IS
   overwritten at each step. This is expected. When done, restore with:
     litescope rewind <source> --to "now"   (or the desired state)

Examples:
  export CLOUDFLARE_API_TOKEN=...
  export CLOUDFLARE_ACCOUNT_ID=...

  # Find when the users table lost its email column
  litescope bisect d1://DB_ID \
    --good "3d ago" --bad "now" \
    --check "SELECT email FROM users LIMIT 1" \
    --expect "any"

  # Find when a specific row count dropped
  litescope bisect d1://DB_ID \
    --good "7d ago" --bad "now" \
    --check "SELECT COUNT(*) FROM orders" \
    --expect "gt:100"

--expect values:
  "any"       — check passes if query returns at least one row (no error)
  "none"      — check passes if query returns zero rows
  "gt:N"      — check passes if first result > N
  "lt:N"      — check passes if first result < N
  "eq:VALUE"  — check passes if first result equals VALUE (string match)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			if !strings.HasPrefix(src, "d1://") {
				return fmt.Errorf("bisect only supports D1 databases (d1://DB_ID); got %q", src)
			}
			if checkSQL == "" {
				return fmt.Errorf("--check is required (SQL to evaluate at each step)")
			}
			if expect == "" {
				return fmt.Errorf("--expect is required (e.g. \"any\", \"none\", \"gt:0\", \"eq:42\")")
			}

			_, accountID, databaseID, err := connector.ParseD1DSN(src)
			if err != nil {
				return err
			}

			goodTime, err := parseRewindTime(good)
			if err != nil {
				return fmt.Errorf("--good %q: %w", good, err)
			}
			badTime, err := parseRewindTime(bad)
			if err != nil {
				return fmt.Errorf("--bad %q: %w", bad, err)
			}

			if !goodTime.Before(badTime) {
				return fmt.Errorf("--good must be earlier than --bad")
			}

			checker, err := parseExpect(expect)
			if err != nil {
				return fmt.Errorf("--expect %q: %w", expect, err)
			}

			fmt.Printf("\n  Bisecting D1 database %s\n", databaseID)
			fmt.Printf("  Good: %s\n", goodTime.Format(time.RFC3339))
			fmt.Printf("  Bad:  %s\n", badTime.Format(time.RFC3339))
			fmt.Printf("  Check: %s\n", checkSQL)
			fmt.Printf("  Expect: %s\n\n", expect)

			totalWindow := badTime.Sub(goodTime)
			steps := int(math.Ceil(math.Log2(float64(totalWindow) / float64(minWindow))))
			fmt.Printf("  Estimated steps: ~%d\n\n", steps)

			// Verify the boundaries first.
			fmt.Printf("  Step 0/%d  Verifying --good state (%s) …\n", steps, good)
			if err := restoreAndCheck(accountID, databaseID, src, goodTime, checkSQL, checker); err != nil {
				return fmt.Errorf("--good state check failed: %w\n  The --good timestamp may not actually be good", err)
			}
			fmt.Printf("  %s  Good state confirmed.\n\n", styleOK.Render("✓"))

			fmt.Printf("  Step 0/%d  Verifying --bad state (%s) …\n", steps, bad)
			if err := restoreAndCheckFails(accountID, databaseID, src, badTime, checkSQL, checker); err != nil {
				return fmt.Errorf("--bad state check unexpectedly passed: %w\n  The --bad timestamp may not actually be bad", err)
			}
			fmt.Printf("  %s  Bad state confirmed.\n\n", styleOK.Render("✓"))

			// Binary search.
			lo, hi := goodTime, badTime
			step := 0
			for hi.Sub(lo) > minWindow {
				step++
				mid := lo.Add(hi.Sub(lo) / 2)
				fmt.Printf("  Step %d/%d  Testing %s …", step, steps, mid.Format(time.RFC3339))

				conn, err := connector.Open(src)
				if err != nil {
					return err
				}
				// Restore to mid.
				result, err := connector.D1TimeTravel(accountID, databaseID, mid)
				conn.Close()
				if err != nil {
					fmt.Printf(" restore failed (%v), narrowing range\n", err)
					// Can't restore here; try a slightly different point.
					hi = mid
					continue
				}
				_ = result

				// Check condition.
				good, cerr := checkCondition(src, checkSQL, checker)
				if cerr != nil {
					fmt.Printf(" check error (%v)\n", cerr)
					hi = mid
					continue
				}

				if good {
					fmt.Printf(" GOOD\n")
					lo = mid
				} else {
					fmt.Printf(" BAD\n")
					hi = mid
				}
			}

			// Restore to bad state so the user can inspect.
			fmt.Printf("\n  Restoring to --bad state for inspection …\n")
			if _, err := connector.D1TimeTravel(accountID, databaseID, badTime); err != nil {
				fmt.Printf("  %s  Could not restore to bad state: %v\n", styleWarn.Render("!"), err)
			}

			fmt.Printf("\n  %s  Breaking change occurred between:\n", styleOK.Render("✓"))
			fmt.Printf("       GOOD: %s\n", lo.Format(time.RFC3339))
			fmt.Printf("       BAD:  %s\n", hi.Format(time.RFC3339))
			fmt.Printf("       Window: %v\n\n", hi.Sub(lo).Round(time.Second))
			fmt.Printf("  To restore to just before the break:\n")
			fmt.Printf("    litescope rewind %s --to %q\n\n", src, lo.UTC().Format(time.RFC3339))
			return nil
		},
	}

	cmd.Flags().StringVar(&good, "good", "", `Timestamp known to be good: "3d ago", "yesterday", RFC 3339 (required)`)
	cmd.Flags().StringVar(&bad, "bad", "now", `Timestamp known to be bad (default: "now")`)
	cmd.Flags().StringVar(&checkSQL, "check", "", "SQL query to evaluate at each step (required)")
	cmd.Flags().StringVar(&expect, "expect", "", `Expected result: "any", "none", "gt:N", "lt:N", "eq:VALUE" (required)`)
	cmd.Flags().DurationVar(&minWindow, "min-window", time.Minute, "Stop bisecting when window is smaller than this")
	_ = cmd.MarkFlagRequired("good")
	_ = cmd.MarkFlagRequired("check")
	_ = cmd.MarkFlagRequired("expect")
	return cmd
}

// restoreAndCheck restores to ts and verifies the condition passes.
func restoreAndCheck(accountID, databaseID, src string, ts time.Time, sql string, checker func(string) bool) error {
	if _, err := connector.D1TimeTravel(accountID, databaseID, ts); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	ok, err := checkCondition(src, sql, checker)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("condition evaluated to false/fail at this timestamp")
	}
	return nil
}

// restoreAndCheckFails restores to ts and verifies the condition FAILS.
func restoreAndCheckFails(accountID, databaseID, src string, ts time.Time, sql string, checker func(string) bool) error {
	if _, err := connector.D1TimeTravel(accountID, databaseID, ts); err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	ok, err := checkCondition(src, sql, checker)
	if err != nil {
		// A query error counts as "bad" (e.g. column doesn't exist).
		return nil
	}
	if ok {
		return fmt.Errorf("condition passed (expected it to fail at --bad timestamp)")
	}
	return nil
}

// checkCondition runs the SQL against the D1 source and evaluates the checker.
func checkCondition(src, sql string, checker func(string) bool) (bool, error) {
	conn, err := connector.Open(src)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	rows, err := connector.Query(conn, sql)
	if err != nil {
		return false, err
	}

	if len(rows) == 0 {
		return checker(""), nil
	}

	// Grab first value of first row.
	var firstVal string
	for _, v := range rows[0] {
		if v == nil {
			firstVal = ""
		} else {
			firstVal = fmt.Sprintf("%v", v)
		}
		break
	}
	if len(rows) > 1 {
		firstVal = fmt.Sprintf("%d", len(rows)) // for "any" / count comparisons
	}
	return checker(firstVal), nil
}

// parseExpect returns a checker function from an --expect string.
func parseExpect(s string) (func(string) bool, error) {
	switch {
	case s == "any":
		return func(v string) bool { return v != "" }, nil
	case s == "none":
		return func(v string) bool { return v == "" }, nil
	case strings.HasPrefix(s, "eq:"):
		want := strings.TrimPrefix(s, "eq:")
		return func(v string) bool { return v == want }, nil
	case strings.HasPrefix(s, "gt:"):
		var n float64
		if _, err := fmt.Sscanf(strings.TrimPrefix(s, "gt:"), "%f", &n); err != nil {
			return nil, fmt.Errorf("gt: requires a number, got %q", s)
		}
		return func(v string) bool {
			var got float64
			fmt.Sscanf(v, "%f", &got)
			return got > n
		}, nil
	case strings.HasPrefix(s, "lt:"):
		var n float64
		if _, err := fmt.Sscanf(strings.TrimPrefix(s, "lt:"), "%f", &n); err != nil {
			return nil, fmt.Errorf("lt: requires a number, got %q", s)
		}
		return func(v string) bool {
			var got float64
			fmt.Sscanf(v, "%f", &got)
			return got < n
		}, nil
	default:
		return nil, fmt.Errorf("unknown format %q; use \"any\", \"none\", \"gt:N\", \"lt:N\", or \"eq:VALUE\"", s)
	}
}
