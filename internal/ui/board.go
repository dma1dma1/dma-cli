package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

const (
	colGap       = 1
	collapsedCol = 12 // width of a non-focused, collapsed column
	minCardWidth = 16
)

// columnWidths computes per-column widths. A non-focused column can be
// collapsed to its header and a count, giving the focused column room.
func (m Model) columnWidths(total int) [4]int {
	var w [4]int
	avail := total - colGap*3
	if avail < 4*minCardWidth {
		// Terminal is too narrow for four full columns: collapse the ones the
		// cursor is not in rather than truncating every card into uselessness.
		focused := m.selCol()
		rest := avail - collapsedCol*3
		if rest < minCardWidth {
			rest = minCardWidth
		}
		for i := range w {
			if i == focused {
				w[i] = rest
			} else {
				w[i] = collapsedCol
			}
		}
		return w
	}
	base := avail / 4
	extra := avail % 4
	for i := range w {
		w[i] = base
		if i < extra {
			w[i]++
		}
	}
	return w
}

// selCol is the column the cursor currently sits in.
func (m Model) selCol() int {
	p := m.layout.find(m.selectedID)
	if p.OK {
		return p.Col
	}
	return 0
}

func (m Model) viewBoard() string {
	st := m.styles
	widths := m.columnWidths(m.width)

	var b strings.Builder
	b.WriteString(m.renderColumnHeaders(widths))
	b.WriteString("\n")

	selPos := m.layout.find(m.selectedID)

	bodyHeight := m.height - 1 /*headers*/ - 1 /*bottom bar*/ - 1 /*status*/
	rendered := 0

	for li, l := range m.layout.Lanes {
		if rendered >= bodyHeight && bodyHeight > 0 {
			break
		}
		header := m.renderGroupHeader(l, li == selPos.Lane && selPos.OK)
		b.WriteString(header)
		b.WriteString("\n")
		rendered++

		if l.Collapsed {
			continue
		}

		cols := make([]string, 4)
		for c := 0; c < 4; c++ {
			var cards []string
			for r, s := range l.Columns[c] {
				sel := selPos.OK && selPos.Lane == li && selPos.Col == c && selPos.Row == r
				if widths[c] <= collapsedCol {
					cards = append(cards, m.renderMiniCard(s, sel, widths[c]))
					continue
				}
				cards = append(cards, m.renderCard(s, sel, widths[c]))
			}
			cols[c] = lipgloss.NewStyle().Width(widths[c]).
				Render(strings.Join(cards, "\n"))
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top,
			cols[0], strings.Repeat(" ", colGap),
			cols[1], strings.Repeat(" ", colGap),
			cols[2], strings.Repeat(" ", colGap),
			cols[3])
		b.WriteString(row)
		b.WriteString("\n")
		rendered += lipgloss.Height(row)
	}

	if len(m.layout.Lanes) == 0 {
		b.WriteString("\n")
		b.WriteString(st.Status.Render("  No sessions yet. Press n to create one."))
		b.WriteString("\n")
	}

	return b.String()
}

// renderMiniCard is the collapsed-column representation: enough to see that
// something is there and whether it needs attention.
func (m Model) renderMiniCard(s *core.Session, selected bool, width int) string {
	st := m.styles
	style := st.Card
	switch {
	case s.AgentState == core.AgentNeedsYou:
		style = st.CardAttention
	case selected:
		style = st.CardSelected
	}
	if selected {
		style = style.BorderForeground(st.P.Selected)
	}
	body := truncate(s.Title, max(width-4, 3))
	return zone.Mark(zoneCard(s.ID), style.Width(max(width-2, 3)).Render(body))
}

func (m Model) renderColumnHeaders(widths [4]int) string {
	st := m.styles
	focused := m.selCol()
	parts := make([]string, 0, 7)
	for i, c := range core.Columns {
		count := m.countIn(c)
		label := fmt.Sprintf("%s (%d)", c.Title(), count)
		style := st.ColumnHeader
		if i == focused {
			style = st.ColumnHeaderFocused
		}
		cell := style.Width(widths[i]).Render(truncate(label, max(widths[i]-2, 3)))
		parts = append(parts, zone.Mark(zoneColumn(i), cell))
		if i < 3 {
			parts = append(parts, strings.Repeat(" ", colGap))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Bottom, parts...)
}

func (m Model) countIn(c core.Lifecycle) int {
	n := 0
	for _, l := range m.layout.Lanes {
		n += len(l.Columns[c.ColumnIndex()])
	}
	return n
}

// renderGroupHeader draws the swimlane header and its rollup.
//
// The rollup stays visible when the group is collapsed: collapsing must never
// hide the fact that something needs attention.
func (m Model) renderGroupHeader(l lane, focused bool) string {
	st := m.styles
	marker := "▾"
	if l.Collapsed {
		marker = "▸"
	}
	name := st.GroupHeader.Render(l.Label())
	if focused {
		name = lipgloss.NewStyle().Foreground(st.P.Selected).Bold(true).Render(l.Label())
	}
	head := fmt.Sprintf("%s %s", marker, name)
	if r := rollupText(l.Rollup); r != "" {
		head += st.GroupRollup.Render(" · " + r)
	}
	return zone.Mark(zoneGroup(l.Group), head)
}

func rollupText(r core.Rollup) string {
	if r.Total == 0 {
		return "empty"
	}
	var parts []string
	if r.Working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", r.Working))
	}
	if r.NeedsYou > 0 {
		parts = append(parts, fmt.Sprintf("%d needs you", r.NeedsYou))
	}
	if r.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", r.Done))
	}
	if r.Idle > 0 {
		parts = append(parts, fmt.Sprintf("%d idle", r.Idle))
	}
	return strings.Join(parts, ", ")
}
