package ui

// `dma attach` can add a session from a shell, and another open board can prune
// one. These cover a board reconciling both directions without replacing newer
// in-memory observation and PR facts on cards it already knows.

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func TestBoardTakesOnSessionsAddedFromOutside(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"))

	attached := sess("b", "", core.LifecycleIdle, core.AgentIdle, "r")
	attached.AgentSessionID = "conv-1"
	next, _ := m.handleAdoptedSessions(adoptedSessionsMsg{sessions: []*core.Session{attached}})

	got := next.(Model)
	if len(got.sessions) != 2 {
		t.Fatalf("board holds %d sessions, want 2", len(got.sessions))
	}
	if core.FindByID(got.sessions, "b") == nil {
		t.Error("the attached session is not on the board")
	}
	// It arrives in a column the user may not be looking at, so it says so.
	if got.notice == "" {
		t.Error("nothing was said about the new session")
	}
}

// The panel is not pulled onto an arriving card, for the same reason starting a
// session in the background does not move it.
func TestAdoptingASessionDoesNotStealThePanel(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.selectedID = "a"

	next, _ := m.handleAdoptedSessions(adoptedSessionsMsg{
		sessions: []*core.Session{sess("b", "", core.LifecycleIdle, core.AgentIdle, "r")},
	})
	if got := next.(Model); got.selectedID != "a" {
		t.Errorf("panel moved to %q", got.selectedID)
	}
}

// An arriving session is about to be resized to this board's panel, and a
// resize dma issues must never be read as the agent working.
func TestAdoptingASessionRecordsTheResizeItCauses(t *testing.T) {
	m := testModel(nil)
	next, _ := m.handleAdoptedSessions(adoptedSessionsMsg{
		sessions: []*core.Session{sess("b", "", core.LifecycleIdle, core.AgentIdle, "r")},
	})
	if _, ok := next.(Model).touchedAt["b"]; !ok {
		t.Error("the resize was not recorded against the new session")
	}
}

// A session attached into a project the board has never heard of registers it,
// so the project chip and filters can reach it.
func TestAdoptingASessionRegistersItsProject(t *testing.T) {
	m := testModel(nil)
	attached := sess("b", "billing", core.LifecycleIdle, core.AgentIdle, "r")

	next, _ := m.handleAdoptedSessions(adoptedSessionsMsg{sessions: []*core.Session{attached}})
	if _, ok := next.(Model).cfg.Project("billing"); !ok {
		t.Error("the attached session's project was not registered")
	}
}

func TestAdoptingNothingChangesNothing(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.notice = "something earlier"

	next, cmd := m.handleAdoptedSessions(adoptedSessionsMsg{})
	if cmd != nil {
		t.Error("an empty poll result did work anyway")
	}
	if got := next.(Model); got.notice != "something earlier" {
		t.Errorf("notice = %q, want the earlier one left alone", got.notice)
	}
}

func TestBoardDropsSessionPrunedByAnotherBoard(t *testing.T) {
	a := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	b := sess("b", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(nil, a, b)
	m.selectSession(b)
	m.preview = "b's old terminal"

	next, cmd := m.handleAdoptedSessions(adoptedSessionsMsg{removedIDs: []string{"b"}})
	got := next.(Model)
	if cmd != nil {
		t.Error("a remote prune tried to resize a session")
	}
	if ids := idsOf(got.sessions); len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("sessions after remote prune = %v, want only a", ids)
	}
	if got.selectedID != "a" || got.preview != "" {
		t.Errorf("panel after remote prune = selected %q, preview %q", got.selectedID, got.preview)
	}
}

func TestExternalPollReportsDurablyPrunedSessions(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	a := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	b := sess("b", "", core.LifecycleIdle, core.AgentIdle, "r")
	if err := core.SaveSessions([]*core.Session{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := core.DeleteSession("b"); err != nil {
		t.Fatal(err)
	}

	msg, ok := adoptExternalCmd([]*core.Session{a, b})().(adoptedSessionsMsg)
	if !ok {
		t.Fatal("external poll returned the wrong message")
	}
	if len(msg.sessions) != 0 || len(msg.removedIDs) != 1 || msg.removedIDs[0] != "b" {
		t.Fatalf("external poll = %+v, want b removed", msg)
	}
}

func TestRepeatedExternalPollDoesNotRepeatPruneNotice(t *testing.T) {
	a := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(nil, a)
	m.notice = "pruned on another board"

	next, cmd := m.handleAdoptedSessions(adoptedSessionsMsg{
		sessions:   []*core.Session{a},
		removedIDs: []string{"already-gone"},
	})
	got := next.(Model)
	if cmd != nil || got.notice != m.notice {
		t.Errorf("repeated poll changed model: cmd=%v notice=%q", cmd != nil, got.notice)
	}
}

// Several at once is one line, not one line per session: the notice is a single
// row and three of them would each overwrite the last.
func TestAdoptingSeveralSessionsSaysHowMany(t *testing.T) {
	m := testModel(nil)
	next, _ := m.handleAdoptedSessions(adoptedSessionsMsg{sessions: []*core.Session{
		sess("b", "", core.LifecycleIdle, core.AgentIdle, "r"),
		sess("c", "", core.LifecycleIdle, core.AgentIdle, "r"),
	}})
	if got := next.(Model).notice; got != "attached: 2 sessions" {
		t.Errorf("notice = %q", got)
	}
}
