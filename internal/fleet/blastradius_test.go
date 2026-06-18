package fleet

import (
	"testing"
	"time"

	"github.com/croc100/litescope/internal/health"
)

// healthReport builds a HealthReport from name→severity for testing.
func healthReport(sev map[string]health.Severity) *HealthReport {
	r := &HealthReport{CheckedAt: time.Now()}
	for name, s := range sev {
		r.Results = append(r.Results, HealthResult{
			Database: name,
			Report:   &health.Report{Severity: s, SeverityLabel: s.String()},
		})
	}
	return r
}

func TestBlastRadius(t *testing.T) {
	dbs := []Database{
		{Name: "t1", Tags: []string{"group:prod"}},
		{Name: "t2", Tags: []string{"group:prod"}},
		{Name: "t3", Tags: []string{"group:prod"}},
		{Name: "t4", Tags: []string{"group:staging"}},
	}
	report := healthReport(map[string]health.Severity{
		"t1": health.SevOK,
		"t2": health.SevCritical, // fault in prod
		"t3": health.SevOK,
		"t4": health.SevOK,
	})

	groups := BlastRadius(dbs, report)
	if len(groups) != 1 {
		t.Fatalf("expected 1 affected cohort, got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Tag != "group:prod" {
		t.Errorf("tag = %s, want group:prod", g.Tag)
	}
	if g.Total != 3 || len(g.Faulted) != 1 || len(g.AtRisk) != 2 {
		t.Errorf("total=%d faulted=%v atRisk=%v", g.Total, g.Faulted, g.AtRisk)
	}
	if g.Faulted[0] != "t2" {
		t.Errorf("faulted = %v, want [t2]", g.Faulted)
	}
}

func TestBlastRadius_NoFaults(t *testing.T) {
	dbs := []Database{{Name: "a", Tags: []string{"group:x"}}}
	report := healthReport(map[string]health.Severity{"a": health.SevOK})
	if g := BlastRadius(dbs, report); len(g) != 0 {
		t.Errorf("no faults should yield no cohorts, got %+v", g)
	}
}

func TestBlastRadius_WarningNotCounted(t *testing.T) {
	// Only critical faults define a blast radius; warnings (bloat) don't.
	dbs := []Database{{Name: "a", Tags: []string{"group:x"}}}
	report := healthReport(map[string]health.Severity{"a": health.SevWarning})
	if g := BlastRadius(dbs, report); len(g) != 0 {
		t.Errorf("warning-only should not trigger blast radius, got %+v", g)
	}
}

func TestBlastRadius_SortedByFaultCount(t *testing.T) {
	dbs := []Database{
		{Name: "a", Tags: []string{"g1"}},
		{Name: "b", Tags: []string{"g1"}},
		{Name: "c", Tags: []string{"g2"}},
	}
	report := healthReport(map[string]health.Severity{
		"a": health.SevCritical, "b": health.SevCritical, "c": health.SevCritical,
	})
	groups := BlastRadius(dbs, report)
	if len(groups) != 2 || groups[0].Tag != "g1" {
		t.Errorf("expected g1 first (2 faults), got %+v", groups)
	}
}
