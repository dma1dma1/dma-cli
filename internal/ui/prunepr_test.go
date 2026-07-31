package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

func openPRSess(id string, state core.PRState, number int) *core.Session {
	s := sess(id, "", core.LifecyclePROpen, core.AgentIdle, "r")
	s.PRNumber, s.PRState = number, state
	return s
}

// Closing the pull request is the half of a prune that other people see, so it
// cannot be the half nobody was told about.
func TestPruneNamesThePRItWillClose(t *testing.T) {
	for _, state := range []core.PRState{core.PROpen, core.PRDraft} {
		m := testModel(nil, openPRSess("shipping", state, 7))

		mm, _ := m.sessionAction("x")
		m = mm.(Model)
		if m.mode != modeConfirm {
			t.Fatalf("%s PR: x did not ask before pruning: mode=%v", state, m.mode)
		}
		if !strings.Contains(m.confirm.message, "PR #7") {
			t.Errorf("%s PR: confirm does not say the PR closes: %q", state, m.confirm.message)
		}
	}
}

// The common prune has no pull request to close, and a prompt that mentioned one
// anyway would make the user go look.
func TestPruneWithNothingOpenAsksAboutTheWorktreeOnly(t *testing.T) {
	merged := sess("done", "", core.LifecycleMerged, core.AgentIdle, "r")
	merged.PRNumber, merged.PRState = 7, core.PRMerged

	m := testModel(nil, merged)
	mm, _ := m.sessionAction("x")
	m = mm.(Model)
	if strings.Contains(m.confirm.message, "PR #") {
		t.Errorf("confirm offers to close a merged PR: %q", m.confirm.message)
	}
}

// A close that could not happen leaves everything intact, so the failure is a
// question rather than a notice: leaving the PR open is a legitimate answer, and
// declining keeps the session until GitHub can be reached.
func TestPRCloseFailureOffersToPruneAnyway(t *testing.T) {
	m := testModel(nil, openPRSess("shipping", core.PROpen, 7))

	mm, _ := m.handleTeardown(teardownMsg{
		id:  "shipping",
		err: &ops.PRCloseError{Number: 7, Err: errors.New("offline")},
	})
	m = mm.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("a failed PR close did not ask what to do: mode=%v", m.mode)
	}
	if !strings.Contains(m.confirm.message, "#7") || !strings.Contains(m.confirm.message, "offline") {
		t.Errorf("confirm does not say what failed: %q", m.confirm.message)
	}
	if len(m.sessions) != 1 {
		t.Errorf("the session left the board with its PR still open: %d remain", len(m.sessions))
	}
}
