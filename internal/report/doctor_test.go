package report

import (
	"strings"
	"testing"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/lint"
)

func TestDoctor_HTML(t *testing.T) {
	data := DoctorData{
		Path:    "app.db",
		Verdict: "attention",
		Health: &health.Report{
			Reachable: true, IntegrityOK: true, SizeBytes: 12288,
			JournalMode: "wal", SeverityLabel: "warning",
		},
		Advisor: &advisor.Report{Findings: []advisor.Finding{
			{Rule: "fk-no-index", Severity: "warning", Table: "orders",
				Detail: "foreign key on (user_id) has no index", Suggestion: "CREATE INDEX ..."},
		}},
		Lint: &lint.Report{Findings: []lint.Finding{
			{Rule: "no-primary-key", Severity: lint.SevWarning, Table: "users",
				Detail: "table has no PRIMARY KEY"},
		}},
		Warnings: 2,
	}

	var b strings.Builder
	if err := Doctor(&b, data); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	for _, want := range []string{
		"<!DOCTYPE html>",
		"litescope doctor — app.db",
		`class="verdict attention"`,
		"Needs attention",
		"12.0 KB",
		"fk-no-index",
		"foreign key on (user_id) has no index",
		"CREATE INDEX",
		"no-primary-key",
		"app.db</title>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestDoctor_EscapesHTML(t *testing.T) {
	data := DoctorData{
		Path:    "a<script>b.db",
		Verdict: "healthy",
		Lint: &lint.Report{Findings: []lint.Finding{
			{Rule: "x", Severity: lint.SevInfo, Detail: "<img src=x onerror=alert(1)>"},
		}},
	}
	var b strings.Builder
	if err := Doctor(&b, data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "<script>b.db") || strings.Contains(out, "<img src=x") {
		t.Errorf("report did not escape user content:\n%s", out)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1024: "1.0 KB", 12288: "12.0 KB", 1048576: "1.0 MB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
