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
	OpSafe        OpKind = iota // ADD COLUMN, CREATE TABLE, CREATE/DROP INDEX — instant, no exclusive lock
	OpRisky                     // TABLE REBUILD without data loss — EXCLUSIVE write-lock for the rebuild duration
	OpDestructive               // DROP TABLE / DROP COLUMN / NOT-NULL-without-default — data loss or a guaranteed failure
)

func (k OpKind) Icon() string {
	switch k {
	case OpSafe:
		return "✓"
	case OpRisky:
		return "⚠"
	default:
		return "✗"
	}
}

// Operation describes one migration operation with its measured blast radius.
type Operation struct {
	Table    string
	Kind     OpKind
	Icon     string // ✓ / ⚠ / ✗
	Headline string // one-line summary
	Detail   string // lock estimate, row count, or advice
	Rows     int64  // rows affected; -1 when unavailable
	LockMs   int64  // estimated exclusive write-lock in ms (rebuilds only); 0 otherwise
}

// lock-estimate tuning. These are deliberately order-of-magnitude heuristics:
// the lock duration the user actually experiences depends on their SQLite engine
// (C SQLite, libsql/Turso, or our pure-Go modernc), disk, and row width — none of
// which we can know. The goal is to flag "this will hurt at scale", conservatively.
const (
	// msPerKRowCopy is the cost of copying 1,000 rows during INSERT...SELECT.
	// ~8ms/1k ≈ 125k rows/sec — a conservative figure that over-warns rather
	// than under-warns, which is the safe direction for a safety tool.
	msPerKRowCopy = 8.0
	// indexRebuildFactor: each index on the table is rebuilt from scratch during
	// the rebuild. Each adds roughly half the base row-copy cost.
	indexRebuildFactor = 0.5
)

// EstimateRebuildLock estimates the SQLite exclusive write-lock duration for a
// table rebuild. SQLite locks the entire database file for DDL — WAL mode does
// not help. Cost scales with rows copied plus re-creating every index.
//
// Returns 0 for an empty table (instant) and -1 when the row count is unknown.
func EstimateRebuildLock(rows int64, indexes int) time.Duration {
	if rows < 0 {
		return -1
	}
	if rows == 0 {
		return 0
	}
	k := float64(rows) / 1000.0
	ms := k * msPerKRowCopy * (1 + float64(indexes)*indexRebuildFactor)
	if ms < 1 {
		ms = 1
	}
	return time.Duration(ms) * time.Millisecond
}

// AnalyzeAll returns a full operation report — safe, risky, and destructive —
// for every table touched by the diff. This is the primary blast-radius API.
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
				Icon:     OpSafe.Icon(),
				Headline: fmt.Sprintf("CREATE TABLE %s", td.Name),
				Detail:   "new table — instant",
			})

		case td.Removed:
			rows := countRows(db, td.Name)
			ops = append(ops, Operation{
				Table:    td.Name,
				Kind:     OpDestructive,
				Icon:     OpDestructive.Icon(),
				Headline: fmt.Sprintf("DROP TABLE %s", td.Name),
				Detail:   fmt.Sprintf("%s rows permanently deleted", fmtRows(rows)),
				Rows:     rows,
			})

		default:
			ops = append(ops, analyzeAlter(db, td)...)
		}
	}

	return ops, nil
}

// analyzeAlter handles a modified table: a rebuild (drop/change columns) or a
// set of in-place safe operations (add column, index changes).
func analyzeAlter(db *sql.DB, td diff.TableDiff) []Operation {
	needsRebuild := len(td.RemovedColumns) > 0 || len(td.ChangedColumns) > 0

	if !needsRebuild {
		return analyzeInPlace(db, td)
	}

	rows := countRows(db, td.Name)
	indexes := countIndexes(db, td.Name)
	lock := EstimateRebuildLock(rows, indexes)

	// Data loss is what separates destructive from merely risky:
	//   - dropping a column destroys its data            → destructive (✗)
	//   - a rebuild with only type changes / added cols   → risky (⚠), lock only
	// Type coercion *can* lose data but we can't prove it without inspecting
	// values, so it's a caution, not a guaranteed loss.
	kind := OpRisky
	if len(td.RemovedColumns) > 0 {
		kind = OpDestructive
	}

	op := Operation{
		Table:    td.Name,
		Kind:     kind,
		Icon:     kind.Icon(),
		Headline: rebuildHeadline(td),
		Detail:   rebuildDetail(rows, indexes, lock),
		Rows:     rows,
	}
	if lock > 0 {
		op.LockMs = lock.Milliseconds()
	}
	return []Operation{op}
}

// analyzeInPlace handles ADD COLUMN + index changes (no rebuild).
func analyzeInPlace(db *sql.DB, td diff.TableDiff) []Operation {
	var ops []Operation
	for _, c := range td.AddedColumns {
		op := Operation{
			Table:    td.Name,
			Kind:     OpSafe,
			Icon:     OpSafe.Icon(),
			Headline: fmt.Sprintf("ADD COLUMN %s.%s", td.Name, c.Name),
			Detail:   "metadata-only — instant, no lock",
		}
		// NOT NULL without a DEFAULT cannot be added to a table with rows:
		// SQLite rejects it outright. This is a guaranteed migration failure.
		if c.NotNull && c.Default == "" {
			rows := countRows(db, td.Name)
			if rows > 0 {
				op.Kind = OpDestructive
				op.Icon = OpDestructive.Icon()
				op.Detail = fmt.Sprintf("NOT NULL without DEFAULT — fails on %s existing rows", fmtRows(rows))
				op.Rows = rows
			}
		}
		ops = append(ops, op)
	}
	for _, ix := range td.AddedIndexes {
		ops = append(ops, Operation{
			Table:    td.Name,
			Kind:     OpSafe,
			Icon:     OpSafe.Icon(),
			Headline: fmt.Sprintf("CREATE INDEX %s", ix.Name),
			Detail:   "index build — brief read-lock only",
		})
	}
	for _, ix := range td.RemovedIndexes {
		ops = append(ops, Operation{
			Table:    td.Name,
			Kind:     OpSafe,
			Icon:     OpSafe.Icon(),
			Headline: fmt.Sprintf("DROP INDEX %s", ix.Name),
			Detail:   "instant",
		})
	}
	return ops
}

func rebuildHeadline(td diff.TableDiff) string {
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
	h := fmt.Sprintf("TABLE REBUILD %s", td.Name)
	for i, ch := range changes {
		if i == 0 {
			h += " — "
		} else {
			h += ", "
		}
		h += ch
	}
	return h
}

func rebuildDetail(rows int64, indexes int, lock time.Duration) string {
	idxNote := ""
	if indexes > 0 {
		idxNote = fmt.Sprintf(", %d index(es) rebuilt", indexes)
	}
	var lockNote string
	switch {
	case lock < 0:
		lockNote = "write-lock duration unknown"
	case lock == 0:
		lockNote = "instant (empty table)"
	default:
		lockNote = fmt.Sprintf("~%s write-lock (app-wide, WAL included)", fmtDuration(lock))
	}
	return fmt.Sprintf("%s rows%s → %s", fmtRows(rows), idxNote, lockNote)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func countRows(db *sql.DB, table string) int64 {
	var n int64
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %q", table)).Scan(&n); err != nil {
		return -1
	}
	return n
}

// countIndexes counts indexes on a table via PRAGMA index_list. A rebuild
// re-creates all of them, so they factor into the lock estimate.
func countIndexes(db *sql.DB, table string) int {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return 0
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
	}
	return n
}

func fmtRows(n int64) string {
	switch {
	case n < 0:
		return "unknown"
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func fmtDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "unknown"
	case d == 0:
		return "instant"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	default:
		return fmt.Sprintf("%.1fmin", d.Minutes())
	}
}
