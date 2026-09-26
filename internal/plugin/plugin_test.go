// Package plugin_test guards the Claude Code plugin shipped in ./plugin.
//
// The plugin's skills tell an agent which litescope tools and commands to run.
// A skill that names something that does not exist is worse than no skill: the
// agent follows it, the call fails, and the user watches the tool contradict
// its own documentation. These tests keep the two in step.
package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/croc100/litescope/internal/cli"
	"github.com/croc100/litescope/internal/mcp"
)

const pluginDir = "../../plugin"

func skillFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(pluginDir, "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read skills dir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "SKILL.md")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("skill %q has no SKILL.md", e.Name())
			continue
		}
		out[e.Name()] = string(b)
	}
	if len(out) == 0 {
		t.Fatal("no skills found")
	}
	return out
}

// Every litescope_* tool a skill tells the agent to call must be registered.
func TestSkills_ReferenceRealTools(t *testing.T) {
	registered := map[string]bool{}
	for _, tool := range mcp.Registry(true) {
		registered[tool.Name] = true
	}
	re := regexp.MustCompile(`litescope_[a-z0-9_]+`)
	for name, body := range skillFiles(t) {
		for _, ref := range re.FindAllString(body, -1) {
			if !registered[ref] {
				t.Errorf("skill %q references unknown MCP tool %q", name, ref)
			}
		}
	}
}

// Every `litescope <command>` a skill tells the user to run must exist.
func TestSkills_ReferenceRealCommands(t *testing.T) {
	root := cli.Root()
	re := regexp.MustCompile("litescope ([a-z][a-z0-9-]*)(?: ([a-z][a-z0-9-]*))?")
	for name, body := range skillFiles(t) {
		// Only code is instruction; prose that happens to contain the word
		// "litescope" is not a command the agent will run.
		for _, m := range re.FindAllStringSubmatch(codeOnly(body), -1) {
			cmd, sub := m[1], m[2]
			target, _, err := root.Find([]string{cmd})
			if err != nil || target == root {
				t.Errorf("skill %q references unknown command `litescope %s`", name, cmd)
				continue
			}
			if sub == "" || !target.HasSubCommands() {
				continue
			}
			// A second word is only a subcommand when the parent has any;
			// otherwise it is the positional database argument.
			found := false
			for _, c := range target.Commands() {
				if c.Name() == sub {
					found = true
					break
				}
			}
			if !found && !looksLikeArgument(sub) {
				t.Errorf("skill %q references unknown subcommand `litescope %s %s`", name, cmd, sub)
			}
		}
	}
}

// codeOnly reduces a skill to its fenced blocks and inline code spans, which
// is where the commands an agent copies actually live.
func codeOnly(body string) string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		parts := strings.Split(line, "`")
		for i := 1; i < len(parts); i += 2 {
			out = append(out, parts[i])
		}
	}
	return strings.Join(out, "\n")
}

// looksLikeArgument reports whether a word after a command is a positional
// argument rather than a subcommand name.
func looksLikeArgument(w string) bool {
	return strings.Contains(w, ".") || w == "app" || w == "db"
}

// Skill frontmatter drives whether Claude ever loads the skill, so a missing
// name or description silently disables it.
func TestSkills_Frontmatter(t *testing.T) {
	for name, body := range skillFiles(t) {
		if !strings.HasPrefix(body, "---\n") {
			t.Errorf("skill %q has no frontmatter", name)
			continue
		}
		end := strings.Index(body[4:], "\n---\n")
		if end < 0 {
			t.Errorf("skill %q has an unterminated frontmatter block", name)
			continue
		}
		fm := body[4 : end+4]
		var gotName, desc string
		for _, line := range strings.Split(fm, "\n") {
			if strings.HasPrefix(line, "name:") {
				gotName = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			}
			if strings.HasPrefix(line, "description:") {
				desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			}
		}
		if gotName != name {
			t.Errorf("skill %q declares name %q — it must match its directory", name, gotName)
		}
		if desc == "" {
			t.Errorf("skill %q has no description, so Claude cannot decide when to load it", name)
		}
		// description and when_to_use share a 1536-character budget.
		if len(fm) > 1536 {
			t.Errorf("skill %q frontmatter is %d chars, over the 1536 budget", name, len(fm))
		}
		if !strings.Contains(strings.ToLower(desc), "use when") && !strings.Contains(strings.ToLower(desc), "use whenever") {
			t.Errorf("skill %q description states no trigger condition", name)
		}
	}
}

// The manifests are what Claude Code parses before anything else runs.
func TestPluginManifests(t *testing.T) {
	var manifest struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin/plugin.json"), &manifest)
	if manifest.Name != "litescope" {
		t.Errorf("plugin name = %q", manifest.Name)
	}
	if manifest.Description == "" {
		t.Errorf("plugin has no description")
	}

	var servers struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	readJSON(t, filepath.Join(pluginDir, ".mcp.json"), &servers)
	srv, ok := servers.MCPServers["litescope"]
	if !ok {
		t.Fatalf("plugin declares no litescope MCP server")
	}
	if srv.Command == "" || len(srv.Args) == 0 {
		t.Errorf("litescope MCP server has no command: %+v", srv)
	}
	// Read-only by default: the plugin must not hand an agent write access to
	// a production database just by being installed.
	for _, a := range srv.Args {
		if a == "--allow-writes" {
			t.Errorf("the bundled MCP server must not enable writes by default")
		}
	}

	var market struct {
		Name    string                `json:"name"`
		Owner   struct{ Name string } `json:"owner"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	readJSON(t, "../../.claude-plugin/marketplace.json", &market)
	if market.Name == "" || market.Owner.Name == "" || len(market.Plugins) == 0 {
		t.Fatalf("marketplace is missing a required field: %+v", market)
	}
	entry := market.Plugins[0]
	if entry.Source != "./plugin" {
		t.Errorf("marketplace entry source = %q, want ./plugin", entry.Source)
	}
	if _, err := os.Stat(filepath.Join("../..", strings.TrimPrefix(entry.Source, "./"))); err != nil {
		t.Errorf("marketplace entry points at a missing directory: %v", err)
	}
}

// The plugin version tracks the release it documents.
func TestPluginVersionMatchesRelease(t *testing.T) {
	var manifest struct {
		Version string `json:"version"`
	}
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin/plugin.json"), &manifest)
	b, err := os.ReadFile("../../server.json")
	if err != nil {
		t.Skip("no server.json to compare against")
	}
	var reg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &reg) != nil || reg.Version == "" {
		t.Skip("server.json has no version")
	}
	if manifest.Version != reg.Version {
		t.Errorf("plugin version %q != released version %q", manifest.Version, reg.Version)
	}
}

func readJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// A skill is only useful if it is discoverable; keep the set explicit so a
// rename or deletion is a deliberate change.
func TestSkills_Inventory(t *testing.T) {
	want := []string{
		"fleet-health-sweep",
		"migration-review",
		"safe-db-writes",
		"sqlite-corruption-recovery",
		"sqlite-lock-doctor",
	}
	var got []string
	for name := range skillFiles(t) {
		got = append(got, name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("skills = %v, want %v", got, want)
	}
}
