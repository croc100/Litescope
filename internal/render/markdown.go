package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/croc100/litescope/internal/diff"
)

func Markdown(w io.Writer, r *diff.Result) error {
	if len(r.Schema) == 0 && len(r.Data) == 0 {
		_, err := fmt.Fprintln(w, "_No changes detected._")
		return err
	}

	var b strings.Builder

	// ── Schema changes ────────────────────────────────────────────────────────
	if len(r.Schema) > 0 {
		b.WriteString("#### Schema\n\n")
		b.WriteString("| Status | Table | Detail |\n")
		b.WriteString("|--------|-------|--------|\n")

		for _, td := range r.Schema {
			switch {
			case td.Added:
				b.WriteString(fmt.Sprintf("| ✅ added | `%s` | — |\n", td.Name))
			case td.Removed:
				b.WriteString(fmt.Sprintf("| ❌ removed | `%s` | — |\n", td.Name))
			default:
				details := []string{}
				for _, c := range td.AddedColumns {
					details = append(details, fmt.Sprintf("+ col `%s`", c.Name))
				}
				for _, c := range td.RemovedColumns {
					details = append(details, fmt.Sprintf("- col `%s`", c.Name))
				}
				for _, c := range td.ChangedColumns {
					details = append(details, fmt.Sprintf("~ col `%s` (%s→%s)", c.Name, c.Old.Type, c.New.Type))
				}
				for _, ix := range td.AddedIndexes {
					details = append(details, fmt.Sprintf("+ idx `%s`", ix.Name))
				}
				for _, ix := range td.RemovedIndexes {
					details = append(details, fmt.Sprintf("- idx `%s`", ix.Name))
				}
				b.WriteString(fmt.Sprintf("| ⚠️ modified | `%s` | %s |\n",
					td.Name, strings.Join(details, ", ")))
			}
		}
		b.WriteString("\n")
	}

	// ── Data changes ──────────────────────────────────────────────────────────
	if len(r.Data) > 0 {
		b.WriteString("#### Data\n\n")
		b.WriteString("| Table | Added rows | Removed rows |\n")
		b.WriteString("|-------|-----------|-------------|\n")
		for _, dd := range r.Data {
			b.WriteString(fmt.Sprintf("| `%s` | +%d | -%d |\n", dd.Table, dd.Added, dd.Removed))
		}
		b.WriteString("\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}
