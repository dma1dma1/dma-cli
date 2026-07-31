package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// On-PR-open lines are set in the app rather than only in config.json, for the
// same reason repos are: deciding that a pull request should look after itself is
// part of working, not setup.
//
// And it is a toggle, not a text field. Asking someone to type
// "/cdl-pr:pr-shepherd {pr}" makes them go and look up a plugin-qualified skill
// name in order to express "yes" -- so the command is detected the way bootstrap
// paths are, and o just switches it on. The editor stays on O for the rare line
// nobody could have guessed.

// shepherdDefault is the line a toggle turns on for an agent.
//
// Only a Claude Code profile can be handed a slash command, so anything else is
// asked in words. A machine with no shepherd skill installed gets the same
// words, which is why turning shepherding on always does something.
func (m Model) shepherdDefault(profileName string) string {
	prof, _ := m.cfg.Profile(profileName)
	if prof.Hooks && m.shepherdSkill != "" {
		return m.shepherdSkill + " {pr}"
	}
	return ops.ShepherdFallback
}

// shepherdSummary describes a repo's on-PR-open setting for the repo list.
//
// A line is resolved per session -- the repo plus that session's agent -- so a
// row about the repo alone has to name which agent it is reading. The honest
// choice is the one new sessions here will use.
func (m Model) shepherdSummary(r core.Repo) string {
	if r.OnPROpen != nil {
		if strings.TrimSpace(*r.OnPROpen) == "" {
			return "on PR open: off for this repo"
		}
		return "on PR open: " + strings.TrimSpace(*r.OnPROpen)
	}
	agent := m.cfg.DefaultProfile
	line := m.cfg.PROpenLine(r.ID, agent)
	if line == "" {
		return fmt.Sprintf("on PR open: nothing — %s sets no line", agent)
	}
	return fmt.Sprintf("on PR open: %s — from %s", line, agent)
}

// shepherdLabel annotates an agent in the selector with its default line, and
// when there is none, with the line o would turn on -- so the toggle can be
// understood before it is pressed.
func (m Model) shepherdLabel(p core.AgentProfile) string {
	if line := strings.TrimSpace(p.OnPROpen); line != "" {
		return "on PR open: " + line
	}
	return "on PR open: off  (o → " + truncate(m.shepherdDefault(p.Name), 44) + ")"
}

// toggleProfileShepherd switches an agent's default line on or off, using the
// detected command when switching on.
func (m *Model) toggleProfileShepherd(name string) tea.Cmd {
	prof, ok := m.cfg.Profile(name)
	if !ok {
		return nil
	}
	if strings.TrimSpace(prof.OnPROpen) != "" {
		return m.setProfileShepherd(name, "")
	}
	return m.setProfileShepherd(name, m.shepherdDefault(name))
}

// cycleRepoShepherd steps a repo through the three settings it can hold, so
// neither exception needs typing: follows its agent, off here, on here.
//
// One key rather than two because the three are one decision, and the row names
// which of them is in force -- a cycle whose state is invisible would be the
// worse trade.
func (m *Model) cycleRepoShepherd(r core.Repo) tea.Cmd {
	switch {
	case r.OnPROpen == nil:
		off := ""
		return m.setRepoShepherd(r.ID, &off)
	case strings.TrimSpace(*r.OnPROpen) == "":
		return m.setRepoShepherd(r.ID, ptrTo(m.shepherdDefault(m.cfg.DefaultProfile)))
	default:
		return m.setRepoShepherd(r.ID, nil)
	}
}

func ptrTo(s string) *string { return &s }

// setProfileShepherd writes an agent's default on-PR-open line. Empty clears it,
// which is how an agent goes back to sending nothing anywhere.
func (m *Model) setProfileShepherd(name, line string) tea.Cmd {
	for i := range m.cfg.AgentProfiles {
		if m.cfg.AgentProfiles[i].Name != name {
			continue
		}
		m.cfg.AgentProfiles[i].OnPROpen = line
		if err := core.SaveConfig(m.cfg); err != nil {
			return errStatus(err)
		}
		if line == "" {
			return status(name + " sends nothing when a PR opens")
		}
		return status(withPending(fmt.Sprintf("%s on PR open: %s", name, line), m.pendingShepherds()))
	}
	return nil
}

// setRepoShepherd writes a repo's override. A nil line clears it, so the repo
// inherits its agent's again; an empty one turns shepherding off here.
func (m *Model) setRepoShepherd(id string, line *string) tea.Cmd {
	for i := range m.cfg.Repos {
		if m.cfg.Repos[i].ID != id {
			continue
		}
		m.cfg.Repos[i].OnPROpen = line
		if err := core.SaveConfig(m.cfg); err != nil {
			return errStatus(err)
		}
		switch {
		case line == nil:
			return status(id + " follows its agent's on-PR-open line")
		case strings.TrimSpace(*line) == "":
			return status(id + " will not be shepherded")
		default:
			return status(withPending(fmt.Sprintf("%s on PR open: %s", id, strings.TrimSpace(*line)),
				m.pendingShepherds()))
		}
	}
	return nil
}

// pendingShepherds counts the sessions the settings as they now stand would pick
// up, by asking the same question the poll asks.
//
// Turning a line on with work already in flight is the surprising case -- several
// agents start a turn at once on the next poll -- so it is reported rather than
// left to be discovered. Reusing shepherdCmdFor is what keeps the number from
// drifting away from the behaviour it describes.
func (m Model) pendingShepherds() int {
	n := 0
	for _, s := range m.sessions {
		if m.shepherdCmdFor(s) != nil {
			n++
		}
	}
	return n
}

func withPending(text string, n int) string {
	if n == 0 {
		return text
	}
	return fmt.Sprintf("%s — %d open PR(s) will be picked up", text, n)
}

// startRepoShepherdPrompt opens the editor for a repo's override, seeded with
// whatever is in force so editing an inherited line starts from that line rather
// than from nothing.
func (m *Model) startRepoShepherdPrompt(r core.Repo) {
	seed := m.cfg.PROpenLine(r.ID, m.cfg.DefaultProfile)
	if r.OnPROpen != nil {
		seed = strings.TrimSpace(*r.OnPROpen)
	}
	if seed == "" {
		seed = m.shepherdDefault(m.cfg.DefaultProfile)
	}
	m.startPrompt(promptRepoShepherd, "on PR open in "+r.ID, seed, r.ID)
	m.mode = modePrompt
}
