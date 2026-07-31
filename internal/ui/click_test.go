package ui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// zoneMarker matches bubblezone's markers, which are private CSI sequences.
var zoneMarker = regexp.MustCompile("\x1b\\[[0-9]+z")

// rendered renders the model -- render() scans for zones itself -- and waits for
// the zone manager's worker to publish the zones the caller is about to read.
//
// It has to be told which ones, and it has to forget them first. The manager
// publishes off a worker goroutine, and zones an earlier test's render published
// are still there, so waiting on a zone that is already set -- any zone but the
// one in question -- can return before this render has been scanned at all.
func rendered(t *testing.T, m Model, want ...string) {
	t.Helper()
	if len(want) == 0 {
		want = []string{zoneInput}
	}
	for _, id := range want {
		zone.Clear(id)
	}
	m.render()
	for i := 0; i < 500; i++ {
		if published(want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("zone manager never published %v", want)
}

func published(ids []string) bool {
	for _, id := range ids {
		if zone.Get(id).IsZero() {
			return false
		}
	}
	return true
}

func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// clickModel is clickAt through handleClick, for the tests that care about the
// model the click leaves behind rather than the command it returns.
func clickModel(m Model, x, y int) Model {
	next, _ := m.handleClick(clickAt(x, y))
	return next.(Model)
}

// A card's click target is the whole card. Marking each of its lines separately
// collapses the zone onto the last line, which leaves cards that look clickable
// but only respond on the few cells that line's text happens to cover.
func TestCardZoneCoversEveryLineFullWidth(t *testing.T) {
	m := testModel(nil,
		sess("Hello!", "", core.LifecycleIdle, core.AgentNeedsYou, "r"),
		sess("Hello again", "", core.LifecycleIdle, core.AgentNeedsYou, "r"),
	)
	m.width, m.height = 140, 40
	m.layoutSizes()
	rendered(t, m, zoneCard(m.sessions[0].ID))

	widths := columnWidths(m.width)
	rows := len(m.renderCard(m.sessions[0], false, widths[0]-4))

	z := zone.Get(zoneCard(m.sessions[0].ID))
	if z.IsZero() {
		t.Fatal("no zone recorded for the first card")
	}
	if got := z.EndY - z.StartY + 1; got != rows {
		t.Errorf("card zone is %d rows tall, want %d (the card's own height)", got, rows)
	}
	// Two frame cells and two padding cells: the zone should span the rest.
	if got := z.EndX - z.StartX + 1; got != widths[0]-4 {
		t.Errorf("card zone is %d cells wide, want %d (the column interior)", got, widths[0]-4)
	}
}

// Every cell of a card selects it: the far side of the row and the lines below
// the title, not just the title itself.
func TestClickAnywhereOnCardSelectsIt(t *testing.T) {
	a := sess("Hello!", "", core.LifecycleIdle, core.AgentNeedsYou, "r")
	b := sess("Hello again", "", core.LifecycleIdle, core.AgentNeedsYou, "r")
	m := testModel(nil, a, b)
	m.width, m.height = 140, 40
	m.layoutSizes()
	m.selectedID = b.ID
	rendered(t, m, zoneCard(a.ID))

	z := zone.Get(zoneCard(a.ID))
	if z.IsZero() {
		t.Fatal("no zone recorded for the target card")
	}
	for _, pt := range [][2]int{
		{z.StartX, z.StartY},
		{z.EndX, z.StartY},
		{z.StartX, z.EndY},
		{z.EndX, z.EndY},
		{(z.StartX + z.EndX) / 2, (z.StartY + z.EndY) / 2},
	} {
		next, _ := m.handleClick(clickAt(pt[0], pt[1]))
		if got := next.(Model).selectedID; got != a.ID {
			t.Errorf("click at %v selected %q, want %q", pt, got, a.ID)
		}
	}
}

// The input row spans the panel, so clicking the empty space after the hint
// focuses it too.
func TestClickAtEndOfInputRowFocusesInput(t *testing.T) {
	m := testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r"))
	m.width, m.height = 140, 40
	m.layoutSizes()
	rendered(t, m)

	z := zone.Get(zoneInput)
	if z.IsZero() {
		t.Fatal("no zone recorded for the input row")
	}
	if got := z.EndX - z.StartX + 1; got != m.width-4 {
		t.Errorf("input zone is %d cells wide, want %d (the panel interior)", got, m.width-4)
	}
	next, _ := m.handleClick(clickAt(z.EndX, z.EndY))
	if got := next.(Model).focus; got != focusInput {
		t.Errorf("clicking the end of the input row left focus at %d", got)
	}
}

// A card that cannot be drawn whole is dropped and reported by the scrollbar,
// because the box clips by line and half a card carries no click zone.
func TestShortColumnReportsHiddenCardsInsteadOfClippingOne(t *testing.T) {
	var sessions []*core.Session
	for _, id := range []string{"one", "two", "three", "four"} {
		sessions = append(sessions, sess(id, "", core.LifecycleIdle, core.AgentNeedsYou, "r"))
	}
	m := testModel(nil, sessions...)
	m.width, m.height = 140, 40

	// Nine rows of interior is not enough for four cards.
	rows, bar := m.columnRows(m.columns()[core.LifecycleIdle.ColumnIndex()], 0, cardPos{}, columnWidths(m.width)[0], 9)
	body := zoneMarker.ReplaceAllString(strings.Join(rows, "\n"), "")
	if bar == nil {
		t.Fatalf("short column dropped cards without saying so:\n%s", body)
	}
	if bar.Total != len(sessions) {
		t.Errorf("scrollbar counted %d cards, want %d", bar.Total, len(sessions))
	}
	kept := 0
	for _, s := range sessions {
		if strings.Contains(body, s.Title) {
			kept++
		}
	}
	if kept == 0 {
		t.Fatal("short column showed no cards at all")
	}
	if kept == len(sessions) {
		t.Fatal("every card fit; the test no longer exercises overflow")
	}
}
