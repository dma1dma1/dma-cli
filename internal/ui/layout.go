package ui

import (
	"github.com/dma1dma1/dma-cli/internal/core"
)

// lane is one swimlane: a user-defined group, split across the four columns.
// Swimlanes are always groups, never repos.
type lane struct {
	Group     string
	Collapsed bool
	Columns   [4][]*core.Session
	All       []*core.Session
	Rollup    core.Rollup
}

// Label is the group's display name; the empty group renders last as
// "ungrouped".
func (l lane) Label() string {
	if l.Group == "" {
		return "ungrouped"
	}
	return l.Group
}

type layout struct {
	Lanes []lane
}

// buildLayout groups sessions into swimlanes and columns, applying the repo
// filter and per-column sort.
func buildLayout(cfg *core.Config, sessions []*core.Session, collapsed map[string]bool, repoFilter string) layout {
	var visible []*core.Session
	for _, s := range sessions {
		if repoFilter != "" && s.RepoID != repoFilter {
			continue
		}
		visible = append(visible, s)
	}

	order := core.GroupOrder(cfg, visible)
	var lanes []lane
	for _, g := range order {
		var members []*core.Session
		for _, s := range visible {
			if s.Group == g {
				members = append(members, s)
			}
		}
		if len(members) == 0 && g == "" {
			// Do not render an empty ungrouped lane just because it exists.
			continue
		}
		l := lane{Group: g, Collapsed: collapsed[g], All: members, Rollup: core.RollupOf(members)}
		for _, s := range members {
			idx := s.Lifecycle.ColumnIndex()
			l.Columns[idx] = append(l.Columns[idx], s)
		}
		for i := range l.Columns {
			core.SortColumn(l.Columns[i])
		}
		lanes = append(lanes, l)
	}
	return layout{Lanes: lanes}
}

// pos is a resolved cursor position within the layout.
type pos struct {
	Lane, Col, Row int
	OK             bool
}

// find locates a session in the layout. Selection is anchored to a session id
// and re-resolved every frame, so re-sorting a column never drags the cursor
// onto a different card.
func (ly layout) find(id string) pos {
	for li, l := range ly.Lanes {
		for c := 0; c < 4; c++ {
			for r, s := range l.Columns[c] {
				if s.ID == id {
					return pos{Lane: li, Col: c, Row: r, OK: true}
				}
			}
		}
	}
	return pos{}
}

func (ly layout) at(p pos) *core.Session {
	if p.Lane < 0 || p.Lane >= len(ly.Lanes) {
		return nil
	}
	l := ly.Lanes[p.Lane]
	if p.Col < 0 || p.Col > 3 {
		return nil
	}
	col := l.Columns[p.Col]
	if p.Row < 0 || p.Row >= len(col) {
		return nil
	}
	return col[p.Row]
}

// first returns the first selectable session, scanning lanes then columns.
func (ly layout) first() *core.Session {
	for _, l := range ly.Lanes {
		if l.Collapsed {
			continue
		}
		for c := 0; c < 4; c++ {
			if len(l.Columns[c]) > 0 {
				return l.Columns[c][0]
			}
		}
	}
	// Everything is collapsed: fall back to any session so actions still work.
	for _, l := range ly.Lanes {
		for c := 0; c < 4; c++ {
			if len(l.Columns[c]) > 0 {
				return l.Columns[c][0]
			}
		}
	}
	return nil
}

// moveHorizontal steps to the nearest non-empty column in the given direction
// within the same lane, so pressing l never lands the cursor on nothing.
func (ly layout) moveHorizontal(from pos, dir int) *core.Session {
	if !from.OK {
		return nil
	}
	l := ly.Lanes[from.Lane]
	for c := from.Col + dir; c >= 0 && c <= 3; c += dir {
		if len(l.Columns[c]) > 0 {
			row := min(from.Row, len(l.Columns[c])-1)
			return l.Columns[c][row]
		}
	}
	return nil
}

// moveVertical steps within the current column, spilling into the same column
// of the next or previous lane when it runs off the end.
func (ly layout) moveVertical(from pos, dir int, collapsed map[string]bool) *core.Session {
	if !from.OK {
		return nil
	}
	col := ly.Lanes[from.Lane].Columns[from.Col]
	next := from.Row + dir
	if next >= 0 && next < len(col) {
		return col[next]
	}
	for li := from.Lane + dir; li >= 0 && li < len(ly.Lanes); li += dir {
		l := ly.Lanes[li]
		if l.Collapsed {
			continue
		}
		c := l.Columns[from.Col]
		if len(c) == 0 {
			continue
		}
		if dir > 0 {
			return c[0]
		}
		return c[len(c)-1]
	}
	return nil
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
