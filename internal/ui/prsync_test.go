package ui

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
)

func branchSess(id, repo, branch string, lc core.Lifecycle) *core.Session {
	s := sess(id, "", lc, core.AgentIdle, repo)
	s.Branch = branch
	return s
}

// Polling asks only about branches on the board. Listing a repo's whole open-PR
// page is what made GitHub time out on a busy monorepo, and the extra PRs were
// never of any use to the board.
func TestTrackedBranchesCoversOnlyLiveSessions(t *testing.T) {
	got := trackedBranches([]*core.Session{
		branchSess("a", "r1", "feat-a", core.LifecycleActive),
		branchSess("b", "r1", "feat-b", core.LifecyclePROpen),
		branchSess("c", "r1", "feat-c", core.LifecycleMerged),
		branchSess("d", "r2", "feat-d", core.LifecycleIdle),
		branchSess("e", "r2", "", core.LifecycleIdle),
	})

	if want := []string{"feat-a", "feat-b"}; !sameSet(got["r1"], want) {
		t.Errorf("r1 branches = %v, want %v", got["r1"], want)
	}
	if want := []string{"feat-d"}; !sameSet(got["r2"], want) {
		t.Errorf("r2 branches = %v, want %v", got["r2"], want)
	}
}

// One query answers for every session on that branch, so the branch must not be
// queried once per session.
func TestTrackedBranchesDeduplicates(t *testing.T) {
	got := trackedBranches([]*core.Session{
		branchSess("a", "r1", "shared", core.LifecycleActive),
		branchSess("b", "r1", "shared", core.LifecycleActive),
	})
	if len(got["r1"]) != 1 {
		t.Errorf("r1 branches = %v, want one entry", got["r1"])
	}
}

// A branch with no open PR is a real answer: the session's PR has reached a
// terminal state and must be resolved directly.
func TestPRSyncFollowsUpOnAnAnsweredBranchWithNoOpenPR(t *testing.T) {
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.PRNumber, s.PRState = 7, core.PROpen
	m := testModel(nil, s)

	_, cmd := m.handlePRSync(prSyncMsg{
		repoID: "r1",
		poll:   ghx.Poll{Open: map[string]ghx.PR{}, Answered: map[string]bool{"feat-a": true}},
	})
	if cmd == nil {
		t.Fatal("a tracked PR that left the open set was not followed up on")
	}
	if s.PRSyncedAt.IsZero() {
		t.Error("an answered branch was not marked as synced")
	}
}

// A branch whose query failed is absent from Answered. Reading that silence as
// "the PR closed" would mislabel the card and fire a pointless follow-up query
// against a remote that just failed.
func TestPRSyncLeavesUnansweredBranchesAlone(t *testing.T) {
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.PRNumber, s.PRState, s.PRCI = 7, core.PROpen, core.CIPass
	m := testModel(nil, s)

	_, cmd := m.handlePRSync(prSyncMsg{
		repoID: "r1",
		poll:   ghx.Poll{Open: map[string]ghx.PR{}, Answered: map[string]bool{}},
	})
	if cmd != nil {
		t.Error("an unanswered branch triggered a follow-up query")
	}
	if s.PRNumber != 7 || s.PRState != core.PROpen || s.PRCI != core.CIPass {
		t.Errorf("an unanswered branch changed the card: %+v", s)
	}
	if !s.PRSyncedAt.IsZero() {
		t.Error("an unanswered branch was marked as synced")
	}
}

// The poll is per repo and keyed by branch, and applying it must respect that:
// two repos on the same branch name are different sessions.
func TestPRSyncDoesNotCrossAssignBetweenRepos(t *testing.T) {
	mine := branchSess("a", "r1", "shared", core.LifecycleActive)
	theirs := branchSess("b", "r2", "shared", core.LifecycleActive)
	m := testModel(nil, mine, theirs)

	m.handlePRSync(prSyncMsg{
		repoID: "r1",
		poll: ghx.Poll{
			Open:     map[string]ghx.PR{"shared": {Number: 42, Branch: "shared", State: core.PROpen}},
			Answered: map[string]bool{"shared": true},
		},
	})
	if mine.PRNumber != 42 {
		t.Errorf("r1 session PR = %d, want 42", mine.PRNumber)
	}
	if theirs.PRNumber != 0 {
		t.Errorf("r2 session picked up r1's PR: %d", theirs.PRNumber)
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}
