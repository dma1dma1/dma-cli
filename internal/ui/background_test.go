package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// created is the message a finished start sends back.
func created(s *core.Session) createdMsg {
	return createdMsg{
		res:  &ops.CreateResult{Session: s},
		task: "do a thing",
	}
}

// oneRepoCfg is enough config for the composer to describe a session.
func oneRepoCfg() *core.Config {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}}
	return cfg
}

// The whole point of the background start: the agent you were reading is still
// the agent in the panel when the new one lands, seconds later.
func TestBackgroundStartLeavesThePanelAlone(t *testing.T) {
	m := testModel(nil, liveSess("a"), liveSess("b"))
	m.selectSession(m.sessions[1])

	next, _ := m.handleCreated(created(sess("new", "", core.LifecycleActive, core.AgentWorking, "r")))
	got := next.(Model)

	if got.selectedID != "b" {
		t.Errorf("panel moved to %q, want it left on b", got.selectedID)
	}
	if len(got.sessions) != 3 {
		t.Fatalf("board holds %d sessions, want the new one added", len(got.sessions))
	}
}

// Nothing moved on screen and the card may have landed in a column that is
// scrolled or full, so the one thing the background start owes you is a line
// saying which session it was -- and it is news, not a failure.
func TestBackgroundStartNamesWhatItStarted(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	s := sess("new", "", core.LifecycleActive, core.AgentWorking, "r")
	s.Title = "retry the flaky upload test"

	next, _ := m.handleCreated(created(s))
	got := next.(Model)

	if !strings.Contains(got.notice, s.Title) {
		t.Errorf("notice = %q, want it to name %q", got.notice, s.Title)
	}
	if got.noticeErr {
		t.Error("the notice is styled as an error; starting a session in the background is not one")
	}
}

// An empty panel has nothing to be pulled away from, so the first card to arrive
// fills it. Otherwise the very first session of a board would start behind a
// panel that says nothing is selected.
func TestBackgroundStartFillsAnEmptyPanel(t *testing.T) {
	m := testModel(nil)

	next, _ := m.handleCreated(created(sess("new", "", core.LifecycleActive, core.AgentWorking, "r")))
	got := next.(Model)

	if got.selectedID != "new" {
		t.Errorf("panel shows %q, want the new session: there was nothing to keep", got.selectedID)
	}
	if got.notice != "" {
		t.Errorf("notice = %q, want nothing: the session is on the panel", got.notice)
	}
}

// enter spends the composer: task sent, box emptied, keyboard handed back to the
// board.
func TestEnterSubmitsTheTask(t *testing.T) {
	m := testModel(oneRepoCfg())
	m.focus = focusInput
	m.input.Focus()
	m.input.SetValue("retry the flaky upload test")
	m.layoutSizes()

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := next.(Model)

	if cmd == nil {
		t.Fatal("enter produced no command; no session was started")
	}
	if v := got.input.Value(); v != "" {
		t.Errorf("input still holds %q, want it spent", v)
	}
	if got.focus != focusBoard {
		t.Errorf("focus = %v, want the board back", got.focus)
	}
}

// ctrl-enter was the background start when starts could also be in the
// foreground. Every start is a background start now, so it stays a second submit
// key rather than punishing the habit -- and rather than reaching the field,
// where it would insert a newline.
func TestCtrlEnterStillSubmitsTheTask(t *testing.T) {
	m := testModel(oneRepoCfg())
	m.focus = focusInput
	m.input.Focus()
	m.input.SetValue("retry the flaky upload test")
	m.layoutSizes()

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	got := next.(Model)

	if cmd == nil {
		t.Fatal("ctrl-enter produced no command; no session was started")
	}
	if v := got.input.Value(); v != "" {
		t.Errorf("input still holds %q, want it spent", v)
	}
	if got.focus != focusBoard {
		t.Errorf("focus = %v, want the board back", got.focus)
	}
}

// ctrl-enter on an empty composer is enter on an empty composer: there is no
// task to start, so it just closes the box.
func TestCtrlEnterOnAnEmptyTaskJustCloses(t *testing.T) {
	m := testModel(oneRepoCfg())
	m.focus = focusInput
	m.input.Focus()

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	if got := next.(Model); got.focus != focusBoard {
		t.Errorf("focus = %v, want the board back", got.focus)
	}
}
