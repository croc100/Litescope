package mcp

// Prompt is a canned, parameterized workflow exposed over MCP's prompts
// capability. Clients list these and present them to the user as ready-made
// actions ("diagnose my locked database"), then the server fills in a guiding
// message that tells the agent exactly which Litescope tools to chain.
type Prompt struct {
	Name        string
	Description string
	Arguments   []PromptArg
	// Render builds the user-facing message text from the supplied arguments.
	Render func(args map[string]string) string
}

// PromptArg describes one argument a prompt accepts.
type PromptArg struct {
	Name        string
	Description string
	Required    bool
}

// Prompts returns the canned workflows. They reference Litescope's own tools so
// an agent gets a concrete, safe plan rather than improvising.
func Prompts() []Prompt {
	return []Prompt{
		{
			Name:        "diagnose_locked_database",
			Description: "Diagnose and fix a 'database is locked' / SQLITE_BUSY error.",
			Arguments: []PromptArg{
				{Name: "source", Description: "Database source (local path, d1://…, turso://…)", Required: true},
			},
			Render: func(a map[string]string) string {
				src := a["source"]
				return "My SQLite database " + src + " is reporting \"database is locked\" / SQLITE_BUSY.\n\n" +
					"Please diagnose and fix it:\n" +
					"1. Call litescope_locks with source=\"" + src + "\" to read the static lock configuration (journal mode, busy_timeout, locking mode, WAL size) and its prescribed fixes.\n" +
					"2. Call litescope_locks again with source=\"" + src + "\" and live=true to see whether a writer is holding the lock right now and which process owns the file.\n" +
					"3. Summarize the verdict and apply the recommended PRAGMA/config changes, explaining each one."
			},
		},
		{
			Name:        "review_migration",
			Description: "Review a SQL migration for blast radius before applying it.",
			Arguments: []PromptArg{
				{Name: "source", Description: "Database the migration targets", Required: true},
				{Name: "sql", Description: "The migration SQL", Required: true},
			},
			Render: func(a map[string]string) string {
				return "Review this migration against " + a["source"] + " before I apply it:\n\n" +
					"```sql\n" + a["sql"] + "\n```\n\n" +
					"Steps:\n" +
					"1. Call litescope_migrate_apply with source=\"" + a["source"] + "\", the SQL above, and apply=false (dry-run) to measure exact rows affected per statement and surface any error.\n" +
					"2. Flag any destructive or high-blast-radius statements and explain the impact.\n" +
					"3. Only if it looks safe, tell me the exact call to apply it (apply=true) — a snapshot is taken automatically first."
			},
		},
		{
			Name:        "safe_optimize",
			Description: "Tune a database with autopilot, reviewing the plan before applying.",
			Arguments: []PromptArg{
				{Name: "source", Description: "Local SQLite file to optimize", Required: true},
			},
			Render: func(a map[string]string) string {
				src := a["source"]
				return "Optimize " + src + " safely.\n\n" +
					"1. Call litescope_autopilot with source=\"" + src + "\" and apply=false to see the proposed maintenance/optimization plan and the plain-language reason for each action.\n" +
					"2. Walk me through the plan, separating safe actions from risky ones.\n" +
					"3. If I approve, call litescope_autopilot with apply=true (add aggressive=true only if I explicitly want the risky actions). A snapshot is taken automatically before any change."
			},
		},
		{
			Name:        "health_checkup",
			Description: "Run a full operational checkup on a database and explain the findings.",
			Arguments: []PromptArg{
				{Name: "source", Description: "Database to inspect", Required: true},
			},
			Render: func(a map[string]string) string {
				src := a["source"]
				return "Give " + src + " a full operational checkup.\n\n" +
					"1. Call litescope_health with source=\"" + src + "\" for corruption, WAL bloat, fragmentation, and backup posture.\n" +
					"2. Call litescope_advise with source=\"" + src + "\" for missing indexes and other performance issues.\n" +
					"3. If no backup exists, recommend litescope_snapshot. Summarize the overall verdict and the top three things I should do."
			},
		},
	}
}

// promptDescriptors renders the prompt list for prompts/list.
func promptDescriptors(prompts []Prompt) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(prompts))
	for _, p := range prompts {
		args := make([]map[string]interface{}, 0, len(p.Arguments))
		for _, a := range p.Arguments {
			args = append(args, map[string]interface{}{
				"name":        a.Name,
				"description": a.Description,
				"required":    a.Required,
			})
		}
		out = append(out, map[string]interface{}{
			"name":        p.Name,
			"description": p.Description,
			"arguments":   args,
		})
	}
	return out
}
