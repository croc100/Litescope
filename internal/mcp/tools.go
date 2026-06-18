// Package mcp exposes Litescope as a Model Context Protocol server over stdio,
// so an LLM agent (Claude Desktop, Claude Code, or any MCP client) can call
// Litescope operations as tools. This first cut exposes read-only diagnostic
// tools only — an AI can inspect databases freely, but cannot mutate them.
package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/schema"
)

// Tool is one callable operation exposed to an AI agent. The same registry
// backs both the MCP server and (in future) a built-in BYOK agent.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]interface{} // JSON Schema for the arguments
	Handler     func(args map[string]interface{}) (string, error)
}

// Registry returns all tools the MCP server exposes. Read-only by design.
func Registry() []Tool {
	return []Tool{
		{
			Name: "litescope_health",
			Description: "Inspect a local SQLite database for operational faults: corruption " +
				"(PRAGMA integrity check), WAL bloat from a starved checkpoint, freelist " +
				"fragmentation, and reachability. Returns a JSON report with a severity " +
				"(ok / warning / critical) and a list of issues. Read-only.",
			InputSchema: obj(props{
				"path": strProp("Absolute path to the SQLite database file"),
				"deep": boolProp("Use the exhaustive integrity_check instead of the faster quick_check"),
			}, "path"),
			Handler: func(args map[string]interface{}) (string, error) {
				path, _ := args["path"].(string)
				if path == "" {
					return "", fmt.Errorf("path is required")
				}
				deep, _ := args["deep"].(bool)
				return toJSON(health.Inspect(path, deep))
			},
		},
		{
			Name: "litescope_schema",
			Description: "Load the schema of a local SQLite database — tables, columns " +
				"(name, type, not-null, primary key), and indexes. Returns JSON. Read-only.",
			InputSchema: obj(props{
				"path": strProp("Absolute path to the SQLite database file"),
			}, "path"),
			Handler: func(args map[string]interface{}) (string, error) {
				path, _ := args["path"].(string)
				if path == "" {
					return "", fmt.Errorf("path is required")
				}
				s, err := schema.Load(path)
				if err != nil {
					return "", err
				}
				return toJSON(s)
			},
		},
		{
			Name: "litescope_diff",
			Description: "Compare two local SQLite databases and return their schema and " +
				"row-count differences as JSON. Use this to understand what a migration " +
				"changed between a 'before' and 'after' database. Read-only.",
			InputSchema: obj(props{
				"old": strProp("Absolute path to the baseline ('before') database"),
				"new": strProp("Absolute path to the changed ('after') database"),
			}, "old", "new"),
			Handler: func(args map[string]interface{}) (string, error) {
				oldP, _ := args["old"].(string)
				newP, _ := args["new"].(string)
				if oldP == "" || newP == "" {
					return "", fmt.Errorf("both 'old' and 'new' paths are required")
				}
				r, err := diff.Compare(oldP, newP)
				if err != nil {
					return "", err
				}
				return toJSON(r)
			},
		},
	}
}

// ── JSON Schema helpers ─────────────────────────────────────────────────────

type props map[string]map[string]interface{}

func obj(p props, required ...string) map[string]interface{} {
	properties := map[string]interface{}{}
	for k, v := range p {
		properties[k] = v
	}
	m := map[string]interface{}{"type": "object", "properties": properties}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

func boolProp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "boolean", "description": desc}
}

func toJSON(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
