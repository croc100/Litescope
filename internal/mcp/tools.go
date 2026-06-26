// Package mcp exposes Litescope as a Model Context Protocol server over stdio,
// so an LLM agent (Claude Desktop, Claude Code, or any MCP client) can call
// Litescope operations as tools. All tools are read-only by default.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/check"
	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/health"
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

const sourcePropDesc = "Database source: a local file path (./app.db), a Cloudflare D1 DSN " +
	"(d1://DB_ID when CLOUDFLARE_API_TOKEN+CLOUDFLARE_ACCOUNT_ID are set, or " +
	"d1://TOKEN@ACCOUNT_ID/DB_ID), or a Turso DSN (turso://TOKEN@ORG/DB)."

// Registry returns all tools the MCP server exposes. Read-only by design.
func Registry() []Tool {
	return []Tool{
		{
			Name: "litescope_health",
			Description: "Inspect a SQLite or D1 database for operational faults: corruption " +
				"(PRAGMA integrity check), WAL bloat from a starved checkpoint, freelist " +
				"fragmentation, and reachability. Returns a JSON report with a severity " +
				"(ok / warning / critical) and a list of issues. Read-only.\n\n" +
				"For D1: set CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID and use source=d1://DB_ID.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
				"deep":   boolProp("Use the exhaustive integrity_check instead of the faster quick_check"),
			}, "source"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				deep, _ := args["deep"].(bool)
				if isRemote(src) {
					// Remote sources: reachability check via connector
					c, err := connector.Open(src)
					if err != nil {
						return toJSON(map[string]interface{}{
							"source": src, "reachable": false,
							"severity": "critical", "issues": []string{err.Error()},
						})
					}
					defer c.Close()
					_, schErr := c.Schema()
					if schErr != nil {
						return toJSON(map[string]interface{}{
							"source": src, "reachable": false,
							"severity": "critical", "issues": []string{schErr.Error()},
						})
					}
					return toJSON(map[string]interface{}{
						"source": src, "reachable": true,
						"severity": "ok", "issues": []string{},
						"note": "Full PRAGMA checks require a local file; remote health shows reachability only.",
					})
				}
				return toJSON(health.Inspect(src, deep))
			},
		},
		{
			Name: "litescope_schema",
			Description: "Load the schema of a SQLite or D1 database — tables, columns " +
				"(name, type, not-null, primary key), and indexes. Returns JSON. Read-only.\n\n" +
				"Works with local files, D1, and Turso. For D1: set CLOUDFLARE_API_TOKEN + " +
				"CLOUDFLARE_ACCOUNT_ID and use source=d1://DB_ID.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
			}, "source"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					c, err := connector.Open(src)
					if err != nil {
						return "", err
					}
					defer c.Close()
					s, err := c.Schema()
					if err != nil {
						return "", err
					}
					return toJSON(shapeSchema(s))
				}
				s, err := schema.Load(src)
				if err != nil {
					return "", err
				}
				return toJSON(shapeSchema(s))
			},
		},
		{
			Name: "litescope_diff",
			Description: "Compare two SQLite or D1 databases and return their schema and " +
				"row-count differences as JSON. Works across any combination of local files, " +
				"D1, and Turso — e.g. diff a local migration target against a live D1 database. " +
				"Read-only.",
			InputSchema: obj(props{
				"old": strProp("Baseline ('before') source — " + sourcePropDesc),
				"new": strProp("Changed ('after') source — " + sourcePropDesc),
			}, "old", "new"),
			Handler: func(args map[string]interface{}) (string, error) {
				oldP, _ := args["old"].(string)
				newP, _ := args["new"].(string)
				if oldP == "" || newP == "" {
					return "", fmt.Errorf("both 'old' and 'new' are required")
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
			Description: "Plan a migration between two SQLite or D1 databases WITHOUT applying it. " +
				"Returns the migration SQL plus a blast-radius analysis: each operation classified " +
				"safe / risky / destructive, with an estimated write-lock duration for table rebuilds. " +
				"Use this before applying any migration to a D1 database. Read-only.",
			InputSchema: obj(props{
				"old": strProp("Current ('before') source — " + sourcePropDesc),
				"new": strProp("Target ('after') source with the desired schema — " + sourcePropDesc),
			}, "old", "new"),
			Handler: func(args map[string]interface{}) (string, error) {
				oldP, _ := args["old"].(string)
				newP, _ := args["new"].(string)
				if oldP == "" || newP == "" {
					return "", fmt.Errorf("both 'old' and 'new' are required")
				}
				d, err := diff.Compare(oldP, newP)
				if err != nil {
					return "", err
				}
				var newSch *schema.Schema
				if isRemote(newP) {
					c, err := connector.Open(newP)
					if err != nil {
						return "", err
					}
					defer c.Close()
					newSch, err = c.Schema()
					if err != nil {
						return "", err
					}
				} else {
					newSch, err = schema.Load(newP)
					if err != nil {
						return "", err
					}
				}
				m := migrate.Generate(d, newSch)
				ops, _ := migrate.AnalyzeAll(d, oldP)
				return toJSON(buildPlan(m, ops))
			},
		},
		{
			Name: "litescope_query",
			Description: "Run a read-only SQL query on any SQLite or D1 database and return the " +
				"results as JSON. Only SELECT statements and read-only PRAGMAs are allowed. " +
				"This is the primary tool for an AI agent to explore data in a D1 database.\n\n" +
				"For D1: set CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID and use source=d1://DB_ID.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
				"sql":    strProp("A read-only SQL query (SELECT or read-only PRAGMA). Mutations are rejected."),
			}, "source", "sql"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				sql, _ := args["sql"].(string)
				if sql == "" {
					return "", fmt.Errorf("sql is required")
				}
				if err := rejectMutation(sql); err != nil {
					return "", err
				}
				c, err := connector.Open(src)
				if err != nil {
					return "", err
				}
				defer c.Close()
				rows, err := connector.Query(c, sql)
				if err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{"rows": rows, "count": len(rows)})
			},
		},
		{
			Name: "litescope_advise",
			Description: "Analyze a local SQLite database for performance problems and recommend " +
				"fixes: foreign keys with no index, redundant indexes, and full table scans for " +
				"any supplied queries. Returns findings with runnable CREATE/DROP INDEX suggestions. " +
				"Read-only — recommends, never alters the schema. (Local files only.)",
			InputSchema: obj(props{
				"source": strProp("Local SQLite file path (advise requires direct file access)"),
				"queries": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional SQL queries to check for full table scans",
				},
			}, "source"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("litescope_advise requires a local file; use litescope_schema to inspect a D1 database's schema")
				}
				var queries []string
				if raw, ok := args["queries"].([]interface{}); ok {
					for _, q := range raw {
						if s, ok := q.(string); ok && s != "" {
							queries = append(queries, s)
						}
					}
				}
				r, err := advisor.Analyze(src, queries)
				if err != nil {
					return "", err
				}
				return toJSON(r)
			},
		},
		{
			Name: "litescope_check",
			Description: "Verify a SQLite backup. Runs a PRAGMA integrity check; if 'against' " +
				"is given, also compares schema and row counts to a reference database. " +
				"Returns a JSON report. Read-only. (Local files only.)",
			InputSchema: obj(props{
				"source":  strProp("Local path to the backup database to verify"),
				"against": strProp("Optional local reference database to compare schema against"),
				"data":    boolProp("Also compare row counts per table"),
			}, "source"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				against, _ := args["against"].(string)
				data, _ := args["data"].(bool)
				r, err := check.Check(src, against, data)
				if err != nil {
					return "", err
				}
				return toJSON(r)
			},
		},
		{
			Name: "litescope_d1_list",
			Description: "List all Cloudflare D1 databases in the account. Returns each database's " +
				"UUID, name, creation date, table count, and the DSN to use with other litescope tools. " +
				"Requires CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID environment variables. Read-only.",
			InputSchema: obj(props{}),
			Handler: func(args map[string]interface{}) (string, error) {
				dbs, err := connector.ListD1Databases()
				if err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"databases": dbs,
					"count":     len(dbs),
					"tip":       "Use the 'dsn' field as the 'source' parameter in other litescope tools.",
				})
			},
		},
		{
			Name: "litescope_fingerprint",
			Description: "Cluster a fleet of SQLite databases by schema and report how many distinct " +
				"schemas are running, with each cluster's drift from the canonical (largest) one. " +
				"Reads a fleet config file (litescope.fleet.yaml). Read-only.",
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
				"Reads a fleet config file (litescope.fleet.yaml). Read-only.",
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

// ── helpers ─────────────────────────────────────────────────────────────────

func requireSource(args map[string]interface{}) (string, error) {
	// Accept "source" (new canonical name) or "path" (legacy) for backwards compat.
	src, _ := args["source"].(string)
	if src == "" {
		src, _ = args["path"].(string)
	}
	if src == "" {
		return "", fmt.Errorf("source is required (local path, d1://DB_ID, or turso://TOKEN@ORG/DB)")
	}
	return src, nil
}

func isRemote(src string) bool {
	return strings.HasPrefix(src, "d1://") || strings.HasPrefix(src, "turso://")
}

// rejectMutation blocks SQL that would mutate the database.
func rejectMutation(sql string) error {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "REPLACE", "TRUNCATE", "ATTACH"} {
		if strings.HasPrefix(upper, kw) {
			return fmt.Errorf("litescope_query is read-only; %q statements are not allowed", kw)
		}
	}
	return nil
}

// fleetDBs loads the fleet config and returns the databases matching an optional tag.
func fleetDBs(args map[string]interface{}) ([]fleet.Database, error) {
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
			continue
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
