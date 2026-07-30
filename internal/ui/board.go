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
	minColWidth  = 18
	minBoardRows = 7
)

// columnWidths divides the width into four, distributing the remainder so the
// row always fills the terminal exactly.
func columnWidths(total int) [4]int {
	var w [4]int
	avail := max(total-colGap*3, 4*minColWidth)
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

// viewBoard renders the four columns. Cards live inside the column frames as
// rows with a colored accent bar rather than nested boxes -- borders inside
// borders read as noise, and the bar carries the state signal more cheaply.
func (m Model) viewBoard(height int) string {
	widths := columnWidths(m.width)
	cols := m.columns()
	selPos := m.findSelected()

	rendered := make([]string, 4)
	for i := range core.Columns {
		var rows []string
		for j, s := range cols[i] {
			rows = append(rows, m.renderCard(s, selPos.col == i && selPos.row == j, widths[i]-4)...)
			rows = append(rows, "")
		}
		if len(cols[i]) == 0 {
			rows = []string{"", m.styles.Faint.Render("  —")}
		}

		b := Box{
			Title:    core.Columns[i].Title(),
			Subtitle: core.Columns[i].Subtitle(),
			Accent:   m.styles.columnAccent(core.Columns[i]),
			Border:   m.styles.P.Border,
			Width:    widths[i],
			Height:   height,
			Focused:  m.focus == focusBoard && selPos.col == i,
		}
		if n := len(cols[i]); n > 0 {
			b.Title = fmt.Sprintf("%s %d", b.Title, n)
		}
		rendered[i] = zone.Mark(zoneColumn(i), b.Render(strings.Join(rows, "\n")))
	}

	gap := strings.Repeat(" ", colGap)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		rendered[0], gap, rendered[1], gap, rendered[2], gap, rendered[3])
}

// renderCard returns the lines for one card, sized to the column interior.
func (m Model) renderCard(s *core.Session, selected bool, width int) []string {
	st := m.styles
	accent := st.agentColor(s.AgentState)

	bar := lipgloss.NewStyle().Foreground(accent).Render("▌")
	if selected {
		bar = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("┃")
	}

	title := st.CardTitle
	if selected {
		title = st.CardTitleSelected
	}
	textW := max(width-2, 4)

	var lines []string
	push := func(prefix, content string) {
		lines = append(lines, prefix+" "+content)
	}

	push(bar, title.Render(truncate(s.Title, textW)))

	// The repo handle only earns space once more than one repo is registered.
	if m.cfg.MultiRepo() {
		push(bar, st.RepoTag.Render(truncate(s.RepoID, textW)))
	}
	// Likewise the project, and only when the board is not already filtered to
	// one -- otherwise every card would repeat the same label.
	if s.Group != "" && m.projectFilter == "" {
		push(bar, st.ProjectTag.Render(truncate("◆ "+s.Group, textW)))
	}

	push(bar, st.Meta.Render(truncate(m.branchOrPR(s), textW)))
	push(bar, st.badgeStyle(s).Render(truncate(st.badgeText(s), textW)))

	if s.AgentState == core.AgentNeedsYou && s.AgentStateDetail != "" {
		push(bar, st.Faint.Render(truncate(s.AgentStateDetail, textW)))
	}

	if s.DiffAdded > 0 || s.DiffRemoved > 0 {
		stat := lipgloss.NewStyle().Foreground(st.P.Success).Render(fmt.Sprintf("+%d", s.DiffAdded)) +
			" " + lipgloss.NewStyle().Foreground(st.P.Danger).Render(fmt.Sprintf("−%d", s.DiffRemoved))
		push(bar, stat)
	}
	if !s.TmuxAlive {
		push(bar, st.Faint.Render(truncate("⚠ not running", textW)))
	}

	// The whole card is one click target, so a mis-aimed click on any of its
	// lines still selects the right session.
	for i := range lines {
		lines[i] = zone.Mark(zoneCard(s.ID), lines[i])
	}
	return lines
}

// branchOrPR shows the PR and its most urgent fact once one exists, and the
// branch name before that.
func (m Model) branchOrPR(s *core.Session) string {
	if !s.HasPR() {
		return s.Branch
	}
	label := fmt.Sprintf("#%d", s.PRNumber)
	// Mergeable first: conflicts matter most and matter earliest. Then CI, then
	// review, then draft.
	switch {
	case s.PRMergeable == core.MergeConflicts:
		return label + " ⚠ conflicts"
	case s.PRState == core.PRClosed:
		return label + " closed"
	case s.PRState == core.PRMerged:
		return label + " merged"
	}
	switch s.PRCI {
	case core.CIFail:
		return label + " ✗ ci"
	case core.CIPending:
		return label + " ◌ ci"
	case core.CIPass:
		switch s.PRReview {
		case core.ReviewChangesRequested:
			return label + " ✓ ci ⟲ changes"
		case core.ReviewApproved:
			return label + " ✓ ci ✓ approved"
		}
		return label + " ✓ ci"
	}
	if s.PRState == core.PRDraft {
		return label + " draft"
	}
	return label
}

// truncate cuts a display string to n cells, appending an ellipsis. It operates
// on plain text; style the result, not the input.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		cand := string(runes) + "…"
		if lipgloss.Width(cand) <= n {
			return cand
		}
		runes = runes[:len(runes)-1]
	}
	return ""
}
