package cli

import (
	"fmt"

	"github.com/croc100/litescope/internal/audit"
	"github.com/croc100/litescope/internal/team"
	"github.com/spf13/cobra"
)

func cmdTeam() *cobra.Command {
	return &cobra.Command{
		Use:   "team",
		Short: "Show team members and the current operator's role",
		Long: `Print the team roster and what the current operator is allowed to do.

Person-scoped, local-first access control: a committed team file names members
and roles (admin / editor / viewer). Viewers are blocked from operations that
change a database; admins and editors may proceed. The current operator is
$LITESCOPE_OPERATOR (or your OS username).

Loaded from, in order:
  $LITESCOPE_TEAM, ./litescope.team.yaml, ~/.litescope/team.yaml

Example litescope.team.yaml:

  team: Acme
  strict: false        # when true, operators not listed are blocked
  members:
    - name: alice
      role: admin
    - name: bob
      role: viewer`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := team.Load()
			if err != nil {
				return err
			}
			op := audit.Operator()
			if c.Empty() {
				fmt.Printf("\n  %s  No team file — every operator may write. (operator: %s)\n\n",
					styleDim.Render("○"), op)
				return nil
			}
			fmt.Printf("\n  Team %s · %s\n\n", c.Team, styleDim.Render(c.Source()))
			for _, m := range c.Members {
				mark := " "
				if m.Name == op {
					mark = styleOK.Render("›")
				}
				fmt.Printf("  %s %s  %s\n", mark, padRight(m.Name, 16), styleDim.Render(m.Role))
			}
			ok, reason := c.CanWrite(op)
			fmt.Println()
			if ok {
				fmt.Printf("  %s  operator %q may write\n\n", styleOK.Render("✓"), op)
			} else {
				fmt.Printf("  %s  %s\n\n", styleErr.Render("✗"), reason)
			}
			return nil
		},
	}
}
