package mcp

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newHTTPTestServer starts the Streamable HTTP transport on an httptest server.
func newHTTPTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := &httpTransport{version: "test", sessions: map[string]*httpSession{}}
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, session, body string) (*http.Response, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var decoded map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	resp.Body.Close()
	return resp, decoded
}

// newHTTPTestServerOpts starts the transport with a custom auth configuration.
func newHTTPTestServerOpts(t *testing.T, token string, origins ...string) *httptest.Server {
	t.Helper()
	om := map[string]bool{}
	for _, o := range origins {
		om[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	h := &httpTransport{version: "test", token: token, origins: om, sessions: map[string]*httpSession{}}
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTP_BearerTokenEnforced(t *testing.T) {
	srv := newHTTPTestServerOpts(t, "s3cret")
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`

	// No token → 401.
	resp, _ := post(t, srv.URL+"/mcp", "", init)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("missing token should be 401, got %d", resp.StatusCode)
	}

	// Wrong token → 401.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
	req.Header.Set("Authorization", "Bearer nope")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token should be 401, got %d", r.StatusCode)
	}

	// Correct token → ok, session minted.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
	req2.Header.Set("Authorization", "Bearer s3cret")
	r2, _ := http.DefaultClient.Do(req2)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK || r2.Header.Get("Mcp-Session-Id") == "" {
		t.Errorf("correct token should mint a session, got %d", r2.StatusCode)
	}
}

func TestHTTP_OriginRejected(t *testing.T) {
	srv := newHTTPTestServerOpts(t, "", "https://app.example.com")
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`

	// Disallowed Origin → 403.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
	req.Header.Set("Origin", "https://evil.example.com")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Errorf("disallowed origin should be 403, got %d", r.StatusCode)
	}

	// Allowlisted Origin → ok.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
	req2.Header.Set("Origin", "https://app.example.com")
	r2, _ := http.DefaultClient.Do(req2)
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("allowlisted origin should be 200, got %d", r2.StatusCode)
	}

	// localhost Origin is always allowed.
	req3, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
	req3.Header.Set("Origin", "http://localhost:3000")
	r3, _ := http.DefaultClient.Do(req3)
	r3.Body.Close()
	if r3.StatusCode != http.StatusOK {
		t.Errorf("localhost origin should be 200, got %d", r3.StatusCode)
	}
}

func TestHTTP_InitializeMintsSession(t *testing.T) {
	srv := newHTTPTestServer(t)
	resp, body := post(t, srv.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if sid := resp.Header.Get("Mcp-Session-Id"); sid == "" {
		t.Fatal("initialize did not return Mcp-Session-Id header")
	}
	res := body["result"].(map[string]interface{})
	if res["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", res["protocolVersion"])
	}
}

func TestHTTP_RequiresSessionAfterInitialize(t *testing.T) {
	srv := newHTTPTestServer(t)
	resp, _ := post(t, srv.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("tools/list without a session should be 404, got %d", resp.StatusCode)
	}
}

func TestHTTP_ToolsListWithSession(t *testing.T) {
	srv := newHTTPTestServer(t)
	resp, _ := post(t, srv.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	sid := resp.Header.Get("Mcp-Session-Id")

	_, body := post(t, srv.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools, ok := body["result"].(map[string]interface{})["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		t.Fatalf("expected a non-empty tools list, got: %v", body)
	}
}

func TestHTTP_NotificationReturns202(t *testing.T) {
	srv := newHTTPTestServer(t)
	resp, _ := post(t, srv.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	sid := resp.Header.Get("Mcp-Session-Id")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Mcp-Session-Id", sid)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusAccepted {
		t.Errorf("notification should return 202, got %d", r.StatusCode)
	}
}

func TestHTTP_DeleteTerminatesSession(t *testing.T) {
	srv := newHTTPTestServer(t)
	resp, _ := post(t, srv.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	sid := resp.Header.Get("Mcp-Session-Id")

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/mcp", nil)
	req.Header.Set("Mcp-Session-Id", sid)
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE should return 204, got %d", r.StatusCode)
	}

	// The session is gone: a follow-up request must 404.
	after, _ := post(t, srv.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if after.StatusCode != http.StatusNotFound {
		t.Errorf("request after DELETE should be 404, got %d", after.StatusCode)
	}
}

func TestHTTP_SSEDeliversNotifications(t *testing.T) {
	srv := newHTTPTestServer(t)
	resp, _ := post(t, srv.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	sid := resp.Header.Get("Mcp-Session-Id")

	// Open the SSE stream.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp", nil)
	req.Header.Set("Mcp-Session-Id", sid)
	streamResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer streamResp.Body.Close()
	if ct := streamResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", ct)
	}

	// Trigger a notification: debug-level logging + a tool call.
	post(t, srv.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":2,"method":"logging/setLevel","params":{"level":"debug"}}`)
	post(t, srv.URL+"/mcp", sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"litescope_d1_list","arguments":{}}}`)

	// Read the first SSE data line; it should be a notifications/message.
	reader := bufio.NewReader(streamResp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading SSE: %v", err)
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &m); err != nil {
			t.Fatalf("bad SSE payload %q: %v", line, err)
		}
		if m["method"] == "notifications/message" {
			return // success
		}
	}
}
