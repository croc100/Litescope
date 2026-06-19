package cli

import (
	"fmt"

	"github.com/croc100/litescope/internal/policy"
	"github.com/croc100/litescope/internal/team"
	"github.com/spf13/cobra"
)

// guardWrite checks both governance layers before a mutating operation: the
// target-scoped policy and the operator-scoped team roles. Returns the blocking
// error, or nil when allowed.
func guardWrite(target string) error {
	pol, err := policy.Load()
	if err != nil {
		return err
	}
	if err := pol.Allow(target); err != nil {
		return err
	}
	return team.Allow()
}

func cmdPolicy() *cobra.Command {
	return &cobra.Command{
		Use:   "policy",
		Short: "Show the active write-protection policy",
		Long: `Print the guardrail policy Litescope enforces before any operation that
changes a database (migrate apply, fleet converge/recover, SQL/row writes).

The policy is local-first. It is loaded from, in order:
  $LITESCOPE_POLICY, ./litescope.policy.yaml, ~/.litescope/policy.yaml

Example litescope.policy.yaml:

  read_only: true            # block every write, everywhere
  protected:                 # ...or block writes to matching targets
    - prod
    - /data/critical

With no policy file, all operations are allowed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pol, err := policy.Load()
			if err != nil {
				return err
			}
			if pol.Empty() {
				fmt.Printf("\n  %s  No policy in effect — all operations allowed.\n\n", styleDim.Render("○"))
				return nil
			}
			fmt.Printf("\n  Policy · %s\n\n", styleDim.Render(pol.Source()))
			if pol.ReadOnly {
				fmt.Printf("  %s  read-only — every write is blocked\n", styleWarn.Render("!"))
			}
			if len(pol.Protected) > 0 {
				fmt.Printf("  %s  protected targets (writes blocked when matched):\n", styleWarn.Render("!"))
				for _, p := range pol.Protected {
					fmt.Printf("        %s\n", p)
				}
			}
			fmt.Println()
			return nil
		},
	}
}
