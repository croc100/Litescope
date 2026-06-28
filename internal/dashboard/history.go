package dashboard

import (
	"database/sql"
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
	db     *sql.DB
	mu     sync.Mutex
	lastTS time.Time
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
	h := &History{db: db}
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
