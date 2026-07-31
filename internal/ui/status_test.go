package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/dma1dma1/dma-cli/internal/core"
)

// drainStatus runs a command and reports the status message it produced, if
// any. Batches are walked one level down, which is as deep as the UI nests
// them.
func drainStatus(t *testing.T, cmd tea.Cmd) (statusMsg, bool) {
	t.Helper()
	if cmd == nil {
		return statusMsg{}, false
	}
	switch msg := cmd().(type) {
	case statusMsg:
		return msg, true
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if s, ok := c().(statusMsg); ok {
				return s, true
			}
		}
	}
	return statusMsg{}, false
}

// A status message takes the footer away from the shortcut bar for ten seconds.
// That is a fair trade for something the user cannot otherwise see, and a bad
// one for narrating a change already on screen -- the card moved, the filter
// tag appeared, the selector reads back the choice.
func TestVisibleActionsDoNotDisplaceTheShortcutBar(t *testing.T) {
	repoCfg := core.DefaultConfig()
	repoCfg.Repos = []core.Repo{{ID: "r1", Path: "/tmp/r1"}, {ID: "r2", Path: "/tmp/r2"}}

	cases := []struct {
		name string
		act  func() tea.Cmd
	}{
		{"moving a card between columns", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			_, cmd := m.moveCard(true)
			return cmd
		}},
		{"toggling the repo filter on", func() tea.Cmd {
			m := testModel(repoCfg, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			_, cmd := m.keyBoard("f")
			return cmd
		}},
		{"declining a confirm", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			mm, _ := m.sessionAction("x")
			m = mm.(Model)
			_, cmd := m.keyConfirm("n")
			return cmd
		}},
		{"choosing an agent", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			m.openDropdown(focusAgent)
			return m.applyDropdown()
		}},
		{"choosing a repo", func() tea.Cmd {
			m := testModel(repoCfg, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			m.openDropdown(focusRepo)
			return m.applyDropdown()
		}},
		{"a merge completing", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecyclePROpen, core.AgentIdle, "r1"))
			_, cmd := m.Update(mergedMsg{id: "a"})
			return cmd
		}},
		{"a kill completing", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentIdle, "r1"))
			_, cmd := m.Update(killedMsg{id: "a"})
			return cmd
		}},
		{"a prune completing", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			_, cmd := m.handleTeardown(teardownMsg{id: "a"})
			return cmd
		}},
		{"a PR opening", func() tea.Cmd {
			m := testModel(nil, branchSess("a", "r1", "feat-a", core.LifecycleActive))
			_, cmd := m.handleShipped(shippedMsg{id: "a", number: 42})
			return cmd
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg, ok := drainStatus(t, c.act()); ok {
				t.Errorf("posted %q to the footer", msg.text)
			}
		})
	}
}

// The other half of the rule: work the board cannot show must still say so.
func TestInFlightAndFailedWorkStillReachTheFooter(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))

	_, cmd := m.handlePRSync(prSyncMsg{repoID: "r1", err: errors.New("github timed out")})
	if _, ok := drainStatus(t, cmd); !ok {
		t.Error("a failed PR poll said nothing")
	}
	if _, ok := drainStatus(t, status("refreshing…")); !ok {
		t.Error("an in-flight message said nothing")
	}
}

// A failure must be styled as one, not as a neutral notice.
func TestFailuresAreStyledAsErrors(t *testing.T) {
	msg, ok := drainStatus(t, errText("devops-copilot: github timed out"))
	if !ok {
		t.Fatal("errText produced no status")
	}
	if !msg.isErr {
		t.Error("a failure was posted in the neutral style")
	}
}

// The footer falls back to the shortcut bar once nothing is pending.
func TestFooterShowsShortcutsWhenQuiet(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
	m.layoutSizes()
	// The tail of the bar truncates at narrow widths, so assert on the hints
	// that lead it rather than on the ones that fall off.
	bar := m.statusBar()
	for _, want := range []string{"move", "new task", "attach"} {
		if !strings.Contains(bar, want) {
			t.Errorf("footer is missing the %q shortcut:\n%s", want, bar)
		}
	}
}
