package core

import (
	"testing"
	"time"
)

// Two repos sharing a branch name is the case that breaks branch-only matching.
func TestFindByKeyDistinguishesReposSharingABranch(t *testing.T) {
	a := &Session{ID: "a", RepoID: "api", Branch: "feat/auth"}
	b := &Session{ID: "b", RepoID: "web", Branch: "feat/auth"}
	sessions := []*Session{a, b}

	if got := FindByKey(sessions, Key{RepoID: "api", Branch: "feat/auth"}); got != a {
		t.Fatalf("api/feat-auth resolved to %v, want session a", got)
	}
	if got := FindByKey(sessions, Key{RepoID: "web", Branch: "feat/auth"}); got != b {
		t.Fatalf("web/feat-auth resolved to %v, want session b", got)
	}
	if got := FindByKey(sessions, Key{RepoID: "cli", Branch: "feat/auth"}); got != nil {
		t.Fatalf("unregistered repo matched %v, want nil", got)
	}
}

func TestKeyStringDoesNotCollideAcrossSplits(t *testing.T) {
	// "a" + "b/c" and "a/b" + "c" must not produce the same key.
	k1 := Key{RepoID: "a", Branch: "b/c"}
	k2 := Key{RepoID: "a/b", Branch: "c"}
	if k1.String() == k2.String() {
		t.Fatalf("keys collided: %q", k1.String())
	}
}

func TestSetAgentStateNeverTouchesLifecycle(t *testing.T) {
	s := &Session{Lifecycle: LifecyclePROpen, AgentState: AgentIdle}
	for _, st := range []AgentState{AgentWorking, AgentNeedsYou, AgentDone, AgentIdle} {
		s.SetAgentState(st, "")
		if s.Lifecycle != LifecyclePROpen {
			t.Fatalf("agent state %s moved lifecycle to %s", st, s.Lifecycle)
		}
	}
}

func TestSetAgentStateResetsClockOnlyOnChange(t *testing.T) {
	s := &Session{AgentState: AgentWorking, AgentStateSince: time.Now().Add(-time.Hour)}
	before := s.AgentStateSince

	if s.SetAgentState(AgentWorking, "") {
		t.Fatal("re-reporting the same state counted as a change")
	}
	if !s.AgentStateSince.Equal(before) {
		t.Fatal("clock reset on an unchanged state; time in state would never accumulate")
	}

	if !s.SetAgentState(AgentNeedsYou, "bash approval") {
		t.Fatal("a real transition was not reported as a change")
	}
	if !s.AgentStateSince.After(before) {
		t.Fatal("clock did not reset on a real transition")
	}
}

// A heartbeat that only changes the detail string should keep the clock, so
// "working 8m" does not reset to 0s on every tool call.
func TestSetAgentStateKeepsClockWhenOnlyDetailChanges(t *testing.T) {
	since := time.Now().Add(-30 * time.Minute)
	s := &Session{AgentState: AgentWorking, AgentStateSince: since}
	s.SetAgentState(AgentWorking, "bash")
	if !s.AgentStateSince.Equal(since) {
		t.Fatal("detail-only change reset the time-in-state clock")
	}
}

func TestSortColumnPutsNeedsYouFirstThenOldest(t *testing.T) {
	now := time.Now()
	old := &Session{ID: "old", AgentState: AgentWorking, AgentStateSince: now.Add(-time.Hour)}
	recent := &Session{ID: "recent", AgentState: AgentWorking, AgentStateSince: now}
	blockedRecent := &Session{ID: "blocked-recent", AgentState: AgentNeedsYou, AgentStateSince: now}
	blockedOld := &Session{ID: "blocked-old", AgentState: AgentNeedsYou, AgentStateSince: now.Add(-2 * time.Hour)}

	in := []*Session{recent, blockedRecent, old, blockedOld}
	SortColumn(in)

	want := []string{"blocked-old", "blocked-recent", "old", "recent"}
	for i, id := range want {
		if in[i].ID != id {
			t.Fatalf("position %d = %s, want %s (order: %v)", i, in[i].ID, id, ids(in))
		}
	}
}

func ids(in []*Session) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.ID
	}
	return out
}

func TestGroupOrderPutsUngroupedLast(t *testing.T) {
	cfg := &Config{Groups: []string{"auth", "infra"}}
	sessions := []*Session{
		{Group: ""},
		{Group: "infra"},
		{Group: "zebra"}, // not in config: appended after configured groups
		{Group: "auth"},
	}
	got := GroupOrder(cfg, sessions)
	want := []string{"auth", "infra", "zebra", ""}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestRollupCounts(t *testing.T) {
	r := RollupOf([]*Session{
		{AgentState: AgentWorking},
		{AgentState: AgentWorking},
		{AgentState: AgentNeedsYou},
		{AgentState: AgentIdle},
	})
	if r.Working != 2 || r.NeedsYou != 1 || r.Idle != 1 || r.Total != 4 {
		t.Fatalf("unexpected rollup: %+v", r)
	}
}

func TestSlugProducesBranchSafeNames(t *testing.T) {
	cases := map[string]string{
		"auth refresh":            "auth-refresh",
		"Fix THE  thing!!":        "fix-the-thing",
		"  leading and trailing ": "leading-and-trailing",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slug("!!!"); got == "" {
		t.Error("Slug of punctuation-only produced an empty branch name")
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{8 * time.Minute, "8m"},
		{90 * time.Minute, "1h30m"},
		{50 * time.Hour, "2d"},
	}
	for _, c := range cases {
		if got := FormatDuration(c.d); got != c.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
