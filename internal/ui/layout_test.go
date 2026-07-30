package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func sess(id, group string, lc core.Lifecycle, st core.AgentState, repo string) *core.Session {
	return &core.Session{
		ID: id, Title: id, Group: group, Lifecycle: lc, AgentState: st, RepoID: repo,
		AgentStateSince: time.Now(),
	}
}

func testModel(cfg *core.Config, sessions ...*core.Session) Model {
	if cfg == nil {
		cfg = core.DefaultConfig()
	}
	m := New(Options{Config: cfg, Sessions: sessions})
	m.width, m.height = 140, 40
	return m
}

func TestColumnsBucketByLifecycle(t *testing.T) {
	m := testModel(nil,
		sess("a", "", core.LifecycleIdle, core.AgentNeedsYou, "r"),
		sess("b", "", core.LifecycleActive, core.AgentWorking, "r"),
		sess("c", "", core.LifecyclePROpen, core.AgentIdle, "r"),
		sess("d", "", core.LifecycleMerged, core.AgentIdle, "r"),
	)
	cols := m.columns()
	for i, want := range []string{"a", "b", "c", "d"} {
		if len(cols[i]) != 1 || cols[i][0].ID != want {
			t.Errorf("column %d = %v, want [%s]", i, idsOf(cols[i]), want)
		}
	}
}

// The agent owns the first two columns: a session that starts working must move
// from idle to active on its own.
func TestAgentStateMovesBetweenIdleAndActive(t *testing.T) {
	s := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")

	s.SetAgentState(core.AgentWorking, "")
	if s.Lifecycle != core.LifecycleActive {
		t.Fatalf("working session sits in %s, want active", s.Lifecycle)
	}
	s.SetAgentState(core.AgentDone, "")
	if s.Lifecycle != core.LifecycleIdle {
		t.Fatalf("finished session sits in %s, want idle", s.Lifecycle)
	}
	s.SetAgentState(core.AgentNeedsYou, "approve?")
	if s.Lifecycle != core.LifecycleIdle {
		t.Fatalf("blocked session sits in %s, want idle", s.Lifecycle)
	}
}

// The PR-owned columns record git facts, so agent activity must not pull a card
// out of them.
func TestAgentStateCannotLeavePRColumns(t *testing.T) {
	for _, col := range []core.Lifecycle{core.LifecyclePROpen, core.LifecycleMerged} {
		s := sess("a", "", col, core.AgentIdle, "r")
		for _, st := range []core.AgentState{core.AgentWorking, core.AgentDone, core.AgentNeedsYou, core.AgentIdle} {
			s.SetAgentState(st, "")
			if s.Lifecycle != col {
				t.Fatalf("agent state %s moved a card out of %s into %s", st, col, s.Lifecycle)
			}
		}
	}
}

func TestSelectionFollowsCardAcrossColumns(t *testing.T) {
	a := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	b := sess("b", "", core.LifecycleActive, core.AgentWorking, "r")
	m := testModel(nil, a, b)
	m.selectedID = "b"

	if p := m.findSelected(); !p.ok || m.sessionAt(p).ID != "b" {
		t.Fatal("selection did not resolve")
	}

	// b stops working and crosses into the idle column. Because selection is
	// anchored to the id, the cursor must follow it rather than staying put and
	// landing on a different card.
	b.SetAgentState(core.AgentNeedsYou, "approve?")
	p := m.findSelected()
	if !p.ok {
		t.Fatal("selection lost when its card changed column")
	}
	if got := m.sessionAt(p); got == nil || got.ID != "b" {
		t.Fatalf("cursor landed on %v, want b", got)
	}
	if p.col != core.LifecycleIdle.ColumnIndex() {
		t.Errorf("b is in column %d, want idle", p.col)
	}
}

func TestSortPutsNeedsYouFirstWithinColumn(t *testing.T) {
	now := time.Now()
	working := sess("working", "", core.LifecycleIdle, core.AgentDone, "r")
	working.AgentStateSince = now
	blocked := sess("blocked", "", core.LifecycleIdle, core.AgentNeedsYou, "r")
	blocked.AgentStateSince = now.Add(-time.Hour)

	m := testModel(nil, working, blocked)
	col := m.columns()[core.LifecycleIdle.ColumnIndex()]
	if len(col) != 2 || col[0].ID != "blocked" {
		t.Fatalf("column order = %v, want blocked first", idsOf(col))
	}
}

func TestProjectFilterNarrowsBoard(t *testing.T) {
	m := testModel(nil,
		sess("a", "auth", core.LifecycleIdle, core.AgentIdle, "r"),
		sess("b", "infra", core.LifecycleIdle, core.AgentIdle, "r"),
	)
	if n := len(m.visible()); n != 2 {
		t.Fatalf("unfiltered board shows %d, want 2", n)
	}
	m.projectFilter = "auth"
	vis := m.visible()
	if len(vis) != 1 || vis[0].ID != "a" {
		t.Fatalf("filtered board shows %v, want [a]", idsOf(vis))
	}
}

func TestRepoFilterNarrowsBoard(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api"}, {ID: "web"}}
	m := testModel(cfg,
		sess("a", "", core.LifecycleIdle, core.AgentIdle, "api"),
		sess("b", "", core.LifecycleIdle, core.AgentIdle, "web"),
	)
	m.repoFilter = "web"
	vis := m.visible()
	if len(vis) != 1 || vis[0].ID != "b" {
		t.Fatalf("filtered board shows %v, want [b]", idsOf(vis))
	}
}

func TestMoveHSkipsEmptyColumns(t *testing.T) {
	m := testModel(nil,
		sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"),
		// Nothing active; the next occupied column is pr open.
		sess("c", "", core.LifecyclePROpen, core.AgentIdle, "r"),
	)
	m.selectedID = "a"
	if got := m.moveH(1); got == nil || got.ID != "c" {
		t.Fatalf("moving right landed on %v, want c", got)
	}
	m.selectedID = "c"
	if got := m.moveH(1); got != nil {
		t.Fatalf("moving past the last column returned %v, want nil", got)
	}
}

func TestColumnWidthsFillTheTerminal(t *testing.T) {
	for _, total := range []int{100, 120, 137, 160, 200} {
		w := columnWidths(total)
		sum := w[0] + w[1] + w[2] + w[3] + colGap*3
		if sum != total {
			t.Errorf("width %d: columns sum to %d", total, sum)
		}
	}
}

// A box must occupy exactly the space it claims, or columns laid side by side
// drift out of alignment.
func TestBoxIsExactlyItsDeclaredSize(t *testing.T) {
	b := Box{Title: "idle", Subtitle: "waiting on you", Width: 30, Height: 8}
	out := b.Render("one\ntwo")
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("box rendered %d lines, want 8", len(lines))
	}
	for i, l := range lines {
		if w := lipglossWidth(l); w != 30 {
			t.Errorf("line %d is %d cells wide, want 30: %q", i, w, l)
		}
	}
}

func TestBoxClipsOverlongBody(t *testing.T) {
	b := Box{Title: "t", Width: 20, Height: 4}
	out := b.Render(strings.Repeat("x\n", 20))
	if got := len(strings.Split(out, "\n")); got != 4 {
		t.Fatalf("box grew to %d lines despite Height 4", got)
	}
}

func TestBoxDropsSubtitleWhenNarrow(t *testing.T) {
	narrow := Box{Title: "idle", Subtitle: "waiting on you", Width: 12, Height: 3}.Render("")
	if strings.Contains(narrow, "waiting") {
		t.Error("subtitle kept at a width where it cannot fit")
	}
	if !strings.Contains(narrow, "idle") {
		t.Error("title dropped; it should survive before the subtitle does")
	}
}

func TestTruncateRespectsWidth(t *testing.T) {
	if got := truncate("hello world", 5); lipglossWidth(got) > 5 {
		t.Fatalf("truncate produced %q, wider than 5", got)
	}
	if got := truncate("short", 20); got != "short" {
		t.Fatalf("truncate shortened a string that fit: %q", got)
	}
	if got := truncate("anything", 0); got != "" {
		t.Fatalf("truncate to zero returned %q", got)
	}
}

func TestFocusRingCyclesAllAreas(t *testing.T) {
	seen := map[focusArea]bool{}
	f := focusBoard
	for i := 0; i < len(focusRing); i++ {
		seen[f] = true
		f = focusRing[wrap(indexOfFocus(f)+1, len(focusRing))]
	}
	if f != focusBoard {
		t.Error("cycling the whole ring did not return to the board")
	}
	for _, want := range focusRing {
		if !seen[want] {
			t.Errorf("focus area %d unreachable by tab", want)
		}
	}
}

func idsOf(in []*core.Session) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.ID
	}
	return out
}
