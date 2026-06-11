package fleet

import (
	"fmt"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/migrate"
)

// RolloutOptions controls a staged fleet migration.
type RolloutOptions struct {
	DryRun    bool   // validate against every database without committing
	Canary    int    // stop after this many successful applies (0 = no canary)
	BackupDir string // where local backups are written ("" = alongside each DB)
	NoBackup  bool   // skip local backups (dry-run never backs up)
}

// DBState is the outcome for one database in a rollout.
type DBState string

const (
	StateApplied DBState = "applied"  // committed successfully
	StateDryRun  DBState = "dry-run"  // validated, rolled back
	StateFailed  DBState = "failed"   // errored; halts the rollout
	StateSkipped DBState = "skipped"  // not attempted (rollout halted earlier)
	StateCanary  DBState = "canary"   // not attempted (canary limit reached)
)

// RolloutResult is the per-database record.
type RolloutResult struct {
	Database   string        `json:"database"`
	DSN        string        `json:"dsn"`
	State      DBState       `json:"state"`
	Executed   int           `json:"executed"`              // statements applied
	BackupPath string        `json:"backup_path,omitempty"` // local backup, if any
	Provider   string        `json:"provider"`              // local, turso, d1
	Err        error         `json:"-"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"-"`
}

// RolloutReport aggregates a staged migration across the fleet.
type RolloutReport struct {
	Results   []RolloutResult `json:"results"`
	StartedAt time.Time       `json:"started_at"`
	DryRun    bool            `json:"dry_run"`
	Halted    bool            `json:"halted"` // true when a failure stopped the rollout
}

// Counts tallies databases by terminal state.
func (r *RolloutReport) Counts() (applied, failed, skipped int) {
	for _, res := range r.Results {
		switch res.State {
		case StateApplied, StateDryRun:
			applied++
		case StateFailed:
			failed++
		case StateSkipped, StateCanary:
			skipped++
		}
	}
	return
}

// Rollout applies a migration to each database in order, stopping at the first
// failure so a bad migration can't cascade across the fleet.
//
//   - Local files use migrate.Apply: pre-flight integrity check, VACUUM INTO
//     backup, single transaction, foreign-key + integrity verification, and
//     automatic rollback/restore on failure.
//   - Remote databases (Turso, D1) execute via their provider API. Turso runs
//     transactionally; D1 cannot roll back a multi-statement batch.
//
// In dry-run mode every database is validated (apply + rollback) and the
// rollout never halts early, so you see which databases would fail. Otherwise
// the first failure halts the rollout and the remaining databases are marked
// skipped.
func Rollout(dbs []Database, sqlText string, opts RolloutOptions) *RolloutReport {
	report := &RolloutReport{StartedAt: time.Now().UTC(), DryRun: opts.DryRun}
	stmts := migrate.SplitStatements(sqlText)

	halted := false
	applied := 0

	for _, db := range dbs {
		res := RolloutResult{Database: db.Name, DSN: db.DSN, Provider: providerOf(db.DSN)}

		if halted {
			res.State = StateSkipped
			report.Results = append(report.Results, res)
			continue
		}
		if opts.Canary > 0 && applied >= opts.Canary && !opts.DryRun {
			res.State = StateCanary
			report.Results = append(report.Results, res)
			continue
		}
		if len(stmts) == 0 {
			res.State = StateFailed
			res.Err = fmt.Errorf("no executable statements in migration")
			res.Error = res.Err.Error()
			report.Results = append(report.Results, res)
			halted = true
			report.Halted = true
			continue
		}

		applyOne(&res, db, sqlText, stmts, opts)
		report.Results = append(report.Results, res)

		switch res.State {
		case StateApplied, StateDryRun:
			applied++
		case StateFailed:
			if !opts.DryRun {
				halted = true
				report.Halted = true
			}
		}
	}

	return report
}

// applyOne runs the migration against a single database, filling in res.
func applyOne(res *RolloutResult, db Database, sqlText string, stmts []string, opts RolloutOptions) {
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	// Local files get the full migrate.Apply safety pipeline (incl. backup).
	if isLocalFileDSN(db.DSN) {
		ar, err := migrate.Apply(db.DSN, sqlText, migrate.ApplyOptions{
			DryRun:    opts.DryRun,
			BackupDir: opts.BackupDir,
			NoBackup:  opts.NoBackup,
		})
		if ar != nil {
			res.Executed = ar.Executed
			res.BackupPath = ar.BackupPath
		}
		if err != nil {
			res.State = StateFailed
			res.Err = err
			res.Error = err.Error()
			return
		}
		res.State = appliedState(opts.DryRun)
		return
	}

	// Remote databases execute via the provider connector.
	conn, err := connector.Open(db.DSN)
	if err != nil {
		res.fail(err)
		return
	}
	defer conn.Close()

	exec, ok := connector.AsExecutor(conn)
	if !ok {
		res.fail(fmt.Errorf("provider does not support migrations"))
		return
	}
	if err := exec.Exec(stmts, opts.DryRun); err != nil {
		res.fail(err)
		return
	}
	res.Executed = len(stmts)
	res.State = appliedState(opts.DryRun)
}

func (res *RolloutResult) fail(err error) {
	res.State = StateFailed
	res.Err = err
	res.Error = err.Error()
}

func appliedState(dryRun bool) DBState {
	if dryRun {
		return StateDryRun
	}
	return StateApplied
}

func isLocalFileDSN(dsn string) bool {
	return !strings.HasPrefix(dsn, "turso://") && !strings.HasPrefix(dsn, "d1://")
}

func providerOf(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "turso://"):
		return "turso"
	case strings.HasPrefix(dsn, "d1://"):
		return "d1"
	default:
		return "local"
	}
}
