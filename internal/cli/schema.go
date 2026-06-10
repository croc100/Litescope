package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
)

func cmdSchema() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "schema <file.db>",
		Short: "Dump schema of a SQLite database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := schema.Load(args[0])
			if err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
			fmt.Print(s.String())
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json")
	return cmd
}
