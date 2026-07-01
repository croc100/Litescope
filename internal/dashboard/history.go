package dashboard

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// minSampleGap coalesces rapid refreshes: a new sample is only recorded once
// this much time has passed since the last one, so an impatient user mashing
// refresh does not flood the history with near-identical points.
const minSampleGap = 20 * time.Second

// maxSamples caps the ring buffer. At one sample per refresh this is plenty of
// history for the dashboard's trend view while keeping the file tiny.
const maxSamples = 20000

// Sample is one point on the fleet's health timeline.
type Sample struct {
	TS        int64 `json:"ts"` // unix milliseconds
	Total     int   `json:"total"`
	OK        int   `json:"ok"`
	Warning   int   `json:"warning"`
	Critical  int   `json:"critical"`
	SizeBytes int64 `json:"size_bytes"`
}

// History persists fleet-health snapshots to a local SQLite file. The store is
// itself a SQLite database — the monitoring history of a SQLite tool lives in a
// SQLite file, no external time-series infrastructure required. It is safe for
// concurrent use.
type History struct {
	db         *sql.DB
	mu         sync.Mutex
	lastTS     time.Time
	lockMu     sync.Mutex
	lastLockTS map[string]time.Time
}

// minLockEventGap coalesces rapid live-lock probes (e.g. from `locks --watch`
// polling every second) per source, so a sustained lock doesn't flood the
// store with near-duplicate rows.
const minLockEventGap = 5 * time.Second

// maxLockEvents caps the lock-event ring buffer, same rationale as maxSamples.
const maxLockEvents = 20000

// LockHolderInfo is a process holding a database file open, recorded alongside
// a lock event (mirrors locks.Holder, kept local to avoid an import cycle).
type LockHolderInfo struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// LockEvent is one point-in-time observation of a database's lock state —
// either an event-driven capture (a real SQLITE_BUSY hit during a litescope
// operation) or a `locks --watch` poll. Source of truth for the lock doctor's
// per-database contention timeline.
type LockEvent struct {
	TS      int64            `json:"ts"` // unix milliseconds
	Source  string           `json:"source"`
	State   string           `json:"state"` // "locked" | "readable" | "free" | "error"
	WaitMS  int64            `json:"wait_ms"`
	Holders []LockHolderInfo `json:"holders,omitempty"`
	Detail  string           `json:"detail,omitempty"`
}

// RecordLockEvent stores one lock observation for source, rate-limited per
// source by minLockEventGap. Free/steady-state events are also recorded (at
// the coarser rate) so the timeline shows recovery, not just contention.
func (h *History) RecordLockEvent(source, state string, waitMS int64, holders []LockHolderInfo, detail string) error {
	if h == nil || h.db == nil || source == "" {
		return nil
	}
	now := time.Now().UTC()
	h.lockMu.Lock()
	if last, ok := h.lastLockTS[source]; ok && now.Sub(last) < minLockEventGap {
		h.lockMu.Unlock()
		return nil
	}
	h.lastLockTS[source] = now
	h.lockMu.Unlock()

	var holdersJSON []byte
	if len(holders) > 0 {
		holdersJSON, _ = json.Marshal(holders)
	}
	if _, err := h.db.Exec(
		`INSERT INTO lock_events (ts, source, state, wait_ms, holders, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		now.UnixMilli(), source, state, waitMS, string(holdersJSON), detail); err != nil {
		return err
	}
	_, _ = h.db.Exec(
		`DELETE FROM lock_events WHERE id NOT IN
		 (SELECT id FROM lock_events ORDER BY id DESC LIMIT ?)`, maxLockEvents)
	return nil
}

// LockSeries returns lock events for source with ts >= sinceMs (0 for all),
// oldest first.
func (h *History) LockSeries(source string, sinceMs int64) ([]LockEvent, error) {
	if h == nil || h.db == nil {
		return nil, nil
	}
	rows, err := h.db.Query(
		`SELECT ts, source, state, wait_ms, holders, detail
		 FROM lock_events WHERE source = ? AND ts >= ? ORDER BY ts ASC`, source, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LockEvent
	for rows.Next() {
		var e LockEvent
		var holdersJSON, detail sql.NullString
		if err := rows.Scan(&e.TS, &e.Source, &e.State, &e.WaitMS, &holdersJSON, &detail); err != nil {
			return nil, err
		}
		e.Detail = detail.String
		if holdersJSON.String != "" {
			_ = json.Unmarshal([]byte(holdersJSON.String), &e.Holders)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OpenHistory opens (creating if needed) the SQLite history store at path.
func OpenHistory(path string) (*History, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// A single writer keeps the schema simple and avoids "database is locked".
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS fleet_samples (
			ts         INTEGER PRIMARY KEY,
			total      INTEGER NOT NULL,
			ok         INTEGER NOT NULL,
			warning    INTEGER NOT NULL,
			critical   INTEGER NOT NULL,
			size_bytes INTEGER NOT NULL
		)`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS lock_events (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			ts       INTEGER NOT NULL,
			source   TEXT NOT NULL,
			state    TEXT NOT NULL,
			wait_ms  INTEGER NOT NULL,
			holders  TEXT,
			detail   TEXT
		)`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS lock_events_source_ts ON lock_events (source, ts)`); err != nil {
		db.Close()
		return nil, err
	}

	h := &History{db: db, lastLockTS: map[string]time.Time{}}
	// Seed lastTS from the most recent persisted sample so a restart does not
	// immediately write a duplicate.
	var lastMs int64
	if err := db.QueryRow(`SELECT ts FROM fleet_samples ORDER BY ts DESC LIMIT 1`).Scan(&lastMs); err == nil {
		h.lastTS = time.UnixMilli(lastMs)
	}
	return h, nil
}

// Close releases the underlying database.
func (h *History) Close() error {
	if h == nil || h.db == nil {
		return nil
	}
	return h.db.Close()
}

// Record stores a snapshot derived from the overview. It is best-effort and
// rate-limited by minSampleGap; callers may ignore the returned error.
func (h *History) Record(ov *Overview) error {
	if h == nil || ov == nil || ov.Health == nil {
		return nil
	}
	now := time.Now().UTC()
	h.mu.Lock()
	if !h.lastTS.IsZero() && now.Sub(h.lastTS) < minSampleGap {
		h.mu.Unlock()
		return nil
	}
	h.lastTS = now
	h.mu.Unlock()

	ok, warning, critical := ov.Health.Counts()
	var size int64
	for _, res := range ov.Health.Results {
		if res.Report != nil {
			size += res.Report.SizeBytes
		}
	}

	if _, err := h.db.Exec(
		`INSERT OR REPLACE INTO fleet_samples (ts, total, ok, warning, critical, size_bytes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		now.UnixMilli(), ov.Total, ok, warning, critical, size); err != nil {
		return err
	}
	// Prune to the ring-buffer cap.
	_, _ = h.db.Exec(
		`DELETE FROM fleet_samples WHERE ts NOT IN
		 (SELECT ts FROM fleet_samples ORDER BY ts DESC LIMIT ?)`, maxSamples)
	return nil
}

// Series returns samples with ts >= sinceMs (0 for all), oldest first.
func (h *History) Series(sinceMs int64) ([]Sample, error) {
	if h == nil {
		return nil, nil
	}
	rows, err := h.db.Query(
		`SELECT ts, total, ok, warning, critical, size_bytes
		 FROM fleet_samples WHERE ts >= ? ORDER BY ts ASC`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var s Sample
		if err := rows.Scan(&s.TS, &s.Total, &s.OK, &s.Warning, &s.Critical, &s.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
