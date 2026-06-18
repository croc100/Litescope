// Package mcp exposes Litescope as a Model Context Protocol server over stdio,
// so an LLM agent (Claude Desktop, Claude Code, or any MCP client) can call
// Litescope operations as tools. This first cut exposes read-only diagnostic
// tools only — an AI can inspect databases freely, but cannot mutate them.
package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/check"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/license"
	"github.com/croc100/litescope/internal/migrate"
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
				return toJSON(shapeSchema(s))
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
				return toJSON(shapeDiff(r))
			},
		},
		{
			Name: "litescope_migrate_plan",
			Description: "Plan a migration from one SQLite database to another WITHOUT applying it. " +
				"Returns the migration SQL plus a blast-radius analysis: each operation classified " +
				"safe / risky / destructive, with an estimated write-lock duration for table rebuilds " +
				"(SQLite locks the whole file for DDL). Use this to judge whether a migration is safe " +
				"before a human applies it. Read-only — never mutates a database.",
			InputSchema: obj(props{
				"old": strProp("Absolute path to the current ('before') database"),
				"new": strProp("Absolute path to the target ('after') database with the desired schema"),
			}, "old", "new"),
			Handler: func(args map[string]interface{}) (string, error) {
				oldP, _ := args["old"].(string)
				newP, _ := args["new"].(string)
				if oldP == "" || newP == "" {
					return "", fmt.Errorf("both 'old' and 'new' paths are required")
				}
				d, err := diff.Compare(oldP, newP)
				if err != nil {
					return "", err
				}
				newSchema, err := schema.Load(newP)
				if err != nil {
					return "", err
				}
				m := migrate.Generate(d, newSchema)
				ops, _ := migrate.AnalyzeAll(d, oldP)
				return toJSON(buildPlan(m, ops))
			},
		},
		{
			Name: "litescope_advise",
			Description: "Analyze a local SQLite database for performance problems and recommend " +
				"fixes: foreign keys with no index (a full scan on every join — SQLite does not " +
				"auto-index FK columns), redundant indexes, and full table scans for any supplied " +
				"queries (via EXPLAIN QUERY PLAN). Returns findings with runnable CREATE/DROP INDEX " +
				"suggestions. Read-only — recommends, never alters the schema.",
			InputSchema: obj(props{
				"path": strProp("Absolute path to the SQLite database file"),
				"queries": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional SQL queries to check for full table scans",
				},
			}, "path"),
			Handler: func(args map[string]interface{}) (string, error) {
				path, _ := args["path"].(string)
				if path == "" {
					return "", fmt.Errorf("path is required")
				}
				var queries []string
				if raw, ok := args["queries"].([]interface{}); ok {
					for _, q := range raw {
						if s, ok := q.(string); ok && s != "" {
							queries = append(queries, s)
						}
					}
				}
				r, err := advisor.Analyze(path, queries)
				if err != nil {
					return "", err
				}
				return toJSON(r)
			},
		},
		{
			Name: "litescope_check",
			Description: "Verify a SQLite backup. Runs a PRAGMA integrity check (free); if 'against' " +
				"is given, also compares schema and row counts to a reference database (requires a Pro " +
				"license). Returns a JSON report. Read-only.",
			InputSchema: obj(props{
				"path":    strProp("Absolute path to the backup database to verify"),
				"against": strProp("Optional reference database to compare schema against (Pro)"),
				"data":    boolProp("Also compare row counts per table (Pro)"),
			}, "path"),
			Handler: func(args map[string]interface{}) (string, error) {
				path, _ := args["path"].(string)
				if path == "" {
					return "", fmt.Errorf("path is required")
				}
				against, _ := args["against"].(string)
				data, _ := args["data"].(bool)
				if against != "" || data {
					if err := license.RequirePro(); err != nil {
						return "", err
					}
				}
				r, err := check.Check(path, against, data)
				if err != nil {
					return "", err
				}
				return toJSON(r)
			},
		},
		{
			Name: "litescope_fingerprint",
			Description: "Cluster a fleet of SQLite databases by schema and report how many distinct " +
				"schemas are actually running, with each cluster's drift from the canonical (largest) " +
				"one. Reads a fleet config file (litescope.fleet.yaml). Requires a Pro license. Read-only.",
			InputSchema: obj(props{
				"config": strProp("Path to the fleet config (default: litescope.fleet.yaml)"),
				"tag":    strProp("Only include databases with this tag"),
			}),
			Handler: func(args map[string]interface{}) (string, error) {
				dbs, err := fleetDBs(args)
				if err != nil {
					return "", err
				}
				return toJSON(fleet.Fingerprint(dbs, 0))
			},
		},
		{
			Name: "litescope_fleet_health",
			Description: "Triage operational faults across a whole fleet of SQLite databases in " +
				"parallel — corruption, WAL bloat, fragmentation, reachability — sorted worst-first. " +
				"Reads a fleet config file (litescope.fleet.yaml). Requires a Pro license. Read-only.",
			InputSchema: obj(props{
				"config": strProp("Path to the fleet config (default: litescope.fleet.yaml)"),
				"tag":    strProp("Only include databases with this tag"),
				"deep":   boolProp("Use the exhaustive integrity_check instead of quick_check"),
			}),
			Handler: func(args map[string]interface{}) (string, error) {
				dbs, err := fleetDBs(args)
				if err != nil {
					return "", err
				}
				deep, _ := args["deep"].(bool)
				return toJSON(fleet.Health(dbs, deep, 0))
			},
		},
	}
}

// ── fleet + plan helpers ────────────────────────────────────────────────────

// fleetDBs loads the fleet config (Pro-gated) and returns the databases matching
// an optional tag.
func fleetDBs(args map[string]interface{}) ([]fleet.Database, error) {
	if err := license.RequirePro(); err != nil {
		return nil, err
	}
	configPath, _ := args["config"].(string)
	if configPath == "" {
		configPath = fleet.DefaultConfigFile
	}
	cfg, err := fleet.Load(configPath)
	if err != nil {
		return nil, err
	}
	tag, _ := args["tag"].(string)
	dbs := cfg.Filter(tag)
	if len(dbs) == 0 {
		return nil, fmt.Errorf("no databases in fleet config %q (tag %q)", configPath, tag)
	}
	return dbs, nil
}

type planOp struct {
	Table   string `json:"table"`
	Kind    string `json:"kind"` // safe | risky | destructive
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
	Rows    int64  `json:"rows,omitempty"`
	LockMs  int64  `json:"lock_ms,omitempty"`
}

type plan struct {
	Statements  int      `json:"statements"`
	Destructive bool     `json:"destructive"`
	Operations  []planOp `json:"operations"`
	SQL         string   `json:"sql"`
}

func buildPlan(m *migrate.Migration, ops []migrate.Operation) plan {
	p := plan{Statements: len(m.Statements), SQL: m.SQL()}
	for _, op := range ops {
		kind := "safe"
		switch op.Kind {
		case migrate.OpRisky:
			kind = "risky"
		case migrate.OpDestructive:
			kind = "destructive"
			p.Destructive = true
		}
		p.Operations = append(p.Operations, planOp{
			Table:   op.Table,
			Kind:    kind,
			Summary: op.Headline,
			Detail:  op.Detail,
			Rows:    op.Rows,
			LockMs:  op.LockMs,
		})
	}
	return p
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

// ── LLM-friendly output shaping ─────────────────────────────────────────────
// schema.Schema and diff.Result are internal structs; dumping them raw leaks
// Go field casing and empty fields. These shapers emit concise, lowercase JSON
// curated for a model to read.

func shapeSchema(s *schema.Schema) map[string]interface{} {
	tables := make([]map[string]interface{}, 0, len(s.Tables))
	for _, t := range s.Tables {
		cols := make([]map[string]interface{}, 0, len(t.Columns))
		for _, c := range t.Columns {
			col := map[string]interface{}{"name": c.Name, "type": c.Type}
			if c.NotNull {
				col["not_null"] = true
			}
			if c.PK > 0 {
				col["pk"] = true
			}
			if c.Default != "" {
				col["default"] = c.Default
			}
			cols = append(cols, col)
		}
		tbl := map[string]interface{}{"name": t.Name, "columns": cols}
		if idx := shapeIndexes(t.Indexes); len(idx) > 0 {
			tbl["indexes"] = idx
		}
		tables = append(tables, tbl)
	}
	return map[string]interface{}{"tables": tables}
}

func shapeIndexes(idxs []schema.Index) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(idxs))
	for _, ix := range idxs {
		m := map[string]interface{}{"name": ix.Name}
		if ix.Unique {
			m["unique"] = true
		}
		out = append(out, m)
	}
	return out
}

func shapeDiff(d *diff.Result) map[string]interface{} {
	added, removed, modified := 0, 0, 0
	changes := make([]map[string]interface{}, 0, len(d.Schema))
	for _, td := range d.Schema {
		ch := map[string]interface{}{"table": td.Name}
		switch {
		case td.Added:
			added++
			ch["change"] = "added"
			ch["columns_added"] = colNames(td.AddedColumns)
		case td.Removed:
			removed++
			ch["change"] = "removed"
		default:
			modified++
			ch["change"] = "modified"
			if len(td.AddedColumns) > 0 {
				ch["columns_added"] = colNames(td.AddedColumns)
			}
			if len(td.RemovedColumns) > 0 {
				ch["columns_removed"] = colNames(td.RemovedColumns)
			}
			if len(td.ChangedColumns) > 0 {
				cc := make([]map[string]interface{}, 0, len(td.ChangedColumns))
				for _, c := range td.ChangedColumns {
					cc = append(cc, map[string]interface{}{"name": c.Name, "from": c.Old.Type, "to": c.New.Type})
				}
				ch["columns_changed"] = cc
			}
			if len(td.AddedIndexes) > 0 {
				ch["indexes_added"] = idxNames(td.AddedIndexes)
			}
			if len(td.RemovedIndexes) > 0 {
				ch["indexes_removed"] = idxNames(td.RemovedIndexes)
			}
		}
		changes = append(changes, ch)
	}
	out := map[string]interface{}{
		"summary": map[string]interface{}{
			"tables_added": added, "tables_removed": removed, "tables_modified": modified,
		},
		"schema_changes": changes,
	}
	var data []map[string]interface{}
	for _, dd := range d.Data {
		if dd.Added == 0 && dd.Removed == 0 && dd.Changed == 0 {
			continue // skip no-op entries — don't feed the model noise
		}
		data = append(data, map[string]interface{}{
			"table": dd.Table, "rows_added": dd.Added, "rows_removed": dd.Removed, "rows_changed": dd.Changed,
		})
	}
	if len(data) > 0 {
		out["data_changes"] = data
	}
	return out
}

func colNames(cols []schema.Column) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Name)
	}
	return out
}

func idxNames(idxs []schema.Index) []string {
	out := make([]string, 0, len(idxs))
	for _, ix := range idxs {
		out = append(out, ix.Name)
	}
	return out
}
