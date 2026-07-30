package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Bootstrap lists the paths a fresh worktree needs in order to be immediately
// usable. It lives on Repo, not on Config: a Node repo and a Python repo need
// different symlink and copy lists, which is the reason a repo has to be a
// first-class record rather than a bare path on the session.
type Bootstrap struct {
	// Symlink paths are shared across worktrees -- dependency trees and caches
	// such as node_modules, .venv, .gradle.
	Symlink []string `json:"symlink"`
	// Copy paths are duplicated per worktree -- per-worktree config such as .env.
	Copy []string `json:"copy"`
}

type Repo struct {
	ID           string    `json:"id"`
	Path         string    `json:"path"`
	Remote       string    `json:"remote"`
	BaseBranch   string    `json:"base_branch"`
	WorktreeRoot string    `json:"worktree_root"`
	BranchPrefix string    `json:"branch_prefix"`
	Bootstrap    Bootstrap `json:"bootstrap"`
}

type AgentProfile struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

type Config struct {
	Repos            []Repo         `json:"repos"`
	DefaultRepo      string         `json:"default_repo"`
	AgentProfiles    []AgentProfile `json:"agent_profiles"`
	DefaultProfile   string         `json:"default_profile"`
	Groups           []string       `json:"groups"`
	PollIntervalSecs int            `json:"poll_interval_secs"`
	HookPort         int            `json:"hook_port"`

	// AutoAdvanceOnStop controls whether the agent's Stop hook moves a session
	// from active to review. Off by default: an agent that resumes work would
	// otherwise flap the card between columns.
	AutoAdvanceOnStop bool `json:"auto_advance_on_stop"`
}

const (
	DefaultPollInterval = 45
	DefaultHookPort     = 8787
)

// Dir is the application's state directory.
func Dir() string {
	if d := os.Getenv("DMA_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".dma"
	}
	return filepath.Join(home, ".dma")
}

func ConfigPath() string { return filepath.Join(Dir(), "config.json") }
func StatePath() string  { return filepath.Join(Dir(), "state.json") }

func DefaultConfig() *Config {
	return &Config{
		Repos:            []Repo{},
		AgentProfiles:    []AgentProfile{{Name: "claude", Command: "claude"}},
		DefaultProfile:   "claude",
		Groups:           []string{},
		PollIntervalSecs: DefaultPollInterval,
		HookPort:         DefaultHookPort,
	}
}

// LoadConfig reads the config file, creating a default one if absent.
func LoadConfig() (*Config, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		if err := SaveConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.normalize()
	return cfg, nil
}

func (c *Config) normalize() {
	if c.PollIntervalSecs <= 0 {
		c.PollIntervalSecs = DefaultPollInterval
	}
	if c.HookPort <= 0 {
		c.HookPort = DefaultHookPort
	}
	if len(c.AgentProfiles) == 0 {
		c.AgentProfiles = []AgentProfile{{Name: "claude", Command: "claude"}}
	}
	if c.DefaultProfile == "" {
		c.DefaultProfile = c.AgentProfiles[0].Name
	}
	for i := range c.Repos {
		r := &c.Repos[i]
		if r.BaseBranch == "" {
			r.BaseBranch = "main"
		}
		r.Path = expandHome(r.Path)
		r.WorktreeRoot = expandHome(r.WorktreeRoot)
		if r.WorktreeRoot == "" {
			r.WorktreeRoot = filepath.Join(Dir(), "worktrees", r.ID)
		}
	}
	if c.DefaultRepo == "" && len(c.Repos) > 0 {
		c.DefaultRepo = c.Repos[0].ID
	}
}

func SaveConfig(c *Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(ConfigPath(), append(data, '\n'))
}

// Repo looks up a registered repo by id.
func (c *Config) Repo(id string) (Repo, bool) {
	for _, r := range c.Repos {
		if r.ID == id {
			return r, true
		}
	}
	return Repo{}, false
}

// ResolveRepo returns the named repo, falling back to default_repo and then to
// the sole registered repo.
func (c *Config) ResolveRepo(id string) (Repo, error) {
	if id != "" {
		if r, ok := c.Repo(id); ok {
			return r, nil
		}
		return Repo{}, fmt.Errorf("no repo registered with id %q", id)
	}
	if c.DefaultRepo != "" {
		if r, ok := c.Repo(c.DefaultRepo); ok {
			return r, nil
		}
	}
	if len(c.Repos) == 1 {
		return c.Repos[0], nil
	}
	return Repo{}, fmt.Errorf("no repo selected and no default_repo set")
}

// MultiRepo reports whether repo handles should be rendered on cards. The
// single-repo case is the common one and stays uncluttered.
func (c *Config) MultiRepo() bool { return len(c.Repos) > 1 }

func (c *Config) Profile(name string) (AgentProfile, bool) {
	for _, p := range c.AgentProfiles {
		if p.Name == name {
			return p, true
		}
	}
	return AgentProfile{}, false
}

// ProfileNames lists configured agent profile names in order.
func (c *Config) ProfileNames() []string {
	out := make([]string, 0, len(c.AgentProfiles))
	for _, p := range c.AgentProfiles {
		out = append(out, p.Name)
	}
	return out
}

// AddGroup registers a group label if it is new, preserving display order.
func (c *Config) AddGroup(g string) bool {
	g = strings.TrimSpace(g)
	if g == "" {
		return false
	}
	for _, e := range c.Groups {
		if e == g {
			return false
		}
	}
	c.Groups = append(c.Groups, g)
	return true
}

func expandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
