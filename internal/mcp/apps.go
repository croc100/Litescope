package mcp

import (
	"embed"
	"fmt"
	"strings"
)

// MCP Apps (extension io.modelcontextprotocol/ui, 2026-01-26) lets a tool
// answer with an interactive HTML view instead of a wall of JSON. Litescope's
// output is exactly the kind that suffers as JSON: a lock diagnosis is a
// verdict plus the PRAGMA that fixes it, a write preview is a blast radius you
// want to read before committing, a fleet sweep is a hundred rows you want
// sorted worst-first. Those four get a view.
//
// A view is a self-contained HTML document served from a ui:// resource and
// named by the tool's _meta.ui.resourceUri. Hosts that don't implement the
// extension ignore the metadata and render the tool's text as before, so every
// view here is an enhancement of a result that already stands on its own.
//
// The views never mutate anything. A button that would apply or undo a write
// hands the request back to the conversation instead, because a click inside
// an embedded panel is not the user approving a production write.

//go:embed ui/*.css ui/*.js
var uiFS embed.FS

const (
	uiExtension = "io.modelcontextprotocol/ui"
	uiMimeType  = "text/html;profile=mcp-app"
	uiScheme    = "ui://litescope/"
)

// uiView is one embedded view: the resource it is served at, the tools whose
// results it renders, and the script that renders them.
type uiView struct {
	slug        string // path segment after ui://litescope/
	title       string
	description string
	script      string   // file under ui/
	tools       []string // tools whose results this view renders
}

func uiViews() []uiView {
	return []uiView{
		{
			slug:        "lock-doctor",
			title:       "Lock doctor",
			description: "Interactive SQLITE_BUSY / \"database is locked\" diagnosis: verdict, the PRAGMA or DSN change that fixes each finding, and a live lock probe.",
			script:      "view-locks.js",
			tools:       []string{"litescope_locks"},
		},
		{
			slug:        "health",
			title:       "Database health",
			description: "Operational health panel for one database: integrity, WAL size, fragmentation, snapshot cover, and the issues found.",
			script:      "view-health.js",
			tools:       []string{"litescope_health"},
		},
		{
			slug:        "write-preview",
			title:       "Write blast radius",
			description: "What a write would do before it does it: rows affected per statement, the schema and row-count blast radius, and the rewind token that undoes it.",
			script:      "view-write.js",
			tools:       []string{"litescope_query_write", "litescope_migrate_apply"},
		},
		{
			slug:        "fleet",
			title:       "Fleet health",
			description: "Every database in the fleet, worst-first, with severity counts and per-database drill-down.",
			script:      "view-fleet.js",
			tools:       []string{"litescope_fleet_health"},
		},
	}
}

// uiResourceFor returns the ui:// resource URI that renders a tool's result,
// or "" when the tool has no view.
func uiResourceFor(tool string) string {
	for _, v := range uiViews() {
		for _, t := range v.tools {
			if t == tool {
				return uiScheme + v.slug
			}
		}
	}
	return ""
}

// uiCapability is the extension entry advertised in server capabilities.
func uiCapability() map[string]interface{} {
	return map[string]interface{}{"mimeTypes": []string{uiMimeType}}
}

// readUIResource renders the HTML document for a ui:// URI. ok is false when
// the URI names no view, which lets the ordinary resource path handle it.
func readUIResource(uri string) (html string, ok bool) {
	if !strings.HasPrefix(uri, uiScheme) {
		return "", false
	}
	slug := strings.TrimPrefix(uri, uiScheme)
	for _, v := range uiViews() {
		if v.slug == slug {
			return uiPage(v), true
		}
	}
	return "", false
}

// uiPage assembles a view into one HTML document. Everything is inlined: the
// host's default CSP for a view that declares no domains is
// `default-src 'none'` with inline script and style allowed, so a view that
// fetches nothing is also a view that cannot leak a database's contents
// anywhere.
func uiPage(v uiView) string {
	css := mustAsset("ui/app.css")
	bridge := mustAsset("ui/bridge.js")
	helpers := mustAsset("ui/helpers.js")
	view := mustAsset("ui/" + v.script)

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s — Litescope</title>
<style>
%s
</style>
</head>
<body>
<div id="root"><div class="empty">Loading %s…</div></div>
<script>
%s
</script>
<script>
%s
</script>
<script>
%s
</script>
</body>
</html>
`, v.title, css, strings.ToLower(v.title), bridge, helpers, view)
}

func mustAsset(name string) string {
	b, err := uiFS.ReadFile(name)
	if err != nil {
		// Embedded at build time: a missing asset is a build error, not a
		// runtime condition.
		panic("litescope: missing embedded UI asset " + name + ": " + err.Error())
	}
	return string(b)
}

// uiResourceMeta is the _meta.ui block returned with a view's content. No
// domains are declared, so the host applies its most restrictive CSP.
func uiResourceMeta() map[string]interface{} {
	return map[string]interface{}{
		"ui": map[string]interface{}{"prefersBorder": true},
	}
}
