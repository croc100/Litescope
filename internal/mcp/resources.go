package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/health"
	"github.com/croc100/litescope/internal/locks"
	"github.com/croc100/litescope/internal/schema"
)

// Litescope exposes four read-only resources per database, addressed by URI:
//
//	litescope://schema/<source>      — CREATE-style schema overview
//	litescope://dictionary/<source>  — a human/agent-readable data dictionary
//	litescope://health/<source>      — live operational health (corruption, WAL, bloat)
//	litescope://locks/<source>       — live lock/contention diagnosis
//
// <source> is any DSN litescope understands (a local path, d1://…, turso://…).
// Because the server is not bound to a single database, these are surfaced as
// resource *templates*; a client substitutes the source. When the server is
// started with a default source, the same resources are also listed
// concretely so simple clients can read them without filling a template.
//
// Unlike schema/dictionary, health and locks are computed fresh on every read
// rather than cached. They also use a different subscription trigger: schema
// rarely changes, so schema/dictionary notify on any file-mtime bump. Health
// and locks recompute their verdict every tick and notify only when severity
// or verdict actually changes — otherwise a busy database would fire an
// "updated" notification on every single write. See watchResources/liveSignature.
//
// litescope://health/{source} accepts an optional ?stale_after=<duration>
// (e.g. "1h") to flag a heartbeat stall — no writes within that window — the
// same check as `litescope health --stale-after`. Omitted or zero disables it.
const (
	schemaScheme = "litescope://schema/"
	dictScheme   = "litescope://dictionary/"
	healthScheme = "litescope://health/"
	locksScheme  = "litescope://locks/"
)

// resourceTemplates returns the URI templates advertised by resources/templates/list.
func resourceTemplates() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"uriTemplate": "litescope://schema/{source}",
			"name":        "Database schema",
			"description": "CREATE-style schema overview (tables, columns, indexes, foreign keys) for {source}.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "litescope://dictionary/{source}",
			"name":        "Data dictionary",
			"description": "Human-readable data dictionary (every table and column described) for {source}.",
			"mimeType":    "text/markdown",
		},
		{
			"uriTemplate": "litescope://health/{source}",
			"name":        "Database health",
			"description": "Live operational health for {source}: corruption, WAL bloat, fragmentation, reachability. Append ?stale_after=1h to flag a heartbeat stall. Subscribe to be notified only when severity actually changes (not on every write).",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "litescope://locks/{source}",
			"name":        "Lock contention diagnosis",
			"description": "Live lock/writer-starvation diagnosis for {source}. Subscribe to be notified only when the verdict actually changes (not on every write).",
			"mimeType":    "application/json",
		},
	}
}

// concreteResources lists the two resources for a bound default source, or nil.
func concreteResources(defaultSource string) []map[string]interface{} {
	// Always return a non-nil slice: the MCP spec requires resources/list to
	// return a JSON array, and a nil slice marshals to null (rejected by spec-
	// compliant clients). When no source is bound the list is empty, not null.
	if defaultSource == "" {
		return []map[string]interface{}{}
	}
	return []map[string]interface{}{
		{
			"uri":         schemaScheme + defaultSource,
			"name":        "Database schema (" + defaultSource + ")",
			"description": "CREATE-style schema overview for " + defaultSource + ".",
			"mimeType":    "text/markdown",
		},
		{
			"uri":         dictScheme + defaultSource,
			"name":        "Data dictionary (" + defaultSource + ")",
			"description": "Human-readable data dictionary for " + defaultSource + ".",
			"mimeType":    "text/markdown",
		},
		{
			"uri":         healthScheme + defaultSource,
			"name":        "Database health (" + defaultSource + ")",
			"description": "Live operational health for " + defaultSource + ".",
			"mimeType":    "application/json",
		},
		{
			"uri":         locksScheme + defaultSource,
			"name":        "Lock contention diagnosis (" + defaultSource + ")",
			"description": "Live lock/writer-starvation diagnosis for " + defaultSource + ".",
			"mimeType":    "application/json",
		},
	}
}

// readResource resolves a litescope:// URI to its rendered text content.
func readResource(uri string) (text, mime string, err error) {
	switch {
	case strings.HasPrefix(uri, schemaScheme):
		src := strings.TrimPrefix(uri, schemaScheme)
		s, err := loadSchema(src)
		if err != nil {
			return "", "", err
		}
		return renderSchema(src, s), "text/markdown", nil
	case strings.HasPrefix(uri, dictScheme):
		src := strings.TrimPrefix(uri, dictScheme)
		s, err := loadSchema(src)
		if err != nil {
			return "", "", err
		}
		return renderDictionary(src, s), "text/markdown", nil
	case strings.HasPrefix(uri, healthScheme):
		src, staleAfter := splitStaleAfter(strings.TrimPrefix(uri, healthScheme))
		if src == "" {
			return "", "", fmt.Errorf("resource URI is missing a source")
		}
		rep := inspectHealth(src, false)
		if r, ok := rep.(*health.Report); ok {
			r.CheckStaleness(staleAfter)
		}
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return "", "", err
		}
		return string(b), "application/json", nil
	case strings.HasPrefix(uri, locksScheme):
		src := strings.TrimPrefix(uri, locksScheme)
		if src == "" {
			return "", "", fmt.Errorf("resource URI is missing a source")
		}
		r, err := locks.Diagnose(src)
		if err != nil {
			return "", "", err
		}
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return "", "", err
		}
		return string(b), "application/json", nil
	default:
		return "", "", fmt.Errorf("unknown resource URI: %s", uri)
	}
}

// resourceFilePath returns the local file backing a schema/dictionary resource
// URI, or "" when the URI is remote (d1/turso) or unrecognized. Used by the
// subscription watcher, which can only observe local files.
func resourceFilePath(uri string) string {
	var src string
	switch {
	case strings.HasPrefix(uri, schemaScheme):
		src = strings.TrimPrefix(uri, schemaScheme)
	case strings.HasPrefix(uri, dictScheme):
		src = strings.TrimPrefix(uri, dictScheme)
	case strings.HasPrefix(uri, healthScheme):
		src, _ = splitStaleAfter(strings.TrimPrefix(uri, healthScheme))
	case strings.HasPrefix(uri, locksScheme):
		src = strings.TrimPrefix(uri, locksScheme)
	default:
		return ""
	}
	if src == "" || isRemote(src) {
		return ""
	}
	return src
}

// splitStaleAfter pulls a "?stale_after=<duration>" suffix off a health
// resource's source, e.g. "app.db?stale_after=1h" -> ("app.db", time.Hour).
// An absent, empty, or unparseable duration disables the check (0).
func splitStaleAfter(raw string) (src string, staleAfter time.Duration) {
	const key = "?stale_after="
	i := strings.Index(raw, key)
	if i < 0 {
		return raw, 0
	}
	d, err := time.ParseDuration(raw[i+len(key):])
	if err != nil {
		return raw[:i], 0
	}
	return raw[:i], d
}

// liveSignature computes a short state signature for a health/locks resource
// — severity for health, verdict for locks — that changes only when the
// underlying diagnosis actually changes. The subscription watcher uses this
// instead of raw file-mtime so a busy database doesn't fire a notification on
// every write; only real severity/verdict transitions (including a heartbeat
// going stale) are pushed. Returns ok=false for anything else (remote source,
// unrecognized URI).
func liveSignature(uri string) (sig string, ok bool) {
	switch {
	case strings.HasPrefix(uri, healthScheme):
		src, staleAfter := splitStaleAfter(strings.TrimPrefix(uri, healthScheme))
		if src == "" || isRemote(src) {
			return "", false
		}
		r := health.Inspect(src, false)
		r.CheckStaleness(staleAfter)
		return fmt.Sprintf("%s|%d", r.SeverityLabel, len(r.Issues)), true
	case strings.HasPrefix(uri, locksScheme):
		src := strings.TrimPrefix(uri, locksScheme)
		if src == "" || isRemote(src) {
			return "", false
		}
		rep, err := locks.Diagnose(src)
		if err != nil {
			return "error", true
		}
		return fmt.Sprintf("%s|%d", rep.Verdict, len(rep.Findings)), true
	default:
		return "", false
	}
}

func loadSchema(src string) (*schema.Schema, error) {
	if src == "" {
		return nil, fmt.Errorf("resource URI is missing a source")
	}
	c, err := connector.Open(src)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Schema()
}

func renderSchema(src string, s *schema.Schema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Schema — %s\n\n%d table(s).\n\n", src, len(s.Tables))
	tables := sortedTables(s)
	for _, t := range tables {
		fmt.Fprintf(&b, "## %s\n\n", t.Name)
		for _, c := range t.Columns {
			line := "- `" + c.Name + "` " + c.Type
			if c.PK > 0 {
				line += " PRIMARY KEY"
			}
			if c.NotNull {
				line += " NOT NULL"
			}
			if c.Default != "" {
				line += " DEFAULT " + c.Default
			}
			b.WriteString(line + "\n")
		}
		for _, fk := range t.ForeignKeys {
			fmt.Fprintf(&b, "- FK: `%s` → `%s(%s)`\n", fk.From, fk.Table, fk.To)
		}
		for _, idx := range t.Indexes {
			kind := "index"
			if idx.Unique {
				kind = "unique index"
			}
			fmt.Fprintf(&b, "- %s: `%s`\n", kind, idx.Name)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderDictionary(src string, s *schema.Schema) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Data dictionary — %s\n\n", src)
	for _, t := range sortedTables(s) {
		pk := primaryKey(t)
		fmt.Fprintf(&b, "## %s\n\n", t.Name)
		if pk != "" {
			fmt.Fprintf(&b, "Primary key: %s\n\n", pk)
		}
		b.WriteString("| Column | Type | Nullable | Key |\n|---|---|---|---|\n")
		fkByCol := map[string]string{}
		for _, fk := range t.ForeignKeys {
			fkByCol[fk.From] = fk.Table + "(" + fk.To + ")"
		}
		for _, c := range t.Columns {
			null := "yes"
			if c.NotNull || c.PK > 0 {
				null = "no"
			}
			key := ""
			if c.PK > 0 {
				key = "PK"
			}
			if ref, ok := fkByCol[c.Name]; ok {
				if key != "" {
					key += ", "
				}
				key += "FK→" + ref
			}
			typ := c.Type
			if typ == "" {
				typ = "(untyped)"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.Name, typ, null, key)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func sortedTables(s *schema.Schema) []schema.Table {
	out := make([]schema.Table, len(s.Tables))
	copy(out, s.Tables)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func primaryKey(t schema.Table) string {
	var cols []string
	for _, c := range t.Columns {
		if c.PK > 0 {
			cols = append(cols, c.Name)
		}
	}
	return strings.Join(cols, ", ")
}
