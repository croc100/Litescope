package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/health"
)

func TestRender(t *testing.T) {
	fr := &fleet.HealthReport{
		CheckedAt: time.Now(),
		Results: []fleet.HealthResult{
			{Database: "acme", Report: &health.Report{
				Reachable: true, IntegrityOK: true, Severity: health.SevOK,
				SizeBytes: 20480, WALBytes: 0, FreelistCount: 0,
			}},
			{Database: "broken", Report: &health.Report{
				Reachable: true, IntegrityOK: false, Severity: health.SevCritical,
				SizeBytes: 4096,
			}},
		},
	}
	fp := &fleet.FingerprintReport{
		Clusters: []fleet.FingerprintCluster{
			{ID: "aaaa", Count: 1, Members: []string{"acme"}, IsCanonical: true},
			{ID: "bbbb", Count: 1, Members: []string{"drifted-db"}, IsCanonical: false},
		},
	}

	out := Render(fr, fp)

	wants := []string{
		"# TYPE litescope_database_severity gauge",
		`litescope_database_severity{database="acme"} 0`,
		`litescope_database_severity{database="broken"} 2`,
		`litescope_database_integrity_ok{database="broken"} 0`,
		`litescope_database_size_bytes{database="acme"} 20480`,
		`litescope_fleet_databases{severity="critical"} 1`,
		`litescope_fleet_databases{severity="ok"} 1`,
		"litescope_fleet_databases_total 2",
		"litescope_fleet_schema_clusters 2",
		"litescope_fleet_drifted_databases_total 1",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestRenderNoFingerprint(t *testing.T) {
	fr := &fleet.HealthReport{Results: []fleet.HealthResult{
		{Database: "x", Report: &health.Report{Reachable: true, IntegrityOK: true}},
	}}
	out := Render(fr, nil)
	if strings.Contains(out, "litescope_database_drifted") {
		t.Error("drift metric should be absent when fp is nil")
	}
	if strings.Contains(out, "litescope_fleet_schema_clusters") {
		t.Error("schema cluster metric should be absent when fp is nil")
	}
}

func TestEscapeLabel(t *testing.T) {
	if got := escapeLabel(`a"b\c`); got != `a\"b\\c` {
		t.Errorf("escapeLabel = %q", got)
	}
}
