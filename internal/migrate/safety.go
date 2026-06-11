package migrate

import (
	"database/sql"
	"fmt"

	"github.com/croc100/litescope/internal/diff"
	_ "modernc.org/sqlite"
)

// Risk is one destructive or risky operation with its measured blast radius.
type Risk struct {
	Table       string
	Description string
	Rows        int64 // rows affected; -1 when the count is unavailable
}

func (r Risk) String() string {
	if r.Rows < 0 {
		return r.Description
	}
	return fmt.Sprintf("%s (%d rows affected)", r.Description, r.Rows)
}

// Analyze measures the blast radius of destructive changes by counting
// affected rows in the source database. It also flags risky-but-valid
// patterns such as adding a NOT NULL column without a default.
func Analyze(d *diff.Result, oldPath string) ([]Risk, error) {
	db, err := sql.Open("sqlite", oldPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", oldPath, err)
	}
	defer db.Close()

	var risks []Risk

	for _, td := range d.Schema {
		switch {
		case td.Removed:
			risks = append(risks, Risk{
				Table:       td.Name,
				Description: fmt.Sprintf("DROP TABLE %s — all data permanently lost", td.Name),
				Rows:        countRows(db, td.Name),
			})

		case td.Added:
			// New tables carry no risk.

		default:
			rows := int64(-1)
			if len(td.RemovedColumns) > 0 || len(td.ChangedColumns) > 0 || hasNotNullNoDefault(td) {
				rows = countRows(db, td.Name)
			}
			for _, c := range td.RemovedColumns {
				risks = append(risks, Risk{
					Table:       td.Name,
					Description: fmt.Sprintf("DROP COLUMN %s.%s — column data permanently lost", td.Name, c.Name),
					Rows:        rows,
				})
			}
			for _, c := range td.ChangedColumns {
				risks = append(risks, Risk{
					Table:       td.Name,
					Description: fmt.Sprintf("TYPE CHANGE %s.%s %s→%s — values may be coerced", td.Name, c.Name, c.Old.Type, c.New.Type),
					Rows:        rows,
				})
			}
			for _, c := range td.AddedColumns {
				if c.NotNull && c.Default == "" && rows > 0 {
					risks = append(risks, Risk{
						Table:       td.Name,
						Description: fmt.Sprintf("ADD COLUMN %s.%s NOT NULL without DEFAULT — rebuild will fail on existing rows", td.Name, c.Name),
						Rows:        rows,
					})
				}
			}
		}
	}

	return risks, nil
}

func countRows(db *sql.DB, table string) int64 {
	var n int64
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", table)).Scan(&n); err != nil {
		return -1
	}
	return n
}

func hasNotNullNoDefault(td diff.TableDiff) bool {
	for _, c := range td.AddedColumns {
		if c.NotNull && c.Default == "" {
			return true
		}
	}
	return false
}
