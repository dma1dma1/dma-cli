package ops

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Shepherding is turned on with a keystroke rather than by typing a command,
// which means dma has to know what the command is. It is detected the same way
// bootstrap paths are: by reading what is installed, instead of asking someone
// to transcribe a name they would have to look up first.

// ShepherdFallback asks for the same work in words. It is what an agent with no
// shepherd skill gets, and it needs no plugin, no command name and no support
// from the agent beyond following instructions.
const ShepherdFallback = "Shepherd PR {url}: watch CI, fix what fails, " +
	"resolve review threads, and report when it is mergeable."

// preferredShepherdSkill is taken over any other match, so a directory of
// several shepherd-ish skills resolves the same way every time.
const preferredShepherdSkill = "pr-shepherd"

// shepherdSources are the places Claude Code keeps skills, with how far above
// the skill directory its plugin name sits.
//
// A plugin skill is invoked as /plugin:skill, and the depth differs by layout:
// an installed copy is filed under its version, a marketplace copy is not, and a
// user skill has no plugin at all.
var shepherdSources = []struct {
	glob string
	// pluginBack is how many elements back from the skill directory the plugin
	// name is. Zero means the skill has no plugin prefix.
	pluginBack int
}{
	{filepath.Join("plugins", "cache", "*", "*", "*", "skills", "*shepherd*"), 3},
	{filepath.Join("plugins", "marketplaces", "*", "plugins", "*", "skills", "*shepherd*"), 2},
	{filepath.Join("skills", "*shepherd*"), 0},
}

// DetectShepherd returns the on-PR-open line to offer for a profile.
//
// hooks distinguishes a Claude Code profile, which is the only kind that can be
// handed a slash command: a name Claude Code resolves means nothing to another
// agent, so everything else is asked in words instead.
func DetectShepherd(hooks bool) string {
	if hooks {
		if cmd := FindShepherdSkill(ClaudeHome()); cmd != "" {
			return cmd + " {pr}"
		}
	}
	return ShepherdFallback
}

// ClaudeHome is where Claude Code keeps user-level skills and installed plugins.
func ClaudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// FindShepherdSkill returns the slash command for an installed shepherd skill,
// or empty when there is none. The result carries no arguments.
func FindShepherdSkill(claudeHome string) string {
	if claudeHome == "" {
		return ""
	}
	best := ""
	for _, src := range shepherdSources {
		matches, err := filepath.Glob(filepath.Join(claudeHome, src.glob))
		if err != nil {
			continue
		}
		// Sorted because a glob's order is the filesystem's, and two installed
		// versions of one plugin would otherwise resolve differently per machine.
		sort.Strings(matches)
		for _, dir := range matches {
			cmd := shepherdCommand(dir, src.pluginBack)
			if cmd == "" {
				continue
			}
			// An exact name wins outright; anything else only fills a gap, and the
			// earlier source wins because installed beats merely available.
			if filepath.Base(dir) == preferredShepherdSkill {
				return cmd
			}
			if best == "" {
				best = cmd
			}
		}
	}
	return best
}

// shepherdCommand builds the slash command for a skill directory, after checking
// that it is one: a directory matching the glob but holding no SKILL.md is not
// something the agent can be asked to run.
func shepherdCommand(dir string, pluginBack int) string {
	if st, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil || st.IsDir() {
		return ""
	}
	skill := filepath.Base(dir)
	if skill == "" {
		return ""
	}
	if pluginBack == 0 {
		return "/" + skill
	}
	parts := strings.Split(filepath.ToSlash(dir), "/")
	i := len(parts) - 1 - pluginBack
	if i < 0 || parts[i] == "" {
		return ""
	}
	return "/" + parts[i] + ":" + skill
}
