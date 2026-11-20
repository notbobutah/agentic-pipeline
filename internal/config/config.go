// Package config loads the demo deployment configuration (demo/config.yaml).
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Component struct {
	Repo      string   `yaml:"repo"`      // owner/name
	Root      string   `yaml:"root"`      // project directory inside the repo
	Paths     []string `yaml:"paths"`     // scope-envelope ceiling (globs)
	Forbidden []string `yaml:"forbidden"` // never writable by agents
}

type Role struct {
	MaxTurns     int     `yaml:"maxTurns"`
	MaxBudgetUSD float64 `yaml:"maxBudgetUsd"`
}

type Agents struct {
	Image string          `yaml:"image"`
	Model string          `yaml:"model"`
	Auth  string          `yaml:"auth"`
	Roles map[string]Role `yaml:"roles"`
}

type Identity struct {
	GitHub string `yaml:"github"`
}

type Feed struct {
	Repo   string `yaml:"repo"`   // owner/name hosting the feed branch
	Branch string `yaml:"branch"` // orphan branch with the latest snapshot
}

// URL is where the public page reads the feed.
func (f Feed) URL() string {
	return "https://raw.githubusercontent.com/" + f.Repo + "/" + f.Branch + "/index.json"
}

type Config struct {
	Tenant             string               `yaml:"tenant"`
	Team               string               `yaml:"team"`
	StateDir           string               `yaml:"stateDir"`
	AgentUser          string               `yaml:"agentUser"`
	EligibleIssueTypes []string             `yaml:"eligibleIssueTypes"`
	Identities         map[string]Identity  `yaml:"identities"`
	Components         map[string]Component `yaml:"components"`
	DependencyOrder    []string             `yaml:"dependencyOrder"`
	Agents             Agents               `yaml:"agents"`
	Feed               Feed                 `yaml:"feed"`

	// BaseDir is the directory relative paths resolve against (the repo root).
	BaseDir string `yaml:"-"`
}

// Load reads the config file; relative paths resolve against baseDir.
func Load(path, baseDir string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.BaseDir = baseDir
	if c.StateDir == "" {
		c.StateDir = ".ap"
	}
	if len(c.Components) == 0 {
		return nil, fmt.Errorf("%s: no components", path)
	}
	for name, comp := range c.Components {
		if comp.Repo == "" || len(comp.Paths) == 0 {
			return nil, fmt.Errorf("%s: component %q needs repo and paths", path, name)
		}
	}
	return &c, nil
}

// StatePath resolves a path inside the state directory.
func (c *Config) StatePath(parts ...string) string {
	dir := c.StateDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.BaseDir, dir)
	}
	return filepath.Join(append([]string{dir}, parts...)...)
}

// Path resolves a path relative to the base directory.
func (c *Config) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.BaseDir, p)
}

// IsIdentity reports whether a tracker user is mapped (may guide or approve).
func (c *Config) IsIdentity(user string) bool {
	_, ok := c.Identities[user]
	return ok
}

// Eligible reports whether an issue type may be worked by agents.
func (c *Config) Eligible(issueType string) bool {
	for _, t := range c.EligibleIssueTypes {
		if t == issueType {
			return true
		}
	}
	return false
}
