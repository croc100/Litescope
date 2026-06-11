package fleet

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/monitor"
)

// defaultConcurrency bounds how many databases are queried at once.
const defaultConcurrency = 8

// CheckResult is the drift outcome for a single fleet member.
type CheckResult struct {
	Database string `json:"database"`
	DSN      string `json:"dsn"`
	// State is one of: "ok", "drift", "no-baseline", "error".
	State    string               `json:"state"`
	Drift    *monitor.DriftResult `json:"drift,omitempty"`
	Err      error                `json:"-"`
	Error    string               `json:"error,omitempty"`
	Duration time.Duration        `json:"-"`
}

// FleetReport aggregates per-database results.
type FleetReport struct {
	Results   []CheckResult `json:"results"`
	CheckedAt time.Time     `json:"checked_at"`
}

// Counts returns how many databases fell into each state.
func (r *FleetReport) Counts() (ok, drift, noBaseline, errCount int) {
	for _, res := range r.Results {
		switch res.State {
		case "ok":
			ok++
		case "drift":
			drift++
		case "no-baseline":
			noBaseline++
		case "error":
			errCount++
		}
	}
	return
}

// HasProblems reports whether any database drifted or errored.
func (r *FleetReport) HasProblems() bool {
	_, drift, _, errCount := r.Counts()
	return drift > 0 || errCount > 0
}

// Check runs a drift check across every database in parallel, comparing each
// live schema against its baseline snapshot. concurrency <= 0 uses the default.
func Check(cfg *Config, dbs []Database, concurrency int) *FleetReport {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	results := make([]CheckResult, len(dbs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, db := range dbs {
		wg.Add(1)
		go func(i int, db Database) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = checkOne(cfg, db)
		}(i, db)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].Database < results[j].Database
	})
	return &FleetReport{Results: results, CheckedAt: time.Now().UTC()}
}

func checkOne(cfg *Config, db Database) CheckResult {
	start := time.Now()
	res := CheckResult{Database: db.Name, DSN: db.DSN}

	baselinePath := cfg.BaselinePath(db)
	snap, err := monitor.Load(baselinePath)
	if err != nil {
		res.State = "no-baseline"
		res.Duration = time.Since(start)
		return res
	}

	conn, err := connector.Open(db.DSN)
	if err != nil {
		res.State = "error"
		res.Err = err
		res.Error = err.Error()
		res.Duration = time.Since(start)
		return res
	}
	defer conn.Close()

	live, err := conn.Schema()
	if err != nil {
		res.State = "error"
		res.Err = err
		res.Error = err.Error()
		res.Duration = time.Since(start)
		return res
	}

	drift := monitor.Check(db.DSN, snap, live)
	res.Drift = drift
	if drift.HasDrift {
		res.State = "drift"
	} else {
		res.State = "ok"
	}
	res.Duration = time.Since(start)
	return res
}

// SnapshotResult is the baseline-capture outcome for one database.
type SnapshotResult struct {
	Database string
	Path     string
	Tables   int
	Err      error
}

// Snapshot captures a baseline for every database in parallel, writing each to
// its configured baseline path. concurrency <= 0 uses the default.
func Snapshot(cfg *Config, dbs []Database, concurrency int) []SnapshotResult {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	results := make([]SnapshotResult, len(dbs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, db := range dbs {
		wg.Add(1)
		go func(i int, db Database) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = snapshotOne(cfg, db)
		}(i, db)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		return results[i].Database < results[j].Database
	})
	return results
}

func snapshotOne(cfg *Config, db Database) SnapshotResult {
	res := SnapshotResult{Database: db.Name, Path: cfg.BaselinePath(db)}

	conn, err := connector.Open(db.DSN)
	if err != nil {
		res.Err = err
		return res
	}
	defer conn.Close()

	live, err := conn.Schema()
	if err != nil {
		res.Err = err
		return res
	}

	snap := &monitor.Snapshot{
		Source:     db.DSN,
		CapturedAt: time.Now().UTC(),
		Schema:     live,
	}
	if err := os.MkdirAll(filepath.Dir(res.Path), 0755); err != nil {
		res.Err = err
		return res
	}
	if err := monitor.Save(res.Path, snap); err != nil {
		res.Err = err
		return res
	}
	if live != nil {
		res.Tables = len(live.Tables)
	}
	return res
}
