package cli

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "litescope",
		Short: "Human-readable diff for SQLite databases",
	}

	root.AddCommand(cmdDiff())
	root.AddCommand(cmdSchema())

	return root
}
