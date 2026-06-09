package cli

import (
	"fmt"

	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
)

func cmdSchema() *cobra.Command {
	return &cobra.Command{
		Use:   "schema <file.db>",
		Short: "Dump schema of a SQLite database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := schema.Load(args[0])
			if err != nil {
				return err
			}
			fmt.Print(s.String())
			return nil
		},
	}
}
