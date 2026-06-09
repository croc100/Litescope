package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/croc100/litescope/internal/diff"
)

var (
	added   = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	removed = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	changed = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	header  = lipgloss.NewStyle().Bold(true).Underline(true)
	dim     = lipgloss.NewStyle().Faint(true)
)

func Terminal(r *diff.Result) string {
	var b strings.Builder

	if len(r.Schema) > 0 {
		fmt.Fprintln(&b, header.Render("Schema diff"))
		for _, td := range r.Schema {
			writeTableDiff(&b, td)
		}
		fmt.Fprintln(&b)
	}

	if len(r.Data) > 0 {
		fmt.Fprintln(&b, header.Render("Data diff"))
		for _, dd := range r.Data {
			writeDataDiff(&b, dd)
		}
	}

	return b.String()
}

func writeTableDiff(b *strings.Builder, td diff.TableDiff) {
	switch {
	case td.Added:
		fmt.Fprintf(b, "  %s %-24s %s\n", added.Render("+"), td.Name, dim.Render(fmt.Sprintf("new table (%d columns)", len(td.AddedColumns))))
	case td.Removed:
		fmt.Fprintf(b, "  %s %-24s %s\n", removed.Render("-"), td.Name, dim.Render("table removed"))
	default:
		fmt.Fprintf(b, "  %s %-24s\n", changed.Render("~"), td.Name)
		for _, c := range td.AddedColumns {
			fmt.Fprintf(b, "      %s column added: %s (%s)\n", added.Render("+"), c.Name, c.Type)
		}
		for _, c := range td.RemovedColumns {
			fmt.Fprintf(b, "      %s column removed: %s\n", removed.Render("-"), c.Name)
		}
		for _, c := range td.ChangedColumns {
			fmt.Fprintf(b, "      %s column changed: %s (%s → %s)\n", changed.Render("~"), c.Name, c.Old.Type, c.New.Type)
		}
		for _, idx := range td.AddedIndexes {
			u := ""
			if idx.Unique {
				u = " UNIQUE"
			}
			fmt.Fprintf(b, "      %s index added: %s%s\n", added.Render("+"), idx.Name, u)
		}
		for _, idx := range td.RemovedIndexes {
			fmt.Fprintf(b, "      %s index removed: %s\n", removed.Render("-"), idx.Name)
		}
	}
}

func writeDataDiff(b *strings.Builder, dd diff.DataDiff) {
	var parts []string
	if dd.Added > 0 {
		parts = append(parts, added.Render(fmt.Sprintf("+%d rows", dd.Added)))
	}
	if dd.Removed > 0 {
		parts = append(parts, removed.Render(fmt.Sprintf("-%d rows", dd.Removed)))
	}
	if dd.Changed > 0 {
		parts = append(parts, changed.Render(fmt.Sprintf("~%d rows", dd.Changed)))
	}
	fmt.Fprintf(b, "  %-26s %s\n", dd.Table, strings.Join(parts, "  "))
}
