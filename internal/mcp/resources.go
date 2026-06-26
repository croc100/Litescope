package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/croc100/litescope/internal/connector"
	"github.com/croc100/litescope/internal/schema"
)

// Litescope exposes two read-only resources per database, addressed by URI:
//
//	litescope://schema/<source>      — CREATE-style schema overview
//	litescope://dictionary/<source>  — a human/agent-readable data dictionary
//
// <source> is any DSN litescope understands (a local path, d1://…, turso://…).
// Because the server is not bound to a single database, these are surfaced as
// resource *templates*; a client substitutes the source. When the server is
// started with a default source, the same two resources are also listed
// concretely so simple clients can read them without filling a template.
const (
	schemaScheme = "litescope://schema/"
	dictScheme   = "litescope://dictionary/"
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
	}
}

// concreteResources lists the two resources for a bound default source, or nil.
func concreteResources(defaultSource string) []map[string]interface{} {
	if defaultSource == "" {
		return nil
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
	default:
		return "", "", fmt.Errorf("unknown resource URI: %s", uri)
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
