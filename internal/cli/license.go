package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func cmdLicense() *cobra.Command {
	return &cobra.Command{
		Use:   "license",
		Short: "Show licensing information",
		Long: `Litescope is free and open source under the GNU AGPL-3.0.

The entire CLI — every command, fleet operation, migration, and automation — is
unlocked for everyone. There is no key to set and nothing to activate.

A separate commercial license (waiving the AGPL obligations, with support) is
available for organizations that need it: croc100100@gmail.com`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("\n  %s  Litescope — free and open source (AGPL-3.0)\n", styleOK.Render("●"))
			fmt.Printf("  %s  Every feature is unlocked. No license key required.\n", styleDim.Render(" "))
			fmt.Printf("  %s  Enterprise self-host / support: croc100100@gmail.com\n\n", styleDim.Render(" "))
			return nil
		},
	}
}
