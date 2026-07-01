// Package health inspects a SQLite database for operational faults a storage
// engineer cares about during an incident: corruption, WAL bloat from a starved
// checkpoint, fragmentation/space waste, and basic reachability. It works on
// local database files; remote sources expose only reachability via the
// connector layer.
package health

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/croc100/litescope/internal/snapshot"

	_ "modernc.org/sqlite"
)

// Severity ranks how urgently a database needs attention.
type Severity int

const (
	SevOK Severity = iota
	SevWarning
	SevCritical
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "critical"
	case SevWarning:
		return "warning"
	default:
		return "ok"
	}
}

// Thresholds for fault classification. Deliberately conservative defaults; a
// healthy WAL checkpoints regularly and stays small, and a well-maintained DB
// keeps its freelist low.
const (
	// WALBloatBytes flags a -wal file larger than this outright (checkpoint
	// starvation: a long-running reader is holding the checkpoint back).
	WALBloatBytes = 64 << 20 // 64 MiB
	// WALBloatRatio flags a -wal file larger than this fraction of the main DB.
	WALBloatRatio = 0.5
	// FragmentationRatio flags a freelist larger than this fraction of the DB
	// (reclaimable space — a VACUUM candidate).
	FragmentationRatio = 0.25
	// FragmentationMinBytes avoids nagging about tiny databases.
	FragmentationMinBytes = 16 << 20 // 16 MiB
)

// Report is the operational health of one database.
type Report struct {
	Path          string   `json:"path"`
	Remote        bool     `json:"remote,omitempty"`
	Reachable     bool     `json:"reachable"`
	Severity      Severity `json:"-"`
	SeverityLabel string   `json:"severity"`
	Issues        []string `json:"issues,omitempty"`
	IntegrityOK   bool     `json:"integrity_ok"`
	SizeBytes     int64    `json:"size_bytes"`
	WALBytes      int64    `json:"wal_bytes"`
	PageCount     int64    `json:"page_count"`
	FreelistCount int64    `json:"freelist_count"`
	JournalMode   string   `json:"journal_mode,omitempty"`
	Error         string   `json:"error,omitempty"`

	// Heartbeat staleness — the database file hasn't been written to recently.
	// Zero ModTime means it wasn't checked (remote source, or file missing).
	ModTime time.Time `json:"mod_time,omitempty"`
	Stale   bool      `json:"stale,omitempty"`

	// Backup posture (local files only). HasBackup is false when no litescope
	// snapshot exists for the database. These are informational and do not raise
	// severity — a missing backup is a recommendation, not a fault.
	HasBackup      bool       `json:"has_backup"`
	LastBackupAt   *time.Time `json:"last_backup_at,omitempty"`
	SnapshotCount  int        `json:"snapshot_count"`
}

// FragmentationPct returns reclaimable space as a percentage of the database.
func (r *Report) FragmentationPct() float64 {
	if r.PageCount == 0 {
		return 0
	}
	return float64(r.FreelistCount) / float64(r.PageCount) * 100
}

// Inspect runs a fault inspection on a local SQLite file. When deep is true it
// uses the exhaustive integrity_check; otherwise the faster quick_check, which
// is the right default for fleet-scale triage.
func Inspect(path string, deep bool) *Report {
	r := &Report{Path: path, IntegrityOK: true}

	fi, err := os.Stat(path)
	if err != nil {
		r.Reachable = false
		r.Severity = SevCritical
		r.SeverityLabel = r.Severity.String()
		r.Error = fmt.Sprintf("not found: %v", err)
		r.Issues = []string{"unreachable — file not found"}
		return r
	}
	r.Reachable = true
	r.SizeBytes = fi.Size()
	r.ModTime = fi.ModTime()

	// WAL file lives alongside the main DB; its size reveals checkpoint health.
	// In WAL mode writes land there first, so it — not the main file — carries
	// the freshest mtime; take the max of both for an accurate last-write time.
	if wfi, err := os.Stat(path + "-wal"); err == nil {
		r.WALBytes = wfi.Size()
		if wfi.ModTime().After(r.ModTime) {
			r.ModTime = wfi.ModTime()
		}
	}

	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		r.Severity = SevCritical
		r.SeverityLabel = r.Severity.String()
		r.Error = err.Error()
		r.Issues = append(r.Issues, "cannot open database")
		return r
	}
	defer db.Close()

	// Integrity — the headline fault signal.
	pragma := "PRAGMA quick_check"
	if deep {
		pragma = "PRAGMA integrity_check"
	}
	if errs := runCheck(db, pragma); len(errs) > 0 {
		r.IntegrityOK = false
		r.Severity = SevCritical
		first := errs[0]
		if len(errs) > 1 {
			first = fmt.Sprintf("%s (+%d more)", first, len(errs)-1)
		}
		r.Issues = append(r.Issues, "CORRUPT — "+first)
	}

	scanInt(db, "PRAGMA page_count", &r.PageCount)
	scanInt(db, "PRAGMA freelist_count", &r.FreelistCount)
	scanStr(db, "PRAGMA journal_mode", &r.JournalMode)

	// WAL bloat: large in absolute terms, or large relative to the DB.
	if r.WALBytes >= WALBloatBytes || (r.SizeBytes > 0 && float64(r.WALBytes) >= float64(r.SizeBytes)*WALBloatRatio) {
		r.raise(SevWarning, fmt.Sprintf("WAL %s — checkpoint starved, reads degraded", humanBytes(r.WALBytes)))
	}

	// Fragmentation: reclaimable freelist beyond a meaningful size.
	if r.SizeBytes >= FragmentationMinBytes && r.FragmentationPct() >= FragmentationRatio*100 {
		reclaim := r.FreelistCount * pageSize(r)
		r.raise(SevWarning, fmt.Sprintf("%.0f%% bloat — %s reclaimable, VACUUM recommended",
			r.FragmentationPct(), humanBytes(reclaim)))
	}

	// Backup posture — informational, never raises severity.
	if snaps, err := snapshot.List(path); err == nil && len(snaps) > 0 {
		r.HasBackup = true
		r.SnapshotCount = len(snaps)
		t := snaps[0].CreatedAt
		r.LastBackupAt = &t
	}

	r.SeverityLabel = r.Severity.String()
	return r
}

// CheckStaleness flags a database that stopped being written to — a
// dead-man's-switch for an app that crashed, disconnected, or hung without
// ever corrupting its file. No-op when maxIdle is zero/negative, the database
// is unreachable, or its mtime wasn't captured (e.g. a remote source).
func (r *Report) CheckStaleness(maxIdle time.Duration) {
	if maxIdle <= 0 || !r.Reachable || r.ModTime.IsZero() {
		return
	}
	idle := time.Since(r.ModTime)
	if idle <= maxIdle {
		return
	}
	r.Stale = true
	r.raise(SevWarning, fmt.Sprintf("stale — no writes in %s (last write %s ago)", maxIdle, idle.Round(time.Second)))
	r.SeverityLabel = r.Severity.String()
}

// raise bumps severity to at least sev and records an issue.
func (r *Report) raise(sev Severity, issue string) {
	if sev > r.Severity {
		r.Severity = sev
	}
	r.Issues = append(r.Issues, issue)
}

func pageSize(r *Report) int64 {
	if r.PageCount == 0 {
		return 4096
	}
	ps := r.SizeBytes / r.PageCount
	if ps <= 0 {
		return 4096
	}
	return ps
}

// ── SQLite helpers ──────────────────────────────────────────────────────────

func runCheck(db *sql.DB, pragma string) []string {
	rows, err := db.Query(pragma)
	if err != nil {
		return []string{err.Error()}
	}
	defer rows.Close()
	var errs []string
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			return []string{err.Error()}
		}
		if msg != "ok" {
			errs = append(errs, msg)
		}
	}
	return errs
}

func scanInt(db *sql.DB, pragma string, dst *int64) {
	_ = db.QueryRow(pragma).Scan(dst)
}

func scanStr(db *sql.DB, pragma string, dst *string) {
	_ = db.QueryRow(pragma).Scan(dst)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGT"[exp])
}
