package ui

import (
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func sess(id, group string, lc core.Lifecycle, st core.AgentState, repo string) *core.Session {
	return &core.Session{
		ID: id, Group: group, Lifecycle: lc, AgentState: st, RepoID: repo,
		AgentStateSince: time.Now(),
	}
}

func TestBuildLayoutSwimlanesAreGroupsNotRepos(t *testing.T) {
	cfg := &core.Config{Groups: []string{"auth"}}
	sessions := []*core.Session{
		sess("1", "auth", core.LifecycleActive, core.AgentWorking, "api"),
		// Same group, different repo: group and repo are orthogonal, so this
		// belongs in the same lane.
		sess("2", "auth", core.LifecycleReview, core.AgentIdle, "web"),
		sess("3", "", core.LifecycleActive, core.AgentIdle, "api"),
	}
	ly := buildLayout(cfg, sessions, map[string]bool{}, "")

	if len(ly.Lanes) != 2 {
		t.Fatalf("got %d lanes, want 2 (auth, ungrouped)", len(ly.Lanes))
	}
	if ly.Lanes[0].Group != "auth" {
		t.Fatalf("first lane = %q, want auth", ly.Lanes[0].Group)
	}
	if ly.Lanes[1].Group != "" {
		t.Fatalf("last lane = %q, want ungrouped", ly.Lanes[1].Group)
	}
	if len(ly.Lanes[0].All) != 2 {
		t.Fatalf("auth lane holds %d sessions, want 2 across both repos", len(ly.Lanes[0].All))
	}
	if len(ly.Lanes[0].Columns[0]) != 1 || len(ly.Lanes[0].Columns[1]) != 1 {
		t.Fatal("auth lane's sessions did not split across active and review columns")
	}
}

func TestBuildLayoutRepoFilter(t *testing.T) {
	cfg := &core.Config{}
	sessions := []*core.Session{
		sess("1", "", core.LifecycleActive, core.AgentIdle, "api"),
		sess("2", "", core.LifecycleActive, core.AgentIdle, "web"),
	}
	ly := buildLayout(cfg, sessions, map[string]bool{}, "api")
	if n := len(ly.Lanes[0].All); n != 1 {
		t.Fatalf("filtered lane holds %d sessions, want 1", n)
	}
	if ly.Lanes[0].All[0].ID != "1" {
		t.Fatalf("filter kept the wrong session: %s", ly.Lanes[0].All[0].ID)
	}
}

func TestFindAnchorsSelectionToID(t *testing.T) {
	cfg := &core.Config{}
	sessions := []*core.Session{
		sess("a", "", core.LifecycleActive, core.AgentWorking, "r"),
		sess("b", "", core.LifecycleActive, core.AgentWorking, "r"),
	}
	ly := buildLayout(cfg, sessions, map[string]bool{}, "")
	p := ly.find("b")
	if !p.OK || ly.at(p).ID != "b" {
		t.Fatal("selection did not resolve back to the same session")
	}

	// b becomes blocked and sorts to the top of the column. Because selection
	// is anchored to the id, the cursor must still point at b -- not at
	// whatever card now occupies b's old row.
	sessions[1].AgentState = core.AgentNeedsYou
	sessions[1].AgentStateSince = time.Now().Add(-time.Hour)
	ly = buildLayout(cfg, sessions, map[string]bool{}, "")
	p = ly.find("b")
	if !p.OK || ly.at(p).ID != "b" {
		t.Fatal("re-sorting a column moved the selection off its session")
	}
	if p.Row != 0 {
		t.Fatalf("needs_you card at row %d, want 0", p.Row)
	}
}

func TestMoveHorizontalSkipsEmptyColumns(t *testing.T) {
	cfg := &core.Config{}
	sessions := []*core.Session{
		sess("a", "", core.LifecycleActive, core.AgentIdle, "r"),
		// Nothing in review; the next occupied column is pr_open.
		sess("c", "", core.LifecyclePROpen, core.AgentIdle, "r"),
	}
	ly := buildLayout(cfg, sessions, map[string]bool{}, "")

	got := ly.moveHorizontal(ly.find("a"), 1)
	if got == nil || got.ID != "c" {
		t.Fatalf("moving right landed on %v, want c", got)
	}
	// Off the right edge: stay put rather than selecting nothing.
	if got := ly.moveHorizontal(ly.find("c"), 1); got != nil {
		t.Fatalf("moving past the last column returned %v, want nil", got)
	}
}

func TestMoveVerticalSpillsIntoNextLane(t *testing.T) {
	cfg := &core.Config{Groups: []string{"one", "two"}}
	sessions := []*core.Session{
		sess("a", "one", core.LifecycleActive, core.AgentIdle, "r"),
		sess("b", "two", core.LifecycleActive, core.AgentIdle, "r"),
	}
	ly := buildLayout(cfg, sessions, map[string]bool{}, "")

	got := ly.moveVertical(ly.find("a"), 1, map[string]bool{})
	if got == nil || got.ID != "b" {
		t.Fatalf("moving down from the last card in a lane landed on %v, want b", got)
	}
}

func TestMoveVerticalSkipsCollapsedLanes(t *testing.T) {
	cfg := &core.Config{Groups: []string{"one", "two", "three"}}
	sessions := []*core.Session{
		sess("a", "one", core.LifecycleActive, core.AgentIdle, "r"),
		sess("b", "two", core.LifecycleActive, core.AgentIdle, "r"),
		sess("c", "three", core.LifecycleActive, core.AgentIdle, "r"),
	}
	collapsed := map[string]bool{"two": true}
	ly := buildLayout(cfg, sessions, collapsed, "")

	got := ly.moveVertical(ly.find("a"), 1, collapsed)
	if got == nil || got.ID != "c" {
		t.Fatalf("moving down skipped to %v, want c (collapsed lane should be passed over)", got)
	}
}

func TestColumnWidthsSumToAvailable(t *testing.T) {
	m := Model{}
	for _, total := range []int{100, 120, 137, 160} {
		w := m.columnWidths(total)
		sum := w[0] + w[1] + w[2] + w[3] + colGap*3
		if sum != total {
			t.Errorf("width %d: columns sum to %d, want %d", total, sum, total)
		}
		for i, x := range w {
			if x < minCardWidth {
				t.Errorf("width %d: column %d is %d, below the %d minimum", total, i, x, minCardWidth)
			}
		}
	}
}

func TestTruncateRespectsDisplayWidth(t *testing.T) {
	if got := truncate("hello world", 5); len([]rune(got)) > 5 {
		t.Fatalf("truncate produced %q, wider than 5 cells", got)
	}
	if got := truncate("short", 20); got != "short" {
		t.Fatalf("truncate shortened a string that fit: %q", got)
	}
	if got := truncate("anything", 0); got != "" {
		t.Fatalf("truncate to zero returned %q", got)
	}
}

func TestRollupTextSurvivesCollapse(t *testing.T) {
	// A collapsed group still has to say that something needs attention.
	r := core.RollupOf([]*core.Session{
		{AgentState: core.AgentWorking},
		{AgentState: core.AgentWorking},
		{AgentState: core.AgentNeedsYou},
	})
	got := rollupText(r)
	want := "2 working, 1 needs you"
	if got != want {
		t.Fatalf("rollupText = %q, want %q", got, want)
	}
}
