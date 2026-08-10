package ui

// c and C exist for the board that comes back after a machine restart: every
// card describes a worktree whose agent went away with tmux, and the work itself
// is all still there.

import (
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

// deadSess is a session whose terminal is gone -- what every card on the board
// looks like after a reboot.
func deadSess(id string) *core.Session {
	s := sess(id, "", core.LifecycleIdle, core.AgentIdle, "r")
	s.TmuxSession = "dma-" + id
	s.TmuxAlive = false
	return s
}

// Nothing is at stake in restarting a session nothing is running, and a confirm
// on the way to it would be a keystroke asking whether you meant the keystroke.
func TestRestartAsksNothingForAStoppedSession(t *testing.T) {
	m := testModel(nil, deadSess("a"))

	next, cmd := m.sessionAction("c")
	if cmd == nil {
		t.Fatal("c on a stopped session did nothing")
	}
	if got := next.(Model); got.mode == modeConfirm {
		t.Error("c asked before restarting a session that was not running")
	}
}

// A live agent is a different question: restarting it throws away whatever it is
// in the middle of, and nothing here can tell a wedged agent from a working one.
func TestRestartConfirmsBeforeStoppingALiveAgent(t *testing.T) {
	m := testModel(nil, liveSess("a"))

	next, _ := m.sessionAction("c")
	got := next.(Model)
	if got.mode != modeConfirm {
		t.Fatal("c restarted a running agent without asking")
	}
	if !strings.Contains(got.confirm.message, "a") {
		t.Errorf("confirm = %q, want it to name the session", got.confirm.message)
	}
}

// C is the post-reboot keystroke, so what it takes is every card with no process
// behind it -- and only those. A running agent must not be interrupted by a key
// pressed to revive the ones that are not.
func TestRestartAllTakesOnlyTheStoppedSessions(t *testing.T) {
	live := liveSess("running")
	m := testModel(nil, deadSess("stopped"), live, deadSess("also-stopped"))

	got := idsOf(m.stoppedSessions())
	want := []string{"stopped", "also-stopped"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("C would restart %v, want %v", got, want)
	}
}

// Merged work has landed. An agent started in it would be an agent with nothing
// to do, and a board kept as a record of merged work would answer one keystroke
// with a dozen of them.
func TestRestartAllLeavesMergedSessionsAlone(t *testing.T) {
	merged := deadSess("merged")
	merged.Lifecycle = core.LifecycleMerged
	m := testModel(nil, deadSess("open"), merged)

	if got := idsOf(m.stoppedSessions()); len(got) != 1 || got[0] != "open" {
		t.Errorf("C would restart %v, want only the unmerged session", got)
	}
}

// c is still the way to bring one back: a merged PR with review feedback on it is
// a real reason to want that agent again.
func TestRestartStillWorksOnAMergedSession(t *testing.T) {
	merged := deadSess("merged")
	merged.Lifecycle = core.LifecycleMerged
	m := testModel(nil, merged)

	if _, cmd := m.sessionAction("c"); cmd == nil {
		t.Error("c on a merged session did nothing")
	}
}

// C follows the filters, the rule X follows: what it restarts is the board as it
// is on screen, not the sessions a filter is hiding.
func TestRestartAllFollowsTheProjectFilter(t *testing.T) {
	shown := deadSess("shown")
	shown.Group = "auth"
	hidden := deadSess("hidden")
	hidden.Group = "billing"

	m := testModel(nil, shown, hidden)
	m.projectFilter = "auth"

	if got := idsOf(m.stoppedSessions()); len(got) != 1 || got[0] != "shown" {
		t.Errorf("C would restart %v, want only the project on screen", got)
	}
}

// Several sequenced restarts are seconds of work whose only other sign is cards
// changing badge one at a time, so the count is posted when the key is pressed.
func TestRestartAllSaysHowManyAreComingBack(t *testing.T) {
	m := testModel(nil, deadSess("a"), deadSess("b"), liveSess("c"))

	next, cmd := m.restartDead()
	if cmd == nil {
		t.Fatal("C produced no restarts")
	}
	if got := next.(Model).notice; !strings.Contains(got, "restarting 2 sessions") {
		t.Errorf("notice = %q, want the number being restarted", got)
	}
}

// A board with nothing stopped says so rather than looking like it did something.
func TestRestartAllOnARunningBoardSaysThereIsNothingToDo(t *testing.T) {
	m := testModel(nil, liveSess("a"))

	_, cmd := m.restartDead()
	notice, ok := drainNotice(t, cmd)
	if !ok {
		t.Fatal("C on a fully running board said nothing")
	}
	if !strings.Contains(notice.text, "already running") {
		t.Errorf("notice = %q, want it to say there is nothing stopped", notice.text)
	}
}

// The card has to stop saying "not running" the moment it is, and the state it
// lands in is idle: nothing was asked of the agent, so claiming it is working
// would put the card in the active column on the strength of dma having typed a
// command.
func TestRestartResultMarksTheSessionRunningAndIdle(t *testing.T) {
	s := deadSess("a")
	m := testModel(nil, s)
	m.hookSeen["a"] = true

	next, _ := m.handleRestart(restartMsg{id: "a", tmuxSession: "dma-a", resumed: true})
	got := next.(Model)

	if !s.TmuxAlive {
		t.Error("session is still marked as not running")
	}
	if s.AgentState != core.AgentIdle {
		t.Errorf("agent state = %q, want idle", s.AgentState)
	}
	if s.Lifecycle != core.LifecycleIdle {
		t.Errorf("lifecycle = %q, want idle", s.Lifecycle)
	}
	// The agent is a new process that has reported nothing yet, so the mark saying
	// this board has heard from it has to go with the old one; see stranded.
	if got.hookSeen["a"] {
		t.Error("the restarted session is still marked as having reported to this board")
	}
	// The restart resized the terminal and the agent is about to repaint all of it.
	// None of that is work it chose to do, and the prober must not read it as such.
	if got.touchedAt["a"].IsZero() {
		t.Error("the restart was not recorded against the session")
	}
}

// A session whose name could not be reclaimed is running under a new one. Keeping
// the old name would point every capture, keystroke and kill at a terminal that
// does not exist.
func TestRestartResultAdoptsAReplacedTerminalName(t *testing.T) {
	s := deadSess("a")
	m := testModel(nil, s)

	m.handleRestart(restartMsg{id: "a", tmuxSession: "dma-a-2", resumed: true})
	if s.TmuxSession != "dma-a-2" {
		t.Errorf("tmux session = %q, want the name the restart ended up with", s.TmuxSession)
	}
}

// An agent that came back knowing nothing about the task looks exactly like one
// that resumed, so it is the one thing about a restart worth saying out loud.
func TestRestartSaysWhenTheAgentCameBackWithoutItsHistory(t *testing.T) {
	s := deadSess("a")
	s.AgentProfile = "homegrown"
	m := testModel(nil, s)

	_, cmd := m.handleRestart(restartMsg{id: "a", tmuxSession: "dma-a", resumed: false})
	notice, ok := drainNotice(t, cmd)
	if !ok {
		t.Fatal("a restart with no history said nothing")
	}
	if !strings.Contains(notice.text, "without its history") {
		t.Errorf("notice = %q, want it to say the agent lost its history", notice.text)
	}
	if !strings.Contains(notice.text, "homegrown") {
		t.Errorf("notice = %q, want it to name the profile that needs a resume_command", notice.text)
	}
}

// A restart that resumed says nothing: the agent is back in the panel and the
// card has changed badge, which says it better than a line of prose.
func TestRestartThatResumedPostsNothing(t *testing.T) {
	m := testModel(nil, deadSess("a"))

	_, cmd := m.handleRestart(restartMsg{id: "a", tmuxSession: "dma-a", resumed: true})
	if notice, ok := drainNotice(t, cmd); ok {
		t.Errorf("a restart that worked posted %q", notice.text)
	}
}

// One failure out of many has to name its session: a bulk restart has several
// more behind it, and "worktree is gone" says nothing about which card to look at.
func TestBulkRestartFailureNamesTheSession(t *testing.T) {
	s := deadSess("a")
	s.Title = "rate limiter"
	m := testModel(nil, s)

	_, cmd := m.handleRestart(restartMsg{
		id:   "a",
		bulk: true,
		err:  &ops.WorktreeMissingError{Path: "/gone"},
	})
	notice, ok := drainNotice(t, cmd)
	if !ok {
		t.Fatal("a failed restart said nothing")
	}
	if !strings.Contains(notice.text, "rate limiter") {
		t.Errorf("notice = %q, want it to name the session", notice.text)
	}
}

// A failed restart leaves the card as it was. Reporting it as running would hand
// the user a session whose keys all go nowhere.
func TestFailedRestartLeavesTheSessionStopped(t *testing.T) {
	s := deadSess("a")
	m := testModel(nil, s)

	m.handleRestart(restartMsg{id: "a", err: &ops.WorktreeMissingError{Path: "/gone"}})
	if s.TmuxAlive {
		t.Error("a failed restart marked the session as running")
	}
}

// The launch after a reboot is the one time this is the first thing about the
// board worth knowing, and the cards themselves look much as they did before.
func TestBoardOffersToRestartStoppedSessions(t *testing.T) {
	m := testModel(nil, deadSess("a"), deadSess("b"), liveSess("c"))

	m.hintRestart()
	if !strings.Contains(m.notice, "2 sessions are not running") {
		t.Errorf("notice = %q, want the number of stopped sessions", m.notice)
	}
	if !strings.Contains(m.notice, "C") {
		t.Errorf("notice = %q, want it to name the key that answers it", m.notice)
	}

	// Once per launch. Every poll observes the same sessions, and a line that
	// returned every forty-five seconds would be nagging about something the board
	// already shows on each card.
	m.notice = ""
	m.hintRestart()
	if m.notice != "" {
		t.Errorf("the hint came back as %q", m.notice)
	}
}

// One stopped session is offered the single-session key, since C would be a bulk
// action over a batch of one and the notice reads as though there were more.
func TestBoardOffersTheSingleKeyForOneStoppedSession(t *testing.T) {
	m := testModel(nil, deadSess("a"), liveSess("b"))

	m.hintRestart()
	if !strings.Contains(m.notice, "1 session is not running — press c to restart it") {
		t.Errorf("notice = %q, want the single-session wording", m.notice)
	}
}

// A board where everything is running has nothing to offer.
func TestBoardWithNothingStoppedOffersNothing(t *testing.T) {
	m := testModel(nil, liveSess("a"))

	m.hintRestart()
	if m.notice != "" {
		t.Errorf("notice = %q, want none", m.notice)
	}
}

// The notice line is one row, and a repo that was just registered is news the
// user has not read yet. The hint waits for the next poll rather than overwriting
// it -- and keeps waiting, so it is not spent on a line nobody saw.
func TestRestartHintYieldsToANoticeAlreadyUp(t *testing.T) {
	m := testModel(nil, deadSess("a"))
	m.notice, m.noticeAt = "registered dma-cli", time.Now()

	m.hintRestart()
	if m.notice != "registered dma-cli" {
		t.Fatalf("notice = %q, want the launch notice kept", m.notice)
	}

	m.notice = ""
	m.hintRestart()
	if !strings.Contains(m.notice, "not running") {
		t.Errorf("notice = %q, want the hint once the line was free", m.notice)
	}
}
