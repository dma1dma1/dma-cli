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
	minBoardRows = 7
)

// columnWidths divides the width into four, distributing the remainder so the
// row always fills the width exactly.
//
// There is no minimum: a column narrower than its cards truncates text, but a
// column that refuses to shrink pushes the fourth column off the screen, and a
// board you cannot see all of is worse than one you have to read carefully.
func columnWidths(total int) [4]int {
	var w [4]int
	avail := max(total-colGap*3, 4)
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

// boardContentHeight is the number of rows the columns need to show every card
// in the tallest one, frame included. The board is sized to this rather than to
// a share of the screen: rows beyond it would draw empty column.
//
// It is what the columns want, not what they get -- baseHeights caps it, and the
// columns scroll past the cap.
func (m Model) boardContentHeight() int {
	widths := columnWidths(m.contentWidth())
	cols := m.columns()
	tallest := 2 // the "—" placeholder an empty column shows
	for i := range cols {
		rows := 0
		for _, s := range cols[i] {
			rows += m.cardHeight(s, widths[i])
		}
		tallest = max(tallest, rows)
	}
	return tallest + 2 // the frame
}

// cardHeight is the rows one card occupies in a column of the given outer width,
// the blank line that separates it from the next included.
func (m Model) cardHeight(s *core.Session, width int) int {
	return len(m.cardLines(s, false, width-4)) + 1
}

// viewBoard renders the four columns. Cards live inside the column frames as
// rows with a colored accent bar rather than nested boxes -- borders inside
// borders read as noise, and the bar carries the state signal more cheaply.
func (m Model) viewBoard(height int) string {
	widths := columnWidths(m.contentWidth())
	cols := m.columns()
	selPos := m.findSelected()

	rendered := make([]string, 4)
	for i := range core.Columns {
		rows, bar := m.columnRows(cols[i], i, selPos, widths[i], height)

		b := Box{
			Title:    core.Columns[i].Title(),
			Subtitle: core.Columns[i].Subtitle(),
			Accent:   m.styles.columnAccent(core.Columns[i]),
			Border:   m.styles.P.Border,
			Width:    widths[i],
			Height:   height,
			Focused:  m.focus == focusBoard && selPos.col == i,
			Scroll:   bar,
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

// columnRows stacks one column's cards from its scroll offset, keeping only the
// ones that fit whole, and returns the scrollbar for the frame to draw when some
// were left off.
//
// The frame carries the overflow, not the column: a card counted off in words
// ("3 more") says something is missing without saying the column is a thing you
// can scroll, and it spends an interior row saying it. The bar rides in the
// frame's padding, so that row goes to a card instead.
//
// Cards are dropped rather than clipped mid-card because the box clips by line:
// a card cut in half loses the end of its click zone, which leaves it visible
// but unclickable. That is also why the column scrolls by whole cards rather
// than by rows.
func (m Model) columnRows(col []*core.Session, colIndex int, sel cardPos, width, height int) ([]string, *Scrollbar) {
	if len(col) == 0 {
		return []string{"", m.styles.Faint.Render("  —")}, nil
	}

	avail := max(height-2, 1) // two rows go to the frame
	off := m.scrollOffset(col, colIndex, width, avail)
	shown := m.columnFit(col, off, width, avail)

	var rows []string
	for j := off; j < off+shown; j++ {
		rows = append(rows, m.renderCard(col[j], sel.col == colIndex && sel.row == j, width-4)...)
		rows = append(rows, "")
	}
	if shown >= len(col) {
		return rows, nil
	}
	return rows, &Scrollbar{
		Total:   len(col),
		Visible: shown,
		Offset:  off,
		Track:   m.styles.P.Border,
		Thumb:   m.styles.P.Muted,
	}
}

// columnFit reports how many of col's cards, starting at offset, can be drawn
// whole in an interior avail rows tall. It is the one place that decides what a
// column shows: the renderer and the scroll arithmetic both go through it, so
// they cannot disagree about which card is on screen.
func (m Model) columnFit(col []*core.Session, offset, width, avail int) int {
	used, shown := 0, 0
	for j := offset; j < len(col); j++ {
		h := m.cardHeight(col[j], width)
		// The first card is kept even when it cannot fit: a clipped card beats an
		// empty column.
		if used+h > avail && shown > 0 {
			break
		}
		used += h
		shown++
	}
	return shown
}

// scrollOffset is a column's stored offset, clamped to what the column can
// actually show. The clamp is applied here as well as in syncColumnScroll so that
// rendering a model straight out of a resize -- or a test -- cannot draw a column
// scrolled into empty space.
func (m Model) scrollOffset(col []*core.Session, colIndex, width, avail int) int {
	off := m.colScroll[colIndex]
	if off <= 0 {
		return 0
	}
	return min(off, m.maxScroll(col, width, avail))
}

// maxScroll is the furthest an offset is worth taking: the first one whose page
// still ends on the column's last card. Past it the column would give up cards at
// the top to show blank rows at the bottom.
func (m Model) maxScroll(col []*core.Session, width, avail int) int {
	for off := 0; off < len(col); off++ {
		if off+m.columnFit(col, off, width, avail) >= len(col) {
			return off
		}
	}
	return max(len(col)-1, 0)
}

// renderCard returns the lines for one card, sized to the column interior.
//
// The whole card is one click target: a zone is the rectangle between its two
// markers, so the block is marked once. Marking each line instead would leave
// only the last line clickable, and only across the width of its text.
func (m Model) renderCard(s *core.Session, selected bool, width int) []string {
	lines := m.cardLines(s, selected, width)
	return strings.Split(zone.Mark(zoneCard(s.ID), strings.Join(lines, "\n")), "\n")
}

// cardLines is renderCard without the click zone, so boardContentHeight can
// count a card's rows without registering a zone for a card it is only
// measuring.
//
// The selected card is marked three ways at once, because one way is not enough
// in a grid of otherwise identical cards: it is filled edge to edge, its accent
// bar thickens, and a caret sits on its title. The fill does the work at a
// glance; the caret and the bar keep the mark legible where a background is easy
// to miss -- a low-contrast theme, a screenshot, a terminal ignoring colors.
func (m Model) cardLines(s *core.Session, selected bool, width int) []string {
	st := m.styles

	// Every segment of a selected card carries the fill itself. Wrapping the
	// finished line in one background style would not work: the styles inside end
	// with a reset, and the fill would stop at the first one.
	fill := func(style lipgloss.Style) lipgloss.Style {
		if !selected {
			return style
		}
		return style.Background(st.P.Selection)
	}

	bar := fill(lipgloss.NewStyle().Foreground(st.agentColor(s.AgentState)).Bold(selected))
	glyph := bar.Render("▌")
	if selected {
		glyph = bar.Render("┃")
	}

	title := fill(st.CardTitle)
	if selected {
		title = fill(st.CardTitleSelected)
	}

	// Every card reserves the caret's two cells, selected or not. Letting the
	// caret push the text over instead would shift the title of whichever card the
	// cursor is on, so moving the cursor would twitch text on two cards at once --
	// the mark is easier to follow when it is the only thing that moves.
	blank := fill(lipgloss.NewStyle())
	gutter := blank.Render("   ")
	titleGutter := gutter
	if selected {
		titleGutter = blank.Render(" ") +
			fill(lipgloss.NewStyle().Foreground(st.P.Focus).Bold(true)).Render("▸") + blank.Render(" ")
	}
	textW := max(width-5, 4)

	// The dim rows step up a shade on the fill. Faint is chosen to sit just above
	// the background, so against a lighter one it stops being readable at all --
	// the row the fill is meant to highlight would be the row that disappears.
	meta, detail := st.Meta, st.Faint
	if selected {
		meta = st.Meta.Foreground(st.P.Muted)
		detail = st.Faint.Foreground(st.P.Subtle)
	}

	var lines []string
	push := func(content string) {
		lines = append(lines, glyph+gutter+content)
	}

	lines = append(lines, glyph+titleGutter+title.Render(truncate(s.Title, textW)))

	// The repo handle only earns space once more than one repo is registered.
	if m.cfg.MultiRepo() {
		push(fill(st.RepoTag).Render(truncate(s.RepoID, textW)))
	}
	// Likewise the project, and only when the board is not already filtered to
	// one -- otherwise every card would repeat the same label.
	if s.Group != "" && m.projectFilter == "" {
		push(fill(st.ProjectTag).Render(truncate("◆ "+s.Group, textW)))
	}

	push(fill(meta).Render(truncate(m.branchOrPR(s), textW)))
	push(fill(st.badgeStyle(s)).Render(truncate(st.badgeText(s), textW)))

	if s.AgentState == core.AgentNeedsYou && s.AgentStateDetail != "" {
		push(fill(detail).Render(truncate(s.AgentStateDetail, textW)))
	}

	if s.DiffAdded > 0 || s.DiffRemoved > 0 {
		stat := fill(lipgloss.NewStyle().Foreground(st.P.Success)).Render(fmt.Sprintf("+%d", s.DiffAdded)) +
			blank.Render(" ") +
			fill(lipgloss.NewStyle().Foreground(st.P.Danger)).Render(fmt.Sprintf("−%d", s.DiffRemoved))
		push(stat)
	}
	if !s.Starting && !s.TmuxAlive {
		push(fill(detail).Render(truncate("⚠ not running", textW)))
	}
	for i := range lines {
		lines[i] = padFill(lines[i], width, blank)
	}
	return lines
}

// branchOrPR shows the PR and its most urgent fact once one exists, and the
// branch name before that.
func (m Model) branchOrPR(s *core.Session) string {
	if s.Starting {
		return "worktree + dependencies"
	}
	if !s.HasPR() {
		// Sessions start with no branch at all; the agent names one when it has
		// something to commit.
		if s.Branch == "" {
			return "no branch"
		}
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
	case s.PRQueued:
		// Queued outranks CI: the queue is running its own checks now, and
		// whatever the PR's own last run said is no longer the thing to watch.
		return label + " ◌ queued"
	case s.PRAutoMerge:
		return label + " ◌ auto-merge"
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
