package ui

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// dma creates no branch, so the only way the board ever learns one is by
// reading it back out of the worktree. Until then a card has to say so rather
// than render an empty line.
func TestObserveAdoptsTheBranchTheAgentCreated(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	m := testModel(nil, s)

	if got := m.branchOrPR(s); got != "no branch" {
		t.Errorf("a detached session shows %q, want \"no branch\"", got)
	}

	next, _ := m.Update(observeMsg{obs: []ops.Observation{
		{ID: "a", Alive: true, Branch: "agent-picked-this"},
	}})
	m = next.(Model)

	if s.Branch != "agent-picked-this" {
		t.Fatalf("branch = %q, want agent-picked-this", s.Branch)
	}
	if got := m.branchOrPR(s); got != "agent-picked-this" {
		t.Errorf("card shows %q after adoption", got)
	}
}

// An observation carries no branch when the worktree is unreadable as well as
// when it is detached. Clearing a known branch on that would stop the session's
// PR being polled, with nothing on screen to explain why.
func TestObserveKeepsAKnownBranchWhenNoneIsReported(t *testing.T) {
	s := sess("a", "", core.LifecyclePROpen, core.AgentIdle, "r")
	s.Branch = "already-named"
	m := testModel(nil, s)

	next, _ := m.Update(observeMsg{obs: []ops.Observation{{ID: "a", Alive: false}}})
	m = next.(Model)

	if s.Branch != "already-named" {
		t.Errorf("branch = %q, want it kept as already-named", s.Branch)
	}
}
