package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// crowdedBoard is a board whose idle column holds far more cards than a 40-row
// window can draw, which is the situation the scrolling exists for.
func crowdedBoard(t *testing.T, n int) (Model, []string) {
	t.Helper()
	var sessions []*core.Session
	for i := 0; i < n; i++ {
		sessions = append(sessions, sess(fmt.Sprintf("s%02d", i), "", core.LifecycleIdle, core.AgentNeedsYou, "r"))
	}
	m := testModel(nil, sessions...)
	m.width, m.height = 140, 40
	m.layoutSizes()
	m.syncColumnScroll()

	col := m.columns()[core.LifecycleIdle.ColumnIndex()]
	ids := make([]string, len(col))
	for i, s := range col {
		ids[i] = s.ID
	}
	if boardH, _, _ := m.splitHeights(); boardH >= m.boardContentHeight() {
		t.Fatalf("board fits all %d cards; the test no longer exercises scrolling", n)
	}
	return m, ids
}

// boardText renders the columns as plain text, click markers stripped, so a test
// can ask which cards are on screen.
func boardText(m Model) string {
	boardH, _, _ := m.splitHeights()
	return zoneMarker.ReplaceAllString(m.viewBoard(boardH), "")
}

// syncedUpdate drives a message through Update, which is where the scroll offsets
// are settled -- handleKey alone leaves them as they were.
func syncedUpdate(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

// The point of the cap: a busy column must not push the agent's output down to
// the panel's minimum. Past halfway the board stops growing and scrolls instead.
func TestBoardStopsGrowingBeforeItCrowdsThePanel(t *testing.T) {
	m, _ := crowdedBoard(t, 12)

	boardH, panelH, _ := m.splitHeights()
	if want := (m.height - 1) / 2; boardH > want {
		t.Errorf("board took %d of %d rows, past the %d it is capped at", boardH, m.height-1, want)
	}
	if panelH < boardH {
		t.Errorf("panel got %d rows against the board's %d", panelH, boardH)
	}
	if panelH <= minPanelHeight {
		t.Errorf("panel squeezed to its %d-row minimum by a full column", minPanelHeight)
	}
}

// A capped column has to show it scrolls, or it reads as a column that simply
// holds fewer cards than it does.
func TestScrolledColumnDrawsAScrollbar(t *testing.T) {
	m, ids := crowdedBoard(t, 12)
	idle := core.LifecycleIdle.ColumnIndex()

	_, bar := m.columnRows(m.columns()[idle], idle, m.findSelected(), columnWidths(m.contentWidth())[idle], boardHeight(m))
	if bar == nil {
		t.Fatal("column at the top of a 12-card list drew no scrollbar")
	}
	if bar.Total != 12 || bar.Offset != 0 || bar.Visible >= bar.Total {
		t.Errorf("scrollbar reads %+v, want all 12 cards with only some on screen", *bar)
	}
	if body := boardText(m); !strings.Contains(body, "█") {
		t.Fatalf("no thumb drawn for a column that does not fit:\n%s", body)
	}

	m.scrollColumn(idle, 2)
	body := boardText(m)
	if strings.Contains(body, ids[0]) {
		t.Errorf("card %s is above the scroll offset but still drawn:\n%s", ids[0], body)
	}
	_, bar = m.columnRows(m.columns()[idle], idle, m.findSelected(), columnWidths(m.contentWidth())[idle], boardHeight(m))
	if bar == nil || bar.Offset != 2 {
		t.Errorf("scrollbar did not follow the column to offset 2: %+v", bar)
	}
}

// A column whose cards all fit has nothing to scroll, and a bar there would be
// noise claiming otherwise.
func TestColumnThatFitsDrawsNoScrollbar(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.width, m.height = 140, 40
	m.layoutSizes()
	m.syncColumnScroll()

	if body := boardText(m); strings.Contains(body, "█") {
		t.Errorf("board with one card drew a scrollbar:\n%s", body)
	}
}

// The thumb has to mean something: it starts at the top, ends at the bottom, and
// is sized to the share of the column on screen.
func TestScrollbarThumbTracksThePosition(t *testing.T) {
	rows := 10
	top := (&Scrollbar{Total: 20, Visible: 5, Offset: 0}).glyphs(rows)
	bottom := (&Scrollbar{Total: 20, Visible: 5, Offset: 15}).glyphs(rows)
	for name, got := range map[string][]string{"top": top, "bottom": bottom} {
		if len(got) != rows {
			t.Fatalf("%s bar is %d rows, want %d", name, len(got), rows)
		}
	}
	if !strings.Contains(top[0], "█") {
		t.Errorf("column at offset 0 did not put the thumb at the top: %v", top)
	}
	if !strings.Contains(bottom[rows-1], "█") {
		t.Errorf("column at its last card did not put the thumb at the bottom: %v", bottom)
	}
	if got := thumbLen(top); got != rows*5/20 {
		t.Errorf("thumb is %d rows for a quarter of the column on screen, want %d", got, rows*5/20)
	}
	if thumbLen(bottom) != thumbLen(top) {
		t.Errorf("thumb changed length when the column scrolled: %d then %d", thumbLen(top), thumbLen(bottom))
	}
	// Nothing to scroll, nothing to draw.
	if got := (&Scrollbar{Total: 5, Visible: 5}).glyphs(rows); got != nil {
		t.Errorf("a fully visible list drew a bar: %v", got)
	}
}

// thumbLen counts the filled rows of a rendered scrollbar.
func thumbLen(bar []string) int {
	n := 0
	for _, g := range bar {
		if strings.Contains(g, "█") {
			n++
		}
	}
	return n
}

// boardHeight is the rows the board is drawn in, which the column helpers take
// as their height.
func boardHeight(m Model) int {
	h, _, _ := m.splitHeights()
	return h
}

// Every card must be reachable with the keyboard alone: stepping the cursor onto
// one has to bring it on screen, whatever the column's offset was.
func TestCursorBringsEveryCardIntoView(t *testing.T) {
	m, ids := crowdedBoard(t, 12)

	// Down the column and back up again: scrolling forward is the easy direction,
	// and an offset that only ever grows leaves the top of the column stranded.
	order := append(append([]string{}, ids...), ids[0])
	for _, id := range order {
		m.selectedID = id
		m.unpinScroll()
		m.syncColumnScroll()
		if body := boardText(m); !strings.Contains(body, id) {
			t.Fatalf("selecting %s left it off screen:\n%s", id, body)
		}
	}
}

// Stepping down with j is the common way through a long column, and it must not
// stop at the fold.
func TestSteppingDownScrollsPastTheFold(t *testing.T) {
	m, ids := crowdedBoard(t, 12)
	m.selectedID = ids[0]

	for range ids[1:] {
		m = syncedUpdate(m, keyOf('j'))
	}
	if m.selectedID != ids[len(ids)-1] {
		t.Fatalf("j stopped at %s, want the last card %s", m.selectedID, ids[len(ids)-1])
	}
	if body := boardText(m); !strings.Contains(body, m.selectedID) {
		t.Errorf("the cursor is on %s and the column is not showing it:\n%s", m.selectedID, body)
	}
}

// A column cannot be scrolled into empty space: the furthest offset still ends on
// the last card.
func TestColumnWillNotScrollPastItsLastCard(t *testing.T) {
	m, ids := crowdedBoard(t, 12)
	last := ids[len(ids)-1]

	idle := core.LifecycleIdle.ColumnIndex()
	m.scrollColumn(idle, 99)
	body := boardText(m)
	if !strings.Contains(body, last) {
		t.Errorf("scrolling to the end left the last card %s off screen:\n%s", last, body)
	}
	_, bar := m.columnRows(m.columns()[idle], idle, m.findSelected(), columnWidths(m.contentWidth())[idle], boardHeight(m))
	if bar == nil {
		t.Fatal("the column still does not fit; it should still have a scrollbar")
	}
	if bar.Offset+bar.Visible != bar.Total {
		t.Errorf("column scrolled to %+v, which does not end on its last card", *bar)
	}
}

// The wheel is for looking around the board. It must not change the selection:
// the panel underneath is a live agent terminal, and swapping which agent it shows
// is not what a scroll gesture means.
func TestWheelScrollsTheColumnUnderThePointerOnly(t *testing.T) {
	m, ids := crowdedBoard(t, 12)
	m.selectedID = ids[0]
	rendered(t, m, zoneColumn(core.LifecycleIdle.ColumnIndex()))

	idle := core.LifecycleIdle.ColumnIndex()
	z := zone.Get(zoneColumn(idle))
	if z.IsZero() {
		t.Fatal("no zone recorded for the idle column")
	}
	x, y := (z.StartX+z.EndX)/2, (z.StartY+z.EndY)/2

	next, _ := m.handleMouse(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	m = next.(Model)
	if m.colScroll[idle] != 1 {
		t.Errorf("wheel left the column at offset %d, want 1", m.colScroll[idle])
	}
	if m.selectedID != ids[0] {
		t.Errorf("wheel moved the selection to %s", m.selectedID)
	}
	for i := range m.colScroll {
		if i != idle && m.colScroll[i] != 0 {
			t.Errorf("wheel over column %d also scrolled column %d", idle, i)
		}
	}

	// The scrolled column stays where the pointer put it, selected card off screen
	// and all, until a key moves the cursor.
	m.syncColumnScroll()
	if m.colScroll[idle] != 1 {
		t.Errorf("offset snapped back to %d under the cursor", m.colScroll[idle])
	}
	m = syncedUpdate(m, keyOf('j'))
	if body := boardText(m); !strings.Contains(body, m.selectedID) {
		t.Errorf("moving the cursor did not win the column back:\n%s", body)
	}
}

// Scrolling back to the top hands the column to the cursor again, so a wheel
// gesture that ends where it started leaves no state behind.
func TestWheelBackToTheTopUnpinsTheColumn(t *testing.T) {
	m, _ := crowdedBoard(t, 12)
	idle := core.LifecycleIdle.ColumnIndex()

	m.scrollColumn(idle, 3)
	if !m.scrollPinned[idle] {
		t.Fatal("a scrolled column is not pinned")
	}
	m.scrollColumn(idle, -3)
	if m.scrollPinned[idle] {
		t.Error("column at the top is still pinned")
	}
}

// A window too short for the board keeps no offsets: growing it back must open on
// the top of each column rather than on a scroll position from a layout that was
// never on screen.
func TestBoardlessWindowForgetsTheOffsets(t *testing.T) {
	m, _ := crowdedBoard(t, 12)
	m.scrollColumn(core.LifecycleIdle.ColumnIndex(), 2)

	m.height = 14
	m.syncColumnScroll()
	if m.colScroll != [4]int{} {
		t.Errorf("offsets survived a window with no board: %v", m.colScroll)
	}
}
