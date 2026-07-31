package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/dma1dma1/dma-cli/internal/clip"
	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
)

// drainNotice runs a command and reports the notice it produced, if any.
// Batches are walked one level down, which is as deep as the UI nests them.
func drainNotice(t *testing.T, cmd tea.Cmd) (noticeMsg, bool) {
	t.Helper()
	if cmd == nil {
		return noticeMsg{}, false
	}
	switch msg := cmd().(type) {
	case noticeMsg:
		return msg, true
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if s, ok := c().(noticeMsg); ok {
				return s, true
			}
		}
	}
	return noticeMsg{}, false
}

// Only failures are posted. Work in flight and work that landed both say so by
// changing the board -- the card moves, the selector reads back the choice --
// and a line of prose about it is a row of the screen spent on something
// already on it.
func TestOrdinaryActionsPostNothing(t *testing.T) {
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
		{"refreshing with R", func() tea.Cmd {
			m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
			_, cmd := m.keyBoard("R")
			return cmd
		}},
		{"a PR joining the merge queue", func() tea.Cmd {
			s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
			s.PRNumber, s.PRState = 7, core.PROpen
			m := testModel(nil, s)
			_, cmd := m.handleMerged(mergedMsg{id: "a", outcome: ghx.MergeQueued})
			return cmd
		}},
		{"a PR the queue already held", func() tea.Cmd {
			s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
			s.PRNumber, s.PRState = 7, core.PROpen
			m := testModel(nil, s)
			_, cmd := m.handleMerged(mergedMsg{id: "a", outcome: ghx.MergeAlreadyQueued})
			return cmd
		}},
		{"pasting an image into the task input", func() tea.Cmd {
			m := testModel(nil)
			m.focus = focusInput
			_, cmd := m.handleClipboard(clipboardMsg{content: clip.Content{
				Image: &clip.Image{PNG: []byte("png"), Width: 640, Height: 480},
			}})
			return cmd
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if msg, ok := drainNotice(t, c.act()); ok {
				t.Errorf("posted %q", msg.text)
			}
		})
	}
}

// The other half of the rule: a failure the board cannot show must still say so.
func TestFailedWorkStillPostsANotice(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))

	_, cmd := m.handlePRSync(prSyncMsg{repoID: "r1", err: errors.New("github timed out")})
	if _, ok := drainNotice(t, cmd); !ok {
		t.Error("a failed PR poll said nothing")
	}
}

// The shortcut bar is the bottom line in every state. A notice goes above it,
// never over it.
func TestANoticeDoesNotCostTheShortcutBarItsLine(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
	m.layoutSizes()
	// The tail of the bar truncates at narrow widths, so assert on the hints
	// that lead it rather than on the ones that fall off.
	wantHints := []string{"move", "new task", "attach"}

	quiet := m.footer()
	if len(quiet) != 1 {
		t.Errorf("quiet footer is %d lines, want just the shortcut bar:\n%s",
			len(quiet), strings.Join(quiet, "\n"))
	}

	next, _ := m.Update(errText("github timed out")())
	m = next.(Model)

	noisy := m.footer()
	if len(noisy) != 2 {
		t.Fatalf("footer with a notice is %d lines, want the notice above the bar:\n%s",
			len(noisy), strings.Join(noisy, "\n"))
	}
	if !strings.Contains(noisy[0], "github timed out") {
		t.Errorf("the notice is not on the line above the bar:\n%s", noisy[0])
	}
	for _, want := range wantHints {
		if !strings.Contains(noisy[1], want) {
			t.Errorf("a notice hid the %q shortcut:\n%s", want, noisy[1])
		}
	}
}

// The row the notice occupies comes out of the view above it, so the bottom of
// the screen is still the shortcut bar rather than one row past it.
func TestTheNoticeRowComesOutOfTheViewAboveIt(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r1"))
	m.width, m.height = 120, 36
	m.layoutSizes()

	quiet := strings.Count(m.render(), "\n") + 1

	next, _ := m.Update(errText("github timed out")())
	m = next.(Model)

	if noisy := strings.Count(m.render(), "\n") + 1; noisy != quiet {
		t.Errorf("the board is %d rows tall with a notice up and %d without", noisy, quiet)
	}
}
