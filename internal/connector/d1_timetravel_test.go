package connector

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockD1API points APIBase at a stub Cloudflare API for the duration of a test.
func mockD1API(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = old })
	t.Setenv("CLOUDFLARE_API_TOKEN", "test-token")
}

func TestD1CurrentBookmark(t *testing.T) {
	mockD1API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if want := "/accounts/acct/d1/database/dbid/time_travel/bookmark"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth = %q", got)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  map[string]string{"bookmark": "bm-42"},
		})
	})

	bm, err := D1CurrentBookmark("acct", "dbid")
	if err != nil {
		t.Fatal(err)
	}
	if bm != "bm-42" {
		t.Errorf("bookmark = %q, want bm-42", bm)
	}
}

func TestD1CurrentBookmark_APIError(t *testing.T) {
	mockD1API(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors":  []map[string]interface{}{{"code": 7500, "message": "boom"}},
		})
	})
	if _, err := D1CurrentBookmark("acct", "dbid"); err == nil {
		t.Fatal("expected error from unsuccessful API response")
	}
}

func TestD1TimeTravelToBookmark(t *testing.T) {
	mockD1API(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if want := "/accounts/acct/d1/database/dbid/time_travel/restore"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		if err := json.Unmarshal(body, &req); err != nil || req["bookmark"] != "bm-42" {
			t.Errorf("body = %s, want bookmark bm-42", body)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  map[string]string{"bookmark": "bm-43", "timestamp": "2026-07-02T00:00:00Z"},
		})
	})

	res, err := D1TimeTravelToBookmark("acct", "dbid", "bm-42")
	if err != nil {
		t.Fatal(err)
	}
	if res.Bookmark != "bm-43" {
		t.Errorf("bookmark = %q, want bm-43", res.Bookmark)
	}
}
