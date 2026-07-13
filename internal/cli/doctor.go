package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/lint"
	"github.com/croc100/litescope/internal/report"
	"github.com/spf13/cobra"
)

// doctorReport is the combined, single-command checkup: operational health
// (corruption, WAL bloat, fragmentation) plus performance advice (missing
// indexes, redundant indexes). One command, one verdict — the "point it at a
// database and go oh" experience.
type doctorReport struct {
	Path     string          `json:"path"`
	Verdict  string          `json:"verdict"` // healthy | attention | critical
	Health   *health.Report  `json:"health"`
	Advisor  *advisor.Report `json:"advisor"`
	Lint     *lint.Report    `json:"lint"`
	Warnings int             `json:"warnings"` // advisor + lint warnings
}

func cmdDoctor() *cobra.Command {
	var format string
	var deep bool
	var out string

	cmd := &cobra.Command{
		Use:   "doctor <database.db>",
		Short: "One-shot checkup: integrity, health, and performance advice (free)",
		Long: `Run every single-database check at once and get a single verdict.

doctor combines:
  health    corruption (PRAGMA quick_check / --deep), WAL bloat, fragmentation
  advise    missing FK indexes, redundant indexes, full-table-scan queries
  lint      schema design anti-patterns (no PK, untyped columns, not STRICT, ...)

  litescope doctor app.db
  litescope doctor app.db --deep
  litescope doctor app.db --format json
  litescope doctor app.db --format html -o report.html   # shareable report

Exit code is 1 when the database needs attention (a health warning/critical
state or any performance warning) — safe to drop into CI as a quality gate.

Triage an entire Turso/D1 fleet at once with: litescope fleet health`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			h := health.Inspect(path, deep)
			a, err := advisor.Analyze(path, nil)
			if err != nil {
				return err
			}
			l, err := lint.Analyze(path)
			if err != nil {
				return err
			}

			warnings := 0
			for _, f := range a.Findings {
				if f.Severity == "warning" {
					warnings++
				}
			}
			for _, f := range l.Findings {
				if f.Severity == lint.SevWarning {
					warnings++
				}
			}

			verdict := "healthy"
			switch {
			case h.Severity == health.SevCritical:
				verdict = "critical"
			case h.Severity == health.SevWarning || warnings > 0:
				verdict = "attention"
			}

			rep := doctorReport{Path: path, Verdict: verdict, Health: h, Advisor: a, Lint: l, Warnings: warnings}

			switch format {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			case "html":
				w := cmd.OutOrStdout()
				if out != "" {
					f, err := os.Create(out)
					if err != nil {
						return err
					}
					defer f.Close()
					w = f
				}
				if err := report.Doctor(w, report.DoctorData{
					Path: rep.Path, Verdict: rep.Verdict, Health: rep.Health,
					Advisor: rep.Advisor, Lint: rep.Lint, Warnings: rep.Warnings,
				}); err != nil {
					return err
				}
				if out != "" {
					fmt.Fprintf(os.Stderr, "wrote %s\n", out)
				}
			case "terminal":
				printDoctor(rep)
			default:
				return fmt.Errorf("unknown format %q (want terminal, json, or html)", format)
			}

			if verdict != "healthy" {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "terminal", "output format: terminal, json, html")
	cmd.Flags().BoolVar(&deep, "deep", false, "use exhaustive integrity_check instead of quick_check")
	cmd.Flags().StringVarP(&out, "out", "o", "", "write report to a file instead of stdout (html)")
	return cmd
}

func printDoctor(rep doctorReport) {
	var mark, label string
	switch rep.Verdict {
	case "critical":
		mark, label = styleErr.Render("✗"), styleErr.Render("CRITICAL")
	case "attention":
		mark, label = styleWarn.Render("⚠"), styleWarn.Render("NEEDS ATTENTION")
	default:
		mark, label = styleOK.Render("●"), styleOK.Render("HEALTHY")
	}
	fmt.Printf("\n  %s  %s  %s\n\n", mark, label, styleDim.Render(rep.Path))

	// Health section — body only (the verdict above already covers status).
	printDoctorHealth(rep.Health)

	// Advisor section.
	if len(rep.Advisor.Findings) == 0 {
		fmt.Printf("  %s  No index or query problems found.\n\n", styleOK.Render("●"))
	} else {
		fmt.Printf("  Advisor · %d finding(s)\n\n", len(rep.Advisor.Findings))
		for _, f := range rep.Advisor.Findings {
			m := styleWarn.Render("⚠")
			if f.Severity == "info" {
				m = styleDim.Render("·")
			}
			loc := ""
			if f.Table != "" {
				loc = " " + styleDim.Render(f.Table)
			}
			fmt.Printf("  %s  %s%s\n", m, f.Detail, loc)
			if f.Suggestion != "" {
				fmt.Printf("       %s\n", styleOK.Render(f.Suggestion))
			}
		}
		fmt.Println()
	}

	// Lint section.
	if len(rep.Lint.Findings) == 0 {
		fmt.Printf("  %s  No schema design problems found.\n\n", styleOK.Render("●"))
	} else {
		fmt.Printf("  Lint · %d finding(s)\n\n", len(rep.Lint.Findings))
		for _, f := range rep.Lint.Findings {
			m := styleWarn.Render("⚠")
			if f.Severity == lint.SevInfo {
				m = styleDim.Render("·")
			}
			loc := ""
			if f.Table != "" {
				loc = " " + styleDim.Render(f.Table)
			}
			fmt.Printf("  %s  %s%s  %s\n", m, f.Detail, loc, styleDim.Render("["+f.Rule+"]"))
			if f.Suggestion != "" {
				fmt.Printf("       %s\n", styleOK.Render(f.Suggestion))
			}
		}
		fmt.Println()
	}
}

// printDoctorHealth renders the health metrics without the standalone health
// header — doctor's top verdict line already states overall status.
func printDoctorHealth(r *health.Report) {
	hLabel := styleOK.Render("ok")
	switch r.Severity {
	case health.SevCritical:
		hLabel = styleErr.Render("critical")
	case health.SevWarning:
		hLabel = styleWarn.Render("warning")
	}
	fmt.Printf("  Health · %s\n\n", hLabel)

	if !r.Reachable {
		fmt.Printf("  %s  unreachable: %s\n\n", styleErr.Render("✗"), r.Error)
		return
	}

	row := func(k, v string) { fmt.Printf("  %s  %s\n", styleDim.Render(fmt.Sprintf("%-14s", k)), v) }
	integ := styleOK.Render("ok")
	if !r.IntegrityOK {
		integ = styleErr.Render("FAILED")
	}
	row("integrity", integ)
	row("size", humanBytes(r.SizeBytes))
	if r.JournalMode != "" {
		row("journal mode", strings.ToLower(r.JournalMode))
	}
	walStr := humanBytes(r.WALBytes)
	if r.WALBytes >= health.WALBloatBytes {
		walStr = styleWarn.Render(walStr + "  (bloated)")
	}
	row("wal", walStr)
	fragStr := fmt.Sprintf("%.1f%%", r.FragmentationPct())
	if r.FragmentationPct() >= health.FragmentationRatio*100 {
		fragStr = styleWarn.Render(fragStr + "  (VACUUM candidate)")
	}
	row("fragmentation", fragStr)

	if len(r.Issues) > 0 {
		fmt.Println()
		for _, iss := range r.Issues {
			fmt.Printf("  %s  %s\n", styleWarn.Render("→"), iss)
		}
	}
	fmt.Println()
}
