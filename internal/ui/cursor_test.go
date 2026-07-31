package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// TestDrawCursorPlacesBlock covers the cell arithmetic, which is the whole
// difficulty: a captured line is mostly escape sequences, so the column tmux
// reports has no relation to a byte offset into it.
func TestDrawCursorPlacesBlock(t *testing.T) {
	tests := []struct {
		name string
		line string
		x    int
		want string
	}{
		{"start of a plain line", "abc", 0, cursorOn + "a" + cursorOff + "bc"},
		{"middle of a plain line", "abc", 1, "a" + cursorOn + "b" + cursorOff + "c"},
		{"just past the last character", "abc", 3, "abc" + cursorOn + " " + cursorOff},
		{"a gap before the cursor", "abc", 5, "abc  " + cursorOn + " " + cursorOff},
		{"an empty row", "", 2, "  " + cursorOn + " " + cursorOff},
		// The composer case from the screenshot: a styled prompt with the caret on
		// the cell after it. The agent's own colors must survive intact.
		{
			"after a styled prompt",
			"\x1b[34m❯\x1b[0m ",
			2,
			"\x1b[34m❯\x1b[0m " + cursorOn + " " + cursorOff,
		},
		{
			"inside a styled run",
			"\x1b[34mhi\x1b[0m",
			1,
			"\x1b[34mh" + cursorOn + "i" + cursorOff + "\x1b[0m",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := drawCursor(tc.line, tc.x); got != tc.want {
				t.Errorf("drawCursor(%q, %d)\n got %q\nwant %q", tc.line, tc.x, got, tc.want)
			}
		})
	}
}

// TestDrawCursorKeepsWidth is the invariant the panel depends on: a cursor is an
// attribute of a cell, not an extra cell, so a line it lands inside must occupy
// exactly the display width it did before. A widened row would push the box's
// right border out and skew every row below it.
func TestDrawCursorKeepsWidth(t *testing.T) {
	for _, line := range []string{
		"plain text",
		"\x1b[1m\x1b[31mstyled\x1b[0m text",
		"wide 字 char",
	} {
		before := lipgloss.Width(line)
		for x := range before {
			if got := lipgloss.Width(drawCursor(line, x)); got != before {
				t.Errorf("drawCursor(%q, %d) is %d cells wide, want %d", line, x, got, before)
			}
		}
	}
}

// TestDrawCursorLeavesReverseOff guards against the failure that would be worst
// on screen: reverse video switched on and never off reverses the rest of the
// frame, not just the cell.
func TestDrawCursorLeavesReverseOff(t *testing.T) {
	for _, line := range []string{"abc", "\x1b[31mabc\x1b[0m", ""} {
		for x := range 6 {
			got := drawCursor(line, x)
			if strings.Count(got, cursorOn) != 1 || strings.Count(got, cursorOff) != 1 {
				t.Errorf("drawCursor(%q, %d) = %q: want exactly one on and one off", line, x, got)
			}
			if strings.Index(got, cursorOn) > strings.Index(got, cursorOff) {
				t.Errorf("drawCursor(%q, %d) = %q: off comes before on", line, x, got)
			}
		}
	}
}

func TestPlaceCursor(t *testing.T) {
	lines := []string{"one", "two"}

	t.Run("hidden cursor draws nothing", func(t *testing.T) {
		got := placeCursor(lines, tmuxx.Cursor{X: 1, Y: 0}, 10, 40)
		if got[0] != "one" {
			t.Errorf("row 0 is %q, want it untouched", got[0])
		}
	})

	// capture-pane returns no trailing blank rows, so the row a caret sits on may
	// not exist yet -- which is exactly the case of an agent whose composer is the
	// last thing on an otherwise empty screen.
	t.Run("blank rows below the output come back", func(t *testing.T) {
		got := placeCursor(lines, tmuxx.Cursor{X: 0, Y: 4, Visible: true}, 10, 40)
		if len(got) != 5 {
			t.Fatalf("got %d rows, want 5", len(got))
		}
		if want := cursorOn + " " + cursorOff; got[4] != want {
			t.Errorf("row 4 is %q, want %q", got[4], want)
		}
	})

	// Out of the panel's interior it is dropped rather than clipped: clipping cuts
	// by runes and can strip the reverse-off while leaving the reverse-on.
	t.Run("outside the panel draws nothing", func(t *testing.T) {
		for _, cur := range []tmuxx.Cursor{
			{X: 0, Y: 10, Visible: true}, // below the last visible row
			{X: 40, Y: 0, Visible: true}, // past the right edge
			{X: -1, Y: 0, Visible: true},
		} {
			got := placeCursor(lines, cur, 10, 40)
			if len(got) != 2 || got[0] != "one" || got[1] != "two" {
				t.Errorf("placeCursor(%+v) changed the rows: %q", cur, got)
			}
		}
	})
}
