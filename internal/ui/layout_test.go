package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/clip"
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
	if inputH != minInputBoxHeight {
		t.Errorf("empty input box got %d rows, want %d", inputH, minInputBoxHeight)
	}
	if boardH+panelH+inputH != m.height-1 {
		t.Errorf("board %d + panel %d + input %d does not fill %d rows above the status bar",
			boardH, panelH, inputH, m.height-1)
	}
	box := zoneMarker.ReplaceAllString(m.viewInputBox(inputH), "")
	if got := len(strings.Split(box, "\n")); got != minInputBoxHeight {
		t.Errorf("input box rendered %d lines, want %d", got, minInputBoxHeight)
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

// typeTask types into the focused input one keypress at a time, which is the
// path a wrapped line has to survive.
func typeTask(m Model, task string) Model {
	for _, r := range task {
		m = press(m, keyOf(r))
	}
	return m
}

// focusedInput is a model parked in the task input at a given window size.
func focusedInput(width, height int) Model {
	m := testModel(nil, sess("a", "", core.LifecycleActive, core.AgentWorking, "r"))
	m.selectedID = "a"
	m.width, m.height = width, height
	m.layoutSizes()
	m.focus = focusInput
	m.input.Focus()
	return m
}

// The point of the feature: a task longer than the box wraps onto another row
// rather than scrolling its beginning out of view. Enter spends a worktree and an
// agent on this text, so all of it has to be readable first.
func TestALongTaskWrapsInsteadOfScrollingOutOfView(t *testing.T) {
	m := focusedInput(90, 40)
	task := "rewrite the session store so every write goes through one place, " +
		"and have the board read from it instead of the file"
	m = typeTask(m, task)

	if got := m.input.Value(); got != task {
		t.Fatalf("input holds %q, want %q", got, task)
	}
	if m.inputRows() < 2 {
		t.Fatalf("a %d-cell task in a %d-cell field stayed on one row", len(task), m.inputWidth())
	}

	_, _, inputH := m.splitHeights()
	box := zoneMarker.ReplaceAllString(m.viewInputBox(inputH), "")
	if got := len(strings.Split(box, "\n")); got != inputH {
		t.Errorf("input box rendered %d lines in %d rows", got, inputH)
	}
	// Every word is on screen somewhere: that nothing slid sideways out of the
	// frame is the whole of the change.
	for _, word := range strings.Fields(task) {
		if !strings.Contains(box, word) {
			t.Errorf("word %q is not in the box:\n%s", word, box)
		}
	}
	for i, l := range strings.Split(box, "\n") {
		if w := lipglossWidth(l); w != m.contentWidth() {
			t.Errorf("box line %d is %d cells wide, want %d", i, w, m.contentWidth())
		}
	}
}

// Growing is free: the rows a wrapped task takes come from a board holding more
// than its floor, or from a panel with rows to spare -- never off a panel that is
// already down to its minimum. The field must also agree with the box about how
// many rows it has, since rows it rendered and the frame then clipped would put
// the caret somewhere off screen.
func TestGrowingInputNeverSqueezesTheBoardOrThePanel(t *testing.T) {
	var sessions []*core.Session
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		sessions = append(sessions, sess(id, "", core.LifecycleIdle, core.AgentIdle, "r"))
	}
	m := testModel(nil, sessions...)
	m.focus = focusInput

	for h := minInputBoxHeight + minPanelHeight; h <= 60; h++ {
		m.width, m.height = 90, h
		m.layoutSizes()
		m.input.Focus()

		// The same window with a task that fits on one row, which is what the input
		// cost before it could grow at all.
		m.input.SetValue("short")
		baseBoard, basePanel, _ := m.splitHeights()

		m.input.SetValue(strings.Repeat("a task that keeps going. ", 40))
		boardH, panelH, inputH := m.splitHeights()

		if boardH+panelH+inputH != h-1 {
			t.Errorf("h=%d: board %d + panel %d + input %d does not fill %d rows",
				h, boardH, panelH, inputH, h-1)
		}
		if got := m.input.Height(); got != m.inputRows() {
			t.Errorf("h=%d: field renders %d rows, box shows %d", h, got, m.inputRows())
		}
		if m.inputRows() > maxInputRows {
			t.Errorf("h=%d: input grew to %d rows, past the %d cap", h, m.inputRows(), maxInputRows)
		}
		if !m.inputDetached() {
			// Still inside the panel, where there is one row for it and no frame.
			if inputH != 0 || m.inputRows() != 1 {
				t.Errorf("h=%d: undetached input claimed %d rows over %d text rows",
					h, inputH, m.inputRows())
			}
			continue
		}
		if inputH != m.inputRows()+2 {
			t.Errorf("h=%d: input box is %d rows for %d rows of text", h, inputH, m.inputRows())
		}
		// The panel may be pulled down to its minimum, less the input row it is no
		// longer drawing, and no further.
		if floor := min(basePanel, minPanelHeight-1); panelH < floor {
			t.Errorf("h=%d: panel went from %d to %d rows for a %d-row input, past its %d floor",
				h, basePanel, panelH, inputH, floor)
		}
		if floor := min(baseBoard, minBoardRows); boardH < floor {
			t.Errorf("h=%d: board went from %d to %d rows for a %d-row input, past its %d floor",
				h, baseBoard, boardH, inputH, floor)
		}

		// The notice line borrows from the same board and panel. Its row is held
		// back in the ceiling, so a grown input plus a notice still fits.
		withNotice := m
		withNotice.notice, withNotice.noticeAt = "something failed", time.Now()
		nBoardH, nPanelH, nInputH := withNotice.splitHeights()
		if nBoardH+nPanelH+nInputH != h-1-noticeRows {
			t.Errorf("h=%d: with a notice, board %d + panel %d + input %d does not fill %d rows",
				h, nBoardH, nPanelH, nInputH, h-1-noticeRows)
		}
		if nInputH != inputH {
			t.Errorf("h=%d: a notice resized the input box from %d to %d rows", h, inputH, nInputH)
		}
		if floor := min(basePanel, minPanelHeight-1) - noticeRows; nPanelH < floor {
			t.Errorf("h=%d: panel at %d rows with a notice and a %d-row input, past its %d floor",
				h, nPanelH, nInputH, floor)
		}
	}
}

// The pending-image badge leads the first row and the task wraps clear of it, in
// one column. Written on top of the view instead, it would sit on the first line
// with the rest of the rows hanging under nothing.
func TestImageBadgeLeadsTheFirstRowOfAWrappedTask(t *testing.T) {
	m := focusedInput(64, 40)
	m.pendingImages = []clip.Image{{PNG: []byte("png"), Width: 640, Height: 480}}
	m.layoutSizes()
	m = typeTask(m, "make the prune key ask once for the whole merged column")

	badge := m.imageSummary()
	if m.inputRows() < 2 {
		t.Fatalf("task did not wrap beside a %d-cell badge", lipglossWidth(badge))
	}
	_, _, inputH := m.splitHeights()
	rows := strings.Split(zoneMarker.ReplaceAllString(m.inputRow(m.inputWidth()), ""), "\n")

	if !strings.Contains(rows[0], strings.TrimSpace(badge)) {
		t.Errorf("first row does not lead with the badge: %q", rows[0])
	}
	lead := lipglossWidth(newSessionGlyph) + lipglossWidth(badge)
	for i, r := range rows[1:] {
		if strings.Contains(r, strings.TrimSpace(badge)) {
			t.Errorf("row %d repeats the badge: %q", i+1, r)
		}
		if got := r[:lead]; strings.TrimSpace(got) != "" {
			t.Errorf("row %d does not clear the badge: %q", i+1, r)
		}
	}
	// The field wraps against the width left after the badge, so no row outgrows
	// the frame it is drawn in.
	box := zoneMarker.ReplaceAllString(m.viewInputBox(inputH), "")
	for i, l := range strings.Split(box, "\n") {
		if w := lipglossWidth(l); w != m.contentWidth() {
			t.Errorf("box line %d is %d cells wide, want %d", i, w, m.contentWidth())
		}
	}
}

// A window with no rows to spare keeps the input to the one row it always had:
// the text scrolls inside it instead, which is the graceful end of growing.
func TestWindowWithNoSpareRowsKeepsTheInputToOneRow(t *testing.T) {
	for _, h := range []int{14, 20} {
		m := focusedInput(90, h)
		m.input.SetValue(strings.Repeat("wrap me. ", 20))
		if got := m.inputRows(); got != 1 {
			t.Errorf("h=%d: input took %d rows on a window with none to spare", h, got)
		}
		if got := m.input.Height(); got != 1 {
			t.Errorf("h=%d: field renders %d rows into a one-row box", h, got)
		}
	}
}

// A task can arrive with line breaks in it -- pasted, most of the time. It is
// still one session: the first line names it, and the whole text is the prompt.
func TestPastedTaskTitlesFromItsFirstLine(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}}
	m := testModel(cfg)

	// Leading blank line and all: a paste starting with one still has to name the
	// session after the line that says what the work is.
	task := "\nadd retries to the api client\n\n- back off exponentially\n- give up after 30s"
	req, err := m.newSessionRequest(task)
	if err != nil {
		t.Fatalf("newSessionRequest: %v", err)
	}
	if want := "add retries to the api client"; req.Title != want {
		t.Errorf("title %q, want %q", req.Title, want)
	}
	if req.InitialPrompt != task {
		t.Errorf("prompt %q dropped part of the task", req.InitialPrompt)
	}

	// Unfocused, the row inside the panel has one row to spend, whatever the
	// value holds.
	m.input.SetValue(task)
	m.focus = focusBoard
	if row := m.inputRow(80); strings.Contains(row, "\n") {
		t.Errorf("input row spans several lines inside the panel: %q", row)
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
