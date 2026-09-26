package mcp

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// modernMeta is the per-request metadata every modern request carries.
const modernMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
	`"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},` +
	`"io.modelcontextprotocol/clientCapabilities":{}}`

func modernReq(id int, method, extra string) string {
	params := "{" + modernMeta
	if extra != "" {
		params += "," + extra
	}
	params += "}"
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
}

func resultOf(t *testing.T, msg map[string]interface{}) map[string]interface{} {
	t.Helper()
	res, ok := msg["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("no result in response: %v", msg)
	}
	return res
}

func errorOf(t *testing.T, msg map[string]interface{}) map[string]interface{} {
	t.Helper()
	e, ok := msg["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected an error response, got: %v", msg)
	}
	return e
}

// serverInfo returns the server identity a modern result must carry in _meta.
func serverInfoOf(t *testing.T, res map[string]interface{}) map[string]interface{} {
	t.Helper()
	meta, ok := res["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("result carries no _meta: %v", res)
	}
	info, ok := meta[metaServerInfo].(map[string]interface{})
	if !ok {
		t.Fatalf("_meta carries no %s: %v", metaServerInfo, meta)
	}
	return info
}

// A bare server/discover — the backward-compatibility probe a client sends
// before it knows which era the server speaks — must be answered even without
// per-request metadata.
func TestDiscover_BareProbe(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`)
	res := resultOf(t, r[1])

	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", res["resultType"])
	}
	versions, _ := res["supportedVersions"].([]interface{})
	if len(versions) == 0 || versions[0] != protocolVersion {
		t.Errorf("supportedVersions = %v, want %s first", versions, protocolVersion)
	}
	var sawLegacy bool
	for _, v := range versions {
		if v == legacyVersion {
			sawLegacy = true
		}
	}
	if !sawLegacy {
		t.Errorf("supportedVersions = %v, want the handshake-era revision advertised too", versions)
	}
	caps, _ := res["capabilities"].(map[string]interface{})
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities missing tools: %v", caps)
	}
	if _, ok := caps["logging"]; ok {
		t.Errorf("capabilities advertise deprecated logging to a modern client: %v", caps)
	}
	if info := serverInfoOf(t, res); info["name"] != "litescope" {
		t.Errorf("serverInfo name = %v", info["name"])
	}
	if res["ttlMs"] == nil || res["cacheScope"] != "public" {
		t.Errorf("discover result missing caching hints: ttlMs=%v cacheScope=%v", res["ttlMs"], res["cacheScope"])
	}
}

func TestModern_ListsCarryResultTypeAndCacheHints(t *testing.T) {
	r := run(t,
		modernReq(1, "tools/list", ""),
		modernReq(2, "prompts/list", ""),
		modernReq(3, "resources/templates/list", ""),
	)
	for _, id := range []float64{1, 2, 3} {
		res := resultOf(t, r[id])
		if res["resultType"] != "complete" {
			t.Errorf("id %v: resultType = %v, want complete", id, res["resultType"])
		}
		if res["ttlMs"] != float64(hourMs) {
			t.Errorf("id %v: ttlMs = %v, want %d", id, res["ttlMs"], hourMs)
		}
		if res["cacheScope"] != "public" {
			t.Errorf("id %v: cacheScope = %v, want public", id, res["cacheScope"])
		}
		serverInfoOf(t, res)
	}
}

// Database content is never shareable between callers, and a live diagnosis is
// never cacheable at all.
func TestModern_ResourceReadCacheScope(t *testing.T) {
	path := t.TempDir() + "/cache.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)")
	db.Close()

	r := run(t,
		modernReq(1, "resources/read", fmt.Sprintf(`"uri":%q`, schemaScheme+path)),
		modernReq(2, "resources/read", fmt.Sprintf(`"uri":%q`, healthScheme+path)),
	)
	schemaRes := resultOf(t, r[1])
	if schemaRes["cacheScope"] != "private" {
		t.Errorf("schema cacheScope = %v, want private", schemaRes["cacheScope"])
	}
	if ttl, _ := schemaRes["ttlMs"].(float64); ttl <= 0 {
		t.Errorf("schema ttlMs = %v, want a positive freshness hint", schemaRes["ttlMs"])
	}
	liveRes := resultOf(t, r[2])
	if liveRes["cacheScope"] != "private" {
		t.Errorf("health cacheScope = %v, want private", liveRes["cacheScope"])
	}
	if liveRes["ttlMs"] != float64(0) {
		t.Errorf("health ttlMs = %v, want 0 (never cache a live verdict)", liveRes["ttlMs"])
	}
}

func TestModern_UnsupportedVersion(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"1900-01-01",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
	e := errorOf(t, run(t, req)[1])
	if e["code"] != float64(errUnsupportedVersion) {
		t.Fatalf("code = %v, want %d", e["code"], errUnsupportedVersion)
	}
	data, _ := e["data"].(map[string]interface{})
	supported, _ := data["supported"].([]interface{})
	if len(supported) == 0 {
		t.Errorf("error data must list supported versions so the client can retry: %v", data)
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("requested = %v", data["requested"])
	}
}

// clientCapabilities is required on every modern request; without it the
// server cannot know what the client supports, so the request is malformed.
func TestModern_MissingClientCapabilities(t *testing.T) {
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	e := errorOf(t, run(t, req)[1])
	if e["code"] != float64(-32602) {
		t.Errorf("code = %v, want -32602", e["code"])
	}
}

// ping, logging/setLevel and resources/subscribe were removed in 2026-07-28.
func TestModern_RemovedMethods(t *testing.T) {
	r := run(t,
		modernReq(1, "ping", ""),
		modernReq(2, "logging/setLevel", `"level":"debug"`),
		modernReq(3, "resources/subscribe", `"uri":"litescope://schema/x.db"`),
		modernReq(4, "initialize", ""),
	)
	for _, id := range []float64{1, 2, 3, 4} {
		e := errorOf(t, r[id])
		if e["code"] != float64(-32601) {
			t.Errorf("id %v: code = %v, want -32601 (method not found)", id, e["code"])
		}
	}
}

// The handshake era keeps working, and its results stay exactly as they were:
// no resultType, no caching hints, no _meta.
func TestLegacy_ResultsUnchanged(t *testing.T) {
	r := run(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	)
	if res := resultOf(t, r[1]); res["protocolVersion"] != legacyVersion {
		t.Errorf("negotiated version = %v", res["protocolVersion"])
	}
	res := resultOf(t, r[2])
	for _, k := range []string{"resultType", "ttlMs", "cacheScope", "_meta"} {
		if _, ok := res[k]; ok {
			t.Errorf("legacy tools/list result carries modern field %q", k)
		}
	}
	if _, ok := r[3]["result"]; !ok {
		t.Errorf("ping must still answer in the handshake era: %v", r[3])
	}
}

// subscriptions/listen acknowledges first, tags every message with the
// subscription id, and sends no response while the stream is open.
func TestModern_SubscriptionsListen(t *testing.T) {
	listen := modernReq(7, "subscriptions/listen",
		`"notifications":{"resourceSubscriptions":["litescope://schema/x.db"],"toolsListChanged":true}`)
	msgs := runAll(t, "", listen)

	var ack map[string]interface{}
	for _, m := range msgs {
		if m["method"] == "notifications/subscriptions/acknowledged" {
			ack = m
		}
		if id, ok := m["id"].(float64); ok && id == 7 {
			t.Errorf("listen request must stay open, got a response: %v", m)
		}
	}
	if ack == nil {
		t.Fatalf("no acknowledgement for subscriptions/listen: %v", msgs)
	}
	params, _ := ack["params"].(map[string]interface{})
	meta, _ := params["_meta"].(map[string]interface{})
	if meta[metaSubscriptionID] != float64(7) {
		t.Errorf("subscriptionId = %v, want the listen request id 7", meta[metaSubscriptionID])
	}
	honored, _ := params["notifications"].(map[string]interface{})
	if _, ok := honored["resourceSubscriptions"]; !ok {
		t.Errorf("acknowledgement dropped resourceSubscriptions: %v", honored)
	}
	// We never change the tool list, so we must not claim to honour it.
	if _, ok := honored["toolsListChanged"]; ok {
		t.Errorf("acknowledged a notification type we never emit: %v", honored)
	}
}

// ── Streamable HTTP ─────────────────────────────────────────────────────────

// postModern sends a modern request with the standard headers, letting a test
// override any of them.
func postModern(t *testing.T, url, body string, headers map[string]string) (*http.Response, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
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

func TestHTTPModern_StatelessCall(t *testing.T) {
	srv := newHTTPTestServer(t)
	body := modernReq(1, "tools/list", "")
	resp, decoded := postModern(t, srv.URL+"/mcp", body, map[string]string{
		"MCP-Protocol-Version": protocolVersion,
		"Mcp-Method":           "tools/list",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %v)", resp.StatusCode, decoded)
	}
	if got := resp.Header.Get("Mcp-Session-Id"); got != "" {
		t.Errorf("stateless request minted a session id %q", got)
	}
	res := resultOf(t, decoded)
	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v", res["resultType"])
	}
}

func TestHTTPModern_HeaderValidation(t *testing.T) {
	srv := newHTTPTestServer(t)
	cases := []struct {
		name    string
		body    string
		headers map[string]string
	}{
		{"missing protocol version header", modernReq(1, "tools/list", ""),
			map[string]string{"Mcp-Method": "tools/list"}},
		{"protocol version mismatch", modernReq(1, "tools/list", ""),
			map[string]string{"MCP-Protocol-Version": "2025-06-18", "Mcp-Method": "tools/list"}},
		{"missing method header", modernReq(1, "tools/list", ""),
			map[string]string{"MCP-Protocol-Version": protocolVersion}},
		{"method header mismatch", modernReq(1, "tools/list", ""),
			map[string]string{"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "prompts/list"}},
		{"name header mismatch", modernReq(1, "tools/call", `"name":"litescope_health","arguments":{}`),
			map[string]string{"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "tools/call", "Mcp-Name": "other_tool"}},
		{"missing name header", modernReq(1, "tools/call", `"name":"litescope_health","arguments":{}`),
			map[string]string{"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "tools/call"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, decoded := postModern(t, srv.URL+"/mcp", c.body, c.headers)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			e := errorOf(t, decoded)
			if e["code"] != float64(errHeaderMismatch) {
				t.Errorf("code = %v, want %d", e["code"], errHeaderMismatch)
			}
		})
	}
}

// A resource URI is not always header-safe, so clients wrap it in the base64
// sentinel form; the server must decode it before comparing.
func TestHTTPModern_Base64NameHeader(t *testing.T) {
	srv := newHTTPTestServer(t)
	uri := "litescope://schema/tmp/ünïcode.db"
	body := modernReq(1, "resources/read", fmt.Sprintf(`"uri":%q`, uri))
	encoded := "=?base64?" + base64.StdEncoding.EncodeToString([]byte(uri)) + "?="
	resp, decoded := postModern(t, srv.URL+"/mcp", body, map[string]string{
		"MCP-Protocol-Version": protocolVersion,
		"Mcp-Method":           "resources/read",
		"Mcp-Name":             encoded,
	})
	// The database does not exist, so the read fails — but on its own terms
	// (invalid params), not as a header mismatch.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if e := errorOf(t, decoded); e["code"] != float64(-32602) {
		t.Errorf("code = %v, want -32602", e["code"])
	}
}

func TestHTTPModern_UnknownMethodIs404(t *testing.T) {
	srv := newHTTPTestServer(t)
	body := modernReq(1, "does/notexist", "")
	resp, decoded := postModern(t, srv.URL+"/mcp", body, map[string]string{
		"MCP-Protocol-Version": protocolVersion,
		"Mcp-Method":           "does/notexist",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if e := errorOf(t, decoded); e["code"] != float64(-32601) {
		t.Errorf("code = %v, want -32601", e["code"])
	}
}

func TestHTTPModern_UnsupportedVersionIs400(t *testing.T) {
	srv := newHTTPTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"1900-01-01",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
	resp, decoded := postModern(t, srv.URL+"/mcp", body, map[string]string{
		"MCP-Protocol-Version": "1900-01-01",
		"Mcp-Method":           "server/discover",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %v)", resp.StatusCode, decoded)
	}
	if e := errorOf(t, decoded); e["code"] != float64(errUnsupportedVersion) {
		t.Errorf("code = %v, want %d", e["code"], errUnsupportedVersion)
	}
}

// An ordinary application error must not change the HTTP status: a 400 tells a
// dual-era client to consider falling back to the handshake era.
func TestHTTPModern_ToolErrorStays200(t *testing.T) {
	srv := newHTTPTestServer(t)
	body := modernReq(1, "tools/call", `"name":"no_such_tool","arguments":{}`)
	resp, decoded := postModern(t, srv.URL+"/mcp", body, map[string]string{
		"MCP-Protocol-Version": protocolVersion,
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             "no_such_tool",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	errorOf(t, decoded)
}

// The listen stream is an SSE response to the POST itself — no GET, no session.
func TestHTTPModern_ListenStream(t *testing.T) {
	srv := newHTTPTestServer(t)
	body := modernReq(9, "subscriptions/listen", `"notifications":{"resourceSubscriptions":["litescope://schema/x.db"]}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	req.Header.Set("Mcp-Method", "subscriptions/listen")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want an SSE stream", ct)
	}
	if resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("SSE response should disable proxy buffering")
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	first := string(buf[:n])
	if !strings.Contains(first, "notifications/subscriptions/acknowledged") {
		t.Fatalf("first message on the stream = %q, want the acknowledgement", first)
	}
	if !strings.Contains(first, metaSubscriptionID) {
		t.Errorf("acknowledgement is not tagged with a subscription id: %q", first)
	}
}
