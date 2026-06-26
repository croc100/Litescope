package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/locks"
	"github.com/spf13/cobra"
)

func cmdLocks() *cobra.Command {
	var format string
	var live, watch bool
	var interval time.Duration

	cmd := &cobra.Command{
		Use:   "locks <database>",
		Short: "Diagnose \"database is locked\" / SQLITE_BUSY problems",
		Long: `Diagnose the SQLite locking faults behind "database is locked" and writer
starvation — the single most common SQLite production failure:

  journal mode      DELETE/TRUNCATE block all readers during a write; WAL doesn't
  busy_timeout      defaults to 0 — any contention fails immediately as SQLITE_BUSY
  locking_mode      EXCLUSIVE locks out every other process
  WAL bloat         a stalled checkpoint starves readers

Each finding ships with the exact PRAGMA or DSN change to apply.

  litescope locks app.db              # static PRAGMA diagnosis
  litescope locks app.db --format json
  litescope locks d1://DB_ID          # provider-specific D1 guidance
  litescope locks turso://TOKEN@ORG/DB

Live detection (local files only) shows whether a writer is holding the lock
*right now* and which processes have the file open:

  litescope locks app.db --live       # one-shot live probe
  litescope locks app.db --watch      # stream lock state changes

Exit code is 1 when the verdict is attention or critical (static mode), or
when a writer currently holds the lock (--live).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]

			if watch {
				return runLocksWatch(src, interval, format)
			}
			if live {
				return runLocksLive(src, format)
			}

			r, err := locks.Diagnose(src)
			if err != nil {
				return err
			}

			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(r); err != nil {
					return err
				}
			} else {
				printLocks(r)
			}

			if r.Verdict != locks.SeverityOK {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json")
	cmd.Flags().BoolVar(&live, "live", false, "probe live lock state right now (local files only)")
	cmd.Flags().BoolVar(&watch, "watch", false, "stream live lock state changes (local files only)")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "poll interval for --watch")
	return cmd
}

const liveProbeWait = 250 * time.Millisecond

func runLocksLive(src, format string) error {
	p, err := locks.Probe(src, liveProbeWait)
	if err != nil {
		return err
	}
	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(p); err != nil {
			return err
		}
	} else {
		printLiveProbe(p)
	}
	if p.State == locks.StateLocked {
		os.Exit(1)
	}
	return nil
}

func runLocksWatch(src string, interval time.Duration, format string) error {
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() { <-sig; close(stop) }()

	fmt.Printf("  %s watching %s — press Ctrl-C to stop\n",
		styleDim.Render("●"), styleDim.Render(src))

	return locks.Watch(src, interval, liveProbeWait, stop, func(p *locks.LiveProbe) {
		if format == "json" {
			b, _ := json.Marshal(p)
			fmt.Println(string(b))
			return
		}
		ts := p.Time.Format("15:04:05")
		fmt.Printf("  %s  %s  %s\n", styleDim.Render(ts), liveStateLabel(p.State), styleDim.Render(p.Detail))
		for _, h := range p.Holders {
			fmt.Printf("        %s %s pid=%d (%s)\n", styleDim.Render("holder:"), h.Command, h.PID, h.Access)
		}
	})
}

func liveStateLabel(s locks.LockState) string {
	switch s {
	case locks.StateLocked:
		return styleErr.Render("LOCKED")
	case locks.StateFree:
		return styleOK.Render("FREE")
	case locks.StateReadable:
		return styleWarn.Render("READABLE")
	default:
		return styleWarn.Render("ERROR")
	}
}

func printLiveProbe(p *locks.LiveProbe) {
	fmt.Printf("\n  %s  %s  %s\n", liveStateLabel(p.State), styleDim.Render(p.Source),
		styleDim.Render(fmt.Sprintf("(probed in %dms)", p.WaitMS)))
	fmt.Printf("\n  %s\n", p.Detail)
	if len(p.Holders) > 0 {
		fmt.Println()
		for _, h := range p.Holders {
			fmt.Printf("  %s  %s pid=%d (%s)\n", styleDim.Render("holder"), styleBold.Render(h.Command), h.PID, h.Access)
		}
	}
	if p.Hint != "" {
		fmt.Printf("\n  %s %s\n", styleOK.Render("→"), p.Hint)
	}
	fmt.Println()
}

func printLocks(r *locks.Report) {
	var mark, label string
	switch r.Verdict {
	case "critical":
		mark, label = styleErr.Render("✗"), styleErr.Render("CRITICAL")
	case "attention":
		mark, label = styleWarn.Render("⚠"), styleWarn.Render("ATTENTION")
	default:
		mark, label = styleOK.Render("●"), styleOK.Render("OK")
	}

	fmt.Printf("\n  %s  %s  %s  %s\n",
		mark, label, styleDim.Render(r.Source), styleDim.Render("("+r.Provider+")"))

	if len(r.Pragmas) > 0 {
		fmt.Println()
		for _, k := range []string{"journal_mode", "wal_autocheckpoint", "locking_mode", "synchronous"} {
			if v, ok := r.Pragmas[k]; ok && v != "" {
				fmt.Printf("  %s  %s\n", styleDim.Render(fmt.Sprintf("%-20s", k)), v)
			}
		}
	}

	fmt.Println()
	for _, f := range r.Findings {
		var icon string
		switch f.Severity {
		case locks.SeverityCritical:
			icon = styleErr.Render("✗")
		case locks.SeverityWarning:
			icon = styleWarn.Render("⚠")
		default:
			icon = styleOK.Render("●")
		}
		fmt.Printf("  %s  %s\n", icon, styleBold.Render(f.Summary))
		if f.Detail != "" {
			for _, line := range wrapLines(f.Detail, 72) {
				fmt.Printf("      %s\n", styleDim.Render(line))
			}
		}
		if f.Fix != "" {
			for _, line := range strings.Split(f.Fix, "\n") {
				fmt.Printf("      %s\n", styleOK.Render(line))
			}
		}
		fmt.Println()
	}
}

// wrapLines breaks text into lines no longer than width, on word boundaries.
func wrapLines(text string, width int) []string {
	words := strings.Fields(text)
	var lines []string
	var cur string
	for _, w := range words {
		if cur == "" {
			cur = w
		} else if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
