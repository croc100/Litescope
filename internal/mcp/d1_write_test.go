package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/croc100/litescope/internal/connector"
)

// runWrites feeds requests through Serve with writes enabled.
func runWrites(t *testing.T, requests ...string) map[float64]map[string]interface{} {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(in, &out, "test", true, ""); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := map[float64]map[string]interface{}{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		if id, ok := m["id"].(float64); ok {
			resps[id] = m
		}
	}
	return resps
}

func toolText(t *testing.T, resp map[string]interface{}) (string, bool) {
	t.Helper()
	res, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("no result in %v", resp)
	}
	text := res["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	return text, res["isError"] == true
}

// TestD1ReversibleWriteContract exercises the full agent write loop against a
// stub Cloudflare API: apply=true captures a pre-write bookmark and returns it
// as a rewind_token, and litescope_write_undo restores exactly that bookmark.
func TestD1ReversibleWriteContract(t *testing.T) {
	var restoredBookmark string
	var executedSQL []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/time_travel/bookmark"):
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true, "result": map[string]string{"bookmark": "bm-pre-write"},
			})
		case strings.HasSuffix(r.URL.Path, "/time_travel/restore"):
			var req map[string]string
			json.NewDecoder(r.Body).Decode(&req)
			restoredBookmark = req["bookmark"]
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  map[string]string{"bookmark": "bm-post-restore", "timestamp": "2026-07-02T00:00:00Z"},
			})
		case strings.HasSuffix(r.URL.Path, "/query"):
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			executedSQL = append(executedSQL, req["sql"].(string))
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  []map[string]interface{}{{"success": true, "results": []interface{}{}}},
			})
		default:
			t.Errorf("unexpected API call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := connector.APIBase
	connector.APIBase = srv.URL
	t.Cleanup(func() { connector.APIBase = old })
	t.Setenv("CLOUDFLARE_API_TOKEN", "test-token")

	// 1. Guarded write with apply=true.
	write := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"litescope_query_write","arguments":{"source":"d1://acct/dbid","sql":"DELETE FROM users","apply":true}}}`
	text, isErr := toolText(t, runWrites(t, write)[1])
	if isErr {
		t.Fatalf("query_write errored: %s", text)
	}
	var res struct {
		RewindToken string `json:"rewind_token"`
		Applied     bool   `json:"applied"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Applied || res.RewindToken == "" {
		t.Fatalf("expected applied write with rewind_token, got: %s", text)
	}
	if len(executedSQL) != 1 || executedSQL[0] != "DELETE FROM users" {
		t.Errorf("executed SQL = %v", executedSQL)
	}

	// 2. Undo with the returned token.
	undo := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"litescope_write_undo","arguments":{"source":"d1://acct/dbid","rewind_token":"` + res.RewindToken + `"}}}`
	text, isErr = toolText(t, runWrites(t, undo)[2])
	if isErr {
		t.Fatalf("write_undo errored: %s", text)
	}
	if restoredBookmark != "bm-pre-write" {
		t.Errorf("restored bookmark = %q, want bm-pre-write", restoredBookmark)
	}

	// 3. The same token against a different database is refused.
	wrong := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"litescope_write_undo","arguments":{"source":"d1://acct/other-db","rewind_token":"` + res.RewindToken + `"}}}`
	text, isErr = toolText(t, runWrites(t, wrong)[3])
	if !isErr {
		t.Fatalf("expected token/source mismatch to be an error, got: %s", text)
	}
	if !strings.Contains(text, "refusing") {
		t.Errorf("expected a refusal message, got: %s", text)
	}
}

// TestD1WriteRefusedWhenBookmarkUnavailable: if the pre-write bookmark cannot
// be captured, the write must not execute at all.
func TestD1WriteRefusedWhenBookmarkUnavailable(t *testing.T) {
	queryCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/query") {
			queryCalled = true
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors":  []map[string]interface{}{{"code": 7500, "message": "bookmark unavailable"}},
		})
	}))
	defer srv.Close()
	old := connector.APIBase
	connector.APIBase = srv.URL
	t.Cleanup(func() { connector.APIBase = old })
	t.Setenv("CLOUDFLARE_API_TOKEN", "test-token")

	write := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"litescope_query_write","arguments":{"source":"d1://acct/dbid","sql":"DELETE FROM users","apply":true}}}`
	text, isErr := toolText(t, runWrites(t, write)[1])
	if !isErr {
		t.Fatalf("expected error when bookmark capture fails, got: %s", text)
	}
	if !strings.Contains(text, "write not executed") {
		t.Errorf("expected 'write not executed' in error, got: %s", text)
	}
	if queryCalled {
		t.Error("write executed despite bookmark capture failure")
	}
}
