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

// The line is typed into an agent that is already running, so both placeholders
// have to be filled in before it is sent.
func TestExpandPROpenSubstitutesPlaceholders(t *testing.T) {
	got := ExpandPROpen("/pr-shepherd {pr} {url}", 412, "https://github.com/o/r/pull/412")
	want := "/pr-shepherd 412 https://github.com/o/r/pull/412"
	if got != want {
		t.Errorf("ExpandPROpen = %q, want %q", got, want)
	}
}

// A line with no placeholder is a perfectly good instruction and must survive
// untouched.
func TestExpandPROpenLeavesAPlainLineAlone(t *testing.T) {
	if got := ExpandPROpen("watch the PR until CI is green", 412, "u"); got != "watch the PR until CI is green" {
		t.Errorf("ExpandPROpen = %q", got)
	}
}

// Empty means send nothing, which is what the caller checks.
func TestExpandPROpenIsEmptyWhenUnset(t *testing.T) {
	if got := ExpandPROpen("", 412, "u"); got != "" {
		t.Errorf("ExpandPROpen = %q, want empty", got)
	}
}

// Unlike a launch command this is not handed to a shell, so quoting it would
// put literal quotes in the agent's composer.
func TestExpandPROpenDoesNotShellQuote(t *testing.T) {
	if got := ExpandPROpen("shepherd it, don't stop at the first failure", 1, ""); strings.Contains(got, `'\''`) {
		t.Errorf("ExpandPROpen shell-quoted the line: %q", got)
	}
}

// shepherdConfig is one repo and one profile, each optionally carrying a line.
func shepherdConfig(profileLine string, repoLine *string) *Config {
	return &Config{
		Repos:         []Repo{{ID: "r1", OnPROpen: repoLine}},
		AgentProfiles: []AgentProfile{{Name: "claude", OnPROpen: profileLine}},
	}
}

func ptr(s string) *string { return &s }

// The profile is the default, so setting it once covers every repo that agent
// works in -- which is what makes shepherding unconditional.
func TestPROpenLineFallsBackToTheProfile(t *testing.T) {
	c := shepherdConfig("/pr-shepherd {pr}", nil)
	if got := c.PROpenLine("r1", "claude"); got != "/pr-shepherd {pr}" {
		t.Errorf("PROpenLine = %q, want the profile line", got)
	}
	// A repo nobody registered still gets the profile's line: sessions are
	// created against a repo id, and losing the line over a stale one would be a
	// silent hole in "always".
	if got := c.PROpenLine("unregistered", "claude"); got != "/pr-shepherd {pr}" {
		t.Errorf("PROpenLine for an unknown repo = %q, want the profile line", got)
	}
}

// A repo whose review flow differs replaces the line rather than adding to it.
func TestPROpenLineRepoOverridesTheProfile(t *testing.T) {
	c := shepherdConfig("/pr-shepherd {pr}", ptr("/deploy-watch {pr}"))
	if got := c.PROpenLine("r1", "claude"); got != "/deploy-watch {pr}" {
		t.Errorf("PROpenLine = %q, want the repo line", got)
	}
}

// The reason the repo field is a pointer: a repo with nothing to shepherd has to
// be able to say so, and an empty string is how it says it.
func TestPROpenLineRepoCanDisableTheProfileLine(t *testing.T) {
	c := shepherdConfig("/pr-shepherd {pr}", ptr(""))
	if got := c.PROpenLine("r1", "claude"); got != "" {
		t.Errorf("PROpenLine = %q, want empty", got)
	}
}

// A repo can also turn shepherding on for itself alone.
func TestPROpenLineRepoCanSetALineWithNoProfileDefault(t *testing.T) {
	c := shepherdConfig("", ptr("/pr-shepherd {pr}"))
	if got := c.PROpenLine("r1", "claude"); got != "/pr-shepherd {pr}" {
		t.Errorf("PROpenLine = %q, want the repo line", got)
	}
}

// A repo override has to survive a save/load round trip, including the empty
// one -- omitempty on a pointer must not drop a deliberate opt-out.
func TestPROpenLineOverrideRoundTrips(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.Repos = []Repo{
		{ID: "off", Path: "/tmp/off", OnPROpen: ptr("")},
		{ID: "custom", Path: "/tmp/custom", OnPROpen: ptr("/deploy-watch {pr}")},
		{ID: "inherit", Path: "/tmp/inherit"},
	}
	for i := range cfg.AgentProfiles {
		if cfg.AgentProfiles[i].Name == "claude" {
			cfg.AgentProfiles[i].OnPROpen = "/pr-shepherd {pr}"
		}
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ repo, want string }{
		{"off", ""},
		{"custom", "/deploy-watch {pr}"},
		{"inherit", "/pr-shepherd {pr}"},
	} {
		if line := got.PROpenLine(tc.repo, "claude"); line != tc.want {
			t.Errorf("PROpenLine(%q) = %q, want %q", tc.repo, line, tc.want)
		}
	}
}

// Codex reads its composer through a vim keymap when one is configured, so a
// line typed straight in loses its leading characters. The default profile has
// to carry the keys that put it in insert mode first.
func TestDefaultCodexProfileCarriesAComposePrefix(t *testing.T) {
	c := DefaultConfig()
	got, _ := c.Profile("codex")
	if len(got.ComposePrefix) != 2 || got.ComposePrefix[0] != "Escape" || got.ComposePrefix[1] != "i" {
		t.Errorf("codex compose prefix = %v, want [Escape i]", got.ComposePrefix)
	}
	// Escape interrupts Claude Code rather than changing a mode, so sending one
	// would cancel the turn the line is trying to start.
	claude, _ := c.Profile("claude")
	if len(claude.ComposePrefix) != 0 {
		t.Errorf("claude compose prefix = %v, want none", claude.ComposePrefix)
	}
}

// A config written before the prefix existed would otherwise keep mangling every
// line typed into codex, so this is backfilled rather than only defaulted.
func TestNormalizeBackfillsTheComposePrefix(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{{Name: "codex", Command: "codex"}}}
	c.normalize()

	got, _ := c.Profile("codex")
	if len(got.ComposePrefix) != 2 {
		t.Errorf("codex compose prefix = %v, want it backfilled", got.ComposePrefix)
	}
}

// An explicit empty list is a deliberate "send nothing first" -- someone who
// turned vim mode off -- and must survive normalize.
func TestNormalizeKeepsAnExplicitlyEmptyComposePrefix(t *testing.T) {
	c := &Config{AgentProfiles: []AgentProfile{
		{Name: "codex", Command: "codex", ComposePrefix: []string{}},
	}}
	c.normalize()

	got, _ := c.Profile("codex")
	if got.ComposePrefix == nil || len(got.ComposePrefix) != 0 {
		t.Errorf("codex compose prefix = %v, want it left empty", got.ComposePrefix)
	}
}

// The distinction only holds if it survives the file, and omitempty drops both
// nil and an empty slice -- so an explicit opt-out has to be checked end to end.
func TestComposePrefixOptOutRoundTrips(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	if err := os.WriteFile(ConfigPath(), []byte(`{
	  "agent_profiles": [{"name": "codex", "command": "codex", "compose_prefix": []}],
	  "default_profile": "codex"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := cfg.Profile("codex")
	if len(got.ComposePrefix) != 0 {
		t.Errorf("codex compose prefix = %v, want the opt-out preserved", got.ComposePrefix)
	}
}

// The opt-out has to survive the config being written back, which is what the
// TUI does every time a line is edited. omitempty drops an empty slice as
// readily as a nil one, so this is the case that catches it.
func TestComposePrefixOptOutSurvivesASave(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	cfg := DefaultConfig()
	for i := range cfg.AgentProfiles {
		if cfg.AgentProfiles[i].Name == "codex" {
			cfg.AgentProfiles[i].ComposePrefix = []string{}
		}
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	prof, _ := got.Profile("codex")
	if len(prof.ComposePrefix) != 0 {
		t.Errorf("codex compose prefix = %v after a save/load, want the opt-out preserved", prof.ComposePrefix)
	}
}
