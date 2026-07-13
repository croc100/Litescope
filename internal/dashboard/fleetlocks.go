package dashboard

import (
	"sort"
	"time"
)

// Lock-doctor severity thresholds for the fleet rollup. A single database's
// per-event timeline already exposes the raw numbers; the fleet view needs a
// verdict per database so operators can triage a whole fleet worst-first.
const (
	// fleetJamCriticalMS is the longest contention window past which a database
	// is considered critically jammed (a writer held the lock this long while
	// others saw SQLITE_BUSY).
	fleetJamCriticalMS = 30 * 1000 // 30s
	// fleetWaitCriticalMS is the worst BEGIN IMMEDIATE wait past which a
	// database is critically contended even without a long window.
	fleetWaitCriticalMS = 1000 // 1s
)

// FleetLockSummary is one database's contention rollup — the fleet lock
// doctor's per-member row. It answers, for the whole fleet at a glance, "which
// databases are jammed, how badly, and who is holding them?"
type FleetLockSummary struct {
	Name            string       `json:"name"`
	Source          string       `json:"source"`
	Severity        string       `json:"severity"` // ok | warning | critical
	Events          int          `json:"events"`
	LockedEvents    int          `json:"locked_events"`
	Windows         int          `json:"windows"`
	LongestWindowMS int64        `json:"longest_window_ms"`
	MaxWaitMS       int64        `json:"max_wait_ms"`
	P95WaitMS       int64        `json:"p95_wait_ms"`
	LastContention  int64        `json:"last_contention_ts,omitempty"` // unix ms of the most recent jam
	WALLastBytes    int64        `json:"wal_last_bytes"`
	WALPeakBytes    int64        `json:"wal_peak_bytes"`
	WALBloated      bool         `json:"wal_bloated"`
	TopHolders      []HolderStat `json:"top_holders,omitempty"`
}

// FleetLockReport aggregates per-database contention across the fleet,
// sorted worst-first (critical → warning → ok).
type FleetLockReport struct {
	SinceMS   int64              `json:"since_ms"`
	Databases []FleetLockSummary `json:"databases"`
	CheckedAt time.Time          `json:"checked_at"`
}

// Counts tallies databases by contention severity.
func (r *FleetLockReport) Counts() (ok, warning, critical int) {
	for _, d := range r.Databases {
		switch d.Severity {
		case "critical":
			critical++
		case "warning":
			warning++
		default:
			ok++
		}
	}
	return
}

// HasContention reports whether any database saw lock contention in the window.
func (r *FleetLockReport) HasContention() bool {
	_, w, c := r.Counts()
	return w > 0 || c > 0
}

// FleetLockEntry pairs a fleet member's display name and source DSN with its
// raw lock event stream (oldest first, as returned by LockSeries).
type FleetLockEntry struct {
	Name   string
	Source string
	Events []LockEvent
}

// FleetSource identifies one fleet member for the lock rollup: a display name
// and the source key its lock events were recorded under (the DSN).
type FleetSource struct {
	Name   string
	Source string
}

// FleetLockReport reads each source's recorded lock series from the store and
// aggregates them into a fleet-wide contention report, worst-first. A member
// with no recorded events still appears (as ok), so the fleet roster is
// complete.
func (h *History) FleetLockReport(sources []FleetSource, sinceMs int64) (*FleetLockReport, error) {
	entries := make([]FleetLockEntry, 0, len(sources))
	for _, s := range sources {
		events, err := h.LockSeries(s.Source, sinceMs)
		if err != nil {
			return nil, err
		}
		entries = append(entries, FleetLockEntry{Name: s.Name, Source: s.Source, Events: events})
	}
	return BuildFleetLockReport(sinceMs, entries), nil
}

// BuildFleetLockReport reduces each member's raw lock stream to a contention
// summary and sorts the fleet worst-first. Pure function so it can be
// unit-tested without a store.
func BuildFleetLockReport(sinceMs int64, entries []FleetLockEntry) *FleetLockReport {
	r := &FleetLockReport{SinceMS: sinceMs, CheckedAt: time.Now().UTC()}
	for _, e := range entries {
		tl := BuildLockTimeline(e.Source, sinceMs, e.Events)
		s := FleetLockSummary{
			Name:         e.Name,
			Source:       e.Source,
			Events:       tl.Events,
			LockedEvents: tl.LockedEvents,
			Windows:      len(tl.Windows),
			MaxWaitMS:    tl.MaxWaitMS,
			P95WaitMS:    tl.P95WaitMS,
			WALLastBytes: tl.WALLastBytes,
			WALPeakBytes: tl.WALPeakBytes,
			WALBloated:   tl.WALBloated,
			TopHolders:   tl.TopHolders,
		}
		for _, w := range tl.Windows {
			if w.DurationMS > s.LongestWindowMS {
				s.LongestWindowMS = w.DurationMS
			}
			if w.EndTS > s.LastContention {
				s.LastContention = w.EndTS
			}
		}
		s.Severity = fleetLockSeverity(s)
		r.Databases = append(r.Databases, s)
	}

	sort.Slice(r.Databases, func(i, j int) bool {
		a, b := r.Databases[i], r.Databases[j]
		if rank := sevRank(a.Severity) - sevRank(b.Severity); rank != 0 {
			return rank > 0
		}
		if a.LockedEvents != b.LockedEvents {
			return a.LockedEvents > b.LockedEvents
		}
		if a.MaxWaitMS != b.MaxWaitMS {
			return a.MaxWaitMS > b.MaxWaitMS
		}
		return a.Name < b.Name
	})
	return r
}

// fleetLockSeverity classifies a database's contention. Critical when the WAL
// checkpoint is starved, a jam ran long, or a writer stalled others for over a
// second; warning on any observed contention; ok otherwise.
func fleetLockSeverity(s FleetLockSummary) string {
	if s.WALBloated || s.LongestWindowMS >= fleetJamCriticalMS || s.MaxWaitMS >= fleetWaitCriticalMS {
		return "critical"
	}
	if s.LockedEvents > 0 {
		return "warning"
	}
	return "ok"
}

func sevRank(sev string) int {
	switch sev {
	case "critical":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}
