package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// created is the message a finished start sends back.
func created(s *core.Session) createdMsg {
	return createdMsg{
		res: &ops.CreateResult{Session: s},
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
	if len(got.sessions) != 1 {
		t.Fatalf("submit showed %d cards, want one immediately", len(got.sessions))
	}
	pending := got.sessions[0]
	if !pending.Starting {
		t.Error("the immediate card is not marked as preparing")
	}
	if pending.Title != "retry the flaky upload test" || pending.RepoID != "api" {
		t.Errorf("pending card = %#v", pending)
	}
	if pending.WorktreePath != "" || pending.TmuxSession != "" {
		t.Errorf("pending card claims resources it does not have: worktree=%q tmux=%q",
			pending.WorktreePath, pending.TmuxSession)
	}
	if got.selectedID != pending.ID {
		t.Errorf("empty board selected %q, want pending card %q", got.selectedID, pending.ID)
	}
	if view := got.render(); !strings.Contains(view, "preparing worktree and dependencies") {
		t.Errorf("pending card/panel does not explain the wait:\n%s", view)
	}
}

func TestCompletedStartReplacesItsPendingCard(t *testing.T) {
	pending := sess("pending", "", core.LifecycleActive, core.AgentWorking, "r")
	pending.Starting = true
	pending.Title = "summarized while cloning"
	m := testModel(nil, pending)

	real := sess("generated-by-ops", "", core.LifecycleActive, core.AgentWorking, "r")
	real.Title = "raw first line"
	real.WorktreePath = "/worktrees/ready"
	real.TmuxSession = "ready"
	real.TmuxAlive = true
	next, _ := m.handleCreated(createdMsg{
		id:  pending.ID,
		res: &ops.CreateResult{Session: real},
	})
	got := next.(Model)

	if len(got.sessions) != 1 {
		t.Fatalf("completion left %d cards, want one replacement", len(got.sessions))
	}
	ready := got.sessions[0]
	if ready.Starting || ready.ID != pending.ID {
		t.Errorf("completed card = %#v", ready)
	}
	if ready.Title != "summarized while cloning" {
		t.Errorf("completion replaced the early summary with %q", ready.Title)
	}
	if ready.WorktreePath != "/worktrees/ready" || !ready.TmuxAlive {
		t.Errorf("completion did not install the real session: %#v", ready)
	}
}

func TestFailedStartRemovesOnlyItsPendingCard(t *testing.T) {
	existing := liveSess("existing")
	pending := sess("pending", "", core.LifecycleActive, core.AgentWorking, "r")
	pending.Starting = true
	otherPending := sess("other-pending", "", core.LifecycleActive, core.AgentWorking, "r")
	otherPending.Starting = true
	m := testModel(nil, existing, pending, otherPending)
	m.selectSession(pending)

	next, cmd := m.handleCreated(createdMsg{id: pending.ID, err: errors.New("clone failed")})
	got := next.(Model)

	if ids := idsOf(got.sessions); len(ids) != 2 || ids[0] != existing.ID || ids[1] != otherPending.ID {
		t.Fatalf("sessions after failure = %v, want existing and the other pending start", ids)
	}
	if got.selectedID != existing.ID {
		t.Errorf("selection = %q, want surviving session", got.selectedID)
	}
	if cmd == nil {
		t.Fatal("failed start did not report an error")
	}
	note, ok := cmd().(noticeMsg)
	if !ok || !strings.Contains(note.text, "clone failed") {
		t.Errorf("failure message = %#v", note)
	}
}

func TestPendingCardsAreNeverPersisted(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	established := sess("ready", "", core.LifecycleIdle, core.AgentIdle, "r")
	pending := sess("pending", "", core.LifecycleActive, core.AgentWorking, "r")
	pending.Starting = true
	m := testModel(nil, established, pending)
	m.save()

	loaded, err := core.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != established.ID {
		t.Fatalf("persisted sessions = %v, want only ready", idsOf(loaded))
	}
}

func TestPendingCardsAreExcludedFromSessionOperations(t *testing.T) {
	ready := sess("ready", "", core.LifecycleIdle, core.AgentIdle, "r")
	pending := sess("pending", "", core.LifecycleActive, core.AgentWorking, "r")
	pending.Starting = true
	m := testModel(nil, ready, pending)

	got := m.establishedSessions()
	if len(got) != 1 || got[0].ID != ready.ID {
		t.Fatalf("established sessions = %v, want only ready", idsOf(got))
	}
	if restartable(pending) {
		t.Error("a pending card is restartable before it has a worktree")
	}
	if cmd := previewCmd(pending); cmd != nil {
		t.Error("a pending card attempted to capture an empty tmux target")
	}
	m.selectSession(pending)
	next, cmd := m.keyBoard("x")
	if next.(Model).confirm.active || cmd == nil {
		t.Error("prune reached a pending card instead of reporting that it is preparing")
	}
}

func BenchmarkSubmitShowsPendingCard(b *testing.B) {
	base := testModel(oneRepoCfg())
	base.focus = focusInput
	b.ReportAllocs()
	for b.Loop() {
		m := base
		m.input.SetValue("retry the flaky upload test")
		next, cmd := m.startTask()
		got := next.(Model)
		if cmd == nil || len(got.sessions) != 1 || !got.sessions[0].Starting {
			b.Fatal("submit did not synchronously produce one pending card")
		}
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

func TestCreateProgressUpdatesPendingCardAndRearms(t *testing.T) {
	pending := sess("pending", "", core.LifecycleActive, core.AgentWorking, "r")
	pending.Starting = true
	pending.Title = "retry the flaky upload test"
	m := testModel(nil, pending)
	m.selectSession(pending)

	events := make(chan createEvent, 1)
	events <- createEvent{err: errors.New("stopped after progress"), complete: true}
	progress := createProgressMsg{
		id:       pending.ID,
		progress: "cloning node_modules (1/2)",
		ch:       events,
	}
	next, wait := m.Update(progress)
	got := next.(Model)
	if got.sessions[0].StartingDetail != string(progress.progress) {
		t.Fatalf("startup detail = %q, want %q", got.sessions[0].StartingDetail, progress.progress)
	}
	if view := got.render(); !strings.Contains(view, string(progress.progress)) {
		t.Errorf("rendered view does not contain progress:\n%s", view)
	}
	if wait == nil {
		t.Fatal("progress did not rearm the create event channel")
	}
	if _, ok := wait().(createdMsg); !ok {
		t.Fatal("rearmed command did not receive the completion event")
	}

	interactive, _ := got.Update(tea.KeyPressMsg{Code: '?'})
	if interactive.(Model).mode != modeHelp {
		t.Error("startup progress blocked normal UI updates")
	}
}
