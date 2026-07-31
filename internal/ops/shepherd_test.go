package ops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillAt creates a skill directory the way Claude Code lays one out.
func skillAt(t *testing.T, parts ...string) {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// An installed plugin skill is filed under its version, and the command that
// invokes it is /plugin:skill -- so the plugin name is two levels above the
// version, not the directory immediately containing the skill.
func TestFindShepherdSkillReadsAnInstalledPluginSkill(t *testing.T) {
	home := t.TempDir()
	skillAt(t, home, "plugins", "cache", "resolve-ai", "cdl-pr", "1.0.6", "skills", "pr-shepherd")

	if got := FindShepherdSkill(home); got != "/cdl-pr:pr-shepherd" {
		t.Errorf("FindShepherdSkill = %q, want /cdl-pr:pr-shepherd", got)
	}
}

// A marketplace copy is not filed under a version, so the plugin sits one level
// closer. Getting this wrong would produce a command named after a version.
func TestFindShepherdSkillReadsAMarketplaceSkill(t *testing.T) {
	home := t.TempDir()
	skillAt(t, home, "plugins", "marketplaces", "resolve-ai", "plugins", "cdl-pr", "skills", "pr-shepherd")

	if got := FindShepherdSkill(home); got != "/cdl-pr:pr-shepherd" {
		t.Errorf("FindShepherdSkill = %q, want /cdl-pr:pr-shepherd", got)
	}
}

// A user skill has no plugin to qualify it.
func TestFindShepherdSkillReadsAUserSkill(t *testing.T) {
	home := t.TempDir()
	skillAt(t, home, "skills", "pr-shepherd")

	if got := FindShepherdSkill(home); got != "/pr-shepherd" {
		t.Errorf("FindShepherdSkill = %q, want /pr-shepherd", got)
	}
}

// Installed beats merely available: both layouts commonly exist for the same
// plugin, and the answer must not depend on which glob ran first.
func TestFindShepherdSkillPrefersTheInstalledCopy(t *testing.T) {
	home := t.TempDir()
	skillAt(t, home, "plugins", "cache", "mp", "installed-pr", "1.0.0", "skills", "shepherd-it")
	skillAt(t, home, "plugins", "marketplaces", "mp", "plugins", "available-pr", "skills", "shepherd-it")

	if got := FindShepherdSkill(home); got != "/installed-pr:shepherd-it" {
		t.Errorf("FindShepherdSkill = %q, want the installed copy", got)
	}
}

// A directory matching the glob but holding no SKILL.md is not something the
// agent can be asked to run.
func TestFindShepherdSkillIgnoresADirectoryWithNoSkillFile(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "skills", "pr-shepherd"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := FindShepherdSkill(home); got != "" {
		t.Errorf("FindShepherdSkill = %q, want empty", got)
	}
}

// The exact name wins over anything else that merely matches, so a directory of
// several shepherd-ish skills resolves the same way every time.
func TestFindShepherdSkillPrefersTheExactName(t *testing.T) {
	home := t.TempDir()
	skillAt(t, home, "skills", "aardvark-shepherd")
	skillAt(t, home, "skills", "pr-shepherd")

	if got := FindShepherdSkill(home); got != "/pr-shepherd" {
		t.Errorf("FindShepherdSkill = %q, want /pr-shepherd", got)
	}
}

func TestFindShepherdSkillFindsNothingInAnEmptyHome(t *testing.T) {
	if got := FindShepherdSkill(t.TempDir()); got != "" {
		t.Errorf("FindShepherdSkill = %q, want empty", got)
	}
	if got := FindShepherdSkill(""); got != "" {
		t.Errorf("FindShepherdSkill(\"\") = %q, want empty", got)
	}
}

// Only Claude Code can be handed a slash command; every other agent is asked in
// words, which needs no plugin and no command name.
func TestDetectShepherdAsksInWordsForANonHookAgent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	skillAt(t, os.Getenv("CLAUDE_CONFIG_DIR"), "skills", "pr-shepherd")

	if got := DetectShepherd(false); got != ShepherdFallback {
		t.Errorf("DetectShepherd(false) = %q, want the worded fallback", got)
	}
	if got := DetectShepherd(true); got != "/pr-shepherd {pr}" {
		t.Errorf("DetectShepherd(true) = %q, want the detected command", got)
	}
}

// With nothing installed there is still a line to offer, or turning shepherding
// on would do nothing at all.
func TestDetectShepherdAlwaysReturnsALine(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if got := DetectShepherd(true); got != ShepherdFallback {
		t.Errorf("DetectShepherd = %q, want the worded fallback", got)
	}
}

// The fallback is only useful if it carries a placeholder the sender fills in.
func TestShepherdFallbackNamesThePullRequest(t *testing.T) {
	if !strings.Contains(ShepherdFallback, "{url}") {
		t.Errorf("fallback %q does not name the pull request", ShepherdFallback)
	}
}
