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
	var erd bool

	cmd := &cobra.Command{
		Use:   "schema <source>",
		Short: "Dump schema of a SQLite database",
		Long: `Dump the schema of a SQLite database.

Sources:
  litescope schema file.db
  litescope schema turso://TOKEN@ORG/DBNAME

Formats:
  -f terminal   human-readable (default)
  -f json       machine-readable
  -f mermaid    Mermaid erDiagram (paste into a README)
  --erd         shorthand for -f mermaid`,
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

			if erd {
				format = "mermaid"
			}
			switch format {
			case "json":
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			case "mermaid":
				fmt.Print(s.Mermaid())
				return nil
			case "terminal":
				fmt.Print(s.String())
				return nil
			default:
				return fmt.Errorf("unknown format %q (want terminal, json, or mermaid)", format)
			}
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json | mermaid")
	cmd.Flags().BoolVar(&erd, "erd", false, "output a Mermaid erDiagram (shorthand for -f mermaid)")
	return cmd
}
