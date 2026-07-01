package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
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

// ApplyStaleness flags databases that haven't been written to within maxIdle
// (a dead-man's-switch for an app that quietly stopped reporting) and
// re-sorts the report worst-first to reflect any newly raised severities.
// No-op when maxIdle is zero/negative.
func (r *HealthReport) ApplyStaleness(maxIdle time.Duration) {
	if maxIdle <= 0 {
		return
	}
	for i := range r.Results {
		r.Results[i].Report.CheckStaleness(maxIdle)
	}
	sort.Slice(r.Results, func(i, j int) bool {
		si, sj := r.Results[i].Report.Severity, r.Results[j].Report.Severity
		if si != sj {
			return si > sj
		}
		return r.Results[i].Database < r.Results[j].Database
	})
}

// SendHealthAlert POSTs a fault summary to a webhook (Slack-compatible when the
// URL points at Slack, otherwise generic JSON). It is a no-op when the fleet is
// healthy. Used by scheduled/continuous health watch.
func SendHealthAlert(webhookURL string, r *HealthReport) error {
	if !r.HasFaults() {
		return nil
	}
	ok, warning, critical := r.Counts()
	var lines []string
	for _, res := range r.Results {
		if res.Report.Severity == health.SevCritical || res.Report.Severity == health.SevWarning {
			issue := "fault"
			if len(res.Report.Issues) > 0 {
				issue = strings.Join(res.Report.Issues, "; ")
			}
			lines = append(lines, fmt.Sprintf("• `%s` — %s", res.Database, issue))
		}
	}
	text := fmt.Sprintf("⚠️ Litescope fleet health: %d critical, %d warning, %d healthy\n%s",
		critical, warning, ok, strings.Join(lines, "\n"))

	payload, err := json.Marshal(map[string]interface{}{"text": text})
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
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
