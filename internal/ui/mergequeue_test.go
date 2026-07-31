package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
)

var errFailed = errors.New("gh said no")

func queuedSess(id string) *core.Session {
	s := branchSess(id, "r1", "feat-"+id, core.LifecyclePROpen)
	s.PRNumber, s.PRState = 7, core.PROpen
	return s
}

// Joining a merge queue is not merging: the queue may still drop the PR, and a
// card in the merged column is one nobody looks at again.
func TestMergeQueuedKeepsTheCardOpen(t *testing.T) {
	s := queuedSess("a")
	m := testModel(nil, s)

	m.handleMerged(mergedMsg{id: s.ID, outcome: ghx.MergeQueued})

	if !s.PRQueued {
		t.Error("a queued PR was not marked as queued")
	}
	if s.Lifecycle != core.LifecyclePROpen {
		t.Errorf("card moved to %q on a queue join, want it left in the PR column", s.Lifecycle)
	}
	if s.PRState != core.PROpen {
		t.Errorf("PR state = %q, want it still open", s.PRState)
	}
}

// Pressing m on a PR the queue already holds must not look like nothing
// happened -- gh treats it as success and exits zero. The card is what says so:
// it stays in the PR column and picks up the queue label.
func TestMergeAlreadyQueuedShowsOnTheCard(t *testing.T) {
	s := queuedSess("a")
	s.PRCI = core.CIPass
	m := testModel(nil, s)

	_, cmd := m.handleMerged(mergedMsg{id: s.ID, outcome: ghx.MergeAlreadyQueued})

	if _, ok := drainNotice(t, cmd); ok {
		t.Error("an already-queued PR posted a notice as well as labelling the card")
	}
	if !s.PRQueued {
		t.Error("an already-queued PR was not marked as queued")
	}
	if s.Lifecycle != core.LifecyclePROpen {
		t.Errorf("card moved to %q, want it left in the PR column", s.Lifecycle)
	}
	if got := m.branchOrPR(s); !strings.Contains(got, "queued") {
		t.Errorf("card label = %q, want the queue named", got)
	}
}

// The direct path is unchanged: a merge that landed moves the card.
func TestMergeCompletedStillMovesTheCard(t *testing.T) {
	s := queuedSess("a")
	s.PRQueued = true
	m := testModel(nil, s)

	m.handleMerged(mergedMsg{id: s.ID, outcome: ghx.MergeCompleted})

	if s.Lifecycle != core.LifecycleMerged || s.PRState != core.PRMerged {
		t.Errorf("lifecycle=%q state=%q, want merged", s.Lifecycle, s.PRState)
	}
	if s.PRQueued {
		t.Error("a merged PR is still flagged as queued")
	}
}

func TestMergeFailureLeavesTheCardAlone(t *testing.T) {
	s := queuedSess("a")
	m := testModel(nil, s)

	m.handleMerged(mergedMsg{id: s.ID, err: errFailed})

	if s.Lifecycle != core.LifecyclePROpen || s.PRQueued {
		t.Errorf("a failed merge changed the card: lifecycle=%q queued=%v", s.Lifecycle, s.PRQueued)
	}
}

// A queued PR is still an open PR, so the poll alone cannot tell that the queue
// let go of it. The card claiming to be queued is what earns the extra query.
func TestPRSyncRechecksQueuedCards(t *testing.T) {
	queued := queuedSess("a")
	queued.PRQueued = true
	plain := queuedSess("b")
	m := testModel(nil, queued, plain)

	poll := ghx.Poll{
		Open: map[string]ghx.PR{
			"feat-a": {Number: 7, Branch: "feat-a", State: core.PROpen},
			"feat-b": {Number: 8, Branch: "feat-b", State: core.PROpen},
		},
		Answered: map[string]bool{"feat-a": true, "feat-b": true},
	}
	_, cmd := m.handlePRSync(prSyncMsg{repoID: "r1", poll: poll})
	if cmd == nil {
		t.Fatal("a queued card was not re-checked")
	}
}

func TestPRSyncSkipsTheQueueQueryWhenNothingIsQueued(t *testing.T) {
	s := queuedSess("a")
	m := testModel(nil, s)

	_, cmd := m.handlePRSync(prSyncMsg{repoID: "r1", poll: ghx.Poll{
		Open:     map[string]ghx.PR{"feat-a": {Number: 7, Branch: "feat-a", State: core.PROpen}},
		Answered: map[string]bool{"feat-a": true},
	}})
	if cmd != nil {
		t.Error("a card that is not queued triggered a queue query")
	}
}

// The queue dropping a PR back out has to reach the card, or it sits there
// claiming to be queued forever.
func TestQueueRecheckClearsAnEjectedPR(t *testing.T) {
	s := queuedSess("a")
	s.PRQueued = true
	m := testModel(nil, s)

	m.handlePRQueue(prQueueMsg{sessionID: s.ID, inQueue: false})
	if s.PRQueued {
		t.Error("a PR the queue no longer holds is still shown as queued")
	}
}

// A failed re-check says nothing about the queue, so it must not clear the flag.
func TestQueueRecheckIgnoresFailure(t *testing.T) {
	s := queuedSess("a")
	s.PRQueued = true
	m := testModel(nil, s)

	m.handlePRQueue(prQueueMsg{sessionID: s.ID, inQueue: false, err: errFailed})
	if !s.PRQueued {
		t.Error("a failed re-check cleared the queue flag")
	}
}

func TestQueuedCardSaysSo(t *testing.T) {
	s := queuedSess("a")
	s.PRQueued, s.PRCI = true, core.CIPass
	m := testModel(nil, s)

	if got := m.branchOrPR(s); !strings.Contains(got, "queued") {
		t.Errorf("card label = %q, want the queue named", got)
	}
}
