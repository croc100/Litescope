package fleet

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/migrate"
)

// RecoverState is the outcome of attempting to recover one database.
type RecoverState string

const (
	RecoverHealthy     RecoverState = "healthy"     // no fault — nothing to do
	RecoverRestored    RecoverState = "restored"    // restored from a verified backup
	RecoverQuarantined RecoverState = "quarantined" // faulted, no healthy backup — excluded from future ops
	RecoverFailed      RecoverState = "failed"      // recovery attempt errored
	RecoverRemote      RecoverState = "remote"      // remote DB faulted — needs manual recovery
)

// RecoverResult is the per-database recovery record.
type RecoverResult struct {
	Database   string        `json:"database"`
	DSN        string        `json:"dsn"`
	State      RecoverState  `json:"state"`
	BackupPath string        `json:"backup_path,omitempty"` // backup used to restore
	Detail     string        `json:"detail,omitempty"`
	Err        error         `json:"-"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"-"`
}

// RecoverReport aggregates a fleet recovery.
type RecoverReport struct {
	Results   []RecoverResult `json:"results"`
	DryRun    bool            `json:"dry_run"`
	StartedAt time.Time       `json:"started_at"`
}

// Quarantine returns the names of databases that should be marked quarantined.
func (r *RecoverReport) Quarantine() []string {
	var names []string
	for _, res := range r.Results {
		if res.State == RecoverQuarantined {
			names = append(names, res.Database)
		}
	}
	return names
}

// Counts tallies databases by recovery outcome.
func (r *RecoverReport) Counts() (restored, quarantined, failed, healthy int) {
	for _, res := range r.Results {
		switch res.State {
		case RecoverRestored:
			restored++
		case RecoverQuarantined:
			quarantined++
		case RecoverFailed, RecoverRemote:
			failed++
		case RecoverHealthy:
			healthy++
		}
	}
	return
}

// RecoverOptions controls a fleet recovery.
type RecoverOptions struct {
	BackupDir  string // where to look for backups ("" = alongside each DB)
	DryRun     bool   // report what would happen without restoring or quarantining
	Deep       bool   // exhaustive integrity_check when assessing health
	Quarantine bool   // quarantine databases with no healthy backup
}

// Recover triages the fleet and restores every critically faulted local
// database from its most recent *verified-healthy* backup. Databases with no
// healthy backup are flagged for quarantine. Bloat/warning-level issues are not
// recovery targets and are reported as healthy here.
//
// Recovery only applies to local files; a faulted remote database is reported
// as needing manual recovery. In dry-run mode nothing is written.
func Recover(dbs []Database, opts RecoverOptions) *RecoverReport {
	report := &RecoverReport{DryRun: opts.DryRun, StartedAt: time.Now().UTC()}

	for _, db := range dbs {
		report.Results = append(report.Results, recoverOne(db, opts))
	}
	return report
}

func recoverOne(db Database, opts RecoverOptions) RecoverResult {
	start := time.Now()
	res := RecoverResult{Database: db.Name, DSN: db.DSN}
	defer func() { res.Duration = time.Since(start) }()

	// Remote databases can't be restored from a local file backup.
	if !isLocalFileDSN(db.DSN) {
		rep := remoteHealth(db.DSN)
		if rep.Severity == health.SevCritical {
			res.State = RecoverRemote
			res.Detail = "remote database unreachable — manual recovery required"
		} else {
			res.State = RecoverHealthy
			res.Detail = "remote · reachable"
		}
		return res
	}

	rep := health.Inspect(db.DSN, opts.Deep)
	// Only critical faults (corruption, unreachable) are recovery targets.
	if rep.Severity != health.SevCritical {
		res.State = RecoverHealthy
		if rep.Severity == health.SevWarning {
			res.Detail = "warning only (not a recovery target): " + joinIssues(rep.Issues)
		}
		return res
	}

	// Find the newest backup that is itself healthy.
	backups := findBackups(db.DSN, opts.BackupDir)
	healthyBackup := ""
	for _, b := range backups {
		if br := health.Inspect(b, false); br.Severity != health.SevCritical {
			healthyBackup = b
			break
		}
	}

	if healthyBackup == "" {
		res.State = RecoverQuarantined
		if len(backups) == 0 {
			res.Detail = "no backup found — quarantined"
		} else {
			res.Detail = "all backups also corrupt — quarantined"
		}
		return res
	}

	res.BackupPath = healthyBackup
	if opts.DryRun {
		res.State = RecoverRestored
		res.Detail = "would restore from " + filepath.Base(healthyBackup)
		return res
	}

	if err := migrate.Restore(db.DSN, healthyBackup); err != nil {
		res.State = RecoverFailed
		res.Err = err
		res.Error = err.Error()
		return res
	}

	// Verify the restored database is actually healthy now.
	if vr := health.Inspect(db.DSN, false); vr.Severity == health.SevCritical {
		res.State = RecoverFailed
		res.Detail = "restore completed but database still fails integrity check"
		return res
	}

	res.State = RecoverRestored
	res.Detail = "restored from " + filepath.Base(healthyBackup)
	return res
}

// findBackups returns backup files for a database, newest first. Backups are
// named "<base>.backup-<timestamp>.db" (see migrate.Apply); the timestamp is
// lexically sortable, so a reverse string sort yields newest-first.
func findBackups(dbPath, backupDir string) []string {
	dir := backupDir
	if dir == "" {
		dir = filepath.Dir(dbPath)
	}
	base := filepath.Base(dbPath)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	matches, err := filepath.Glob(filepath.Join(dir, stem+".backup-*.db"))
	if err != nil {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	return matches
}

func joinIssues(issues []string) string {
	if len(issues) == 0 {
		return ""
	}
	return issues[0]
}
