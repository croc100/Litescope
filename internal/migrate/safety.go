package migrate

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/croc100/litescope/internal/diff"
	_ "modernc.org/sqlite"
)

// OpKind classifies the blast radius of a migration operation.
type OpKind int

const (
	OpSafe        OpKind = iota // ADD COLUMN, CREATE TABLE, CREATE INDEX — instant, no lock
	OpRisky                     // TABLE REBUILD — EXCLUSIVE write-lock for N seconds
	OpDestructive               // DROP TABLE / DROP COLUMN / TYPE CHANGE — data loss
)

// Operation describes one migration operation with its measured blast radius.
type Operation struct {
	Table    string
	Kind     OpKind
	Icon     string // ✓ / ⚠ / ✗
	Headline string // one-line summary
	Detail   string // lock estimate, row count, or advice
	Rows     int64  // rows affected; -1 when unavailable
}

// Risk is kept for backward compatibility. New code should use AnalyzeAll.
type Risk struct {
	Table       string
	Description string
	Rows        int64
}

func (r Risk) String() string {
	if r.Rows < 0 {
		return r.Description
	}
	return fmt.Sprintf("%s (%d rows affected)", r.Description, r.Rows)
}

// EstimateLockDuration estimates the SQLite exclusive write-lock duration for a
// table rebuild. SQLite locks the entire file for DDL — WAL mode does not help.
//
// Empirical baseline: 8ms per 1,000 rows (conservative SSD estimate; spinning
// disk or large rows will be slower). The goal is to show "this will hurt" at
// production scale, not to predict clock time precisely.
func EstimateLockDuration(rows int64) time.Duration {
	if rows <= 0 {
		return 0
	}
	ms := rows * 8 / 1000
	if ms < 1 {
		ms = 1
	}
	return time.Duration(ms) * time.Millisecond
}

// AnalyzeAll returns a full operation report — safe, risky, and destructive —
// for every table touched by the diff. This is the primary API for the
// blast-radius analyzer.
func AnalyzeAll(d *diff.Result, oldPath string) ([]Operation, error) {
	db, err := sql.Open("sqlite", oldPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", oldPath, err)
	}
	defer db.Close()

	var ops []Operation

	for _, td := range d.Schema {
		switch {
		case td.Added:
			ops = append(ops, Operation{
				Table:    td.Name,
				Kind:     OpSafe,
				Icon:     "✓",
				Headline: fmt.Sprintf("CREATE TABLE %s", td.Name),
				Detail:   "new table — instant",
				Rows:     0,
			})

		case td.Removed:
			rows := countRows(db, td.Name)
			ops = append(ops, Operation{
				Table:    td.Name,
				Kind:     OpDestructive,
				Icon:     "✗",
				Headline: fmt.Sprintf("DROP TABLE %s", td.Name),
				Detail:   fmt.Sprintf("%s rows permanently deleted", fmtRows(rows)),
				Rows:     rows,
			})

		default:
			needsRebuild := len(td.RemovedColumns) > 0 || len(td.ChangedColumns) > 0

			if needsRebuild {
				rows := countRows(db, td.Name)
				lockEst := EstimateLockDuration(rows)
				detail := fmt.Sprintf("%s rows → ~%s write-lock (app-wide, WAL included)",
					fmtRows(rows), fmtDuration(lockEst))

				// Rebuild is risky (lock), but columns being dropped make it destructive.
				kind := OpRisky
				icon := "⚠"
				if len(td.RemovedColumns) > 0 || len(td.ChangedColumns) > 0 {
					kind = OpDestructive
					icon = "✗"
				}

				// Summarize what the rebuild does.
				var changes []string
				for _, c := range td.RemovedColumns {
					changes = append(changes, fmt.Sprintf("DROP COLUMN %s", c.Name))
				}
				for _, c := range td.ChangedColumns {
					changes = append(changes, fmt.Sprintf("%s %s→%s", c.Name, c.Old.Type, c.New.Type))
				}
				for _, c := range td.AddedColumns {
					changes = append(changes, fmt.Sprintf("ADD COLUMN %s", c.Name))
				}

				headline := fmt.Sprintf("TABLE REBUILD %s", td.Name)
				if len(changes) > 0 {
					headline += " — "
					for i, ch := range changes {
						if i > 0 {
							headline += ", "
						}
						headline += ch
					}
				}

				ops = append(ops, Operation{
					Table:    td.Name,
					Kind:     kind,
					Icon:     icon,
					Headline: headline,
					Detail:   detail,
					Rows:     rows,
				})

			} else {
				// Pure ADD COLUMN(s) and/or index changes — safe.
				for _, c := range td.AddedColumns {
					op := Operation{
						Table:    td.Name,
						Kind:     OpSafe,
						Icon:     "✓",
						Headline: fmt.Sprintf("ADD COLUMN %s.%s", td.Name, c.Name),
						Detail:   "metadata-only — instant, no lock",
						Rows:     0,
					}
					// NOT NULL without DEFAULT will cause rebuild to fail.
					if c.NotNull && c.Default == "" {
						rows := countRows(db, td.Name)
						op.Kind = OpDestructive
						op.Icon = "✗"
						op.Detail = fmt.Sprintf("NOT NULL without DEFAULT — rebuild will fail on %s existing rows", fmtRows(rows))
						op.Rows = rows
					}
					ops = append(ops, op)
				}
				for _, ix := range td.AddedIndexes {
					ops = append(ops, Operation{
						Table:    td.Name,
						Kind:     OpSafe,
						Icon:     "✓",
						Headline: fmt.Sprintf("CREATE INDEX %s", ix.Name),
						Detail:   "index build — brief read-lock only",
					})
				}
				for _, ix := range td.RemovedIndexes {
					ops = append(ops, Operation{
						Table:    td.Name,
						Kind:     OpSafe,
						Icon:     "✓",
						Headline: fmt.Sprintf("DROP INDEX %s", ix.Name),
						Detail:   "instant",
					})
				}
			}
		}
	}

	return ops, nil
}

// Analyze returns only the risky/destructive operations (Risk slice), preserved
// for backward compatibility with callers that only care about warnings.
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

// ── helpers ───────────────────────────────────────────────────────────────────

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

func fmtRows(n int64) string {
	if n < 0 {
		return "unknown"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func fmtDuration(d time.Duration) string {
	if d == 0 {
		return "instant"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("~%.0fs", d.Seconds())
	}
	return fmt.Sprintf("~%.1fmin", d.Minutes())
}
