package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Bootstrap lists the paths a fresh worktree needs in order to be immediately
// usable. It lives on Repo, not on Config: a Node repo and a Python repo need
// different symlink and copy lists, which is the reason a repo has to be a
// first-class record rather than a bare path on the session.
type Bootstrap struct {
	// Symlink paths are shared across worktrees -- content-addressed caches and
	// vendored source, where one copy on disk serves every checkout that points
	// at it.
	Symlink []string `json:"symlink"`
	// Clone paths get a private copy per worktree, made with a filesystem clone
	// where the platform offers one. They are dependency trees that record the
	// absolute path they were built for, so pointing two worktrees at one copy
	// makes them fight over it. See IsPathKeyed.
	Clone []string `json:"clone,omitempty"`
	// Copy paths are duplicated per worktree -- per-worktree config such as .env.
	Copy []string `json:"copy"`
}

// pathKeyedNames are directory names whose contents only make sense for the one
// checkout that built them.
//
// A Python venv records its own location in pyvenv.cfg and records the source
// directory of every editable install in a .pth file, so a venv shared between
// worktrees is rewritten to point at whichever one activated last -- taking the
// main checkout's imports with it. pnpm resolves its root node_modules through
// symlinks, finds the virtual store where some other checkout left it, and
// offers to delete and reinstall the tree from scratch, which would be the copy
// every other worktree is using.
//
// Both fail the same way: the tree is not content, it is content plus the path
// it was materialized at.
var pathKeyedNames = map[string]bool{
	"node_modules": true,
	".venv":        true,
	"venv":         true,
	".tox":         true,
}

// IsPathKeyed reports whether rel names a dependency tree that has to belong to
// exactly one checkout, and so must be cloned per worktree rather than shared.
//
// Note what is deliberately absent: a repo's own toolchain cache, such as flox's
// $FLOX_ENV_CACHE. Those caches hold the hash files an activation hook checks to
// decide whether to install anything, so handing a worktree a populated one
// tells the hook its work is already done -- and the step it then skips is the
// one that would have pointed the cloned venv at this worktree's sources instead
// of the checkout it was cloned from. A slow first activation is the cheaper
// mistake.
func IsPathKeyed(rel string) bool {
	return pathKeyedNames[path.Base(filepath.ToSlash(filepath.Clean(rel)))]
}

// reclassifyPathKeyed moves path-keyed trees off the symlink list, where every
// config written before cloning existed still holds them.
//
// Without this the fix would reach new registrations and nobody else: a repo
// registered months ago keeps sharing one node_modules between every worktree,
// which is the failure the clone list exists to end. The move is safe to repeat
// and loses nothing -- both lists name the same paths to the same bootstrap, and
// entries this build does not recognize are left where the user put them.
func (b *Bootstrap) reclassifyPathKeyed() {
	kept := b.Symlink[:0]
	for _, p := range b.Symlink {
		if IsPathKeyed(p) && !containsPath(b.Clone, p) {
			b.Clone = append(b.Clone, p)
			continue
		}
		kept = append(kept, p)
	}
	b.Symlink = kept
	if len(b.Symlink) == 0 {
		b.Symlink = nil
	}
	sort.Strings(b.Clone)
}

func containsPath(list []string, want string) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
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
	// ResumeCommand is the shell line that starts this agent back on the
	// conversation it already had in a worktree. It is what a restart runs; see
	// RestartCommand. Empty means this agent has no resume form dma knows, and a
	// restart falls back to Command -- a working agent with no memory of the task.
	//
	// It must identify the conversation from the working directory alone, because
	// that is the only thing distinguishing one session from another: dma gives
	// each its own worktree and starts the agent in it, so "the most recent
	// conversation in this directory" names exactly one session's history and
	// cannot reach across to another card's. A resume form that took the most
	// recent conversation anywhere -- which is what both built-in agents do if you
	// let them look past the working directory -- would hand every session on a
	// restarted board the same one.
	ResumeCommand string `json:"resume_command,omitempty"`
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

// RestartCommand is the shell line that brings this agent back up in a worktree
// it has already worked in, which is what a session whose terminal died needs.
//
// It is the resume line with the plain launch line behind it, joined by "||" so
// the shell runs the second only if the first fails. That fallback is for the
// worktree with nothing to resume: a session whose agent never reached a first
// turn -- started and killed, or interrupted while its dependencies were still
// arriving -- has no conversation on disk, and both built-in agents treat being
// asked for one as an error. Left there, the restart would leave a live tmux
// session sitting at a shell prompt, which the board reads as running while no
// agent is in it. That is worse than the plain relaunch the fallback gets.
//
// A profile with no resume line restarts as a plain launch, and callers tell the
// user which of the two they got: an agent that came back without its history
// looks identical on the board and is not the same thing at all.
func (p AgentProfile) RestartCommand() string {
	launch := p.LaunchCommand("")
	resume := strings.TrimSpace(p.ResumeCommand)
	if resume == "" {
		return launch
	}
	return resume + " || " + launch
}

const (
	promptPlaceholder    = "{prompt}"
	imagePlaceholder     = "{images}"
	imagePathPlaceholder = "{path}"
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
	// NotifierHintShown records that the board has already said the platform's
	// notifier is missing. The board works without it, so the hint is said once
	// and then lives permanently in dma doctor -- repeating it every launch
	// would be nagging about something the user may have decided to accept.
	NotifierHintShown bool `json:"notifier_hint_shown,omitempty"`
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
//
// The resume lines are both scoped to the working directory, which is what makes
// restarting a board of sessions at once correct rather than a shuffle -- see
// ResumeCommand. "claude --continue" says so in as many words: it continues the
// most recent conversation in the current directory. Codex filters its session
// list by the directory it is run in unless asked for --all, so "resume --last"
// is the most recent session in this worktree and not the most recent one on the
// machine.
func DefaultProfiles() []AgentProfile {
	return []AgentProfile{
		{
			Name:          "claude",
			Command:       "claude --permission-mode auto",
			ResumeCommand: "claude --permission-mode auto --continue",
			Hooks:         true,
		},
		{
			Name:          "codex",
			Command:       "codex",
			ImageArgument: "--image {path}",
			ResumeCommand: "codex resume --last",
			Hooks:         false,
		},
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
	c.adoptDefaultResumeCommands()
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
		r.Bootstrap.reclassifyPathKeyed()
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

// adoptDefaultResumeCommands gives built-in profiles the resume line they were
// written without, so a config that predates restarting does not restart its
// agents with no memory of what they were doing.
//
// It fires only for a profile still on the current default command, the rule
// adoptDefaultFlags follows and for the same reason: a resume line is that
// command plus a flag, so pairing a generated one with a command the user
// replaced -- a wrapper script, a different binary, an agent that only shares the
// name -- would restart something other than what starts a session. Those
// profiles restart as a plain launch until their owner writes a resume_command,
// which the board says out loud each time.
func (c *Config) adoptDefaultResumeCommands() {
	for _, p := range DefaultProfiles() {
		if p.ResumeCommand == "" {
			continue
		}
		for i := range c.AgentProfiles {
			mine := &c.AgentProfiles[i]
			if mine.Name == p.Name && mine.ResumeCommand == "" && mine.Command == p.Command {
				mine.ResumeCommand = p.ResumeCommand
			}
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
