// Package team adds person-scoped, local-first access control on top of the
// audit operator. A committed team file lists members and their roles; viewers
// are blocked from operations that change a database, while admins/editors may
// proceed. There is no server — the team file lives in the project (or
// ~/.litescope) and is enforced by the same write entry points as the policy.
package team

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/croc100/litescope/internal/audit"
	"gopkg.in/yaml.v3"
)

// Member is one person on the team.
type Member struct {
	Name string `yaml:"name"`
	Role string `yaml:"role"` // admin | editor | viewer
}

// Config is a parsed team file.
type Config struct {
	Team    string   `yaml:"team"`
	Members []Member `yaml:"members"`
	// Strict blocks any operator who is not listed as a member. When false
	// (default), unknown operators are allowed — the team file only constrains
	// the people it names.
	Strict bool `yaml:"strict"`

	source string
}

// Source returns the file the config was loaded from ("" when none).
func (c *Config) Source() string { return c.source }

// Empty reports whether the config constrains nobody.
func (c *Config) Empty() bool { return c == nil || (len(c.Members) == 0 && !c.Strict) }

// Role returns the role of operator, or "" if not listed. Matching is
// case-insensitive on the name.
func (c *Config) Role(operator string) string {
	if c == nil {
		return ""
	}
	for _, m := range c.Members {
		if strings.EqualFold(m.Name, operator) {
			return strings.ToLower(m.Role)
		}
	}
	return ""
}

// CanWrite reports whether operator may perform a mutating operation, with a
// human-readable reason when they may not. A nil/empty config allows everyone.
func (c *Config) CanWrite(operator string) (bool, string) {
	if c.Empty() {
		return true, ""
	}
	switch c.Role(operator) {
	case "admin", "editor":
		return true, ""
	case "viewer":
		return false, fmt.Sprintf("operator %q has role viewer (read-only) in team %q", operator, c.Team)
	default: // unknown operator
		if c.Strict {
			return false, fmt.Sprintf("operator %q is not a member of team %q (strict mode)", operator, c.Team)
		}
		return true, ""
	}
}

// DefaultFile is the conventional per-project team filename.
const DefaultFile = "litescope.team.yaml"

// Load resolves a team config from, in order: $LITESCOPE_TEAM,
// ./litescope.team.yaml, ~/.litescope/team.yaml. A missing file is not an
// error — it returns an empty (allow-all) config.
func Load() (*Config, error) {
	for _, path := range candidatePaths() {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var c Config
		if err := yaml.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("parsing team file %s: %w", path, err)
		}
		c.source = path
		return &c, nil
	}
	return &Config{}, nil
}

// Allow is a convenience that loads the team config and checks the current
// audit operator, returning an error when the operator may not write.
func Allow() error {
	c, err := Load()
	if err != nil {
		return err
	}
	if ok, reason := c.CanWrite(audit.Operator()); !ok {
		return fmt.Errorf("blocked by team policy: %s", reason)
	}
	return nil
}

func candidatePaths() []string {
	paths := []string{os.Getenv("LITESCOPE_TEAM"), DefaultFile}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".litescope", "team.yaml"))
	}
	return paths
}
