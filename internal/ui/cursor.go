package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// A block cursor is drawn as reverse video, switched on and off with raw SGR
// rather than a lipgloss style: the captured line carries the agent's own
// colors, and lipgloss terminates a style with a full reset, which would drop
// the color state the rest of the line is still relying on. SGR 27 turns
// reverse off and leaves every other attribute where it was.
const (
	cursorOn  = "\x1b[7m"
	cursorOff = "\x1b[27m"
)

// placeCursor draws the agent's text cursor into a captured pane.
//
// capture-pane returns cells and nothing else, so the cursor -- terminal state,
// not screen content -- is absent from every frame. Agents that draw their own
// composer (Claude Code, Codex) leave the caret to the real terminal cursor, so
// without this the panel shows a prompt you can type into with nothing marking
// where the characters land.
//
// lines are pane rows top-aligned from row 0, and rows/width are the panel's
// interior. The returned slice may grow: capture-pane drops trailing blank rows,
// and a cursor parked below the last line of output needs its row back before
// anything can be drawn on it.
//
// A cursor outside the panel's interior is dropped rather than clipped later.
// Clipping a row cuts it by runes, which can leave the reverse-video switch on
// with nothing to turn it off, and one dangling switch reverses the rest of the
// frame.
func placeCursor(lines []string, cur tmuxx.Cursor, rows, width int) []string {
	if !cur.Visible || cur.X < 0 || cur.Y < 0 || cur.Y >= rows || cur.X >= width {
		return lines
	}
	for len(lines) <= cur.Y {
		lines = append(lines, "")
	}
	lines[cur.Y] = drawCursor(lines[cur.Y], cur.X)
	return lines
}

// drawCursor puts a block cursor over the cell at column x of one styled line.
//
// It walks cells rather than bytes because the line is full of the escape
// sequences capture-pane -e emits: a byte offset into it has nothing to do with
// the column the terminal reports.
func drawCursor(line string, x int) string {
	col := 0
	var state byte
	for i := 0; i < len(line); {
		seq, w, n, next := ansi.DecodeSequence(line[i:], state, nil)
		if n <= 0 {
			break
		}
		state = next
		// Escape sequences are zero-width, so they never hold the cursor; the
		// first cell reaching past x is the one under it, which is also how the
		// far half of a wide character resolves to the whole character.
		if w > 0 {
			if col+w > x {
				return line[:i] + cursorOn + seq + cursorOff + line[i+n:]
			}
			col += w
		}
		i += n
	}
	// Past the end of the text: an empty row, or the cell just after the last
	// character typed, which is where a composer's caret usually sits.
	return line + strings.Repeat(" ", x-col) + cursorOn + " " + cursorOff
}
