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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.profile.LaunchCommand(tc.prompt); got != tc.want {
				t.Errorf("LaunchCommand(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

// Projects are a config-level list so one can be added before any session uses
// it -- and so added by hand, then removed again, without a session in between.
func TestGroupsAddAndRemove(t *testing.T) {
	c := DefaultConfig()

	if !c.AddGroup("auth") || !c.AddGroup("infra") {
		t.Fatalf("adding new groups reported no change: %q", c.Groups)
	}
	if c.AddGroup("auth") {
		t.Errorf("adding a group twice reported a change: %q", c.Groups)
	}
	if c.AddGroup("  ") {
		t.Errorf("blank group was registered: %q", c.Groups)
	}
	if !c.RemoveGroup("auth") {
		t.Fatalf("removing a known group reported no change: %q", c.Groups)
	}
	if c.RemoveGroup("auth") {
		t.Errorf("removing an unknown group reported a change: %q", c.Groups)
	}
	if len(c.Groups) != 1 || c.Groups[0] != "infra" {
		t.Errorf("groups = %q, want [infra]", c.Groups)
	}
}
