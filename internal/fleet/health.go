package fleet

import (
	"sort"
	"sync"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/health"
)

// HealthResult is the operational health of one fleet member.
type HealthResult struct {
	Database string         `json:"database"`
	DSN      string         `json:"dsn"`
	Report   *health.Report `json:"report"`
	Duration time.Duration  `json:"-"`
}

// HealthReport aggregates fleet-wide health, sorted worst-first.
type HealthReport struct {
	Results   []HealthResult `json:"results"`
	CheckedAt time.Time      `json:"checked_at"`
}

// Counts tallies databases by severity.
func (r *HealthReport) Counts() (ok, warning, critical int) {
	for _, res := range r.Results {
		switch res.Report.Severity {
		case health.SevCritical:
			critical++
		case health.SevWarning:
			warning++
		default:
			ok++
		}
	}
	return
}

// HasFaults reports whether any database is in warning or critical state.
func (r *HealthReport) HasFaults() bool {
	_, w, c := r.Counts()
	return w > 0 || c > 0
}

// Health inspects every database in parallel for operational faults. Local
// files get the full inspection (integrity, WAL bloat, fragmentation, size);
// remote databases (Turso, D1) report reachability only, since file-level
// signals require local access.
func Health(dbs []Database, deep bool, concurrency int) *HealthReport {
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	results := make([]HealthResult, len(dbs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, db := range dbs {
		wg.Add(1)
		go func(i int, db Database) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = healthOne(db, deep)
		}(i, db)
	}
	wg.Wait()

	// Worst first (critical → warning → ok), then by name for stability.
	sort.Slice(results, func(i, j int) bool {
		si, sj := results[i].Report.Severity, results[j].Report.Severity
		if si != sj {
			return si > sj
		}
		return results[i].Database < results[j].Database
	})

	return &HealthReport{Results: results, CheckedAt: time.Now().UTC()}
}

func healthOne(db Database, deep bool) HealthResult {
	start := time.Now()
	res := HealthResult{Database: db.Name, DSN: db.DSN}

	if isLocalFileDSN(db.DSN) {
		res.Report = health.Inspect(db.DSN, deep)
		res.Duration = time.Since(start)
		return res
	}

	// Remote: the connector can only confirm reachability + load the schema.
	res.Report = remoteHealth(db.DSN)
	res.Duration = time.Since(start)
	return res
}

func remoteHealth(dsn string) *health.Report {
	r := &health.Report{Path: dsn, Remote: true, IntegrityOK: true}
	conn, err := connector.Open(dsn)
	if err != nil {
		r.Reachable = false
		r.Severity = health.SevCritical
		r.SeverityLabel = r.Severity.String()
		r.Error = err.Error()
		r.Issues = []string{"unreachable — " + err.Error()}
		return r
	}
	defer conn.Close()

	if _, err := conn.Schema(); err != nil {
		r.Reachable = false
		r.Severity = health.SevCritical
		r.SeverityLabel = r.Severity.String()
		r.Error = err.Error()
		r.Issues = []string{"unreachable — " + err.Error()}
		return r
	}

	r.Reachable = true
	r.Severity = health.SevOK
	r.SeverityLabel = r.Severity.String()
	r.Issues = []string{"remote — file-level health (integrity, WAL, bloat) needs local access"}
	return r
}
