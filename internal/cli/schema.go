package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/schema"
	"github.com/spf13/cobra"
)

func cmdSchema() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "schema <source>",
		Short: "Dump schema of a SQLite database",
		Long: `Dump the schema of a SQLite database.

Sources:
  litescope schema file.db
  litescope schema turso://TOKEN@ORG/DBNAME`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var s *schema.Schema
			var err error

			if isLocalFile(args[0]) {
				s, err = schema.Load(args[0])
			} else {
				conn, e := connector.Open(args[0])
				if e != nil {
					return e
				}
				defer conn.Close()
				s, err = conn.Schema()
			}
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
