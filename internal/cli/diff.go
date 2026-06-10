package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/render"
	"github.com/spf13/cobra"
)

func cmdDiff() *cobra.Command {
	var htmlOut string
	var format string

	cmd := &cobra.Command{
		Use:   "diff <old> <new>",
		Short: "Compare two SQLite databases",
		Long: `Compare two SQLite databases by schema and data.

Sources can be local files or remote connections:
  litescope diff old.db new.db
  litescope diff turso://TOKEN@ORG/DB1 turso://TOKEN@ORG/DB2
  litescope diff old.db turso://TOKEN@ORG/DB          (mixed)`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldDSN, newDSN := args[0], args[1]

			// If both are local files, use the existing fast path (includes data diff)
			if isLocalFile(oldDSN) && isLocalFile(newDSN) {
				return runLocalDiff(oldDSN, newDSN, format, htmlOut)
			}

			// Remote path: schema-only diff via connectors
			return runConnectorDiff(oldDSN, newDSN, format, htmlOut)
		},
	}

	cmd.Flags().StringVar(&htmlOut, "html", "", "write HTML report to file")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json | markdown | html")
	return cmd
}

func isLocalFile(dsn string) bool {
	return !strings.Contains(dsn, "://")
}

func runLocalDiff(oldPath, newPath, format, htmlOut string) error {
	result, err := diff.Compare(oldPath, newPath)
	if err != nil {
		return err
	}
	return outputDiff(result, format, htmlOut)
}

func runConnectorDiff(oldDSN, newDSN, format, htmlOut string) error {
	oldConn, err := connector.Open(oldDSN)
	if err != nil {
		return fmt.Errorf("open %s: %w", oldDSN, err)
	}
	defer oldConn.Close()

	newConn, err := connector.Open(newDSN)
	if err != nil {
		return fmt.Errorf("open %s: %w", newDSN, err)
	}
	defer newConn.Close()

	oldSchema, err := oldConn.Schema()
	if err != nil {
		return fmt.Errorf("schema from %s: %w", oldDSN, err)
	}
	newSchema, err := newConn.Schema()
	if err != nil {
		return fmt.Errorf("schema from %s: %w", newDSN, err)
	}

	result := diff.CompareSchemas(oldSchema, newSchema)
	return outputDiff(result, format, htmlOut)
}

func outputDiff(result *diff.Result, format, htmlOut string) error {
	switch format {
	case "json":
		return render.JSON(os.Stdout, result)
	case "markdown", "md":
		return render.Markdown(os.Stdout, result)
	case "html":
		out := htmlOut
		if out == "" {
			out = "litescope-report.html"
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		return render.HTML(f, result)
	default:
		if htmlOut != "" {
			f, err := os.Create(htmlOut)
			if err != nil {
				return err
			}
			defer f.Close()
			return render.HTML(f, result)
		}
		fmt.Print(render.Terminal(result))
		return nil
	}
}
