package dashboard

import "sort"

// walBloatBytes is the WAL size past which a checkpoint is considered starved.
// Mirrors the local-file threshold used by the lock doctor's static diagnosis.
const walBloatBytes = 100 * 1024 * 1024 // 100 MB

// ContentionWindow is a maximal contiguous run of "locked" observations — a
// period during which some writer held the lock and others would have seen
// SQLITE_BUSY. It answers "when, and for how long, was this database jammed?"
type ContentionWindow struct {
	StartTS    int64    `json:"start_ts"`     // unix ms of the first locked event
	EndTS      int64    `json:"end_ts"`       // unix ms of the last locked event in the run
	DurationMS int64    `json:"duration_ms"`  // EndTS - StartTS
	Events     int      `json:"events"`       // locked observations in the window
	PeakWaitMS int64    `json:"peak_wait_ms"` // worst BEGIN IMMEDIATE wait in the window
	Holders    []string `json:"holders,omitempty"`
}

// HolderStat counts how often a process was seen holding the lock during
// contended observations, ranking the biggest offenders.
type HolderStat struct {
	Command string `json:"command"`
	Events  int    `json:"events"`
}

// LockTimeline is the aggregated per-database contention time-series — the lock
// doctor's drill-down view. It turns a raw stream of lock observations into the
// three questions an operator actually asks: how often is it jammed, who jams
// it, and is the WAL checkpoint keeping up.
type LockTimeline struct {
	Source       string             `json:"source"`
	SinceMS      int64              `json:"since_ms"`
	Events       int                `json:"events"`
	FirstTS      int64              `json:"first_ts,omitempty"`
	LastTS       int64              `json:"last_ts,omitempty"`
	LockedEvents int                `json:"locked_events"`
	ErrorEvents  int                `json:"error_events"`
	MaxWaitMS    int64              `json:"max_wait_ms"`
	P95WaitMS    int64              `json:"p95_wait_ms"`
	Windows      []ContentionWindow `json:"windows,omitempty"`
	TopHolders   []HolderStat       `json:"top_holders,omitempty"`
	WALPeakBytes int64              `json:"wal_peak_bytes"`
	WALLastBytes int64              `json:"wal_last_bytes"`
	WALBloated   bool               `json:"wal_bloated"` // WAL ever crossed the bloat threshold
}

// BuildLockTimeline aggregates a source's lock events (oldest first, as returned
// by LockSeries) into a contention timeline. It is a pure function so it can be
// unit-tested without a store.
func BuildLockTimeline(source string, sinceMs int64, events []LockEvent) LockTimeline {
	t := LockTimeline{Source: source, SinceMS: sinceMs, Events: len(events)}
	if len(events) == 0 {
		return t
	}
	t.FirstTS = events[0].TS
	t.LastTS = events[len(events)-1].TS

	holderEvents := map[string]int{}
	var waits []int64
	var cur *ContentionWindow
	curHolders := map[string]bool{}

	closeWindow := func() {
		if cur == nil {
			return
		}
		cur.DurationMS = cur.EndTS - cur.StartTS
		for h := range curHolders {
			cur.Holders = append(cur.Holders, h)
		}
		sort.Strings(cur.Holders)
		t.Windows = append(t.Windows, *cur)
		cur = nil
		curHolders = map[string]bool{}
	}

	for i := range events {
		e := events[i]
		if e.WALBytes > t.WALPeakBytes {
			t.WALPeakBytes = e.WALBytes
		}
		if e.WALBytes >= walBloatBytes {
			t.WALBloated = true
		}
		switch e.State {
		case "locked":
			t.LockedEvents++
			waits = append(waits, e.WaitMS)
			if e.WaitMS > t.MaxWaitMS {
				t.MaxWaitMS = e.WaitMS
			}
			for _, h := range e.Holders {
				if h.Command != "" {
					holderEvents[h.Command]++
					curHolders[h.Command] = true
				}
			}
			if cur == nil {
				cur = &ContentionWindow{StartTS: e.TS, EndTS: e.TS}
			}
			cur.EndTS = e.TS
			cur.Events++
			if e.WaitMS > cur.PeakWaitMS {
				cur.PeakWaitMS = e.WaitMS
			}
		case "error":
			t.ErrorEvents++
			closeWindow()
		default: // "free" | "readable" — the database recovered
			closeWindow()
		}
	}
	closeWindow()

	// WALLastBytes reflects the most recent observation, so an operator sees the
	// current WAL size, not just the historical peak.
	t.WALLastBytes = events[len(events)-1].WALBytes
	t.P95WaitMS = percentile(waits, 95)

	for cmd, n := range holderEvents {
		t.TopHolders = append(t.TopHolders, HolderStat{Command: cmd, Events: n})
	}
	sort.Slice(t.TopHolders, func(i, j int) bool {
		if t.TopHolders[i].Events != t.TopHolders[j].Events {
			return t.TopHolders[i].Events > t.TopHolders[j].Events
		}
		return t.TopHolders[i].Command < t.TopHolders[j].Command
	})
	if len(t.TopHolders) > 5 {
		t.TopHolders = t.TopHolders[:5]
	}
	return t
}

// percentile returns the p-th percentile (nearest-rank) of vals. Returns 0 for
// an empty slice. vals is copied before sorting so the caller's order is kept.
func percentile(vals []int64, p int) int64 {
	if len(vals) == 0 {
		return 0
	}
	s := make([]int64, len(vals))
	copy(s, vals)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := (p * len(s)) / 100
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}
