// Package mcp exposes Litescope as a Model Context Protocol server over stdio,
// so an LLM agent (Claude Desktop, Claude Code, or any MCP client) can call
// Litescope operations as tools. All tools are read-only by default.
package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/advisor"
	"github.com/croc100/litescope/internal/autopilot"
	"github.com/croc100/litescope/internal/check"
	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/d1sync"
	"github.com/croc100/litescope/internal/diff"
	"github.com/croc100/litescope/internal/fleet"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/locks"
	"github.com/croc100/litescope/internal/migrate"
	"github.com/croc100/litescope/internal/safewrite"
	"github.com/croc100/litescope/internal/salvage"
	"github.com/croc100/litescope/internal/schema"
	"github.com/croc100/litescope/internal/snapshot"
)

// Tool is one callable operation exposed to an AI agent. The same registry
// backs both the MCP server and (in future) a built-in BYOK agent.
type Tool struct {
	Name         string
	Description  string
	InputSchema  map[string]interface{} // JSON Schema for the arguments
	OutputSchema map[string]interface{} // optional JSON Schema for structuredContent (MCP 2025-06-18)
	Handler      func(args map[string]interface{}) (string, error)
}

const sourcePropDesc = "Database source: a local file path (./app.db), a Cloudflare D1 DSN " +
	"(d1://DB_ID when CLOUDFLARE_API_TOKEN+CLOUDFLARE_ACCOUNT_ID are set, or " +
	"d1://TOKEN@ACCOUNT_ID/DB_ID), or a Turso DSN (turso://TOKEN@ORG/DB)."

// Registry returns all MCP tools. When allowWrites is true, write-capable tools
// are appended: litescope_query_write, litescope_migrate_apply, litescope_write_undo,
// litescope_salvage, litescope_d1_create, litescope_d1_delete.
func Registry(allowWrites bool) []Tool {
	tools := []Tool{
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
			OutputSchema: outObj(props{
				"severity": {"type": "string", "enum": []string{"ok", "warning", "critical"}, "description": "Overall verdict."},
				"issues":   {"type": "array", "description": "Detected problems (empty when healthy)."},
			}, "severity"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				deep, _ := args["deep"].(bool)
				return toJSON(inspectHealth(src, deep))
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
			OutputSchema: outObj(props{
				"tables": {"type": "array", "items": map[string]interface{}{"type": "object"}, "description": "Tables with their columns and indexes."},
			}, "tables"),
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
			OutputSchema: outObj(props{
				"summary":        {"type": "object", "description": "Counts of tables added/removed/modified."},
				"schema_changes": {"type": "array", "description": "Per-table schema differences."},
				"data_changes":   {"type": "array", "description": "Per-table row-count differences (present when data differs)."},
			}, "summary", "schema_changes"),
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
			OutputSchema: outObj(props{
				"statements":  {"type": "integer", "description": "Number of SQL statements in the plan."},
				"destructive": {"type": "boolean", "description": "True if any operation drops or rewrites data."},
				"operations":  {"type": "array", "description": "Each operation classified safe/risky/destructive with lock estimate."},
				"sql":         {"type": "string", "description": "The migration SQL."},
			}, "statements", "destructive", "operations", "sql"),
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
			Name: "litescope_migrate_diff",
			Description: "Diff two SQLite or D1 databases and return the migration SQL that would " +
				"bring the 'old' source up to the 'new' schema — without applying it or computing " +
				"blast-radius. Useful when you only need the SQL to review or pass to " +
				"litescope_migrate_apply. For a full blast-radius analysis use litescope_migrate_plan. " +
				"Read-only.",
			InputSchema: obj(props{
				"old": strProp("Current ('before') source — " + sourcePropDesc),
				"new": strProp("Target ('after') source with the desired schema — " + sourcePropDesc),
			}, "old", "new"),
			OutputSchema: outObj(props{
				"sql":        {"type": "string", "description": "The migration SQL that brings 'old' up to the 'new' schema."},
				"statements": {"type": "integer", "description": "Number of SQL statements in the migration."},
			}, "sql", "statements"),
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
				return toJSON(map[string]interface{}{
					"sql":        m.SQL(),
					"statements": len(m.Statements),
					"tip":        "Pass 'sql' to litescope_migrate_apply to apply this migration.",
				})
			},
		},
		{
			Name: "litescope_query",
			Description: "Run a read-only SQL query on any SQLite or D1 database and return the " +
				"results as JSON. Only SELECT statements and read-only PRAGMAs are allowed. " +
				"This is the primary tool for an AI agent to explore data in a D1 database.\n\n" +
				"Token budgeting: results are capped at max_rows (default 200) so a large table " +
				"won't blow your context window — the response reports total_rows and truncated. " +
				"Use the columns argument to project only the fields you need. Narrow with LIMIT / " +
				"WHERE for precise reads.\n\n" +
				"For D1: set CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID and use source=d1://DB_ID.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
				"sql":    strProp("A read-only SQL query (SELECT or read-only PRAGMA). Mutations are rejected."),
				"max_rows": map[string]interface{}{
					"type":        "number",
					"description": "Maximum rows to return (default 200, max 2000). Excess rows are dropped and reported via truncated.",
				},
				"columns": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional: keep only these columns in each row (projection) to save context.",
				},
			}, "source", "sql"),
			OutputSchema: outObj(props{
				"rows":       {"type": "array", "items": map[string]interface{}{"type": "object"}, "description": "The result rows."},
				"count":      {"type": "integer", "description": "Rows returned after truncation."},
				"total_rows": {"type": "integer", "description": "Rows the query produced before truncation."},
				"truncated":  {"type": "boolean", "description": "True when total_rows exceeded max_rows."},
			}, "rows", "count", "total_rows", "truncated"),
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
				return toJSON(budgetRows(rows, args))
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
			OutputSchema: outObj(props{
				"path":     {"type": "string", "description": "The analyzed database."},
				"findings": {"type": "array", "description": "Issues with rule, severity, and a runnable suggestion."},
			}, "findings"),
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
			OutputSchema: outObj(props{
				"Path":            {"type": "string", "description": "The backup database that was verified."},
				"IntegrityOK":     {"type": "boolean", "description": "True if PRAGMA integrity_check passed."},
				"IntegrityErrors": {"type": "array", "description": "Integrity problems found (omitted when none)."},
				"Passed":          {"type": "boolean", "description": "Overall pass/fail across every check performed."},
			}, "Path", "IntegrityOK", "Passed"),
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
			OutputSchema: outObj(props{
				"databases": {"type": "array", "description": "Each D1 database with uuid, name, table count, and dsn."},
				"count":     {"type": "integer", "description": "Number of databases."},
			}, "databases", "count"),
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
			OutputSchema: outObj(props{
				"total":       {"type": "integer", "description": "Databases successfully fingerprinted."},
				"clusters":    {"type": "array", "description": "Schema clusters, canonical first, with drift from canonical."},
				"unreachable": {"type": "array", "description": "Databases that could not be read."},
			}, "clusters"),
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
			OutputSchema: outObj(props{
				"results": {"type": "array", "description": "Per-database health reports, worst-first."},
			}, "results"),
			Handler: func(args map[string]interface{}) (string, error) {
				dbs, err := fleetDBs(args)
				if err != nil {
					return "", err
				}
				deep, _ := args["deep"].(bool)
				return toJSON(fleet.Health(dbs, deep, 0))
			},
		},
		{
			Name: "litescope_locks",
			Description: "Diagnose \"database is locked\" / SQLITE_BUSY and writer-starvation " +
				"problems — the most common SQLite production failure. Inspects journal mode, " +
				"busy_timeout, locking mode, and WAL bloat for local files, and returns " +
				"provider-specific guidance for D1 and Turso. Each finding includes the exact " +
				"PRAGMA or DSN change to apply. Returns a JSON report with a verdict " +
				"(ok / attention / critical).\n\n" +
				"Set live=true (local files only) to instead probe the *current* lock state: " +
				"whether a writer is holding the lock right now and which processes have the " +
				"file open. Use this when an app is actively reporting \"database is locked\". " +
				"Read-only.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
				"live":   boolProp("Probe the live lock state right now instead of static PRAGMA config (local files only)"),
			}, "source"),
			OutputSchema: outObj(props{
				"Verdict":  {"type": "string", "description": "Static diagnosis verdict: ok, attention, or critical."},
				"Provider": {"type": "string", "description": "Backend: local, d1, or turso."},
				"Findings": {"type": "array", "description": "Lock-configuration issues, each with the exact PRAGMA/DSN fix."},
				"Pragmas":  {"type": "object", "description": "Relevant PRAGMA values (local files)."},
				"state":    {"type": "string", "description": "With live=true: current lock state — free, locked, readable, or error."},
				"holders":  {"type": "array", "description": "With live=true: processes holding the database file open."},
			}),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if live, _ := args["live"].(bool); live {
					p, err := locks.Probe(src, 250*time.Millisecond)
					if err != nil {
						return "", err
					}
					return toJSON(p)
				}
				r, err := locks.Diagnose(src)
				if err != nil {
					return "", err
				}
				return toJSON(r)
			},
		},
		{
			Name: "litescope_snapshot_list",
			Description: "List point-in-time snapshots (local backups) for a local SQLite database, " +
				"newest first. Snapshots are created with litescope_snapshot (requires --allow-writes). " +
				"Use this to find a snapshot to restore, or to confirm a database has a backup before " +
				"a risky write. Read-only; local files only.",
			InputSchema: obj(props{
				"source": strProp("Local SQLite file path."),
			}, "source"),
			OutputSchema: outObj(props{
				"source":    {"type": "string", "description": "The database the snapshots belong to."},
				"count":     {"type": "integer", "description": "Number of snapshots."},
				"snapshots": {"type": "array", "description": "Snapshots, newest first, with path, label, and timestamp."},
			}, "source", "count", "snapshots"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("snapshots are for local SQLite files; %q is remote", src)
				}
				snaps, err := snapshot.List(src)
				if err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"source":    src,
					"count":     len(snaps),
					"snapshots": snaps,
				})
			},
		},
		{
			Name: "litescope_autopilot",
			Description: "Self-driving DBA for a local SQLite database. Derives safe maintenance and " +
				"optimization actions — ANALYZE, PRAGMA optimize, missing foreign-key indexes, and " +
				"(when fragmented) VACUUM / redundant-index cleanup — each explained in plain " +
				"language. When queries are supplied, also flags full table scans, predicates on " +
				"an expression (lower(col), date(col) — needs an expression index), and equality " +
				"filters that only match a small slice of the table (needs a partial index instead " +
				"of indexing the whole column). For large databases it also flags an undersized " +
				"page cache (PRAGMA cache_size/mmap_size — guidance only, not stored in the file).\n\n" +
				"Dry-run by default (apply=false): returns the plan without changing anything. " +
				"apply=true executes the safe actions and requires --allow-writes; a snapshot is " +
				"taken first so the run is one litescope_restore away from undo. Risky actions " +
				"(VACUUM, dropping indexes, query-derived indexes) only run with aggressive=true. " +
				"Local files only.",
			InputSchema: obj(props{
				"source":     strProp("Local SQLite file path."),
				"apply":      boolProp("Execute the safe actions. Default false (dry-run). Requires --allow-writes."),
				"aggressive": boolProp("Also run risky actions (VACUUM, drop redundant indexes, query-derived indexes)."),
				"queries": {
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Real SELECT statements this session has observed running against the database. Fed to EXPLAIN QUERY PLAN to detect full scans, expression predicates, and low-selectivity equality filters.",
				},
			}, "source"),
			OutputSchema: outObj(props{
				"path":    {"type": "string", "description": "The optimized database."},
				"applied": {"type": "boolean", "description": "True if actions were executed (apply=true); false for a dry-run plan."},
				"actions": {"type": "array", "description": "Each action with kind, risk, SQL, status, and a plain-language reason."},
				"counts":  {"type": "object", "description": "Action counts by status: applied, proposed, skipped, failed."},
			}, "path", "actions"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("autopilot is for local SQLite files; %q is remote", src)
				}
				apply, _ := args["apply"].(bool)
				if apply && !allowWrites {
					return "", fmt.Errorf("apply=true requires the MCP server to be started with --allow-writes")
				}
				aggressive, _ := args["aggressive"].(bool)
				var queries []string
				if raw, ok := args["queries"].([]interface{}); ok {
					for _, q := range raw {
						if s, ok := q.(string); ok && s != "" {
							queries = append(queries, s)
						}
					}
				}
				plan, err := autopilot.BuildPlan(src, queries)
				if err != nil {
					return "", err
				}
				res, err := autopilot.Run(src, plan, autopilot.RunOptions{Apply: apply, Aggressive: aggressive})
				if err != nil {
					return "", err
				}
				return toJSON(res)
			},
		},
	}

	if allowWrites {
		tools = append(tools, writeTools()...)
	}
	return tools
}

// writeTools returns tools that can mutate databases. Only included when the
// MCP server is started with --allow-writes.
func writeTools() []Tool {
	return []Tool{
		{
			Name: "litescope_rewind",
			Description: "Restore a Cloudflare D1 database to a previous point in time using " +
				"D1 Time Travel. Accepts human-readable timestamps: \"2h ago\", \"3d ago\", " +
				"\"yesterday\", RFC 3339 (\"2024-01-15T10:30:00Z\"), or \"now\". " +
				"Only available when litescope mcp is started with --allow-writes. " +
				"Requires CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID environment variables.\n\n" +
				"⚠ This is destructive: the database is restored in-place. Use litescope_health " +
				"or litescope_query to inspect the database before rewinding.",
			InputSchema: obj(props{
				"source": strProp("D1 database DSN (d1://DB_ID). Only D1 is supported."),
				"to":     strProp("Point in time to restore to: \"2h ago\", \"3d ago\", \"yesterday\", RFC 3339, or \"now\"."),
			}, "source", "to"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if !strings.HasPrefix(src, "d1://") {
					return "", fmt.Errorf("litescope_rewind only supports D1 databases (d1://DB_ID); got %q", src)
				}
				toStr, _ := args["to"].(string)
				if toStr == "" {
					return "", fmt.Errorf("'to' is required (e.g. \"2h ago\", \"yesterday\", RFC 3339)")
				}
				ts, err := parseMCPTime(toStr)
				if err != nil {
					return "", err
				}
				_, accountID, databaseID, err := connector.ParseD1DSN(src)
				if err != nil {
					return "", err
				}
				result, err := connector.D1TimeTravel(accountID, databaseID, ts)
				if err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"ok":        true,
					"source":    src,
					"restored":  result.Timestamp,
					"bookmark":  result.Bookmark,
					"requested": ts.UTC().Format(time.RFC3339),
				})
			},
		},
		{
			Name: "litescope_d1_pull",
			Description: "Download a Cloudflare D1 database to a local SQLite file. Copies the " +
				"full schema and all rows. Useful for local inspection, backup, or diffing with " +
				"another database. Only available when litescope mcp is started with --allow-writes.\n\n" +
				"After pulling, use litescope_health or litescope_schema on the local file.",
			InputSchema: obj(props{
				"source":     strProp("D1 database DSN (d1://DB_ID). Requires CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID."),
				"local_path": strProp("Local file path to write the SQLite database to (e.g. ./snapshot.db). Created or overwritten."),
				"batch_size": map[string]interface{}{"type": "number", "description": "Rows per SELECT page (default 500)."},
			}, "source", "local_path"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if !strings.HasPrefix(src, "d1://") {
					return "", fmt.Errorf("litescope_d1_pull requires a D1 source (d1://DB_ID); got %q", src)
				}
				localPath, _ := args["local_path"].(string)
				if localPath == "" {
					return "", fmt.Errorf("local_path is required")
				}
				batchSize := 500
				if n, ok := args["batch_size"].(float64); ok && n > 0 {
					batchSize = int(n)
				}
				var tables []map[string]interface{}
				opts := d1sync.PullOptions{
					BatchSize: batchSize,
					ProgressFn: func(table string, rows int) {
						tables = append(tables, map[string]interface{}{"table": table, "rows": rows})
					},
				}
				if err := d1sync.Pull(src, localPath, opts); err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"ok":         true,
					"source":     src,
					"local_path": localPath,
					"tables":     tables,
				})
			},
		},
		{
			Name: "litescope_query_write",
			Description: "Execute a mutating SQL statement (INSERT, UPDATE, DELETE, CREATE TABLE, " +
				"DROP TABLE, etc.) with agent guardrails. Only available with --allow-writes.\n\n" +
				"⚠ DRY-RUN BY DEFAULT: with apply=false (the default) the statement is NOT applied — " +
				"it runs inside a transaction that is rolled back, and the tool returns the exact " +
				"rows_affected (blast radius) so you can reason before committing. " +
				"Set apply=true to commit; a snapshot is taken automatically before the write so it " +
				"is one rewind away from undo. On a lock/busy failure the tool returns structured " +
				"lock-doctor remediation instead of a raw error. Local SQLite files only for the " +
				"guarded path; D1/Turso fall back to direct execution and require apply=true.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
				"sql":    strProp("SQL statement(s) to execute, separated by semicolons."),
				"apply":  boolProp("Commit the change. Default false (dry-run: measure impact only)."),
			}, "source", "sql"),
			Handler: func(args map[string]interface{}) (string, error) {
				return handleGuardedWrite(args)
			},
		},
		{
			Name: "litescope_migrate_apply",
			Description: "Apply a multi-statement SQL migration with agent guardrails. Only available " +
				"with --allow-writes.\n\n" +
				"⚠ DRY-RUN BY DEFAULT: with apply=false (the default) the migration is validated " +
				"inside a transaction and rolled back, returning per-statement rows_affected so you " +
				"can review the blast radius first. Set apply=true to commit; a pre-migration " +
				"snapshot is taken automatically and restored if the commit fails. Lock/busy errors " +
				"return structured lock-doctor remediation. Local SQLite files only for the guarded " +
				"path; D1/Turso fall back to sequential execution and require apply=true.",
			InputSchema: obj(props{
				"source": strProp(sourcePropDesc),
				"sql":    strProp("One or more SQL statements separated by semicolons."),
				"apply":  boolProp("Commit the migration. Default false (dry-run: validate + measure only)."),
			}, "source", "sql"),
			Handler: func(args map[string]interface{}) (string, error) {
				return handleGuardedWrite(args)
			},
		},
		{
			Name: "litescope_write_undo",
			Description: "Revert a local SQLite write made via litescope_query_write or " +
				"litescope_migrate_apply, using the rewind_token from that call's response. " +
				"The token is bound to the exact source path it was minted for — passing it " +
				"with a different source is rejected rather than silently restoring the wrong " +
				"database. This restores the pre-write snapshot in place, taking a pre-restore " +
				"safety snapshot of the current (post-write) state first. Only available with " +
				"--allow-writes; local files only.",
			InputSchema: obj(props{
				"source":       strProp("Local SQLite file path to restore into — must match the source the token was minted for."),
				"rewind_token": strProp("The rewind_token returned by litescope_query_write / litescope_migrate_apply."),
			}, "source", "rewind_token"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("litescope_write_undo is for local SQLite files; %q is remote (use litescope_rewind for D1)", src)
				}
				token, _ := args["rewind_token"].(string)
				if token == "" {
					return "", fmt.Errorf("rewind_token is required")
				}
				snapPath, err := safewrite.DecodeRewindToken(token, src)
				if err != nil {
					return "", err
				}
				if err := snapshot.Restore(src, snapPath, true); err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"ok":       true,
					"source":   src,
					"restored": snapPath,
					"note":     "Reverted to the pre-write snapshot. A safety snapshot of the prior (post-write) state was taken first.",
				})
			},
		},
		{
			Name: "litescope_d1_create",
			Description: "Create a new Cloudflare D1 database in the account. Returns the UUID and " +
				"DSN of the newly created database. Requires CLOUDFLARE_API_TOKEN and " +
				"CLOUDFLARE_ACCOUNT_ID environment variables. Only available with --allow-writes.",
			InputSchema: obj(props{
				"name":     strProp("Name for the new D1 database."),
				"location": strProp("Optional primary location hint (e.g. 'wnam', 'enam', 'weur', 'eeur', 'apac')."),
			}, "name"),
			Handler: func(args map[string]interface{}) (string, error) {
				name, _ := args["name"].(string)
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				location, _ := args["location"].(string)
				db, err := connector.D1CreateDatabase(name, location)
				if err != nil {
					return "", err
				}
				return toJSON(db)
			},
		},
		{
			Name: "litescope_d1_delete",
			Description: "Permanently delete a Cloudflare D1 database. This is irreversible — " +
				"Time Travel history is also removed. Requires CLOUDFLARE_API_TOKEN and " +
				"CLOUDFLARE_ACCOUNT_ID environment variables. Only available with --allow-writes.",
			InputSchema: obj(props{
				"database_id": strProp("UUID of the D1 database to delete."),
			}, "database_id"),
			Handler: func(args map[string]interface{}) (string, error) {
				id, _ := args["database_id"].(string)
				if id == "" {
					return "", fmt.Errorf("database_id is required")
				}
				if err := connector.D1DeleteDatabase(id); err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"ok":          true,
					"database_id": id,
					"warning":     "Database deleted permanently including all Time Travel history.",
				})
			},
		},
		{
			Name: "litescope_snapshot",
			Description: "Take a point-in-time snapshot (local backup) of a local SQLite database. " +
				"Uses VACUUM INTO so the copy is consistent even under WAL, stored in a sibling " +
				".litescope-snapshots/ directory and integrity-checked after creation. This is the " +
				"local/Turso equivalent of D1 Time Travel — take one before any risky write. " +
				"Only available with --allow-writes; local files only.",
			InputSchema: obj(props{
				"source": strProp("Local SQLite file path."),
				"label":  strProp("Optional label recorded in the snapshot name (e.g. 'before-migration')."),
				"keep": map[string]interface{}{
					"type":        "number",
					"description": "Retention: keep only the N newest snapshots (0 = keep all).",
				},
			}, "source"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("snapshots are for local SQLite files; %q is remote (use litescope_rewind for D1)", src)
				}
				label, _ := args["label"].(string)
				keep := 0
				if n, ok := args["keep"].(float64); ok && n > 0 {
					keep = int(n)
				}
				snap, err := snapshot.Create(src, snapshot.CreateOptions{Label: label, Keep: keep})
				if err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{"ok": true, "snapshot": snap})
			},
		},
		{
			Name: "litescope_restore",
			Description: "Restore a local SQLite database from a point-in-time snapshot. With no " +
				"'snapshot' argument the newest snapshot is restored. The snapshot is integrity-" +
				"checked first, and the current database is itself snapshotted as a pre-restore " +
				"safety net before being overwritten. Only available with --allow-writes; local " +
				"files only. List options with litescope_snapshot_list.",
			InputSchema: obj(props{
				"source":   strProp("Local SQLite file path to restore into."),
				"snapshot": strProp("Snapshot file path to restore from. Default: newest snapshot."),
			}, "source"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("restore is for local SQLite files; %q is remote (use litescope_rewind for D1)", src)
				}
				snapPath, _ := args["snapshot"].(string)
				if snapPath == "" {
					latest, ok, err := snapshot.Latest(src)
					if err != nil {
						return "", err
					}
					if !ok {
						return "", fmt.Errorf("no snapshots found for %s; create one with litescope_snapshot", src)
					}
					snapPath = latest.Path
				}
				if err := snapshot.Restore(src, snapPath, true); err != nil {
					return "", err
				}
				return toJSON(map[string]interface{}{
					"ok":       true,
					"source":   src,
					"restored": snapPath,
					"note":     "A pre-restore safety snapshot was taken before overwriting.",
				})
			},
		},
		{
			Name: "litescope_salvage",
			Description: "Recover readable rows from a corrupt local SQLite database into a brand-new " +
				"file, for the case where litescope_restore has no healthy snapshot to fall back on. " +
				"Does not modify the source file: replays the schema from sqlite_master into 'output', " +
				"then copies every row it can still read out of each table, isolating and skipping only " +
				"the exact rowids that live on corrupt pages instead of giving up on the whole table. " +
				"This is a pure-Go approximation of the sqlite3 shell's '.recover' command (not available " +
				"through this project's cgo-free driver) — it can't do a full page-level scan, so treat " +
				"the result as best-effort, not a guarantee of completeness. 'output' must not already " +
				"exist. Only available with --allow-writes (it writes a new file, though never the " +
				"source); local files only.",
			InputSchema: obj(props{
				"source": strProp("Local path to the corrupt SQLite database (read-only; never modified)."),
				"output": strProp("Path to write the recovered database to. Must not already exist."),
			}, "source", "output"),
			OutputSchema: outObj(props{
				"source":         {"type": "string", "description": "The corrupt database that was read."},
				"output":         {"type": "string", "description": "The recovered database that was written."},
				"tables":         {"type": "array", "description": "Per-table rows_copied/rows_lost, and any schema-replay error."},
				"schema_lost":    {"type": "array", "description": "Indexes/views/triggers that failed to replay."},
				"output_healthy": {"type": "boolean", "description": "Whether the recovered file itself passes quick_check."},
			}, "source", "output", "tables"),
			Handler: func(args map[string]interface{}) (string, error) {
				src, err := requireSource(args)
				if err != nil {
					return "", err
				}
				if isRemote(src) {
					return "", fmt.Errorf("litescope_salvage is for local SQLite files; %q is remote", src)
				}
				output, _ := args["output"].(string)
				if output == "" {
					return "", fmt.Errorf("output is required")
				}
				res, err := salvage.Recover(src, output)
				if err != nil {
					return "", err
				}
				return toJSON(res)
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

// inspectHealth runs a health check on src, shared by the litescope_health
// tool and the litescope://health/{source} resource. Remote sources only get
// a reachability check; local files get the full corruption/WAL/fragmentation
// inspection.
func inspectHealth(src string, deep bool) interface{} {
	if isRemote(src) {
		c, err := connector.Open(src)
		if err != nil {
			return map[string]interface{}{
				"source": src, "reachable": false,
				"severity": "critical", "issues": []string{err.Error()},
			}
		}
		defer c.Close()
		if _, err := c.Schema(); err != nil {
			return map[string]interface{}{
				"source": src, "reachable": false,
				"severity": "critical", "issues": []string{err.Error()},
			}
		}
		return map[string]interface{}{
			"source": src, "reachable": true,
			"severity": "ok", "issues": []string{},
			"note": "Full PRAGMA checks require a local file; remote health shows reachability only.",
		}
	}
	return health.Inspect(src, deep)
}

// budgetRows enforces token-budgeting on a query result: it applies an optional
// column projection and caps the row count, reporting what was dropped so the
// agent knows the result is partial instead of silently losing data.
func budgetRows(rows []map[string]interface{}, args map[string]interface{}) map[string]interface{} {
	const defaultMax, hardMax = 200, 2000
	maxRows := defaultMax
	if n, ok := args["max_rows"].(float64); ok && n > 0 {
		maxRows = int(n)
	}
	if maxRows > hardMax {
		maxRows = hardMax
	}

	// Column projection.
	if cols := stringSlice(args["columns"]); len(cols) > 0 {
		keep := make(map[string]bool, len(cols))
		for _, c := range cols {
			keep[c] = true
		}
		for i, row := range rows {
			projected := make(map[string]interface{}, len(cols))
			for k, v := range row {
				if keep[k] {
					projected[k] = v
				}
			}
			rows[i] = projected
		}
	}

	total := len(rows)
	truncated := false
	if total > maxRows {
		rows = rows[:maxRows]
		truncated = true
	}

	out := map[string]interface{}{
		"rows":       rows,
		"count":      len(rows),
		"total_rows": total,
		"truncated":  truncated,
	}
	if truncated {
		out["note"] = fmt.Sprintf("showing %d of %d rows (max_rows=%d). Narrow with LIMIT/WHERE or raise max_rows.", len(rows), total, maxRows)
	}
	return out
}

func stringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
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

// handleGuardedWrite backs litescope_query_write and litescope_migrate_apply.
// Local SQLite files go through the safewrite guardrails (dry-run by default,
// exact impact preview, auto-snapshot, lock remediation). Remote providers
// (D1/Turso) have no client-side transaction, so they fall back to direct
// sequential execution and require an explicit apply=true.
func handleGuardedWrite(args map[string]interface{}) (string, error) {
	src, err := requireSource(args)
	if err != nil {
		return "", err
	}
	sqlText, _ := args["sql"].(string)
	if sqlText == "" {
		return "", fmt.Errorf("sql is required")
	}
	apply, _ := args["apply"].(bool)

	if !isRemote(src) {
		res, err := safewrite.PlanLocal(src, sqlText, apply)
		if err != nil {
			return "", err
		}
		return toJSON(res)
	}

	// Remote: no transactional dry-run available.
	if !apply {
		return toJSON(map[string]interface{}{
			"ok":      false,
			"applied": false,
			"note": "Remote databases (D1/Turso) have no client-side transaction, so dry-run " +
				"impact preview is unavailable. Re-run with apply=true to execute. Consider " +
				"litescope_d1_pull to snapshot a D1 database locally first, or litescope_rewind to undo.",
		})
	}
	beforeWrite := time.Now().UTC()
	conn, err := connector.Open(src)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	exec, ok := connector.AsExecutor(conn)
	if !ok {
		return "", fmt.Errorf("connector for %q does not support execution", src)
	}
	stmts := splitStatements(sqlText)
	if err := exec.Exec(stmts, false); err != nil {
		return "", fmt.Errorf("write failed: %w", err)
	}
	out := map[string]interface{}{
		"ok":         true,
		"applied":    true,
		"statements": len(stmts),
		"source":     src,
	}
	if strings.HasPrefix(src, "d1://") {
		// D1 Time Travel gives us a real undo point: the moment just before
		// this write executed. litescope_rewind can restore to it directly.
		out["rewind_token"] = beforeWrite.Format(time.RFC3339)
		out["note"] = "Pass rewind_token as the 'to' argument of litescope_rewind to undo this write via D1 Time Travel."
	} else {
		out["note"] = "Turso has no server-side time-travel undo in litescope; take a litescope_d1_pull-style " +
			"manual backup before writes you may need to revert."
	}
	return toJSON(out)
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

// outObj builds a JSON Schema for a tool's structured output. additionalProperties
// is left unset (defaults to true) so handlers may include extra fields (note,
// tips, source echoes) without violating the declared schema.
func outObj(p props, required ...string) map[string]interface{} {
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

// parseMCPTime converts human-readable time strings to time.Time.
// Supports RFC 3339, "2h ago", "3d ago", "yesterday", "now".
func parseMCPTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	now := time.Now().UTC()

	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	if s == "yesterday" {
		return now.Add(-24 * time.Hour), nil
	}
	if s == "now" {
		return now, nil
	}

	s2 := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), " ago"), "ago"))
	for _, u := range []struct {
		suffix string
		d      time.Duration
	}{
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	} {
		if strings.HasSuffix(s2, u.suffix) {
			var n int
			if _, err := fmt.Sscanf(strings.TrimSuffix(s2, u.suffix), "%d", &n); err == nil {
				return now.Add(-time.Duration(n) * u.d), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format; use \"2h ago\", \"3d ago\", \"yesterday\", RFC 3339, or \"now\"")
}

// splitStatements splits a SQL string on semicolons, trimming whitespace and
// skipping empty statements.
func splitStatements(sql string) []string {
	parts := strings.Split(sql, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ── tool annotations (MCP 2025-03-26+ behavioral hints) ─────────────────────
//
// Annotations are untrusted hints that let a client reason about a tool before
// calling it — most importantly whether it only reads (readOnlyHint) or can
// destroy data (destructiveHint), which lines up with Litescope's
// read-only-by-default / guarded-write safety model. idempotentHint marks
// tools that are safe to retry; openWorldHint marks tools that can reach a
// remote provider (Cloudflare D1 / Turso) rather than only the local file.
type toolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// annotationsByName maps each tool to its behavioral hints. Read-only tools are
// the default posture; only the write/mutating tools set readOnlyHint=false,
// and the data-replacing ones set destructiveHint=true.
var annotationsByName = map[string]toolAnnotations{
	// Read-only diagnostics & inspection.
	"litescope_health":        {Title: "Inspect database health", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_schema":        {Title: "Inspect schema", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_diff":          {Title: "Diff two databases", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_migrate_plan":  {Title: "Plan a migration (no apply)", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_migrate_diff":  {Title: "Generate migration SQL", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_query":         {Title: "Run a read-only query", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_advise":        {Title: "Recommend indexes", ReadOnlyHint: true, IdempotentHint: true},
	"litescope_locks":         {Title: "Diagnose database locks", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},
	"litescope_fingerprint":   {Title: "Fingerprint fleet schemas", ReadOnlyHint: true, IdempotentHint: true},
	"litescope_fleet_health":  {Title: "Fleet health overview", ReadOnlyHint: true, IdempotentHint: true},
	"litescope_check":         {Title: "Verify backup integrity", ReadOnlyHint: true, IdempotentHint: true},
	"litescope_snapshot_list": {Title: "List snapshots", ReadOnlyHint: true, IdempotentHint: true},
	"litescope_d1_list":       {Title: "List D1 databases", ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: true},

	// Mutating but non-destructive (create artifacts / additive changes).
	"litescope_snapshot":  {Title: "Create a snapshot", ReadOnlyHint: false, DestructiveHint: false},
	"litescope_autopilot": {Title: "Self-driving optimize (dry-run default)", ReadOnlyHint: false, DestructiveHint: false},
	"litescope_d1_pull":   {Title: "Pull D1 to a local copy", ReadOnlyHint: false, DestructiveHint: false, OpenWorldHint: true},
	"litescope_d1_create": {Title: "Create a D1 database", ReadOnlyHint: false, DestructiveHint: false, OpenWorldHint: true},
	"litescope_salvage":   {Title: "Recover rows from a corrupt database", ReadOnlyHint: false, DestructiveHint: false},

	// Destructive — replace or remove data.
	"litescope_restore":       {Title: "Restore from a snapshot", ReadOnlyHint: false, DestructiveHint: true, OpenWorldHint: false},
	"litescope_write_undo":    {Title: "Undo a guarded write", ReadOnlyHint: false, DestructiveHint: true, OpenWorldHint: false},
	"litescope_rewind":        {Title: "Rewind via D1 Time Travel", ReadOnlyHint: false, DestructiveHint: true, OpenWorldHint: true},
	"litescope_query_write":   {Title: "Run a write query", ReadOnlyHint: false, DestructiveHint: true, OpenWorldHint: true},
	"litescope_migrate_apply": {Title: "Apply a migration", ReadOnlyHint: false, DestructiveHint: true, OpenWorldHint: true},
	"litescope_d1_delete":     {Title: "Delete a D1 database", ReadOnlyHint: false, DestructiveHint: true, IdempotentHint: true, OpenWorldHint: true},
}

// annotationsFor returns the behavioral hints for a tool. Unknown tools default
// to the conservative posture (not read-only, not destructive) so a client
// never assumes a tool is safe without an explicit hint.
func annotationsFor(name string) toolAnnotations {
	return annotationsByName[name]
}
