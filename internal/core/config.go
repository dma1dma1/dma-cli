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
	Bootstrap    Bootstrap `json:"bootstrap"`
}

type AgentProfile struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	// Hooks is true when the agent reports its own state through dma's hook
	// listener, as Claude Code does. Agents without that channel fall back to
	// liveness plus a pane-change heuristic, which is coarser but never blocks
	// the feature on universal coverage.
	Hooks bool `json:"hooks"`
}

// LaunchCommand is the shell line that starts this agent on a prompt.
//
// The prompt rides on argv rather than being typed into the agent's TUI, because
// typing is not reliable at all. Codex reads its composer through a vim-style
// keymap when the user has one configured, so "how are you" arrives as the
// commands h, o, w... -- the leading characters move the cursor and open a line
// instead of being inserted. Waiting longer before typing does not help: the
// mangling is the keymap, not a startup race. Both built-in agents take an
// opening prompt as a positional argument, which the agent parses itself and so
// cannot misread.
//
// An agent needing the prompt somewhere other than the end -- behind a flag, say
// -- can put {prompt} in its command and have it substituted in place.
func (p AgentProfile) LaunchCommand(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return strings.ReplaceAll(p.Command, promptPlaceholder, "")
	}
	quoted := shellQuote(prompt)
	if strings.Contains(p.Command, promptPlaceholder) {
		return strings.ReplaceAll(p.Command, promptPlaceholder, quoted)
	}
	return p.Command + " " + quoted
}

const promptPlaceholder = "{prompt}"

// shellQuote wraps s so a shell hands it to the agent as one argument, whatever
// it contains. Single quotes protect everything except a single quote itself,
// which has to leave and re-enter the quoted run.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type Config struct {
	Repos            []Repo         `json:"repos"`
	DefaultRepo      string         `json:"default_repo"`
	AgentProfiles    []AgentProfile `json:"agent_profiles"`
	DefaultProfile   string         `json:"default_profile"`
	Groups           []string       `json:"groups"`
	PollIntervalSecs int            `json:"poll_interval_secs"`
	HookPort         int            `json:"hook_port"`
}

const (
	DefaultPollInterval = 45
	DefaultHookPort     = 8787
)

// DefaultProfiles are the agents dma knows how to launch out of the box.
//
// Claude Code starts in auto mode. A board of parallel agents is worth having
// only if they make progress unattended, and the default permission mode stops
// at the first command needing approval -- so every session would sit in
// needs_you until someone opened it, which is the opposite of what the board is
// for. The mode is a starting point, not a lock: shift+tab still cycles it from
// the panel or an attached terminal.
//
// The flag rides in Command because that string is typed into the pane as a
// shell line, so profiles carry their own arguments and dma needs no schema for
// per-agent flags.
func DefaultProfiles() []AgentProfile {
	return []AgentProfile{
		{Name: "claude", Command: "claude --permission-mode auto", Hooks: true},
		{Name: "codex", Command: "codex", Hooks: false},
	}
}

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
		AgentProfiles:    DefaultProfiles(),
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
	// Built-in profiles are backfilled rather than merely defaulted: a config
	// written before dma learned about an agent would otherwise never offer it,
	// and the picker reads straight off this list. Existing entries win, so a
	// user's edited command survives.
	for _, p := range DefaultProfiles() {
		if _, ok := c.Profile(p.Name); !ok {
			c.AgentProfiles = append(c.AgentProfiles, p)
		}
	}
	c.adoptDefaultFlags()
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

// bareCommands are the commands built-in profiles used to ship with, before the
// defaults grew flags. A profile still holding one of these has never been
// edited, so it is safe to move it onto the current default.
var bareCommands = map[string]string{"claude": "claude"}

// adoptDefaultFlags brings profiles written by an older dma onto the current
// default command.
//
// Backfilling only covers agents a config has never heard of, so without this a
// config created before the flag existed would keep launching the bare command
// forever -- the change would apply to new installs and to nobody else.
//
// The upgrade is deliberately narrow: it fires only when the stored command is
// byte-for-byte the old default, which means the user never touched it. Anything
// else -- an added flag, a wrapper script, a different binary -- is a deliberate
// choice and is left alone, the same rule the backfill above follows.
func (c *Config) adoptDefaultFlags() {
	for _, p := range DefaultProfiles() {
		bare, ok := bareCommands[p.Name]
		if !ok || bare == p.Command {
			continue
		}
		for i := range c.AgentProfiles {
			if c.AgentProfiles[i].Name == p.Name && c.AgentProfiles[i].Command == bare {
				c.AgentProfiles[i].Command = p.Command
			}
		}
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

// RemoveGroup forgets a project label.
//
// Sessions are deliberately left alone: the caller refuses to remove a label
// anything is still filed under, so a project cannot take its sessions'
// grouping down with it.
func (c *Config) RemoveGroup(g string) bool {
	for i, e := range c.Groups {
		if e == g {
			c.Groups = append(c.Groups[:i], c.Groups[i+1:]...)
			return true
		}
	}
	return false
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
