package ui

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
)

type raised struct {
	title, body string
}

// captureNotifications swaps the desktop notifier for a recorder, so a test run
// does not spray notifications at whoever is running it.
func captureNotifications(t *testing.T) *[]raised {
	t.Helper()
	var got []raised
	prev := notifyFn
	notifyFn = func(title, body string) { got = append(got, raised{title, body}) }
	t.Cleanup(func() { notifyFn = prev })
	return &got
}

// greenPoll is what GitHub reports for a PR with nothing left blocking it.
func greenPoll(branch string, number int) ghx.Poll {
	return ghx.Poll{
		Open: map[string]ghx.PR{branch: {
			Number: number, Branch: branch, URL: "https://example.test/pull/1",
			State: core.PROpen, CI: core.CIPass, Review: core.ReviewApproved,
			Mergeable: core.MergeClean,
		}},
		Answered: map[string]bool{branch: true},
	}
}

func TestPRSyncNotifiesWhenAPRBecomesMergeable(t *testing.T) {
	got := captureNotifications(t)
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.Title = "auth cleanup"
	s.PRNumber, s.PRState, s.PRCI = 7, core.PROpen, core.CIPending
	m := testModel(nil, s)

	m.handlePRSync(prSyncMsg{repoID: "r1", poll: greenPoll("feat-a", 7)})

	if len(*got) != 1 {
		t.Fatalf("raised %v, want one notification", *got)
	}
	if (*got)[0].title != "auth cleanup" {
		t.Errorf("notification title = %q, want the card's name", (*got)[0].title)
	}
	if (*got)[0].body != "#7 ready to merge" {
		t.Errorf("notification body = %q", (*got)[0].body)
	}
}

// The board polls every few seconds and a PR stays ready until it is merged, so
// a per-poll notification would be a stream of them.
func TestPRSyncNotifiesOnlyOnceWhileAPRStaysMergeable(t *testing.T) {
	got := captureNotifications(t)
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.PRNumber, s.PRState, s.PRCI = 7, core.PROpen, core.CIPending
	m := testModel(nil, s)

	for i := 0; i < 4; i++ {
		m.handlePRSync(prSyncMsg{repoID: "r1", poll: greenPoll("feat-a", 7)})
	}
	if len(*got) != 1 {
		t.Fatalf("raised %d notifications across four polls, want 1", len(*got))
	}
}

// Nothing is announced for a PR that is still blocked, which is the whole point:
// a notification that fires on every PR says nothing.
func TestPRSyncStaysQuietForABlockedPR(t *testing.T) {
	cases := map[string]func(*ghx.PR){
		"conflicts":         func(pr *ghx.PR) { pr.Mergeable = core.MergeConflicts },
		"failing ci":        func(pr *ghx.PR) { pr.CI = core.CIFail },
		"unfinished ci":     func(pr *ghx.PR) { pr.CI = core.CIPending },
		"changes requested": func(pr *ghx.PR) { pr.Review = core.ReviewChangesRequested },
		"draft":             func(pr *ghx.PR) { pr.State = core.PRDraft },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			got := captureNotifications(t)
			s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
			m := testModel(nil, s)

			poll := greenPoll("feat-a", 7)
			pr := poll.Open["feat-a"]
			spoil(&pr)
			poll.Open["feat-a"] = pr
			m.handlePRSync(prSyncMsg{repoID: "r1", poll: poll})

			if len(*got) != 0 {
				t.Errorf("a PR with %s raised %v", name, *got)
			}
		})
	}
}

// A queued PR merges without the user. Announcing it would be a notification
// about something that needs nothing.
func TestPRSyncStaysQuietForAQueuedPR(t *testing.T) {
	got := captureNotifications(t)
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.PRNumber, s.PRState, s.PRQueued = 7, core.PROpen, true
	m := testModel(nil, s)

	m.handlePRSync(prSyncMsg{repoID: "r1", poll: greenPoll("feat-a", 7)})

	if len(*got) != 0 {
		t.Errorf("a queued PR raised %v", *got)
	}
}

// Auto-merge will act when CI becomes green, so the user does not also need a
// notification asking them to merge it manually.
func TestPRSyncStaysQuietWhenAutoMergeIsEnabled(t *testing.T) {
	got := captureNotifications(t)
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.PRNumber, s.PRState, s.PRAutoMerge = 7, core.PROpen, true
	m := testModel(nil, s)
	poll := greenPoll("feat-a", 7)
	pr := poll.Open["feat-a"]
	pr.AutoMerge = true
	poll.Open["feat-a"] = pr

	m.handlePRSync(prSyncMsg{repoID: "r1", poll: poll})

	if len(*got) != 0 {
		t.Errorf("an auto-merging PR raised %v", *got)
	}
}

// A PR the queue dropped back out is the user's problem again, and that happens
// without any polled field changing -- so the queue re-check has to notify too.
func TestQueueDropoutNotifies(t *testing.T) {
	got := captureNotifications(t)
	s := branchSess("a", "r1", "feat-a", core.LifecyclePROpen)
	s.PRNumber, s.PRState, s.PRQueued = 7, core.PROpen, true
	s.PRCI, s.PRReview, s.PRMergeable = core.CIPass, core.ReviewApproved, core.MergeClean
	m := testModel(nil, s)

	m.handlePRQueue(prQueueMsg{sessionID: "a", inQueue: false})

	if len(*got) != 1 {
		t.Fatalf("raised %v, want one notification for the dropped PR", *got)
	}
}
