package cli

import (
	"fmt"
	"os"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/render"
	"github.com/spf13/cobra"
)

func cmdDiff() *cobra.Command {
	var htmlOut string
	var format string

	cmd := &cobra.Command{
		Use:   "diff <old.db> <new.db>",
		Short: "Compare two SQLite databases",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := diff.Compare(args[0], args[1])
			if err != nil {
				return err
			}

			switch format {
			case "json":
				return render.JSON(os.Stdout, result)
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
		},
	}

	cmd.Flags().StringVar(&htmlOut, "html", "", "write HTML report to file")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "output format: terminal | json | html")
	return cmd
}
