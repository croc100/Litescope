package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Every tool that names a view must name one that exists, or the host fetches
// a ui:// URI that 404s and the user sees nothing.
func TestUIViews_ResourcesResolve(t *testing.T) {
	for _, v := range uiViews() {
		uri := uiScheme + v.slug
		html, ok := readUIResource(uri)
		if !ok {
			t.Errorf("view %q does not resolve at %s", v.slug, uri)
			continue
		}
		if !strings.HasPrefix(html, "<!DOCTYPE html>") {
			t.Errorf("view %q is not an HTML document", v.slug)
		}
		for _, want := range []string{"window.litescope", "window.ui", "ui/notifications/tool-result", "--ls-bg"} {
			if !strings.Contains(html, want) {
				t.Errorf("view %q is missing %q — bridge, helpers or stylesheet did not inline", v.slug, want)
			}
		}
		for _, tool := range v.tools {
			if got := uiResourceFor(tool); got != uri {
				t.Errorf("tool %q maps to %q, want %q", tool, got, uri)
			}
		}
	}
	if _, ok := readUIResource(uiScheme + "nope"); ok {
		t.Errorf("an unknown view must not resolve")
	}
	if _, ok := readUIResource("litescope://schema/app.db"); ok {
		t.Errorf("a database resource must not be served as a view")
	}
}

// A view's tools must actually be registered, or the metadata points at a tool
// no client will ever call.
func TestUIViews_ToolsExist(t *testing.T) {
	registered := map[string]bool{}
	for _, tool := range Registry(true) {
		registered[tool.Name] = true
	}
	for _, v := range uiViews() {
		for _, tool := range v.tools {
			if !registered[tool] {
				t.Errorf("view %q renders unknown tool %q", v.slug, tool)
			}
		}
	}
}

func TestUIViews_ToolsCarryResourceURI(t *testing.T) {
	r := run(t, modernReq(1, "tools/list", ""))
	tools, _ := resultOf(t, r[1])["tools"].([]interface{})
	seen := map[string]string{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]interface{})
		name, _ := tool["name"].(string)
		meta, _ := tool["_meta"].(map[string]interface{})
		if meta == nil {
			continue
		}
		ui, _ := meta["ui"].(map[string]interface{})
		if uri, _ := ui["resourceUri"].(string); uri != "" {
			seen[name] = uri
		}
	}
	// Read-only registry: the write view's tools are absent here.
	for _, want := range []string{"litescope_locks", "litescope_health", "litescope_fleet_health"} {
		if seen[want] == "" {
			t.Errorf("tool %q carries no _meta.ui.resourceUri", want)
		}
	}
	if seen["litescope_schema"] != "" {
		t.Errorf("litescope_schema has no view but advertises %q", seen["litescope_schema"])
	}
}

func TestUIViews_CapabilityAdvertised(t *testing.T) {
	// Modern discovery and the handshake era must both advertise the
	// extension: hosts on either revision can render a view.
	r := run(t,
		`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{}}`,
	)
	for _, id := range []float64{1, 2} {
		caps, _ := resultOf(t, r[id])["capabilities"].(map[string]interface{})
		ext, _ := caps["extensions"].(map[string]interface{})
		entry, ok := ext[uiExtension].(map[string]interface{})
		if !ok {
			t.Fatalf("id %v: capabilities do not advertise %s: %v", id, uiExtension, caps)
		}
		mimes, _ := entry["mimeTypes"].([]interface{})
		if len(mimes) == 0 || mimes[0] != uiMimeType {
			t.Errorf("id %v: mimeTypes = %v, want %s", id, mimes, uiMimeType)
		}
	}
}

func TestUIViews_ResourceRead(t *testing.T) {
	uri := uiScheme + "lock-doctor"
	r := run(t, modernReq(1, "resources/read", fmt.Sprintf(`"uri":%q`, uri)))
	res := resultOf(t, r[1])

	contents, _ := res["contents"].([]interface{})
	if len(contents) != 1 {
		t.Fatalf("contents = %v", contents)
	}
	c, _ := contents[0].(map[string]interface{})
	if c["mimeType"] != uiMimeType {
		t.Errorf("mimeType = %v, want %s", c["mimeType"], uiMimeType)
	}
	text, _ := c["text"].(string)
	if !strings.Contains(text, "Lock doctor") {
		t.Errorf("view HTML does not look like the lock doctor: %.80s", text)
	}
	meta, _ := c["_meta"].(map[string]interface{})
	if _, ok := meta["ui"].(map[string]interface{}); !ok {
		t.Errorf("content carries no _meta.ui: %v", meta)
	}
	// A view is a static document with no database content in it.
	if res["cacheScope"] != "public" {
		t.Errorf("cacheScope = %v, want public", res["cacheScope"])
	}
	if ttl, _ := res["ttlMs"].(float64); ttl <= 0 {
		t.Errorf("ttlMs = %v, want a positive freshness hint", res["ttlMs"])
	}
}

// The views are served as a single inline document, so nothing in them may
// reach the network: the host's default CSP would block it, and a database's
// contents must not be able to leave the iframe.
func TestUIViews_NoExternalReferences(t *testing.T) {
	for _, v := range uiViews() {
		html, _ := readUIResource(uiScheme + v.slug)
		for _, bad := range []string{"http://", "https://", "<script src", "<link ", "@import"} {
			if strings.Contains(html, bad) {
				t.Errorf("view %q references something external (%q)", v.slug, bad)
			}
		}
	}
}

// The document must survive being carried inside a JSON-RPC result.
func TestUIViews_JSONRoundTrip(t *testing.T) {
	for _, v := range uiViews() {
		html, _ := readUIResource(uiScheme + v.slug)
		b, err := json.Marshal(map[string]string{"text": html})
		if err != nil {
			t.Fatalf("view %q does not marshal: %v", v.slug, err)
		}
		var back map[string]string
		if err := json.Unmarshal(b, &back); err != nil || back["text"] != html {
			t.Errorf("view %q did not survive a JSON round trip", v.slug)
		}
	}
}
