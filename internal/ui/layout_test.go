package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// Rendering marks click zones, which needs the global manager the program sets
// up in main.
//
// DMA_HOME is redirected first, and for the whole package rather than per test.
// Model.save and core.SaveConfig write straight to the state directory, and any
// test that moves a card or picks from a dropdown reaches them -- against the
// real ~/.dma, that silently replaces the user's board with test fixtures.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "dma-ui-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(home)
	os.Setenv("DMA_HOME", home)

	zone.NewGlobal()
	m.Run()
}

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
	// Narrow widths are included because a floor on the column width used to
	// push the fourth column off the right edge of small terminals.
	for _, total := range []int{40, 60, 100, 120, 137, 160, 200} {
		w := columnWidths(total)
		sum := w[0] + w[1] + w[2] + w[3] + colGap*3
		if sum != total {
			t.Errorf("width %d: columns sum to %d", total, sum)
		}
	}
}

// Every mode has to fit the terminal exactly: anything wider or taller is
// clipped by the renderer, which silently eats a column or the whole panel.
func TestRenderFitsTheTerminalAtEverySize(t *testing.T) {
	sizes := [][2]int{{40, 12}, {60, 20}, {80, 24}, {120, 36}, {225, 79}, {388, 110}}
	modes := []mode{modeBoard, modeHelp, modeRepos, modeDiff}

	for _, sz := range sizes {
		for _, md := range modes {
			m := testModel(nil,
				sess("one", "", core.LifecycleIdle, core.AgentNeedsYou, "r"),
				sess("two", "", core.LifecycleActive, core.AgentWorking, "r"),
			)
			m.mode = md
			m.width, m.height = sz[0], sz[1]
			m.layoutSizes()

			lines := strings.Split(m.render(), "\n")
			if len(lines) > sz[1] {
				t.Errorf("%dx%d mode %d: %d rows rendered", sz[0], sz[1], md, len(lines))
			}
			for i, l := range lines {
				if w := lipglossWidth(l); w > sz[0] {
					t.Errorf("%dx%d mode %d: row %d is %d cells", sz[0], sz[1], md, i, w)
				}
			}
		}
	}
}

// The UI stops widening at maxContentWidth and sits centered in what is left,
// rather than stretching cards across an ultrawide display.
func TestWideScreenCapsAndCentersTheContent(t *testing.T) {
	m := testModel(nil, sess("one", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.width, m.height = 388, 110
	m.layoutSizes()

	if got := m.contentWidth(); got != maxContentWidth {
		t.Fatalf("content width %d, want %d", got, maxContentWidth)
	}
	top := strings.Split(m.render(), "\n")[0]
	indent := lipglossWidth(top) - lipglossWidth(strings.TrimLeft(top, " "))
	if want := (388 - maxContentWidth) / 2; indent != want {
		t.Errorf("content indented %d cells, want %d", indent, want)
	}
}

// A tall screen must not spend its extra rows on empty column: the cards need
// what they need and the panel takes the rest.
func TestTallScreenGivesSpareRowsToThePanel(t *testing.T) {
	m := testModel(nil, sess("one", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.width, m.height = 200, 110

	boardH, panelH, _ := m.splitHeights()
	if boardH != m.boardContentHeight() {
		t.Errorf("board took %d rows for %d rows of cards", boardH, m.boardContentHeight())
	}
	if boardH+panelH != m.height-1 {
		t.Errorf("board %d + panel %d does not fill %d rows above the status bar",
			boardH, panelH, m.height-1)
	}
	if panelH < m.height/2 {
		t.Errorf("panel got %d of %d rows; the spare rows went to empty column",
			panelH, m.height)
	}
}

// Cards must not be clipped while there are rows left to give them.
func TestBoardGrowsWithTheCardsItHolds(t *testing.T) {
	var sessions []*core.Session
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		sessions = append(sessions, sess(id, "", core.LifecycleIdle, core.AgentIdle, "r"))
	}
	m := testModel(nil, sessions...)
	m.width, m.height = 200, 110

	boardH, _, _ := m.splitHeights()
	if boardH < m.boardContentHeight() {
		t.Fatalf("board height %d clips %d rows of cards", boardH, m.boardContentHeight())
	}
	// Same board on a short screen: the panel keeps its minimum and the board
	// gives up the difference.
	m.height = 30
	boardH, panelH, _ := m.splitHeights()
	if panelH < minPanelHeight {
		t.Errorf("panel squeezed to %d rows, below the %d minimum", panelH, minPanelHeight)
	}
	if boardH+panelH != m.height-1 {
		t.Errorf("board %d + panel %d does not fill %d rows", boardH, panelH, m.height-1)
	}
}

// A terminal too short for both keeps the panel: it holds the input and the
// agent's output, and a column frame with nothing in it holds neither.
func TestVeryShortScreenDropsTheBoard(t *testing.T) {
	m := testModel(nil, sess("one", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.width, m.height = 120, 14

	boardH, panelH, _ := m.splitHeights()
	if boardH != 0 {
		t.Errorf("board kept %d rows on a 14-row screen", boardH)
	}
	if panelH != 13 {
		t.Errorf("panel got %d rows, want 13", panelH)
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

// The board's task input sits directly under a live agent that draws its own
// "❯" prompt. Reusing that caret here made the panel look like it had two
// inputs into the same session, so the row must mark itself as a new session.
func TestInputRowDoesNotMimicAgentPrompt(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentWorking, "r"))
	m.selectedID = "a"

	for _, focused := range []bool{false, true} {
		m.focus = focusBoard
		if focused {
			m.focus = focusInput
		}
		row := m.inputRow(80)
		if strings.Contains(row, "❯") {
			t.Errorf("focused=%v: input row uses the agent's prompt caret: %q", focused, row)
		}
		if !strings.Contains(row, strings.TrimSpace(newSessionGlyph)) {
			t.Errorf("focused=%v: input row missing the new-session glyph: %q", focused, row)
		}
	}

	m.focus = focusBoard
	if row := m.inputRow(80); !strings.Contains(row, "new session") {
		t.Errorf("unfocused row should say what it creates: %q", row)
	}
}

// Typing a task moves the input into a frame of its own: inside the panel its
// caret sits two rows under the agent's own prompt, which reads as one crowded
// input rather than two separate ones.
func TestTypingATaskGivesTheInputItsOwnBox(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentWorking, "r"))
	m.selectedID = "a"
	m.width, m.height = 140, 40

	if m.inputDetached() {
		t.Fatal("input claimed its own box while the board had focus")
	}
	panel := m.viewPanel(mustPanelHeight(t, m))
	if !strings.Contains(zoneMarker.ReplaceAllString(panel, ""), "new session") {
		t.Error("unfocused input left the panel entirely")
	}

	m.focus = focusInput
	if !m.inputDetached() {
		t.Fatal("input stayed in the panel while being typed into")
	}
	boardH, panelH, inputH := m.splitHeights()
	if inputH != inputBoxHeight {
		t.Errorf("input box got %d rows, want %d", inputH, inputBoxHeight)
	}
	if boardH+panelH+inputH != m.height-1 {
		t.Errorf("board %d + panel %d + input %d does not fill %d rows above the status bar",
			boardH, panelH, inputH, m.height-1)
	}
	box := zoneMarker.ReplaceAllString(m.viewInputBox(inputH), "")
	if got := len(strings.Split(box, "\n")); got != inputBoxHeight {
		t.Errorf("input box rendered %d lines, want %d", got, inputBoxHeight)
	}
	if !strings.Contains(box, "new session") {
		t.Errorf("input box does not name what it creates:\n%s", box)
	}
	if body := zoneMarker.ReplaceAllString(m.viewPanel(panelH), ""); strings.Contains(body, newSessionGlyph) {
		t.Errorf("panel kept an input row while the input has its own box:\n%s", body)
	}
}

// Focusing the input must not resize the agents: they reflow on resize, and
// losing the output you were reading is a steep price for moving the cursor.
func TestDetachingTheInputDoesNotResizeAgents(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentWorking, "r"))
	m.width, m.height = 140, 40

	cols, rows := m.previewDims()
	m.focus = focusInput
	gotCols, gotRows := m.previewDims()
	if gotCols != cols || gotRows != rows {
		t.Errorf("preview went from %dx%d to %dx%d when the input took focus",
			cols, rows, gotCols, gotRows)
	}
}

// A window with no rows to spare keeps the input inside the panel: a frame of
// its own would come straight off the agent's output.
func TestShortWindowKeepsTheInputInThePanel(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentWorking, "r"))
	m.width, m.height = 140, 14
	m.focus = focusInput

	if m.inputDetached() {
		t.Fatal("input took its own box on a 14-row window")
	}
	boardH, panelH, inputH := m.splitHeights()
	if inputH != 0 {
		t.Errorf("input box claimed %d rows it has no room for", inputH)
	}
	if boardH+panelH != m.height-1 {
		t.Errorf("board %d + panel %d does not fill %d rows", boardH, panelH, m.height-1)
	}
}

func mustPanelHeight(t *testing.T, m Model) int {
	t.Helper()
	_, panelH, _ := m.splitHeights()
	if panelH <= 0 {
		t.Fatalf("panel got %d rows", panelH)
	}
	return panelH
}
