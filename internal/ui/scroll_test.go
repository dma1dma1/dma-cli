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

// A capped column has to say what is off screen at each end, or it reads as a
// column that simply holds fewer cards than it does.
func TestScrolledColumnCountsWhatIsOffEachEnd(t *testing.T) {
	m, ids := crowdedBoard(t, 12)

	if body := boardText(m); !strings.Contains(body, "↓") {
		t.Fatalf("column at the top said nothing about the cards below:\n%s", body)
	}
	m.scrollColumn(core.LifecycleIdle.ColumnIndex(), 2)
	body := boardText(m)
	if !strings.Contains(body, "↑ 2 more") {
		t.Errorf("scrolled column did not count the 2 cards above it:\n%s", body)
	}
	if strings.Contains(body, ids[0]) {
		t.Errorf("card %s is above the scroll offset but still drawn:\n%s", ids[0], body)
	}
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

	m.scrollColumn(core.LifecycleIdle.ColumnIndex(), 99)
	body := boardText(m)
	if !strings.Contains(body, last) {
		t.Errorf("scrolling to the end left the last card %s off screen:\n%s", last, body)
	}
	if strings.Contains(body, "↓") {
		t.Errorf("column scrolled past its last card:\n%s", body)
	}
}

// The wheel is for looking around the board. It must not change the selection:
// the panel underneath is a live agent terminal, and swapping which agent it shows
// is not what a scroll gesture means.
func TestWheelScrollsTheColumnUnderThePointerOnly(t *testing.T) {
	m, ids := crowdedBoard(t, 12)
	m.selectedID = ids[0]
	rendered(t, m)

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
