package core

import (
	"os"
	"strings"
	"testing"
)

func TestNormalizeBackfillsProfilesShippedAfterTheConfigWasWritten(t *testing.T) {
	// A config written when claude was the only known agent must still offer
	// every agent the current build knows how to launch.
	c := &Config{AgentProfiles: []AgentProfile{{Name: "claude", Command: "claude", Hooks: true}}}
	c.normalize()

	for _, want := range DefaultProfiles() {
		got, ok := c.Profile(want.Name)
		if !ok {
			t.Fatalf("profile %q missing after normalize, have %v", want.Name, c.ProfileNames())
		}
		if got.Command != want.Command {
			t.Errorf("profile %q command = %q, want %q", want.Name, got.Command, want.Command)
		}
	}
}

// Every config written before cloning existed lists node_modules and .venv as
// shared. Without this migration the fix would reach new registrations only, and
// a repo registered months ago would go on pointing every worktree at one tree.
func TestNormalizeMovesPathKeyedTreesOffTheSharedList(t *testing.T) {
	c := &Config{Repos: []Repo{{
		ID: "mono",
		Bootstrap: Bootstrap{
			Symlink: []string{
				".pnpm-store",
				".venv",
				"node_modules",
				"packages/db/node_modules",
				"vendor",
			},
			Copy: []string{".env"},
		},
	}}}
	c.normalize()

	b := c.Repos[0].Bootstrap
	for _, want := range []string{".venv", "node_modules", "packages/db/node_modules"} {
		if !containsPath(b.Clone, want) {
			t.Errorf("%s was not moved to the clone list: %+v", want, b)
		}
		if containsPath(b.Symlink, want) {
			t.Errorf("%s is still shared between worktrees: %v", want, b.Symlink)
		}
	}
	// Caches that do not name their own location stay shared: duplicating a
	// package store per worktree buys nothing.
	for _, want := range []string{".pnpm-store", "vendor"} {
		if !containsPath(b.Symlink, want) {
			t.Errorf("%s stopped being shared: %+v", want, b)
		}
	}
	if !containsPath(b.Copy, ".env") {
		t.Errorf("copy list was disturbed: %v", b.Copy)
	}
}

// normalize runs on every load, so a config already migrated must come out the
// same rather than growing a duplicate entry each launch.
func TestNormalizeReclassificationIsRepeatable(t *testing.T) {
	c := &Config{Repos: []Repo{{
		ID:        "mono",
		Bootstrap: Bootstrap{Symlink: []string{"node_modules"}},
	}}}
	c.normalize()
	first := c.Repos[0].Bootstrap
	c.normalize()
	second := c.Repos[0].Bootstrap

	if len(second.Clone) != len(first.Clone) || len(second.Clone) != 1 {
		t.Fatalf("clone list = %v after a second normalize, want one entry", second.Clone)
	}
	if len(second.Symlink) != 0 {
		t.Errorf("symlink list = %v, want empty", second.Symlink)
	}
}

// A path the current build does not recognize is the user's own choice, and
// nothing here knows better than they do.
func TestNormalizeLeavesUnrecognizedSharedPathsAlone(t *testing.T) {
	c := &Config{Repos: []Repo{{
		ID:        "custom",
		Bootstrap: Bootstrap{Symlink: []string{"my-big-cache", "tools/prebuilt"}},
	}}}
	c.normalize()

	b := c.Repos[0].Bootstrap
	if len(b.Clone) != 0 {
		t.Errorf("unrecognized paths were reclassified: %v", b.Clone)
	}
	if len(b.Symlink) != 2 {
		t.Errorf("symlink list = %v, want both entries kept", b.Symlink)
	}
}

func TestIsPathKeyed(t *testing.T) {
	keyed := []string{
		"node_modules", ".venv", "venv", ".tox",
		"packages/db/node_modules", "packages/science/.venv",
	}
	for _, p := range keyed {
		if !IsPathKeyed(p) {
			t.Errorf("IsPathKeyed(%q) = false, want true", p)
		}
	}
	shared := []string{
		".pnpm-store", ".yarn/cache", "vendor", ".bundle", ".terraform",
		// A repo's own toolchain cache is not bootstrapped at all, so it must
		// not be claimed here either.
		".flox/cache", "cache",
	}
	for _, p := range shared {
		if IsPathKeyed(p) {
			t.Errorf("IsPathKeyed(%q) = true, want false", p)
		}
	}
}

func TestNormalizeKeepsCustomizedBuiltinProfiles(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "codex", Command: "codex --yolo"}}}
	c.normalize()

	got, _ := c.Profile("codex")
	if got.Command != "codex --yolo" {
		t.Errorf("codex command = %q, want the user's %q", got.Command, "codex --yolo")
	}
	if names := c.ProfileNames(); names[0] != "codex" {
		t.Errorf("existing profiles should keep their order, got %v", names)
	}
	if got.ImageArgument != "" {
		t.Errorf("customized codex image argument = %q, want path-in-prompt fallback", got.ImageArgument)
	}
}

// Claude Code launches in auto mode: a board of parallel agents only pays off if
// they run unattended, and the default permission mode parks every session at the
// first command needing approval.
func TestDefaultProfilesStartClaudeInAutoMode(t *testing.T) {
	p, ok := DefaultConfig().Profile("claude")
	if !ok {
		t.Fatal("no claude profile in the default config")
	}
	if p.Command != "claude --permission-mode auto" {
		t.Errorf("claude command = %q, want it to request auto mode", p.Command)
	}
}

func TestNormalizeUpgradesUntouchedCodexForInitialImages(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "codex", Command: "codex"}}}
	c.normalize()

	got, _ := c.Profile("codex")
	if got.ImageArgument != "--image {path}" {
		t.Errorf("codex image argument = %q, want the current default", got.ImageArgument)
	}
}

// A config written before the flag existed has a claude profile already, so the
// backfill skips it. Without a migration the change would reach new installs and
// nobody else.
func TestNormalizeUpgradesUntouchedClaudeCommand(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "claude", Command: "claude", Hooks: true}}}
	c.normalize()

	got, _ := c.Profile("claude")
	if got.Command != "claude --permission-mode auto" {
		t.Errorf("claude command = %q, want the current default", got.Command)
	}
	if !got.Hooks {
		t.Error("the upgrade dropped the profile's Hooks flag")
	}
}

// The old pi default left a prompt beginning with a Markdown bullet open to
// pi's option parser, where it was rejected instead of starting a session.
func TestNormalizeUpgradesUntouchedPiCommand(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "pi", Command: "pi -a"}}}
	c.normalize()

	got, _ := c.Profile("pi")
	if got.Command != "pi -a --" {
		t.Errorf("pi command = %q, want the prompt protected from option parsing", got.Command)
	}
}

// The migration must never overwrite a command the user chose. Anything other
// than the exact old default is a deliberate edit.
func TestNormalizeLeavesCustomizedClaudeCommandAlone(t *testing.T) {
	for _, cmd := range []string{
		"claude --model opus",
		"claude --permission-mode plan",
		"claude --permission-mode bypassPermissions",
		"/usr/local/bin/my-claude-wrapper",
		"claude ",
	} {
		c := &Config{AgentProfiles: []AgentProfile{{Name: "claude", Command: cmd, Hooks: true}}}
		c.normalize()
		got, _ := c.Profile("claude")
		if got.Command != cmd {
			t.Errorf("claude command = %q, want the user's %q untouched", got.Command, cmd)
		}
	}
}

// Only profiles listed in bareCommands are eligible, so an agent whose default
// never changed is not rewritten by a later edit to some other profile.
func TestNormalizeDoesNotRewriteOtherProfiles(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "codex", Command: "codex"}}}
	c.normalize()

	got, _ := c.Profile("codex")
	if got.Command != "codex" {
		t.Errorf("codex command = %q, want %q", got.Command, "codex")
	}
}

// The migration has to run on the real load path, not just on a hand-built
// Config: LoadConfig is where an existing install's file is read.
func TestLoadConfigUpgradesBareClaudeCommand(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	if err := os.WriteFile(ConfigPath(), []byte(`{
	  "agent_profiles": [{"name": "claude", "command": "claude", "hooks": true}],
	  "default_profile": "claude"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got, _ := cfg.Profile("claude")
	if got.Command != "claude --permission-mode auto" {
		t.Errorf("loaded claude command = %q, want auto mode", got.Command)
	}
}

// A fresh install writes the flag straight to disk.
func TestLoadConfigCreatesDefaultWithAutoMode(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got, _ := cfg.Profile("claude")
	if got.Command != "claude --permission-mode auto" {
		t.Errorf("new config claude command = %q, want auto mode", got.Command)
	}
	on, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(on), "claude --permission-mode auto") {
		t.Errorf("config on disk lacks the flag:\n%s", on)
	}
}

// Every built-in profile has to know how to come back on the conversation it was
// having, or a restart after a reboot hands the user an agent that has forgotten
// the task.
func TestDefaultProfilesKnowHowToResume(t *testing.T) {
	for _, p := range DefaultProfiles() {
		if p.ResumeCommand == "" {
			t.Errorf("built-in profile %q has no resume command", p.Name)
		}
	}
}

// The resume lines must identify the conversation by the directory they are run
// in, which is the only thing that tells two of this board's sessions apart. A
// form that reached past it would give every session on a restarted board the
// same conversation.
func TestDefaultResumeCommandsAreScopedToTheWorktree(t *testing.T) {
	claude, _ := DefaultConfig().Profile("claude")
	if claude.ResumeCommand != "claude --permission-mode auto --continue" {
		t.Errorf("claude resume = %q, want --continue, which is scoped to the current directory",
			claude.ResumeCommand)
	}
	// Codex filters its session list by the directory it runs in unless it is asked
	// for --all, so that flag must never appear here.
	codex, _ := DefaultConfig().Profile("codex")
	if codex.ResumeCommand != "codex resume --last" {
		t.Errorf("codex resume = %q, want the most recent session in this directory",
			codex.ResumeCommand)
	}
	if strings.Contains(codex.ResumeCommand, "--all") {
		t.Error("codex resume looks past the working directory; every restarted session would resume the same conversation")
	}
}

// pi has no per-tool approval prompt, so its profile needs no permission flag --
// only the startup one, without which a worktree carrying project resources stops
// at a trust dialog before the opening prompt is read.
func TestDefaultPiProfileStartsWithoutAskingAboutTheProject(t *testing.T) {
	pi, ok := DefaultConfig().Profile("pi")
	if !ok {
		t.Fatal("no pi profile in the default config")
	}
	if pi.Command != "pi -a --" {
		t.Errorf("pi command = %q, want trust approved and option parsing ended before the prompt", pi.Command)
	}
	// -c is the most recent session filed under this directory, which is the
	// promise every other resume line here makes.
	if pi.ResumeCommand != "pi -a -c" {
		t.Errorf("pi resume = %q, want the most recent session in this directory", pi.ResumeCommand)
	}
}

// pi records the directory a conversation was held in and works there wherever it
// is resumed from, so attaching one has to copy it rather than reopen it.
func TestDefaultPiProfileForksWithAMintedID(t *testing.T) {
	pi, _ := DefaultConfig().Profile("pi")
	if !pi.ForkMintsID() {
		t.Fatal("pi cannot be told what to call the copy it makes")
	}
	line := pi.ForkLine("01a026e1-2d8d", "abc123")
	if line != "pi -a --fork '01a026e1-2d8d' --session-id 'abc123'" {
		t.Errorf("fork line = %q", line)
	}
	// The fork line must never stand in for a resume: a restart running it would
	// copy the original a second time and abandon everything done since.
	if strings.Contains(pi.RestartCommandFor("abc123"), "--fork") {
		t.Errorf("restart line forks: %q", pi.RestartCommandFor("abc123"))
	}
	if pi.ResumeIDLine("abc123") != "pi -a --session 'abc123'" {
		t.Errorf("resume-by-id line = %q", pi.ResumeIDLine("abc123"))
	}
}

func TestForkLineNeedsBothIDs(t *testing.T) {
	p := AgentProfile{ForkCommand: "pi --fork {session} --session-id {new}"}
	if got := p.ForkLine("source", ""); got != "" {
		t.Errorf("fork line with no id for the copy = %q, want none", got)
	}
	if got := p.ForkLine("", "minted"); got != "" {
		t.Errorf("fork line with nothing to copy = %q, want none", got)
	}
	// A profile that cannot be told an id still forks; dma just does not learn
	// what the copy is called.
	bare := AgentProfile{ForkCommand: "agent fork"}
	if got := bare.ForkLine("source", "minted"); got != "agent fork 'source'" {
		t.Errorf("bare fork line = %q", got)
	}
	if bare.ForkMintsID() {
		t.Error("a fork command with no {new} claimed it could name the copy")
	}
}

// A config written before dma knew pi forks must gain the fork line, or attaching
// a pi conversation would resume it in place and leave the agent working in the
// directory it came from.
func TestNormalizeBackfillsForkCommand(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "pi", Command: "pi -a"}}}
	c.normalize()

	got, _ := c.Profile("pi")
	want, _ := DefaultConfig().Profile("pi")
	if got.ForkCommand != want.ForkCommand {
		t.Errorf("pi fork command = %q, want %q", got.ForkCommand, want.ForkCommand)
	}
}

// A command the user replaced is a deliberate choice, and a generated fork line
// paired with it would fork something other than what starts a session.
func TestNormalizeLeavesACustomizedCommandWithoutAForkLine(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "pi", Command: "my-pi-wrapper"}}}
	c.normalize()

	got, _ := c.Profile("pi")
	if got.ForkCommand != "" {
		t.Errorf("fork command = %q, want none for a replaced command", got.ForkCommand)
	}
}

// A resume that finds nothing must not leave a live terminal sitting at a shell
// prompt: the board reads that as an agent running.
func TestRestartCommandFallsBackToAPlainLaunch(t *testing.T) {
	p, _ := DefaultConfig().Profile("claude")
	got := p.RestartCommand()
	if want := "claude --permission-mode auto --continue || claude --permission-mode auto"; got != want {
		t.Errorf("restart command = %q, want %q", got, want)
	}
}

// A profile with no resume line still restarts -- as a fresh agent, which callers
// are expected to say out loud.
func TestRestartCommandWithoutAResumeLineIsThePlainLaunch(t *testing.T) {
	p := AgentProfile{Name: "custom", Command: "my-agent --flag"}
	if got := p.RestartCommand(); got != "my-agent --flag" {
		t.Errorf("restart command = %q, want the plain launch", got)
	}
}

// The prompt placeholder belongs to a launch, and a restart carries no prompt: a
// {prompt} left in the line would reach the shell as a literal word.
func TestRestartCommandDropsThePromptPlaceholder(t *testing.T) {
	p := AgentProfile{Name: "custom", Command: "my-agent {prompt} --tail"}
	if got := p.RestartCommand(); strings.Contains(got, "{prompt}") {
		t.Errorf("restart command = %q, want the placeholder resolved", got)
	}
}

// A config written before restarting existed has a claude profile already, so
// the profile backfill skips it. Without this the resume line would reach new
// installs and nobody else.
func TestNormalizeBackfillsResumeCommands(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{
		{Name: "claude", Command: "claude --permission-mode auto", Hooks: true},
		{Name: "codex", Command: "codex", ImageArgument: "--image {path}"},
	}}
	c.normalize()

	for _, want := range []struct{ name, resume string }{
		{"claude", "claude --permission-mode auto --continue"},
		{"codex", "codex resume --last"},
	} {
		got, _ := c.Profile(want.name)
		if got.ResumeCommand != want.resume {
			t.Errorf("%s resume = %q, want %q", want.name, got.ResumeCommand, want.resume)
		}
	}
}

// A resume line is the command plus a flag, so a command the user replaced must
// not be handed a generated one: it would restart something other than what
// starts a session.
func TestNormalizeLeavesCustomizedCommandsWithoutAResumeLine(t *testing.T) {
	for _, cmd := range []string{
		"claude --model opus",
		"/usr/local/bin/my-claude-wrapper",
	} {
		c := &Config{AgentProfiles: []AgentProfile{{Name: "claude", Command: cmd, Hooks: true}}}
		c.normalize()
		got, _ := c.Profile("claude")
		if got.ResumeCommand != "" {
			t.Errorf("command %q was given resume line %q", cmd, got.ResumeCommand)
		}
	}
}

// A resume line the user wrote is theirs, whatever the command beside it.
func TestNormalizeKeepsAWrittenResumeCommand(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{
		Name:          "claude",
		Command:       "claude --permission-mode auto",
		ResumeCommand: "claude --resume-my-way",
	}}}
	c.normalize()

	got, _ := c.Profile("claude")
	if got.ResumeCommand != "claude --resume-my-way" {
		t.Errorf("resume command = %q, want the user's untouched", got.ResumeCommand)
	}
}

// The upgrade of a bare command and the resume backfill have to compose: a
// profile still on "claude" gets both, in that order, or it lands on the current
// command with no way to resume it.
func TestNormalizeUpgradesBareClaudeAndGivesItAResumeLine(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "claude", Command: "claude", Hooks: true}}}
	c.normalize()

	got, _ := c.Profile("claude")
	if got.Command != "claude --permission-mode auto" {
		t.Fatalf("claude command = %q, want auto mode", got.Command)
	}
	if got.ResumeCommand != "claude --permission-mode auto --continue" {
		t.Errorf("claude resume = %q, want the current default", got.ResumeCommand)
	}
}

// The prompt goes on the command line as one shell argument. Typing it into the
// agent's UI instead loses characters: codex reads its composer through a vim
// keymap, so "how are you" arrives as cursor motions.
func TestLaunchCommandPassesThePromptAsOneArgument(t *testing.T) {
	cases := []struct {
		name    string
		profile AgentProfile
		prompt  string
		images  []string
		want    string
	}{
		{
			name:    "appended to the command",
			profile: AgentProfile{Command: "codex"},
			prompt:  "fix the flaky test",
			want:    "codex 'fix the flaky test'",
		},
		{
			name:    "kept clear of the profile's own flags",
			profile: AgentProfile{Command: "claude --permission-mode auto"},
			prompt:  "ship it",
			want:    "claude --permission-mode auto 'ship it'",
		},
		{
			name:    "pi multiline bullet list follows the option separator",
			profile: AgentProfile{Command: "pi -a --"},
			prompt:  "- fix login\n- add a regression test",
			want:    "pi -a -- '- fix login\n- add a regression test'",
		},
		{
			name:    "quotes that would end the argument early",
			profile: AgentProfile{Command: "codex"},
			prompt:  "don't stop",
			want:    `codex 'don'\''t stop'`,
		},
		{
			name:    "shell metacharacters stay inert",
			profile: AgentProfile{Command: "codex"},
			prompt:  "rm -rf / ; echo $(whoami) && wc -l *.go",
			want:    "codex 'rm -rf / ; echo $(whoami) && wc -l *.go'",
		},
		{
			name:    "no prompt leaves the command alone",
			profile: AgentProfile{Command: "claude --permission-mode auto"},
			prompt:  "  ",
			want:    "claude --permission-mode auto",
		},
		{
			name:    "placeholder puts the prompt behind a flag",
			profile: AgentProfile{Command: "aider --message {prompt} --yes"},
			prompt:  "add a test",
			want:    "aider --message 'add a test' --yes",
		},
		{
			name:    "placeholder disappears when there is no prompt",
			profile: AgentProfile{Command: "aider --message {prompt}"},
			prompt:  "",
			want:    "aider --message ",
		},
		{
			name:    "image argument repeats before the prompt",
			profile: AgentProfile{Command: "codex", ImageArgument: "--image {path}"},
			prompt:  "inspect these",
			images:  []string{"/tmp/first image.png", "/tmp/don't.png"},
			want:    `codex --image '/tmp/first image.png' --image '/tmp/don'\''t.png' 'inspect these'`,
		},
		{
			name:    "images placeholder controls argument placement",
			profile: AgentProfile{Command: "agent {images} --message {prompt}", ImageArgument: "-i {path}"},
			prompt:  "inspect",
			images:  []string{"/tmp/image.png"},
			want:    "agent -i '/tmp/image.png' --message 'inspect'",
		},
		{
			name:    "profile without image flag receives paths in prompt",
			profile: AgentProfile{Command: "claude"},
			prompt:  "inspect",
			images:  []string{"/tmp/image.png"},
			want:    "claude 'inspect\n\nImages for this task:\n- /tmp/image.png'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.profile.LaunchCommand(tc.prompt, tc.images...); got != tc.want {
				t.Errorf("LaunchCommand(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

// Projects are a config-level list so one can be added before any session uses
// it -- and so added by hand, then removed again, without a session in between.
func TestGroupsAddAndRemove(t *testing.T) {
	c := DefaultConfig()

	if !c.AddProject("auth", "api") || !c.AddProject("infra", "") {
		t.Fatalf("adding new projects reported no change: %v", c.Groups)
	}
	if c.AddProject("auth", "web") {
		t.Errorf("adding a project twice reported a change: %v", c.Groups)
	}
	if c.AddProject("  ", "api") {
		t.Errorf("blank project was registered: %v", c.Groups)
	}
	if !c.RemoveProject("auth") {
		t.Fatalf("removing a known project reported no change: %v", c.Groups)
	}
	if c.RemoveProject("auth") {
		t.Errorf("removing an unknown project reported a change: %v", c.Groups)
	}
	if len(c.Groups) != 1 || c.Groups[0].Name != "infra" {
		t.Errorf("projects = %v, want [infra]", c.Groups)
	}
}

// A project's repo is what makes switching project enough to switch context, so
// it has to survive a round trip through the config file.
func TestProjectRepoIsStored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DMA_HOME", dir)

	c := DefaultConfig()
	c.Repos = []Repo{{ID: "api", Path: dir, BaseBranch: "main"}}
	c.AddProject("auth", "api")
	if err := SaveConfig(c); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if repo := got.ProjectRepo("auth"); repo != "api" {
		t.Errorf("reloaded project repo = %q, want api", repo)
	}
}

// A config written before projects had repos is a list of bare strings. It has
// to keep loading, since the alternative is a user losing their projects to an
// upgrade.
func TestProjectsLoadFromBareLabels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DMA_HOME", dir)

	if err := os.WriteFile(ConfigPath(), []byte(`{"groups":["auth","infra"]}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(c.Groups) != 2 || c.Groups[0].Name != "auth" || c.Groups[1].Name != "infra" {
		t.Fatalf("projects = %v, want auth and infra", c.Groups)
	}
	if repo := c.ProjectRepo("auth"); repo != "" {
		t.Errorf("migrated project claims repo %q, want none", repo)
	}
}

// Binding is how a project learns where its work happens after the fact, and
// rebinding to the repo it already names is not a change worth a config write.
func TestBindProject(t *testing.T) {
	c := DefaultConfig()
	c.Repos = []Repo{{ID: "api"}, {ID: "web"}}

	if !c.BindProject("auth", "api") {
		t.Fatal("binding an unknown project reported no change")
	}
	if c.BindProject("auth", "api") {
		t.Error("rebinding to the same repo reported a change")
	}
	if !c.BindProject("auth", "web") || c.ProjectRepo("auth") != "web" {
		t.Errorf("project repo = %q, want web", c.ProjectRepo("auth"))
	}
}

// A binding outliving the repo it names would send new sessions nowhere, so it
// reads as unbound rather than as a repo that does not exist.
func TestProjectRepoIgnoresUnregisteredRepos(t *testing.T) {
	c := DefaultConfig()
	c.AddProject("auth", "gone")

	if repo := c.ProjectRepo("auth"); repo != "" {
		t.Errorf("project repo = %q, want none for an unregistered repo", repo)
	}
}

// --- resuming one named conversation ---

func TestDefaultProfilesKnowHowToResumeByID(t *testing.T) {
	c := DefaultConfig()
	for _, want := range []struct{ name, line string }{
		{"claude", "claude --permission-mode auto --resume 'abc-123'"},
		{"codex", "codex resume 'abc-123'"},
	} {
		p, ok := c.Profile(want.name)
		if !ok {
			t.Fatalf("no %s profile", want.name)
		}
		if got := p.ResumeIDLine("abc-123"); got != want.line {
			t.Errorf("%s resume-by-id = %q, want %q", want.name, got, want.line)
		}
	}
}

// A profile whose command has no placeholder gets the id appended, so a custom
// agent needs no template syntax to be attachable.
func TestResumeIDLineAppendsWhenThereIsNoPlaceholder(t *testing.T) {
	p := AgentProfile{Name: "custom", ResumeIDCommand: "my-agent --resume"}
	if got := p.ResumeIDLine("abc"); got != "my-agent --resume 'abc'" {
		t.Errorf("resume-by-id = %q", got)
	}
}

// The line is typed into a pane as a shell command, so an id is quoted for the
// same reason a prompt is.
func TestResumeIDLineQuotesTheID(t *testing.T) {
	p := AgentProfile{Name: "custom", ResumeIDCommand: "my-agent --resume {session}"}
	got := p.ResumeIDLine("abc; rm -rf /")
	if want := `my-agent --resume 'abc; rm -rf /'`; got != want {
		t.Errorf("resume-by-id = %q, want %q", got, want)
	}
}

func TestResumeIDLineIsEmptyWithoutBothHalves(t *testing.T) {
	withCommand := AgentProfile{Name: "custom", ResumeIDCommand: "my-agent {session}"}
	if got := withCommand.ResumeIDLine(""); got != "" {
		t.Errorf("resume-by-id with no id = %q, want none", got)
	}
	withoutCommand := AgentProfile{Name: "custom", Command: "my-agent"}
	if got := withoutCommand.ResumeIDLine("abc"); got != "" {
		t.Errorf("resume-by-id with no command = %q, want none", got)
	}
}

// An attached session's transcript lives where the conversation began, not in
// the worktree dma made for it, so restarting one has to name the id rather
// than ask for "the most recent conversation here".
func TestRestartCommandForPrefersTheConversationID(t *testing.T) {
	p, _ := DefaultConfig().Profile("claude")
	got := p.RestartCommandFor("abc-123")
	want := "claude --permission-mode auto --resume 'abc-123' || claude --permission-mode auto"
	if got != want {
		t.Errorf("restart = %q, want %q", got, want)
	}
}

// A session dma started itself has no conversation id, and restarts by
// directory exactly as before.
func TestRestartCommandForWithoutAnIDIsUnchanged(t *testing.T) {
	p, _ := DefaultConfig().Profile("claude")
	if got, want := p.RestartCommandFor(""), p.RestartCommand(); got != want {
		t.Errorf("restart = %q, want %q", got, want)
	}
}

// An existing config has to gain the by-id line without being edited, or
// attaching would work on new installs and nowhere else.
func TestNormalizeBackfillsResumeIDCommands(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{
		{Name: "claude", Command: "claude --permission-mode auto", ResumeCommand: "claude --permission-mode auto --continue"},
		{Name: "codex", Command: "codex", ImageArgument: "--image {path}"},
	}}
	c.normalize()

	for _, want := range []struct{ name, line string }{
		{"claude", "claude --permission-mode auto --resume {session}"},
		{"codex", "codex resume {session}"},
	} {
		got, _ := c.Profile(want.name)
		if got.ResumeIDCommand != want.line {
			t.Errorf("%s resume_id_command = %q, want %q", want.name, got.ResumeIDCommand, want.line)
		}
	}
}

// The same rule the by-directory resume line follows: a command the user
// replaced is not handed a generated flag for a binary dma knows nothing about.
func TestNormalizeLeavesCustomizedCommandsWithoutAResumeIDLine(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{
		{Name: "claude", Command: "/usr/local/bin/my-claude-wrapper"},
	}}
	c.normalize()
	got, _ := c.Profile("claude")
	if got.ResumeIDCommand != "" {
		t.Errorf("a replaced command was given resume_id_command %q", got.ResumeIDCommand)
	}
}

func TestNormalizeKeepsAWrittenResumeIDCommand(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{
		Name:            "claude",
		Command:         "claude --permission-mode auto",
		ResumeIDCommand: "claude --resume-my-way {session}",
	}}}
	c.normalize()
	got, _ := c.Profile("claude")
	if got.ResumeIDCommand != "claude --resume-my-way {session}" {
		t.Errorf("resume_id_command = %q, want the one that was written", got.ResumeIDCommand)
	}
}

// A conversation id is somebody's typing, and it must not be read as a
// placeholder for the id dma is minting.
func TestForkLineDoesNotTreatTheIDAsAPlaceholder(t *testing.T) {
	p := AgentProfile{ForkCommand: "agent --fork {session} --id {new}"}
	got := p.ForkLine("{new}", "minted")
	if got != "agent --fork '{new}' --id 'minted'" {
		t.Errorf("fork line = %q", got)
	}
}
