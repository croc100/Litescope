// Package fleet manages many SQLite databases as a single unit:
// discovery from Turso orgs and Cloudflare D1 accounts, baseline capture,
// and parallel drift detection across the whole fleet.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// DefaultConfigFile is the conventional fleet config name.
const DefaultConfigFile = "litescope.fleet.yaml"

const configVersion = 1

// Database is one member of a fleet.
type Database struct {
	Name     string `yaml:"name"`
	DSN      string `yaml:"dsn"`
	Baseline string `yaml:"baseline,omitempty"` // path to the baseline snapshot
	// Tags allow grouping (e.g. region, tenant) for filtered operations.
	Tags []string `yaml:"tags,omitempty"`
}

// Config is a fleet definition, normally stored as litescope.fleet.yaml.
type Config struct {
	Version      int        `yaml:"version"`
	Name         string     `yaml:"name,omitempty"`
	BaselinesDir string     `yaml:"baseline_dir,omitempty"` // default dir for captured baselines
	Databases    []Database `yaml:"databases"`

	path string // source path, not serialized
}

// Load reads a fleet config from disk.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading fleet config %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing fleet config: %w", err)
	}
	c.path = path
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the config to the given path (or its source path if empty).
func (c *Config) Save(path string) error {
	if path == "" {
		path = c.path
	}
	if path == "" {
		path = DefaultConfigFile
	}
	if c.Version == 0 {
		c.Version = configVersion
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling fleet config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	c.path = path
	return nil
}

func (c *Config) validate() error {
	if len(c.Databases) == 0 {
		return fmt.Errorf("fleet config has no databases")
	}
	seen := map[string]bool{}
	for i, db := range c.Databases {
		if db.Name == "" {
			return fmt.Errorf("database %d has no name", i)
		}
		if db.DSN == "" {
			return fmt.Errorf("database %q has no dsn", db.Name)
		}
		if seen[db.Name] {
			return fmt.Errorf("duplicate database name %q", db.Name)
		}
		seen[db.Name] = true
	}
	return nil
}

// BaselineDir returns the directory baselines are stored in (default: .litescope/baselines).
func (c *Config) BaselineDir() string {
	if c.BaselinesDir != "" {
		return c.BaselinesDir
	}
	return filepath.Join(".litescope", "baselines")
}

// BaselinePath returns the baseline path for a database, computing a default
// under BaselineDir when the entry doesn't specify one.
func (c *Config) BaselinePath(db Database) string {
	if db.Baseline != "" {
		return db.Baseline
	}
	return filepath.Join(c.BaselineDir(), db.Name+".json")
}

// Filter returns databases matching the given tag, or all when tag is "".
func (c *Config) Filter(tag string) []Database {
	if tag == "" {
		return c.Databases
	}
	var out []Database
	for _, db := range c.Databases {
		for _, t := range db.Tags {
			if t == tag {
				out = append(out, db)
				break
			}
		}
	}
	return out
}

// Merge adds or updates databases by name, preserving existing baselines/tags
// when the incoming entry doesn't set them. Returns counts of added/updated.
func (c *Config) Merge(incoming []Database) (added, updated int) {
	idx := map[string]int{}
	for i, db := range c.Databases {
		idx[db.Name] = i
	}
	for _, in := range incoming {
		if i, ok := idx[in.Name]; ok {
			existing := c.Databases[i]
			existing.DSN = in.DSN
			if len(in.Tags) > 0 {
				existing.Tags = in.Tags
			}
			c.Databases[i] = existing
			updated++
		} else {
			c.Databases = append(c.Databases, in)
			idx[in.Name] = len(c.Databases) - 1
			added++
		}
	}
	sort.Slice(c.Databases, func(i, j int) bool {
		return c.Databases[i].Name < c.Databases[j].Name
	})
	return added, updated
}
