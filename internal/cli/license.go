package cli

import (
	"fmt"

	"github.com/croc100/litescope/internal/license"
	"github.com/spf13/cobra"
)

func cmdLicense() *cobra.Command {
	return &cobra.Command{
		Use:   "license",
		Short: "Show the active license tier (Free or Pro)",
		Long: `Report the license tier Litescope detects for the current machine.

Litescope reads your key from, in order:
  1. the LITESCOPE_LICENSE environment variable
  2. the file ~/.litescope/license
  3. the GUI Settings → License panel (desktop app)

The key is verified against the license server, with an offline grace period.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch license.Current() {
			case license.TierPro:
				fmt.Printf("\n  %s  Pro — all features unlocked\n\n", styleOK.Render("●"))
			default:
				fmt.Printf("\n  %s  Free\n", styleDim.Render("○"))
				fmt.Printf("  %s  Set LITESCOPE_LICENSE or ~/.litescope/license to activate Pro.\n", styleDim.Render(" "))
				fmt.Printf("  %s  Get a key: https://litescope-site.pages.dev/#pricing\n\n", styleDim.Render(" "))
			}
			return nil
		},
	}
}
