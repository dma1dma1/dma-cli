package ui

// `dma attach` starts a session from a shell, and the board it belongs on is
// usually already open. These cover the board taking it on, which it has to do
// for a reason stronger than visibility: the board writes its whole session list
// every time it saves, so a session it has not noticed is a session its next
// save deletes.

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
