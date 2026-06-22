package cli

import "github.com/spf13/cobra"

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "litescope",
		Short: "Human-readable diff for SQLite databases",
	}

	root.AddCommand(cmdDiff())
	root.AddCommand(cmdSchema())
	root.AddCommand(cmdDump())
	root.AddCommand(cmdValidate())
	root.AddCommand(cmdCheck())
	root.AddCommand(cmdHealth())
	root.AddCommand(cmdAdvise())
	root.AddCommand(cmdDoctor())
	root.AddCommand(cmdLint())
	root.AddCommand(cmdMigrate())
	root.AddCommand(cmdMonitor())
	root.AddCommand(cmdFleet())
	root.AddCommand(cmdServe())
	root.AddCommand(cmdPush())
	root.AddCommand(cmdMetrics())
	root.AddCommand(cmdMCP())
	root.AddCommand(cmdLicense())
	root.AddCommand(cmdLog())
	root.AddCommand(cmdPolicy())
	root.AddCommand(cmdTeam())

	return root
}
