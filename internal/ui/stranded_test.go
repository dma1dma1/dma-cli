package ui

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/hooks"
)

// hookSess is a session on the hook-reporting profile, which is the one the
// board otherwise never probes.
func hookSess(id string, st core.AgentState) *core.Session {
	s := sess(id, "", core.LifecycleActive, st, "r")
	s.AgentProfile = "claude"
	s.TmuxSession = "tmux-" + id
	s.WorktreePath = "/tmp/wt/" + id
	return s
}

// probes reports whether this board would read the pane of any session it holds.
// A command is only produced when something is worth capturing, so its absence
// is the skip.
func probes(m Model) bool {
	return probeCmd(m.prober, m.cfg, m.sessions, m.touchedAt, m.hookSeen) != nil
}

// A working badge inherited from disk is the one claim a hook-backed session can
// make that no later hook will ever revisit: the turn it describes ended while
// the board was down -- restarting takes seconds, and a refused Stop is simply
// lost -- so the agent has fallen silent with the card still saying it is busy.
// Reading the pane is the only thing left that can settle it.
func TestAnInheritedWorkingBadgeIsProbed(t *testing.T) {
	m := testModel(nil, hookSess("a", core.AgentWorking))
	if !probes(m) {
		t.Error("a working card no hook has confirmed was left alone; " +
			"nothing else will ever correct it")
	}
}

// Every other state either describes an agent that is running -- and therefore
// about to hook again -- or one blocked on a question, which is what the badge
// says. Probing those would put a heuristic up against an exact channel for no
// gain, which is the arrangement this whole design avoids.
func TestOtherInheritedStatesAreLeftToTheirHooks(t *testing.T) {
	for _, st := range []core.AgentState{core.AgentIdle, core.AgentDone, core.AgentNeedsYou} {
		m := testModel(nil, hookSess("a", st))
		if probes(m) {
			t.Errorf("probed a hook-backed session in %q, which reports for itself", st)
		}
	}
}

// The pane is a stopgap for a state nothing confirmed, so the first hook to
// arrive ends it. Otherwise a session that legitimately works for a long stretch
// between tool calls would be read off its terminal for the whole run.
func TestOneHookHandsTheSessionBack(t *testing.T) {
	m := testModel(nil, hookSess("a", core.AgentWorking))
	if !probes(m) {
		t.Fatal("precondition: an unconfirmed working card should be probed")
	}

	m.hookSeen["a"] = true
	if probes(m) {
		t.Error("kept probing a session that has reported to this board")
	}
}

// The mark is set by the traffic itself rather than by anything the board
// arranges, so it has to survive the ordinary hook path.
func TestAHookMarksItsSessionConfirmed(t *testing.T) {
	s := hookSess("a", core.AgentWorking)
	m := testModel(nil, s)

	next, _ := m.handleHook(hooks.Event{
		EventName: "PreToolUse",
		ToolName:  "Bash",
		Cwd:       s.WorktreePath,
	})
	if !next.(Model).hookSeen["a"] {
		t.Error("a correlated hook did not mark its session confirmed; " +
			"the board would go on second-guessing an exact report")
	}
}

// Sessions on an agent that cannot report are probed as they always were,
// whatever state they are in.
func TestHooklessSessionsAreUnaffected(t *testing.T) {
	for _, st := range []core.AgentState{core.AgentIdle, core.AgentDone, core.AgentWorking} {
		s := sess("a", "", core.LifecycleActive, st, "r")
		s.AgentProfile = "codex"
		s.TmuxSession = "tmux-a"
		m := testModel(nil, s)
		if !probes(m) {
			t.Errorf("stopped probing a hookless session in %q", st)
		}
	}
}

// A capture can take longer than the scheduler tick when tmux stalls (for
// example across sleep/wake). The next tick or a manual refresh must reuse the
// in-flight cycle rather than race it through Prober's shared sample history.
func TestProbeCyclesDoNotOverlap(t *testing.T) {
	var sessions []*core.Session
	for _, id := range []string{"a", "b", "c", "d"} {
		s := sess(id, "", core.LifecycleActive, core.AgentWorking, "r")
		s.AgentProfile = "codex"
		s.TmuxSession = "tmux-" + id
		sessions = append(sessions, s)
	}
	m := testModel(nil, sessions...)

	if cmd := m.startProbe(m.sessions); cmd == nil {
		t.Fatal("first probe cycle was not started")
	}
	if !m.probeInFlight {
		t.Fatal("started probe cycle was not marked in flight")
	}
	if cmd := m.startProbe(m.sessions); cmd != nil {
		t.Error("a second probe cycle was started before the first completed")
	}

	next, _ := m.handleProbe(probeMsg{})
	m = next.(Model)
	if m.probeInFlight {
		t.Fatal("completed probe cycle remained marked in flight")
	}
	if cmd := m.startProbe(m.sessions); cmd == nil {
		t.Error("probe cycle did not become available after completion")
	}
}

// Four shards preserve the old per-session cadence while spreading a large
// board's tmux work across one-second ticks.
func TestProbeSessionsAreSharded(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.AgentProfile = "codex"
	s.TmuxSession = "tmux-a"
	m := testModel(nil, s)

	if cmd := m.startProbe(m.sessions); cmd == nil {
		t.Fatal("the session was not included in its first shard")
	}
	m.probeInFlight = false
	m.cancelProbe()
	for i := 0; i < probeShards-1; i++ {
		if cmd := m.startProbe(m.sessions); cmd != nil {
			t.Fatalf("session was redundantly probed in shard %d", i+1)
		}
	}
	if cmd := m.startProbe(m.sessions); cmd == nil {
		t.Error("session did not return after one complete shard cycle")
	}
}

// The mark is dropped with the session, so the map does not grow for the life of
// the process.
func TestConfirmationsAreForgottenWithTheirSessions(t *testing.T) {
	m := testModel(nil, hookSess("a", core.AgentDone))
	m.hookSeen["a"] = true
	m.hookSeen["gone"] = true

	probeCmd(m.prober, m.cfg, m.sessions, m.touchedAt, m.hookSeen)
	if m.hookSeen["gone"] {
		t.Error("kept a confirmation for a session that no longer exists")
	}
	if !m.hookSeen["a"] {
		t.Error("dropped the confirmation for a live session")
	}
}
