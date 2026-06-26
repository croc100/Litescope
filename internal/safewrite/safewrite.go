// Package safewrite wraps mutating SQL with agent-grade guardrails: it runs
// every write as a dry-run first, reports the exact blast radius (rows
// affected), snapshots the database before applying, and turns lock failures
// into structured remediation an agent can act on instead of a raw error.
//
// This is the safety layer behind the litescope MCP write tools — the place
// where the "agent operations" differentiation lives.
package safewrite

import (
	"database/sql"
	"strings"

	"github.com/croc100/litescope/internal/locks"
	"github.com/croc100/litescope/internal/migrate"

	_ "modernc.org/sqlite"
)

// StmtPreview describes the measured impact of a single statement.
type StmtPreview struct {
	SQL          string `json:"sql"`
	Kind         string `json:"kind"`          // update | insert | delete | ddl | other
	RowsAffected int64  `json:"rows_affected"` // exact, measured inside a transaction
}

// Result is the structured outcome of a guarded write.
type Result struct {
	OK           bool          `json:"ok"`
	Applied      bool          `json:"applied"`
	Provider     string        `json:"provider"`
	Statements   int           `json:"statements"`
	RowsAffected int64         `json:"rows_affected"`
	LastInsertID int64         `json:"last_insert_id,omitempty"`
	BackupPath   string        `json:"backup_path,omitempty"`
	Preview      []StmtPreview `json:"preview"`
	Note         string        `json:"note"`
	Error        string        `json:"error,omitempty"`
	// Remediation is populated when a write fails because the database is
	// locked/busy, giving the agent concrete PRAGMA/config fixes to retry with.
	Remediation *Remediation `json:"remediation,omitempty"`
}

// Remediation is a flattened, JSON-friendly view of a lock-doctor report.
type Remediation struct {
	Provider string         `json:"provider"`
	Verdict  string         `json:"verdict"`
	Fixes    []locks.Finding `json:"fixes"`
}

// PlanLocal measures and optionally applies a mutating migration against a
// local SQLite file.
//
//   - Impact is always measured exactly: statements run inside a transaction
//     and RowsAffected is captured per statement.
//   - When apply is false (the default for agents), the transaction is rolled
//     back — nothing changes — and the measured preview is returned.
//   - When apply is true, the database is snapshotted (VACUUM INTO) and the
//     statements are re-run through migrate.Apply, which is transactional and
//     restores the snapshot on commit failure.
//   - A lock/busy failure at any point is converted into structured
//     remediation rather than a bare error string.
func PlanLocal(dbPath, sqlText string, apply bool) (*Result, error) {
	stmts := migrate.SplitStatements(sqlText)
	res := &Result{Provider: "local", Statements: len(stmts)}
	if len(stmts) == 0 {
		res.Error = "no executable statements in SQL"
		res.Note = "Nothing to do."
		return res, nil
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return remediate(res, dbPath, err), nil
	}
	defer db.Close()

	// 1. Measure exact impact inside a throwaway transaction.
	tx, err := db.Begin()
	if err != nil {
		return remediate(res, dbPath, err), nil
	}
	for _, stmt := range stmts {
		r, err := tx.Exec(stmt)
		if err != nil {
			tx.Rollback()
			res.Note = "Statement failed during dry-run; database unchanged."
			return remediate(res, dbPath, err), nil
		}
		n, _ := r.RowsAffected()
		id, _ := r.LastInsertId()
		res.Preview = append(res.Preview, StmtPreview{
			SQL: firstLine(stmt), Kind: kindOf(stmt), RowsAffected: n,
		})
		res.RowsAffected += n
		if id > 0 {
			res.LastInsertID = id
		}
	}
	tx.Rollback() // discard the measurement; nothing is committed here

	if !apply {
		res.OK = true
		res.Note = "Dry-run only — no changes applied. Re-run with apply=true to commit. " +
			"A snapshot is taken automatically before any real write."
		return res, nil
	}

	// 2. Apply for real, with an automatic pre-write snapshot.
	ar, err := migrate.Apply(dbPath, sqlText, migrate.ApplyOptions{})
	if err != nil {
		res.BackupPath = ar.BackupPath
		res.Note = "Write failed; database is unchanged (or restored from snapshot)."
		return remediate(res, dbPath, err), nil
	}
	res.OK = true
	res.Applied = true
	res.BackupPath = ar.BackupPath
	res.Note = "Applied. A snapshot was taken before the write — one rewind away from undo."
	return res, nil
}

// remediate records the error on the result and, when it looks like a
// lock/busy failure, attaches lock-doctor guidance.
func remediate(res *Result, dbPath string, err error) *Result {
	res.OK = false
	res.Error = err.Error()
	if isBusy(err) {
		if rep, derr := locks.Diagnose(dbPath); derr == nil {
			fixes := make([]locks.Finding, 0, len(rep.Findings))
			for _, f := range rep.Findings {
				if f.Severity != locks.SeverityOK {
					fixes = append(fixes, f)
				}
			}
			res.Remediation = &Remediation{
				Provider: rep.Provider,
				Verdict:  rep.Verdict,
				Fixes:    fixes,
			}
			res.Note = "Database is locked. Apply the remediation fixes, then retry the write."
		}
	}
	return res
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "database is locked") ||
		strings.Contains(s, "database table is locked") ||
		strings.Contains(s, "sqlite_busy") ||
		strings.Contains(s, "(5)")
}

// kindOf classifies a statement by its leading keyword.
func kindOf(stmt string) string {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	switch {
	case strings.HasPrefix(upper, "UPDATE"):
		return "update"
	case strings.HasPrefix(upper, "INSERT"), strings.HasPrefix(upper, "REPLACE"):
		return "insert"
	case strings.HasPrefix(upper, "DELETE"):
		return "delete"
	case strings.HasPrefix(upper, "CREATE"), strings.HasPrefix(upper, "DROP"),
		strings.HasPrefix(upper, "ALTER"), strings.HasPrefix(upper, "TRUNCATE"):
		return "ddl"
	default:
		return "other"
	}
}

// firstLine returns the first non-comment line of a statement, for display.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		if l != "" && !strings.HasPrefix(l, "--") {
			return l
		}
	}
	return strings.TrimSpace(s)
}
