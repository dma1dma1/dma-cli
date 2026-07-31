package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// applyTitle drives one summary through the board the way the runtime does.
func applyTitle(t *testing.T, m Model, msg titledMsg) Model {
	t.Helper()
	next, _ := m.handleTitled(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleTitled returned %T, want Model", next)
	}
	return updated
}

// A pasted task is titled from its first line, so the record no longer holds the
// rest of it. The summary is written from the whole thing anyway: what the work
// is is as often in the paste below the first line as in the line itself.
func TestTheWholeTaskIsWhatGetsSummarized(t *testing.T) {
	task := "have a look at this please\n\ncheckout POSTs twice when stripe retries the webhook,\nso the customer is charged twice"
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.Title = "have a look at this please"

	if got := summaryInput(createdMsg{task: task}, s); got != task {
		t.Errorf("summarizing %q, want the whole task", got)
	}
	// Anything that did not carry the task still has the title to work from.
	if got := summaryInput(createdMsg{}, s); got != s.Title {
		t.Errorf("summarizing %q, want the title as a fallback", got)
	}
}

// The card is created carrying the whole task and renamed when the summary
// lands. Everything else about the session -- which column it is in, which
// worktree it owns -- is untouched by the rename.
func TestASummaryRenamesTheCard(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.Title = "the login test flakes on CI about 1 in 5 runs, look at why and fix it"
	s.WorktreePath = "/worktrees/the-login-test-flakes-on-ci-about-1"
	m := testModel(nil, s)

	m = applyTitle(t, m, titledMsg{id: "a", title: "flaky CI login test"})

	if s.Title != "flaky CI login test" {
		t.Errorf("title = %q, want the summary", s.Title)
	}
	if s.Lifecycle != core.LifecycleActive || s.AgentState != core.AgentWorking {
		t.Errorf("rename moved the card: %s/%s", s.Lifecycle, s.AgentState)
	}
	// The worktree exists on disk under the name it was created with, and the
	// agent is running in it. A rename on the board cannot move it.
	if s.WorktreePath != "/worktrees/the-login-test-flakes-on-ci-about-1" {
		t.Errorf("worktree path changed to %q", s.WorktreePath)
	}
}

// No model reachable means no summary, and the card keeps the text that got it
// onto the board. A blank card would be strictly worse than a long one.
func TestAnEmptySummaryLeavesTheCardAlone(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.Title = "rewrite the session cookie handling"
	m := testModel(nil, s)

	for _, title := range []string{"", "   ", "\n"} {
		m = applyTitle(t, m, titledMsg{id: "a", title: title})
		if s.Title != "rewrite the session cookie handling" {
			t.Fatalf("summary %q blanked the card: %q", title, s.Title)
		}
	}
}

// A summary takes seconds and pruning takes one key, so a title can outlive the
// session it was written for.
func TestASummaryForAPrunedSessionIsDropped(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"))

	m = applyTitle(t, m, titledMsg{id: "gone", title: "flaky CI login test"})

	for _, s := range m.sessions {
		if s.Title == "flaky CI login test" {
			t.Fatalf("a stale summary landed on session %q", s.ID)
		}
	}
}

// The rename is what the board shows, so it has to reach the card body rather
// than just the record behind it.
func TestTheRenamedCardIsWhatTheBoardDraws(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.Title = "the login test flakes on CI about 1 in 5 runs, look at why and fix it"
	m := testModel(nil, s)

	m = applyTitle(t, m, titledMsg{id: "a", title: "flaky CI login test"})

	view := m.render()
	if !strings.Contains(view, "flaky CI login test") {
		t.Errorf("the board is not showing the summary:\n%s", view)
	}
	if strings.Contains(view, "the login test flakes") {
		t.Errorf("the board is still showing the raw task:\n%s", view)
	}
}
