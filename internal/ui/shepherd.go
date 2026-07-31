package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// On-PR-open lines are edited in the app rather than only in config.json, for
// the same reason repos are: deciding that a pull request should look after
// itself is part of working, not setup. The agent selector holds the default and
// the repo list holds the exception, which is where each is already being
// looked at.

// shepherdSummary describes a repo's on-PR-open setting for the repo list.
//
// A line is resolved per session -- the repo plus that session's agent -- so a
// row about the repo alone has to name which agent it is reading. The honest
// choice is the one new sessions here will use.
func (m Model) shepherdSummary(r core.Repo) string {
	if r.OnPROpen != nil {
		if strings.TrimSpace(*r.OnPROpen) == "" {
			return "on PR open: nothing — off for this repo"
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

// shepherdLabel annotates an agent in the selector with its default line.
func (m Model) shepherdLabel(p core.AgentProfile) string {
	if strings.TrimSpace(p.OnPROpen) == "" {
		return "no on-PR-open line"
	}
	return "on PR open: " + strings.TrimSpace(p.OnPROpen)
}

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
	m.startPrompt(promptRepoShepherd, "on PR open in "+r.ID, seed, r.ID)
	m.mode = modePrompt
}
