package fleet

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/croc100/litescope/internal/health"
)

func TestSendHealthAlert(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p map[string]interface{}
		json.Unmarshal(b, &p)
		got, _ = p["text"].(string)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	report := healthReport(map[string]health.Severity{
		"good": health.SevOK,
		"bad":  health.SevCritical,
	})
	// give the critical one an issue
	for i := range report.Results {
		if report.Results[i].Database == "bad" {
			report.Results[i].Report.Issues = []string{"CORRUPT — file is not a database"}
		}
	}

	if err := SendHealthAlert(srv.URL, report); err != nil {
		t.Fatalf("SendHealthAlert: %v", err)
	}
	if !strings.Contains(got, "1 critical") || !strings.Contains(got, "bad") {
		t.Errorf("alert text missing summary/db: %q", got)
	}
}

func TestSendHealthAlert_NoOpWhenHealthy(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	report := healthReport(map[string]health.Severity{"a": health.SevOK})
	if err := SendHealthAlert(srv.URL, report); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Errorf("a healthy fleet must not POST an alert")
	}
}
