package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	// the input, open the diff. None may fire while the agent has the keyboard.
	for _, r := range []rune{'q', '?', 'i', 'd', 'x', 'r', 'p', 'a', 't'} {
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
	rendered(t, m, zonePreview)

	z := zone.Get(zonePreview)
	if z.IsZero() {
		t.Fatal("no zone recorded for the preview body")
	}
	next, _ := m.handleClick(clickAt((z.StartX+z.EndX)/2, (z.StartY+z.EndY)/2))
	if got := next.(Model).focus; got != focusPreview {
		t.Errorf("clicking the agent's output left focus at %d", got)
	}
}

// Once the preview owns input it behaves like the attached terminal for the
// other common input gesture too: the wheel reads tmux history rather than
// falling through to the board behind it.
func TestWheelScrollsFocusedPreviewHistory(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "older output\nlatest output"
	m.focus = focusPreview
	m.layoutSizes()
	rendered(t, m)

	z := zone.Get(zonePreview)
	if z.IsZero() {
		t.Fatal("no zone recorded for the preview body")
	}
	x, y := (z.StartX+z.EndX)/2, (z.StartY+z.EndY)/2
	next, cmd := m.handleMouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	m = next.(Model)
	if m.previewScroll != 3 {
		t.Errorf("wheel left preview scroll at %d, want 3", m.previewScroll)
	}
	if cmd == nil {
		t.Error("wheel scheduled no history capture")
	}

	next, _ = m.handleMouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	if got := next.(Model).previewScroll; got != 0 {
		t.Errorf("wheel down left preview scroll at %d, want live position", got)
	}
}

// Full-screen agents keep their own viewport on the alternate screen instead
// of contributing lines to tmux history. Once a capture reports SGR mouse mode,
// the wheel must be forwarded without inventing a history offset.
func TestWheelGoesToFocusedMouseAwarePreview(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "Claude full-screen output"
	m.previewMouseSGR = true
	m.focus = focusPreview
	m.layoutSizes()
	rendered(t, m, zonePreview)

	z := zone.Get(zonePreview)
	next, cmd := m.handleMouse(tea.MouseWheelMsg{
		X: (z.StartX + z.EndX) / 2, Y: (z.StartY + z.EndY) / 2,
		Button: tea.MouseWheelUp,
	})
	m = next.(Model)
	if m.previewScroll != 0 {
		t.Errorf("application wheel left tmux history offset at %d, want 0", m.previewScroll)
	}
	if cmd == nil {
		t.Fatal("application wheel scheduled no forwarded event")
	}
	if m.touchedAt["a"].IsZero() {
		t.Error("application wheel was not recorded as user input")
	}
}

// An agent that draws inline is scrolled through tmux history instead, and that
// gesture has to be recorded too. Whether the wheel reaches the application is a
// detail of how the agent renders -- capability is re-read on every capture and
// re-checked at send time -- so a scroll that turns out to repaint the pane must
// not come back as a turn the user never started.
func TestWheelThroughHistoryIsRecordedToo(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "Codex inline transcript"
	m.previewMouseSGR = false
	m.focus = focusPreview
	m.layoutSizes()
	rendered(t, m, zonePreview)

	z := zone.Get(zonePreview)
	next, cmd := m.handleMouse(tea.MouseWheelMsg{
		X: (z.StartX + z.EndX) / 2, Y: (z.StartY + z.EndY) / 2,
		Button: tea.MouseWheelUp,
	})
	m = next.(Model)
	if m.previewScroll == 0 {
		t.Error("wheel over an inline agent moved no history offset")
	}
	if cmd == nil {
		t.Fatal("wheel over an inline agent scheduled no capture")
	}
	if m.touchedAt["a"].IsZero() {
		t.Error("scrolling was not recorded, so the prober can read the next frame as work")
	}
}

func TestTypingInScrolledPreviewReturnsToLivePane(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview
	m.previewScroll = 12

	next, cmd := m.handleKey(keyOf('x'))
	if got := next.(Model).previewScroll; got != 0 {
		t.Errorf("typing left preview scroll at %d, want live position", got)
	}
	if cmd == nil {
		t.Error("typing produced no command for the agent")
	}
}

// panelBottomEdge is the panel's closing frame line: all frame and no title, so
// two focus states can be compared on how the box is drawn rather than on the
// words in its top edge.
func panelBottomEdge(m Model, f focusArea) string {
	m.focus = f
	lines := strings.Split(m.viewPanel(minPanelHeight), "\n")
	return lines[len(lines)-1]
}

// Clicking the agent's output has to look like it did something. The frame is the
// only part of the panel that can say so -- the body belongs to the agent -- and
// thickening it does not carry the weight alone: the board thickens the selected
// card's column too, so a heavy frame reads as "current", not "typing here".
func TestPreviewFocusColorsThePanelFrame(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "● Working on it…"

	focused := panelBottomEdge(m, focusPreview)
	// The edge is drawn as one styled run, so what identifies its color is the
	// sequence opening the line, not a styled glyph somewhere inside it.
	open, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(m.styles.P.Focus).Bold(true).Render("┗"), "┗")
	if !strings.HasPrefix(focused, open+"┗") {
		t.Errorf("the panel frame is not in the focus color while the agent has the keyboard: %q", focused)
	}

	// The chip case is the one worth pinning: it thickens this same frame, and a
	// state that swallows every keystroke must not look like one that does not.
	for _, other := range []focusArea{focusBoard, focusAgent} {
		if got := panelBottomEdge(m, other); got == focused {
			t.Errorf("focus %d draws the same panel frame as the live agent does: %q", other, got)
		}
	}
}

// emptyColumnPoint is the middle of a column holding no cards -- board space that
// is inside the frame and on no click target of its own.
func emptyColumnPoint(t *testing.T, m Model, col int) (int, int) {
	t.Helper()
	z := zone.Get(zoneColumn(col))
	if z.IsZero() {
		t.Fatalf("no zone recorded for column %d", col)
	}
	return (z.StartX + z.EndX) / 2, (z.StartY + z.EndY) / 2
}

// The other half of click-to-focus: a click on the board hands the keyboard back.
// Without this the panel keeps every keystroke after the pointer has plainly left
// it, and ctrl-q is the only way out of a mode the user already tried to leave.
func TestClickOffPreviewReleasesFocus(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "● Hello! What can I help you with?"
	m.focus = focusPreview
	m.layoutSizes()
	rendered(t, m, zoneColumn(core.LifecycleMerged.ColumnIndex()))

	// The session is active, so the merged column is empty board.
	x, y := emptyColumnPoint(t, m, core.LifecycleMerged.ColumnIndex())
	if got := clickModel(m, x, y).focus; got != focusBoard {
		t.Errorf("clicking empty board left focus at %d, want the board", got)
	}
}

// The task input has the same contract: clicking away stops the caret blinking in
// a box that is no longer taking what you type.
func TestClickOffInputReleasesFocus(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusInput
	m.input.Focus()
	m.layoutSizes()
	rendered(t, m, zoneColumn(core.LifecycleMerged.ColumnIndex()))

	x, y := emptyColumnPoint(t, m, core.LifecycleMerged.ColumnIndex())
	next := clickModel(m, x, y)
	if next.focus != focusBoard {
		t.Errorf("clicking empty board left focus at %d, want the board", next.focus)
	}
	if next.input.Focused() {
		t.Error("the task input kept the caret after the click left it")
	}
}
