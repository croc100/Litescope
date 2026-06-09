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

	cmd := &cobra.Command{
		Use:   "diff <old.db> <new.db>",
		Short: "Compare two SQLite databases",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := diff.Compare(args[0], args[1])
			if err != nil {
				return err
			}

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
		},
	}

	cmd.Flags().StringVar(&htmlOut, "html", "", "write HTML report to file")
	return cmd
}
