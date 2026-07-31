package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// ansiSeq matches the SGR sequences lipgloss emits, so a test can ask where text
// actually lands on screen.
var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(line string) string { return ansiSeq.ReplaceAllString(line, "") }

// hasFill reports whether a row carries the selection background. Asserting on
// the escape sequence is the only way to see a fill from a test: the padding it
// covers is indistinguishable from unstyled spaces once the codes are stripped.
func hasFill(line string) bool { return strings.Contains(line, "48;5;237") }

// A board of cards that differ only in their text needs the selected one to
// stand out on sight, since it is the card whose session the panel below is
// showing. Every one of its rows is filled, not just the title: a fill that
// stops after one line reads as an underline, not as a selected card.
func TestSelectedCardIsFilledOnEveryRow(t *testing.T) {
	s := sess("Hello there", "backend", core.LifecycleIdle, core.AgentNeedsYou, "devops-copilot")
	s.Branch = "hello-there"
	s.AgentStateDetail = "approve the edit"
	s.DiffAdded, s.DiffRemoved = 12, 3
	m := testModel(nil, s)

	lines := m.cardLines(s, true, 30)
	if len(lines) < 5 {
		t.Fatalf("card has %d rows, expected the detail and diff rows too", len(lines))
	}
	for i, l := range lines {
		if !hasFill(l) {
			t.Errorf("selected card row %d is not filled: %q", i, l)
		}
		if got := lipglossWidth(l); got != 30 {
			t.Errorf("selected card row %d is %d cells wide, want 30", i, got)
		}
	}

	// The fill has to survive to the end of the row. It is applied per segment
	// because the styles inside a row each end with a reset, and a single
	// background wrapped around the whole row would stop at the first one.
	for i, l := range lines {
		tail := l[strings.LastIndex(l, "\x1b[m"):]
		if body := l[:strings.LastIndex(l, "\x1b[m")]; !strings.Contains(
			body[strings.LastIndex(body, "\x1b["):], "48;5;237") {
			t.Errorf("row %d stops filling before the end of the line: %q (tail %q)", i, l, tail)
		}
	}

	for i, l := range m.cardLines(s, false, 30) {
		if hasFill(l) {
			t.Errorf("unselected card row %d is filled: %q", i, l)
		}
	}
}

// The fill is the primary signal but not the only one: a low-contrast theme or a
// screenshot can swallow a background, so the caret and the heavier accent bar
// carry the same fact in shape alone.
func TestSelectedCardIsMarkedInShapeAsWellAsColor(t *testing.T) {
	s := sess("Hello there", "", core.LifecycleIdle, core.AgentDone, "r")
	m := testModel(nil, s)

	sel := m.cardLines(s, true, 30)
	un := m.cardLines(s, false, 30)

	if !strings.Contains(sel[0], "▸") {
		t.Errorf("selected title row has no caret: %q", sel[0])
	}
	if strings.Contains(un[0], "▸") {
		t.Errorf("unselected title row has a caret: %q", un[0])
	}
	if !strings.Contains(sel[0], "┃") || !strings.Contains(un[0], "▌") {
		t.Errorf("accent bar does not thicken on selection: selected %q, unselected %q", sel[0], un[0])
	}
}

// The dim rows are chosen to sit just above the background, so on the fill they
// would be the rows that vanish -- on the one card the fill exists to highlight.
func TestSelectedCardKeepsItsDimRowsReadable(t *testing.T) {
	s := sess("Hello there", "", core.LifecycleIdle, core.AgentNeedsYou, "r")
	s.Branch = "hello-there"
	s.AgentStateDetail = "approve the edit"
	m := testModel(nil, s)

	faint := "38;5;239" // Palette.Faint, which is a shade off the background
	for i, l := range m.cardLines(s, true, 30) {
		if strings.Contains(l, faint) {
			t.Errorf("selected card row %d uses faint text on the fill: %q", i, l)
		}
	}
}

// Selecting a card must not move its text. The caret's cells are reserved on
// every card, so walking the cursor down a column changes the marks and nothing
// else -- a title that slid sideways under the cursor would draw the eye to the
// movement rather than to the mark.
func TestSelectionDoesNotMoveCardText(t *testing.T) {
	s := sess("Hello there", "backend", core.LifecycleIdle, core.AgentNeedsYou, "devops-copilot")
	s.Branch = "hello-there"
	s.AgentStateDetail = "approve the edit"
	m := testModel(nil, s)

	sel, un := m.cardLines(s, true, 30), m.cardLines(s, false, 30)
	for i := range sel {
		// Both marks stand in cells that exist either way -- the caret in the
		// reserved gutter, the heavier bar in the bar's own cell -- so undoing them
		// should leave the two rows identical, cell for cell.
		unmarked := strings.NewReplacer("┃", "▌", "▸", " ").Replace(plain(sel[i]))
		if want := plain(un[i]); unmarked != want {
			t.Errorf("row %d moves on selection:\nselected   %q\nunselected %q", i, plain(sel[i]), want)
		}
	}
}

// The width budget has to account for the caret gutter, or a card would render
// wider than the column interior and push the columns beside it off their
// alignment.
func TestSelectionDoesNotChangeCardWidth(t *testing.T) {
	s := sess("A title long enough to need truncating", "grp", core.LifecycleIdle, core.AgentWorking, "r")
	s.Branch = "some-fairly-long-branch-name"
	m := testModel(nil, s)

	for _, w := range []int{12, 20, 30, 44} {
		sel, un := m.cardLines(s, true, w), m.cardLines(s, false, w)
		if len(sel) != len(un) {
			t.Errorf("width %d: selected card has %d rows, unselected %d", w, len(sel), len(un))
		}
		for i := range sel {
			if got := lipglossWidth(sel[i]); got != w {
				t.Errorf("width %d: selected row %d is %d cells", w, i, got)
			}
			if got := lipglossWidth(un[i]); got != w {
				t.Errorf("width %d: unselected row %d is %d cells", w, i, got)
			}
		}
	}
}

// The panel names the session it is showing in the same color the selected card
// wears, so the pair reads as one thing.
func TestPanelTitleMatchesTheSelectedCardsColor(t *testing.T) {
	s := sess("Hello there", "", core.LifecycleIdle, core.AgentDone, "r")
	m := testModel(nil, s)
	m.layoutSizes()

	panel := m.viewPanel(12)
	if !strings.Contains(panel, "38;5;117") {
		t.Errorf("panel title is not in the selection color:\n%s", panel)
	}
	if !strings.Contains(m.cardLines(s, true, 30)[0], "38;5;117") {
		t.Error("selected card title is not in the selection color")
	}
}
