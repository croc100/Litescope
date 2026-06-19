// Package policy enforces local-first guardrails on operations that change a
// database. An optional policy file lets an operator (or an org that ships one
// alongside a project) make targets read-only — a global kill switch, or
// per-target protection — without any server or account system. Every mutating
// entry point asks Allow() before proceeding; a blocked attempt is an error.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Policy is the parsed guardrail configuration.
type Policy struct {
	// ReadOnly blocks every mutating operation, everywhere.
	ReadOnly bool `yaml:"read_only"`
	// Protected lists substrings of a target (database path or fleet name);
	// writes to a target containing any of them are blocked.
	Protected []string `yaml:"protected"`

	source string // where it was loaded from, for display
}

// Source returns the file the policy was loaded from ("" when none).
func (p *Policy) Source() string { return p.source }

// Empty reports whether the policy imposes no restrictions.
func (p *Policy) Empty() bool { return !p.ReadOnly && len(p.Protected) == 0 }

// Allow returns nil if a write to target is permitted, or an error describing
// why it is blocked. A nil policy allows everything.
func (p *Policy) Allow(target string) error {
	if p == nil {
		return nil
	}
	if p.ReadOnly {
		return fmt.Errorf("blocked by policy: read-only mode is enabled (%s)", p.sourceLabel())
	}
	for _, pat := range p.Protected {
		if pat != "" && strings.Contains(target, pat) {
			return fmt.Errorf("blocked by policy: %q is protected (matches %q in %s)", target, pat, p.sourceLabel())
		}
	}
	return nil
}

func (p *Policy) sourceLabel() string {
	if p.source != "" {
		return p.source
	}
	return "policy"
}

// DefaultFile is the conventional per-project policy filename.
const DefaultFile = "litescope.policy.yaml"

// Load resolves a policy from, in order: $LITESCOPE_POLICY, ./litescope.policy.yaml,
// ~/.litescope/policy.yaml. It returns an empty (allow-all) policy when none
// exists — a missing file is not an error.
func Load() (*Policy, error) {
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
		var p Policy
		if err := yaml.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("parsing policy %s: %w", path, err)
		}
		p.source = path
		return &p, nil
	}
	return &Policy{}, nil
}

func candidatePaths() []string {
	paths := []string{os.Getenv("LITESCOPE_POLICY"), DefaultFile}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".litescope", "policy.yaml"))
	}
	return paths
}
