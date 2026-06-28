package mcp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// runSource is like run but binds a default source (for concrete resources).
func runSource(t *testing.T, source string, requests ...string) map[float64]map[string]interface{} {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := Serve(in, &out, "test", false, source); err != nil {
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

func TestServe_InitializeAdvertisesPromptsAndResources(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	caps := r[1]["result"].(map[string]interface{})["capabilities"].(map[string]interface{})
	for _, want := range []string{"tools", "prompts", "resources"} {
		if _, ok := caps[want]; !ok {
			t.Errorf("capabilities missing %q: %v", want, caps)
		}
	}
}

func TestServe_PromptsList(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`)
	prompts := r[2]["result"].(map[string]interface{})["prompts"].([]interface{})
	names := map[string]bool{}
	for _, p := range prompts {
		names[p.(map[string]interface{})["name"].(string)] = true
	}
	for _, want := range []string{"diagnose_locked_database", "review_migration", "safe_optimize", "health_checkup"} {
		if !names[want] {
			t.Errorf("prompts/list missing %s (got %v)", want, names)
		}
	}
}

func TestServe_PromptsGet(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"diagnose_locked_database","arguments":{"source":"./app.db"}}}`)
	res := r[3]["result"].(map[string]interface{})
	msgs := res["messages"].([]interface{})
	text := msgs[0].(map[string]interface{})["content"].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "litescope_locks") || !strings.Contains(text, "./app.db") {
		t.Errorf("rendered prompt missing tool/source: %s", text)
	}
}

func TestServe_PromptsGet_MissingRequiredArg(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"diagnose_locked_database","arguments":{}}}`)
	if _, ok := r[4]["error"].(map[string]interface{}); !ok {
		t.Errorf("missing required arg should error, got: %v", r[4])
	}
}

func TestServe_ResourceTemplatesList(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":5,"method":"resources/templates/list"}`)
	tmpls := r[5]["result"].(map[string]interface{})["resourceTemplates"].([]interface{})
	if len(tmpls) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(tmpls))
	}
}

func TestServe_ResourcesList_BoundSource(t *testing.T) {
	path := makeSchemaDB(t)
	r := runSource(t, path, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	resources := r[6]["result"].(map[string]interface{})["resources"].([]interface{})
	if len(resources) != 2 {
		t.Fatalf("expected 2 concrete resources for a bound source, got %d", len(resources))
	}
}

func TestServe_ResourcesList_NoSource_Empty(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":7,"method":"resources/list"}`)
	resources := r[7]["result"].(map[string]interface{})["resources"]
	// The MCP spec requires resources/list to return a JSON array. A nil slice
	// marshals to null, which spec-compliant clients (e.g. Glama) reject — so
	// without a bound source the result must be an empty array, never null.
	arr, ok := resources.([]interface{})
	if !ok {
		t.Fatalf("resources must be a JSON array (not null), got %T: %v", resources, resources)
	}
	if len(arr) != 0 {
		t.Errorf("expected no concrete resources without a bound source, got %v", arr)
	}
}

func TestServe_ResourceRead_Dictionary(t *testing.T) {
	path := makeSchemaDB(t)
	uri := "litescope://dictionary/" + path
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":8,"method":"resources/read","params":{"uri":%q}}`, uri)
	r := run(t, req)
	res, ok := r[8]["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("resources/read failed: %v", r[8])
	}
	text := res["contents"].([]interface{})[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "Data dictionary") || !strings.Contains(text, "books") || !strings.Contains(text, "FK→authors") {
		t.Errorf("dictionary missing expected content:\n%s", text)
	}
}

func TestServe_ResourceRead_UnknownURI(t *testing.T) {
	r := run(t, `{"jsonrpc":"2.0","id":9,"method":"resources/read","params":{"uri":"litescope://nope/x"}}`)
	if _, ok := r[9]["error"].(map[string]interface{}); !ok {
		t.Errorf("unknown resource URI should error, got: %v", r[9])
	}
}

func TestBudgetRows_CapsAndProjects(t *testing.T) {
	rows := make([]map[string]interface{}, 5)
	for i := range rows {
		rows[i] = map[string]interface{}{"id": i, "name": "n", "secret": "s"}
	}
	out := budgetRows(rows, map[string]interface{}{
		"max_rows": float64(2),
		"columns":  []interface{}{"id", "name"},
	})
	if out["truncated"] != true || out["total_rows"].(int) != 5 || out["count"].(int) != 2 {
		t.Fatalf("bad budgeting: %+v", out)
	}
	first := out["rows"].([]map[string]interface{})[0]
	if _, leaked := first["secret"]; leaked {
		t.Errorf("projection leaked unrequested column: %v", first)
	}
	if _, ok := first["id"]; !ok {
		t.Errorf("projection dropped requested column: %v", first)
	}
}

func TestBudgetRows_DefaultNoTruncation(t *testing.T) {
	rows := []map[string]interface{}{{"a": 1}}
	out := budgetRows(rows, map[string]interface{}{})
	if out["truncated"] != false || out["count"].(int) != 1 {
		t.Errorf("small result should not truncate: %+v", out)
	}
}

// makeSchemaDB builds a tiny DB with a foreign key for resource tests.
func makeSchemaDB(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/app.db"
	db, _ := sql.Open("sqlite", path)
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE books (id INTEGER PRIMARY KEY, author_id INTEGER REFERENCES authors(id), title TEXT);`); err != nil {
		t.Fatal(err)
	}
	return path
}
