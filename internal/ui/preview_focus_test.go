package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// liveSess is a session with a terminal behind it, which is the precondition for
// typing into the panel at all.
func liveSess(id string) *core.Session {
	s := sess(id, "", core.LifecycleActive, core.AgentWorking, "r")
	s.TmuxSession = "dma-" + id
	s.TmuxAlive = true
	return s
}

func press(m Model, key tea.KeyPressMsg) Model {
	next, _ := m.handleKey(key)
	return next.(Model)
}

// keyOf builds the keypress Bubble Tea would deliver for a printable character.
func keyOf(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// The point of the feature: with the panel focused, the board's own single-letter
// bindings stop applying and the keystrokes go to the agent instead.
func TestPreviewFocusDoesNotRunBoardBindings(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	// Each of these does something drastic on the board -- quit, open help, jump to
	// the input, expand the panel. None may fire while the agent has the keyboard.
	for _, r := range []rune{'q', '?', 'i', 'e', 'd', 'x', 'r', 'p', 'a', 't'} {
		next := press(m, keyOf(r))
		if next.quitting {
			t.Errorf("%q quit the program while the panel had focus", r)
		}
		if next.mode != modeBoard {
			t.Errorf("%q changed mode to %d while the panel had focus", r, next.mode)
		}
		if next.focus != focusPreview {
			t.Errorf("%q moved focus off the panel", r)
		}
	}
}

// ctrl+c is the one board-wide binding worth calling out: it quits everywhere
// else, and an agent needs it to interrupt.
func TestPreviewFocusForwardsCtrlCInsteadOfQuitting(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	next := press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if next.quitting {
		t.Error("ctrl+c quit dma instead of interrupting the agent")
	}
	if next.focus != focusPreview {
		t.Error("ctrl+c moved focus off the panel")
	}
}

// ...and it must still quit from the board, which is where every other mode
// expects it to.
func TestCtrlCStillQuitsFromBoard(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusBoard

	if next := press(m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); !next.quitting {
		t.Error("ctrl+c no longer quits from the board")
	}
}

// Escape has to reach the agent: it is how a coding agent is interrupted
// mid-turn, so it cannot double as the way out of the panel.
func TestPreviewFocusForwardsEscape(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: 27}) // esc
	if got := next.(Model).focus; got != focusPreview {
		t.Errorf("esc left the panel (focus=%d); it belongs to the agent", got)
	}
	if cmd == nil {
		t.Error("esc produced no command, so nothing was sent to the agent")
	}
}

// tab is likewise the agent's -- completion -- so it must not walk the focus ring
// from inside the panel.
func TestPreviewFocusForwardsTab(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	if next := press(m, tea.KeyPressMsg{Code: 9}); next.focus != focusPreview {
		t.Errorf("tab walked the focus ring out of the panel (focus=%d)", next.focus)
	}
}

// The single reserved key, and the only way back to the board.
func TestDetachKeyLeavesPreviewFocus(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	next := press(m, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if next.focus != focusBoard {
		t.Errorf("%s did not return focus to the board (focus=%d)", detachKey, next.focus)
	}
	if next.quitting {
		t.Errorf("%s quit the program; it should only hand back the keyboard", detachKey)
	}
}

// detachKey is spelled three ways for three consumers. If the Bubble Tea
// spelling drifts from the display one the panel becomes a trap with no exit.
func TestDetachKeySpellingsAgree(t *testing.T) {
	want := tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}.String()
	if detachKeypress != want {
		t.Errorf("detachKeypress = %q, but Bubble Tea reports %q", detachKeypress, want)
	}
	if detachKey != "ctrl-q" || detachTmuxKey != "C-q" {
		t.Errorf("detachKey/detachTmuxKey = %q/%q, want the ctrl-q spellings", detachKey, detachTmuxKey)
	}
}

func TestTypeKeyFocusesPreview(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusBoard

	if next := press(m, keyOf('t')); next.focus != focusPreview {
		t.Errorf("t left focus at %d, want the panel", next.focus)
	}
}

// Aiming the keyboard at a terminal that is gone would swallow every keystroke,
// so it is refused with a reason instead.
func TestTypeKeyRefusedWhenTerminalGone(t *testing.T) {
	dead := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	dead.TmuxAlive = false
	m := testModel(nil, dead)
	m.focus = focusBoard

	next := press(m, keyOf('t'))
	if next.focus == focusPreview {
		t.Error("t focused the panel for a session with no terminal")
	}
}

// tab must not park on the panel when there is nothing to type into: a focus
// stop that swallows keys and shows no caret looks like a hung UI.
func TestTabSkipsPreviewWithoutLiveTerminal(t *testing.T) {
	dead := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(nil, dead)
	m.focus = focusBoard

	if next := press(m, tea.KeyPressMsg{Code: 9}); next.focus == focusPreview {
		t.Error("tab landed on the panel with no live terminal behind it")
	}
}

func TestTabReachesPreviewWithLiveTerminal(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusBoard

	if next := press(m, tea.KeyPressMsg{Code: 9}); next.focus != focusPreview {
		t.Errorf("tab from the board went to %d, want the panel first", next.focus)
	}
}

// The whole ring stays reachable in both directions.
func TestFocusRingWalksEveryStop(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusBoard

	seen := map[focusArea]bool{focusBoard: true}
	cur := m
	for range focusRing {
		// Leaving the panel by tab is not possible -- tab belongs to the agent --
		// so step the ring directly rather than through handleKey.
		next, _ := cur.moveFocus(1)
		cur = next.(Model)
		seen[cur.focus] = true
	}
	for _, f := range focusRing {
		if !seen[f] {
			t.Errorf("focus %d is unreachable by tab", f)
		}
	}
}

// A session dying while its panel has focus must hand the keyboard back rather
// than quietly eat what the user types.
func TestPreviewFocusReleasedWhenSessionDies(t *testing.T) {
	s := liveSess("a")
	m := testModel(nil, s)
	m.focus = focusPreview

	s.TmuxAlive = false
	if next := press(m, keyOf('x')); next.focus != focusBoard {
		t.Errorf("focus stayed on a dead session's panel (focus=%d)", next.focus)
	}
}

// The preview looks like a terminal; clicking it should aim the keyboard there.
func TestClickOnPreviewFocusesIt(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "● Hello! What can I help you with?"
	m.layoutSizes()
	rendered(t, m)

	z := zone.Get(zonePreview)
	if z.IsZero() {
		t.Fatal("no zone recorded for the preview body")
	}
	next, _ := m.handleClick(clickAt((z.StartX+z.EndX)/2, (z.StartY+z.EndY)/2))
	if got := next.(Model).focus; got != focusPreview {
		t.Errorf("clicking the agent's output left focus at %d", got)
	}
}
