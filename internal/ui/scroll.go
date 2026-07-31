package ui

import (
	"github.com/dma1dma1/dma-cli/internal/core"
)

// syncColumnScroll settles every column's offset against the board as it now
// stands: offsets are pulled back into range, and the column holding the cursor
// is scrolled the least distance that brings the selected card on screen.
//
// The board is capped at a share of the window, so a column with more cards than
// fit is the normal case rather than the pathological one. Scrolling it is what
// keeps those cards reachable without the columns growing until the agent's panel
// is a sliver.
func (m *Model) syncColumnScroll() {
	boardH, _, _ := m.splitHeights()
	if boardH <= 0 {
		// No board on screen: park every column at the top, so growing the window
		// back opens on the cards the columns start with rather than on an offset
		// left over from a layout nobody can see.
		m.colScroll, m.scrollPinned = [4]int{}, [4]bool{}
		return
	}

	widths := columnWidths(m.contentWidth())
	avail := max(boardH-2, 1) // two rows go to the column frame
	cols := m.columns()
	sel := m.findSelected()

	for i := range cols {
		off := clamp(m.colScroll[i], 0, m.maxScroll(cols[i], widths[i], avail))
		if sel.ok && sel.col == i && !m.scrollPinned[i] {
			off = m.scrollToRow(cols[i], off, sel.row, widths[i], avail)
		}
		m.colScroll[i] = off
	}
}

// scrollToRow is the smallest offset at or after the current one that draws row
// whole, or row itself when the column is already scrolled past it.
//
// It moves as little as possible so that stepping down a long column advances it
// a card at a time, rather than re-centering and taking the cards either side of
// the cursor with it.
func (m Model) scrollToRow(col []*core.Session, offset, row, width, avail int) int {
	if row <= offset {
		return max(row, 0)
	}
	for offset < row && offset+m.columnFit(col, offset, width, avail) <= row {
		offset++
	}
	return offset
}

// scrollColumn moves one column by delta cards, and pins it there.
func (m *Model) scrollColumn(colIndex, delta int) {
	boardH, _, _ := m.splitHeights()
	if boardH <= 0 || colIndex < 0 || colIndex > 3 {
		return
	}
	col := m.columns()[colIndex]
	width := columnWidths(m.contentWidth())[colIndex]
	avail := max(boardH-2, 1)

	off := clamp(m.colScroll[colIndex]+delta, 0, m.maxScroll(col, width, avail))
	m.colScroll[colIndex] = off
	// Back at the top there is nothing left to hold: an unpinned column at zero is
	// the same picture, and it goes back to following the cursor.
	m.scrollPinned[colIndex] = off > 0
}

// unpinScroll hands the columns back to the cursor. Every deliberate change of
// selection calls it: the card you just picked has to be visible, whatever the
// wheel was doing before.
func (m *Model) unpinScroll() { m.scrollPinned = [4]bool{} }
