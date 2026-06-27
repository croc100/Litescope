package mcp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// run feeds newline-delimited JSON-RPC requests through Serve and returns the
// decoded responses keyed by their numeric id.
func run(t *testing.T, requests ...string) map[float64]map[string]interface{} {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(in, &out, "test", false, ""); err != nil {
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

func TestServe_Initialize(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	res, ok := r[1]["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("no result in initialize response: %v", r[1])
	}
	if res["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %s", res["protocolVersion"], protocolVersion)
	}
	info := res["serverInfo"].(map[string]interface{})
	if info["name"] != "litescope" || info["version"] != "test" {
		t.Errorf("serverInfo = %v", info)
	}
}

func TestServe_Initialize_EchoesClientProtocolVersion(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	res := r[1]["result"].(map[string]interface{})
	if res["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want echoed 2025-06-18", res["protocolVersion"])
	}
}

func TestServe_DiffOutput_IsCurated(t *testing.T) {
	dir := t.TempDir()
	oldP, newP := dir+"/old.db", dir+"/new.db"
	od, _ := sql.Open("sqlite", oldP)
	od.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, legacy INTEGER)")
	od.Close()
	nd, _ := sql.Open("sqlite", newP)
	nd.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY)") // drop legacy
	nd.Exec("CREATE TABLE audit (id INTEGER PRIMARY KEY)") // add table
	nd.Close()

	r := run(t, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"litescope_diff","arguments":{"old":"`+oldP+`","new":"`+newP+`"}}}`)
	text := r[8]["result"].(map[string]interface{})["content"].([]interface{})[0].(map[string]interface{})["text"].(string)

	// Curated output: lowercase keys, a summary, no raw Go field names.
	for _, want := range []string{`"summary"`, `"schema_changes"`, `"tables_added"`, `"columns_removed"`} {
		if !strings.Contains(text, want) {
			t.Errorf("curated diff missing %s; got:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"AddedColumns"`) || strings.Contains(text, `"NotNull"`) {
		t.Errorf("diff output leaked raw struct fields:\n%s", text)
	}
}

func TestServe_Notification_NoResponse(t *testing.T) {
	// A notification (no id) must not produce a response line.
	in := strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	var out bytes.Buffer
	if err := Serve(in, &out, "test", false, ""); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("notification produced output: %q", out.String())
	}
}

func TestServe_ToolsList(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	res := r[7]["result"].(map[string]interface{})
	tools := res["tools"].([]interface{})
	names := map[string]bool{}
	for _, ti := range tools {
		names[ti.(map[string]interface{})["name"].(string)] = true
	}
	for _, want := range []string{
		"litescope_health", "litescope_schema", "litescope_diff",
		"litescope_migrate_plan", "litescope_check",
		"litescope_fingerprint", "litescope_fleet_health",
	} {
		if !names[want] {
			t.Errorf("tools/list missing %s (got %v)", want, names)
		}
	}
}

func TestServe_ToolCall_MigratePlan(t *testing.T) {
	dir := t.TempDir()
	oldP, newP := dir+"/old.db", dir+"/new.db"
	od, _ := sql.Open("sqlite", oldP)
	od.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, legacy INTEGER)")
	od.Exec("INSERT INTO users (name, legacy) VALUES ('a', 1)")
	od.Close()
	nd, _ := sql.Open("sqlite", newP)
	nd.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)") // drop legacy
	nd.Close()

	req := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"litescope_migrate_plan","arguments":{"old":"` + oldP + `","new":"` + newP + `"}}}`
	r := run(t, req)
	res := r[9]["result"].(map[string]interface{})
	if res["isError"] == true {
		t.Fatalf("migrate_plan errored: %v", res)
	}
	text := res["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	// Dropping a column is a destructive table rebuild.
	if !strings.Contains(text, `"destructive": true`) {
		t.Errorf("expected destructive plan for a column drop, got: %s", text)
	}
	if !strings.Contains(text, "TABLE REBUILD") {
		t.Errorf("expected a TABLE REBUILD operation, got: %s", text)
	}
}

func TestServe_ToolCall_Health(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/ok.db"
	db, _ := sql.Open("sqlite", path)
	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)")
	db.Close()

	req := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"litescope_health","arguments":{"path":"` + path + `"}}}`
	r := run(t, req)
	res := r[3]["result"].(map[string]interface{})
	if res["isError"] != false {
		t.Errorf("healthy DB reported isError=true: %v", res)
	}
	text := res["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, `"severity": "ok"`) {
		t.Errorf("expected severity ok in result, got: %s", text)
	}
}

func TestServe_ToolCall_MissingArg_IsError(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"litescope_health","arguments":{}}}`)
	res := r[4]["result"].(map[string]interface{})
	if res["isError"] != true {
		t.Errorf("missing path should be a tool error, got: %v", res)
	}
}

func TestServe_UnknownMethod_Error(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":5,"method":"does/not/exist"}`)
	if _, ok := r[5]["error"].(map[string]interface{}); !ok {
		t.Errorf("unknown method should return an error, got: %v", r[5])
	}
}

func TestServe_UnknownTool_Error(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if _, ok := r[6]["error"].(map[string]interface{}); !ok {
		t.Errorf("unknown tool should return a protocol error, got: %v", r[6])
	}
}

// TestToolAnnotationsCoverage ensures every registered tool (read and write)
// has an explicit behavioral-hint entry, and that the safety hints line up with
// Litescope's model: write tools must not claim readOnlyHint, and the
// data-replacing tools must be marked destructive.
func TestToolAnnotationsCoverage(t *testing.T) {
	mustDestructive := map[string]bool{
		"litescope_restore":       true,
		"litescope_rewind":        true,
		"litescope_query_write":   true,
		"litescope_migrate_apply": true,
		"litescope_d1_delete":     true,
	}
	for _, tool := range Registry(true) {
		a, ok := annotationsByName[tool.Name]
		if !ok {
			t.Errorf("tool %q has no annotations entry", tool.Name)
			continue
		}
		if a.Title == "" {
			t.Errorf("tool %q has no annotation title", tool.Name)
		}
		if mustDestructive[tool.Name] {
			if a.ReadOnlyHint {
				t.Errorf("destructive tool %q is marked readOnlyHint=true", tool.Name)
			}
			if !a.DestructiveHint {
				t.Errorf("tool %q must be marked destructiveHint=true", tool.Name)
			}
		}
		if a.ReadOnlyHint && a.DestructiveHint {
			t.Errorf("tool %q cannot be both read-only and destructive", tool.Name)
		}
	}
}
