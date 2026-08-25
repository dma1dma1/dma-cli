package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// X is a bulk prune, so the one thing it must never do is reach a card that is
// not merged.
func TestPruneMergedTakesOnlyTheMergedColumn(t *testing.T) {
	m := testModel(nil,
		sess("idle", "", core.LifecycleIdle, core.AgentIdle, "r"),
		sess("open", "", core.LifecyclePROpen, core.AgentIdle, "r"),
		sess("done-a", "", core.LifecycleMerged, core.AgentIdle, "r"),
		sess("done-b", "", core.LifecycleMerged, core.AgentIdle, "r"),
	)

	mm, _ := m.keyBoard("X")
	m = mm.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("X did not ask before pruning: mode=%v", m.mode)
	}
	if !strings.Contains(m.confirm.message, "2 merged sessions") {
		t.Errorf("confirm does not name what it will prune: %q", m.confirm.message)
	}
}

// The filters are what the board is showing, and X prunes what is on screen.
func TestPruneMergedFollowsTheProjectFilter(t *testing.T) {
	m := testModel(nil,
		sess("mine", "auth", core.LifecycleMerged, core.AgentIdle, "r"),
		sess("theirs", "billing", core.LifecycleMerged, core.AgentIdle, "r"),
	)
	m.projectFilter = "auth"

	mm, _ := m.keyBoard("X")
	m = mm.(Model)
	if !strings.Contains(m.confirm.message, "1 merged session?") {
		t.Errorf("confirm reached past the filter: %q", m.confirm.message)
	}
}

// With nothing merged there is no change to see, so the refusal has to be said.
func TestPruneMergedWithNothingMergedSaysSo(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentWorking, "r"))

	mm, cmd := m.keyBoard("X")
	if mm.(Model).mode == modeConfirm {
		t.Fatal("X asked about pruning nothing")
	}
	msg, ok := drainNotice(t, cmd)
	if !ok {
		t.Fatalf("X said nothing about having no merged sessions: %+v", msg)
	}
}

// A bulk teardown cannot answer per-session questions: several of them would
// queue up, each hiding the last. The card stays put and x asks there instead.
func TestBulkTeardownFailureKeepsTheCard(t *testing.T) {
	m := testModel(nil, sess("done", "", core.LifecycleMerged, core.AgentIdle, "r"))
	m.sessions[0].Pruning = true

	mm, cmd := m.handleTeardown(teardownMsg{
		id: "done", bulk: true, err: &ops.DirtyError{Path: "/tmp/wt"},
	})
	m = mm.(Model)
	if m.mode == modeConfirm {
		t.Error("a bulk prune failure opened a confirm")
	}
	if len(m.sessions) != 1 {
		t.Errorf("the failed session left the board: %d sessions remain", len(m.sessions))
	}
	if m.sessions[0].Pruning {
		t.Error("a failed prune remained marked for retry")
	}
	msg, ok := drainNotice(t, cmd)
	if !ok || !strings.Contains(msg.text, "done") {
		t.Errorf("the failure did not name the session: %+v", msg)
	}
}

// The single-session path keeps its recovery: x on a dirty worktree still
// offers to discard.
func TestSingleTeardownFailureStillAsks(t *testing.T) {
	m := testModel(nil, sess("done", "", core.LifecycleMerged, core.AgentIdle, "r"))

	mm, _ := m.handleTeardown(teardownMsg{id: "done", err: &ops.DirtyError{Path: "/tmp/wt"}})
	if mm.(Model).mode != modeConfirm {
		t.Error("x on a dirty worktree no longer offers to discard")
	}
}

// An error with no recovery behind it is reported either way.
func TestPlainTeardownFailureIsReported(t *testing.T) {
	m := testModel(nil, sess("done", "", core.LifecycleMerged, core.AgentIdle, "r"))

	_, cmd := m.handleTeardown(teardownMsg{id: "done", err: errors.New("tmux is gone")})
	if msg, ok := drainNotice(t, cmd); !ok {
		t.Errorf("a failed prune said nothing: %+v", msg)
	}
}
