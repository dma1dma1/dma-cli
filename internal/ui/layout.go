package ui

import (
	"github.com/dma1dma1/dma-cli/internal/core"
)

// cardPos is a resolved cursor position on the board.
type cardPos struct {
	col, row int
	ok       bool
}

// visible applies the repo and project filters. Filtering, rather than
// swimlanes, is how the board narrows: with four bordered columns there is no
// room for a second visual axis.
func (m Model) visible() []*core.Session {
	var out []*core.Session
	for _, s := range m.sessions {
		if m.repoFilter != "" && s.RepoID != m.repoFilter {
			continue
		}
		if m.projectFilter != "" && s.Group != m.projectFilter {
			continue
		}
		out = append(out, s)
	}
	return out
}

// columns buckets the visible sessions into the four columns, sorted so the
// sessions wanting attention are at the top of each.
func (m Model) columns() [4][]*core.Session {
	var cols [4][]*core.Session
	for _, s := range m.visible() {
		cols[s.Lifecycle.ColumnIndex()] = append(cols[s.Lifecycle.ColumnIndex()], s)
	}
	for i := range cols {
		core.SortColumn(cols[i])
	}
	return cols
}

// findSelected re-resolves the cursor from the selected session id every frame.
//
// Anchoring to the id rather than to a coordinate is what makes agent-driven
// columns safe: when a session stops working and its card moves from active to
// idle, the cursor follows the card instead of landing on whichever card took
// its place.
func (m Model) findSelected() cardPos {
	cols := m.columns()
	for c := range cols {
		for r, s := range cols[c] {
			if s.ID == m.selectedID {
				return cardPos{col: c, row: r, ok: true}
			}
		}
	}
	return cardPos{}
}

func (m Model) sessionAt(p cardPos) *core.Session {
	if !p.ok {
		return nil
	}
	cols := m.columns()
	if p.col < 0 || p.col > 3 || p.row < 0 || p.row >= len(cols[p.col]) {
		return nil
	}
	return cols[p.col][p.row]
}

// firstSession returns something selectable, scanning columns left to right.
func (m Model) firstSession() *core.Session {
	cols := m.columns()
	for c := range cols {
		if len(cols[c]) > 0 {
			return cols[c][0]
		}
	}
	return nil
}

// moveH steps to the nearest occupied column in a direction, so pressing l
// never parks the cursor on an empty column.
func (m Model) moveH(dir int) *core.Session {
	p := m.findSelected()
	if !p.ok {
		return m.firstSession()
	}
	cols := m.columns()
	for c := p.col + dir; c >= 0 && c <= 3; c += dir {
		if len(cols[c]) > 0 {
			return cols[c][min(p.row, len(cols[c])-1)]
		}
	}
	return nil
}

// moveV steps within the current column.
func (m Model) moveV(dir int) *core.Session {
	p := m.findSelected()
	if !p.ok {
		return m.firstSession()
	}
	col := m.columns()[p.col]
	next := p.row + dir
	if next < 0 || next >= len(col) {
		return nil
	}
	return col[next]
}

// flatOrder is the board read left to right, top to bottom, for stepping
// through every session regardless of column.
func (m Model) flatOrder() []*core.Session {
	var flat []*core.Session
	cols := m.columns()
	for c := range cols {
		flat = append(flat, cols[c]...)
	}
	return flat
}

func (m Model) stepSession(dir int) *core.Session {
	flat := m.flatOrder()
	if len(flat) == 0 {
		return nil
	}
	idx := -1
	for i, s := range flat {
		if s.ID == m.selectedID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return flat[0]
	}
	next := idx + dir
	if next < 0 || next >= len(flat) {
		return nil
	}
	return flat[next]
}

// projects lists the project labels in use, for the picker.
func (m Model) projects() []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range m.cfg.Groups {
		if g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	for _, s := range m.sessions {
		if s.Group != "" && !seen[s.Group] {
			seen[s.Group] = true
			out = append(out, s.Group)
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
