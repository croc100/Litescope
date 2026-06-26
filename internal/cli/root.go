package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "litescope",
		Short: "Human-readable diff for SQLite databases",
	}

	root.AddCommand(cmdDiff())
	root.AddCommand(cmdSchema())
	root.AddCommand(cmdDump())
	root.AddCommand(cmdImport())
	root.AddCommand(cmdExport())
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
	root.AddCommand(cmdD1())
	root.AddCommand(cmdRewind())
	root.AddCommand(cmdBisect())
	root.AddCommand(cmdMCP())
	root.AddCommand(cmdLicense())
	root.AddCommand(cmdLog())
	root.AddCommand(cmdPolicy())
	root.AddCommand(cmdTeam())

	return root
}

// ResolveArgs implements zero-config onboarding: when the first argument is a
// plain file path (not a subcommand or flag), it routes by file type so
// `litescope data.db` just works.
//
//   - a SQLite database  -> `doctor <file>`  (instant health verdict)
//   - a .csv/.tsv/.json  -> `import <file>`  (turn it into a database)
//
// Anything else is left untouched for Cobra to handle (or error on).
func ResolveArgs(root *cobra.Command, args []string) []string {
	if len(args) == 0 {
		return args
	}
	first := args[0]
	if strings.HasPrefix(first, "-") || isKnownCommand(root, first) {
		return args
	}
	switch classifyFile(first) {
	case "db":
		return append([]string{"doctor"}, args...)
	case "data":
		return append([]string{"import"}, args...)
	default:
		return args
	}
}

func isKnownCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return true
		}
		for _, a := range c.Aliases {
			if a == name {
				return true
			}
		}
	}
	// built-in cobra commands
	return name == "help" || name == "completion"
}

// classifyFile returns "db", "data", or "" for a path. SQLite files are detected
// by their magic header (so extensionless databases still work); data files by
// extension.
func classifyFile(path string) string {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return ""
	}
	if isSQLiteFile(path) {
		return "db"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv", ".tab", ".json":
		return "data"
	}
	return ""
}

// isSQLiteFile reports whether path starts with the SQLite file magic string.
func isSQLiteFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hdr := make([]byte, 16)
	n, _ := f.Read(hdr)
	return n >= 16 && string(hdr[:15]) == "SQLite format 3"
}
