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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/croc100/litescope/internal/diff"
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
	// RewindToken identifies the pre-write snapshot that undoes this write.
	// It is the local snapshot path; pass it to litescope_write_undo to revert.
	RewindToken string `json:"rewind_token,omitempty"`
	// BlastRadiusDiff is the exact schema+data diff between the pre-write
	// snapshot and the post-write database. Populated only after a real apply.
	BlastRadiusDiff *diff.Result  `json:"blast_radius_diff,omitempty"`
	Preview         []StmtPreview `json:"preview"`
	Note            string        `json:"note"`
	Error           string        `json:"error,omitempty"`
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

// rewindTokenPayload binds a snapshot to the exact source path it was taken
// from, so a token minted for one database can't be replayed against another.
type rewindTokenPayload struct {
	Snapshot string `json:"snapshot"`
	Source   string `json:"source"` // absolute path
}

// EncodeRewindToken produces an opaque token binding a snapshot file to the
// source database it was taken from.
func EncodeRewindToken(snapshotPath, dbPath string) string {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		abs = dbPath
	}
	b, _ := json.Marshal(rewindTokenPayload{Snapshot: snapshotPath, Source: abs})
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeRewindToken recovers the snapshot path from a token and verifies it
// was minted for dbPath. It refuses to decode a token minted for a different
// source, preventing an agent from accidentally restoring the wrong database.
func DecodeRewindToken(token, dbPath string) (snapshotPath string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("invalid rewind_token: %w", err)
	}
	var p rewindTokenPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("invalid rewind_token: %w", err)
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		abs = dbPath
	}
	if p.Source != abs {
		return "", fmt.Errorf("rewind_token was minted for %q, not %q — refusing to restore the wrong database", p.Source, abs)
	}
	return p.Snapshot, nil
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
		if d, derr := dryRunDiff(dbPath, sqlText); derr == nil {
			res.BlastRadiusDiff = d
		}
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
	res.RewindToken = EncodeRewindToken(ar.BackupPath, dbPath)
	res.Note = "Applied. A snapshot was taken before the write — pass rewind_token to " +
		"litescope_write_undo (with the same source) to revert."
	if d, derr := diff.Compare(ar.BackupPath, dbPath); derr == nil {
		res.BlastRadiusDiff = d
	}
	return res, nil
}

// dryRunDiff computes the exact blast-radius diff a write would produce
// without touching dbPath: it VACUUM INTOs a scratch copy, applies the
// statements for real against that copy, diffs it against the original, and
// discards it. Non-deterministic SQL (random(), CURRENT_TIMESTAMP, etc.) can
// make this differ slightly from a later real apply — it's a preview, not a
// guarantee.
func dryRunDiff(dbPath, sqlText string) (*diff.Result, error) {
	tmp, err := os.CreateTemp("", "litescope-dryrun-*.db")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath) // VACUUM INTO refuses to write to an existing file
	defer os.Remove(tmpPath)

	src, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if _, err := src.Exec("VACUUM INTO ?", tmpPath); err != nil {
		return nil, err
	}

	if _, err := migrate.Apply(tmpPath, sqlText, migrate.ApplyOptions{NoBackup: true}); err != nil {
		return nil, err
	}
	return diff.Compare(dbPath, tmpPath)
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
