package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// Project is a named grouping of sessions. It may also name the repo its work
// happens in: selecting a project then aims new sessions at that repo, so
// switching context is one choice rather than two remembered separately.
//
// The binding is a default, not a rule. Sessions already filed under a project
// keep whatever repo they were started in, and the repo chip still overrides it
// for the next session.
type Project struct {
	Name   string `json:"name"`
	RepoID string `json:"repo,omitempty"`
}

// UnmarshalJSON also accepts the bare label a config written before projects
// had repos would hold, so an existing groups list keeps working and gains an
// empty binding rather than failing to parse.
func (p *Project) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		*p = Project{Name: name}
		return nil
	}
	type plain Project
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = Project(v)
	return nil
}

type AgentProfile struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	// ImageArgument is repeated once per initial image. {path} is replaced by
	// the shell-quoted staged image path. Profiles without one receive image
	// paths in the opening prompt instead.
	ImageArgument string `json:"image_argument,omitempty"`
	// Hooks is true when the agent reports its own state through dma's hook
	// listener, as Claude Code does. Agents without that channel fall back to
	// liveness plus a pane-change heuristic, which is coarser but never blocks
	// the feature on universal coverage.
	Hooks bool `json:"hooks"`
	// OnPROpen is a line typed into the agent the first time a pull request
	// appears for its session -- a slash command, or plain instructions. Empty,
	// the default, sends nothing.
	//
	// It lives on the profile rather than on the repo because the line is
	// written in one agent's vocabulary: "/pr-shepherd 412" means something to
	// Claude Code and nothing to anything else. Setting it once therefore covers
	// every repo that agent works in, which is what "always" has to mean.
	OnPROpen string `json:"on_pr_open,omitempty"`
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
// An agent needing the prompt or image arguments somewhere other than the end
// can put {prompt} or {images} in its command and have them substituted in
// place.
func (p AgentProfile) LaunchCommand(prompt string, images ...string) string {
	prompt = strings.TrimSpace(prompt)
	imageArgs := p.imageArguments(images)
	command := strings.ReplaceAll(p.Command, imagePlaceholder, imageArgs)
	if imageArgs != "" && !strings.Contains(p.Command, imagePlaceholder) {
		command += " " + imageArgs
	}
	if len(images) > 0 && strings.TrimSpace(p.ImageArgument) == "" {
		prompt = promptWithImagePaths(prompt, images)
	}
	if prompt == "" {
		return strings.ReplaceAll(command, promptPlaceholder, "")
	}
	quoted := shellQuote(prompt)
	if strings.Contains(command, promptPlaceholder) {
		return strings.ReplaceAll(command, promptPlaceholder, quoted)
	}
	return command + " " + quoted
}

// PROpenCommand is the line to type when a pull request appears, with {pr} and
// {url} substituted.
//
// Nothing is shell-quoted here, unlike LaunchCommand: that string is a command
// line handed to a shell, this one is typed into an agent that is already
// running. Quoting it would put literal quotes in the agent's composer.
func (p AgentProfile) PROpenCommand(number int, url string) string {
	line := strings.TrimSpace(p.OnPROpen)
	if line == "" {
		return ""
	}
	line = strings.ReplaceAll(line, prPlaceholder, strconv.Itoa(number))
	return strings.ReplaceAll(line, urlPlaceholder, url)
}

const (
	promptPlaceholder    = "{prompt}"
	imagePlaceholder     = "{images}"
	imagePathPlaceholder = "{path}"
	prPlaceholder        = "{pr}"
	urlPlaceholder       = "{url}"
)

func (p AgentProfile) imageArguments(images []string) string {
	template := strings.TrimSpace(p.ImageArgument)
	if template == "" || len(images) == 0 {
		return ""
	}
	args := make([]string, 0, len(images))
	for _, path := range images {
		quoted := shellQuote(path)
		arg := template
		if strings.Contains(arg, imagePathPlaceholder) {
			arg = strings.ReplaceAll(arg, imagePathPlaceholder, quoted)
		} else {
			arg += " " + quoted
		}
		args = append(args, arg)
	}
	return strings.Join(args, " ")
}

func promptWithImagePaths(prompt string, images []string) string {
	var b strings.Builder
	if prompt != "" {
		b.WriteString(prompt)
		b.WriteString("\n\n")
	}
	b.WriteString("Images for this task:")
	for _, path := range images {
		b.WriteString("\n- ")
		b.WriteString(path)
	}
	return b.String()
}

// shellQuote wraps s so a shell hands it to the agent as one argument, whatever
// it contains. Single quotes protect everything except a single quote itself,
// which has to leave and re-enter the quoted run.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type Config struct {
	Repos          []Repo         `json:"repos"`
	DefaultRepo    string         `json:"default_repo"`
	AgentProfiles  []AgentProfile `json:"agent_profiles"`
	DefaultProfile string         `json:"default_profile"`
	// Groups is the project list. The key keeps its old name so a config written
	// before projects were records still loads.
	Groups           []Project `json:"groups"`
	PollIntervalSecs int       `json:"poll_interval_secs"`
	HookPort         int       `json:"hook_port"`
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
		{Name: "codex", Command: "codex", ImageArgument: "--image {path}", Hooks: false},
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
		Groups:           []Project{},
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
	c.adoptDefaultImageArguments()
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

// adoptDefaultImageArguments upgrades an untouched Codex profile written
// before image launch support existed. Custom commands are left alone and use
// the documented path-in-prompt fallback.
func (c *Config) adoptDefaultImageArguments() {
	for i := range c.AgentProfiles {
		p := &c.AgentProfiles[i]
		if p.Name == "codex" && p.Command == "codex" && p.ImageArgument == "" {
			p.ImageArgument = "--image {path}"
		}
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

// Project looks up a registered project by name.
func (c *Config) Project(name string) (Project, bool) {
	for _, p := range c.Groups {
		if p.Name == name {
			return p, true
		}
	}
	return Project{}, false
}

// ProjectRepo is the repo a project aims new work at, empty when it is not
// bound to one or is bound to a repo no longer registered.
func (c *Config) ProjectRepo(name string) string {
	p, ok := c.Project(name)
	if !ok || p.RepoID == "" {
		return ""
	}
	if _, ok := c.Repo(p.RepoID); !ok {
		return ""
	}
	return p.RepoID
}

// AddProject registers a project if it is new, preserving display order. An
// existing project keeps the repo it already has: registration is not the place
// a binding changes, BindProject is.
func (c *Config) AddProject(name, repoID string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if _, ok := c.Project(name); ok {
		return false
	}
	c.Groups = append(c.Groups, Project{Name: name, RepoID: repoID})
	return true
}

// BindProject points a project at a repo, registering the project if it is new.
// It reports whether anything changed, so callers can skip a config write.
func (c *Config) BindProject(name, repoID string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for i := range c.Groups {
		if c.Groups[i].Name != name {
			continue
		}
		if c.Groups[i].RepoID == repoID {
			return false
		}
		c.Groups[i].RepoID = repoID
		return true
	}
	c.Groups = append(c.Groups, Project{Name: name, RepoID: repoID})
	return true
}

// RemoveProject forgets a project.
//
// Sessions are deliberately left alone: the caller refuses to remove a project
// anything is still filed under, so a project cannot take its sessions'
// grouping down with it.
func (c *Config) RemoveProject(name string) bool {
	for i, p := range c.Groups {
		if p.Name == name {
			c.Groups = append(c.Groups[:i], c.Groups[i+1:]...)
			return true
		}
	}
	return false
}

// UnbindRepo drops a repo from every project that named it, so unregistering a
// repo cannot leave projects pointing at something that is gone.
func (c *Config) UnbindRepo(repoID string) {
	for i := range c.Groups {
		if c.Groups[i].RepoID == repoID {
			c.Groups[i].RepoID = ""
		}
	}
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
