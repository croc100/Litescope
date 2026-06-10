package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/validate"
	"github.com/spf13/cobra"
)

func cmdValidate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <before.db> <after.db>",
		Short: "Validate that a migration produced only expected changes",
	}
	cmd.AddCommand(cmdValidateRun())
	cmd.AddCommand(cmdValidateInit())
	return cmd
}

// litescope validate <before.db> <after.db> --expect migration.yaml
func cmdValidateRun() *cobra.Command {
	var expectFile string
	var format string
	var strict bool

	cmd := &cobra.Command{
		Use:   "run <before.db> <after.db>",
		Short: "Compare actual migration changes against expected spec",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := diff.Compare(args[0], args[1])
			if err != nil {
				return err
			}

			spec, err := validate.LoadSpec(expectFile)
			if err != nil {
				return err
			}

			result := validate.Validate(d, spec)

			switch format {
			case "json":
				return printValidateJSON(result)
			default:
				printValidateTerminal(result, spec.Description)
			}

			if !result.Passed {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&expectFile, "expect", "e", "", "path to expectation YAML/JSON file (required)")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json")
	cmd.Flags().BoolVar(&strict, "strict", true, "fail on any unexpected change")
	_ = cmd.MarkFlagRequired("expect")
	return cmd
}

// litescope validate init <before.db> <after.db> --output migration.yaml
func cmdValidateInit() *cobra.Command {
	var output string
	var description string

	cmd := &cobra.Command{
		Use:   "init <before.db> <after.db>",
		Short: "Generate an expectation file from the actual diff (run once after manual migration)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := diff.Compare(args[0], args[1])
			if err != nil {
				return err
			}

			spec := validate.SpecFromDiff(d, description)

			if err := validate.WriteSpec(output, spec); err != nil {
				return err
			}

			fmt.Printf("Spec written to %s\n", output)
			fmt.Println("Review the file and commit it. Use `litescope validate run` in CI.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "migration.yaml", "output spec file path")
	cmd.Flags().StringVar(&description, "description", "", "human-readable description of this migration")
	return cmd
}

// ── Terminal renderer ─────────────────────────────────────────────────────────

var (
	styleOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ec9b0"))
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#f44747"))
	styleWarn     = lipgloss.NewStyle().Foreground(lipgloss.Color("#dcdcaa"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("#858585"))
	styleBold     = lipgloss.NewStyle().Bold(true)
)

func printValidateTerminal(r *validate.Result, description string) {
	if description != "" {
		fmt.Println(styleDim.Render("  " + description))
		fmt.Println()
	}

	for _, c := range r.Confirmed {
		fmt.Printf("  %s  %s\n",
			styleOK.Render("✓"),
			c.String())
	}
	for _, c := range r.Missing {
		fmt.Printf("  %s  %s  %s\n",
			styleErr.Render("✗"),
			c.String(),
			styleWarn.Render("(expected but not found)"))
	}
	for _, c := range r.Unexpected {
		fmt.Printf("  %s  %s  %s\n",
			styleErr.Render("✗"),
			c.String(),
			styleErr.Render("(UNEXPECTED)"))
	}

	fmt.Println()

	if r.Passed {
		fmt.Println(styleBold.Render(styleOK.Render(fmt.Sprintf(
			"Validation passed — %d/%d expected changes confirmed, 0 unexpected",
			len(r.Confirmed), len(r.Confirmed),
		))))
	} else {
		parts := []string{}
		if len(r.Unexpected) > 0 {
			parts = append(parts, fmt.Sprintf("%d unexpected", len(r.Unexpected)))
		}
		if len(r.Missing) > 0 {
			parts = append(parts, fmt.Sprintf("%d missing", len(r.Missing)))
		}
		fmt.Println(styleBold.Render(styleErr.Render(
			"Validation failed — " + strings.Join(parts, ", "),
		)))
	}
}

// ── JSON renderer ─────────────────────────────────────────────────────────────

func printValidateJSON(r *validate.Result) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
