package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// viewDiff is the full-screen review of one session's changes: the files it
// touched on the left, the diff of the one under the cursor on the right. The
// board panel already shows live output, so this view exists purely for the
// diff -- the two compete for space and are never rendered together.
func (m Model) viewDiff() string {
	s := m.selected()
	if s == nil {
		return m.styles.Faint.Render("  no session selected")
	}
	st := m.styles

	body := lipgloss.JoinVertical(lipgloss.Left,
		m.diffChips(s),
		"",
		m.diffPanes(),
	)

	b := Box{
		Title:    s.Title,
		Subtitle: m.diffSubtitle(s),
		Accent:   st.P.Accent,
		Border:   st.P.Border,
		Width:    m.contentWidth(),
		Height:   m.height - 1,
		Focused:  true,
	}
	return b.Render(body)
}

// diffModeLabel names the range on screen.
func (m Model) diffModeLabel(s *core.Session) string {
	if m.diffMode == gitx.DiffBranch {
		return s.BaseBranch + "...HEAD"
	}
	return "working tree"
}

// diffSubtitle says which range, which layout, and which row -- the three things
// that decide what the pane is showing.
func (m Model) diffSubtitle(s *core.Session) string {
	parts := []string{"diff", m.diffModeLabel(s)}
	if m.diffSideBySide {
		parts = append(parts, "side by side")
	}
	if path := m.diffFiles.selectedPath(); path != "" {
		parts = append(parts, path)
	} else {
		parts = append(parts, rootLabel)
	}
	// Where you are in the file, in changes rather than in lines: a scroll bar
	// says how far down a wall of text you are, which is not the question.
	if n := len(m.diffHunks); n > 1 {
		parts = append(parts, fmt.Sprintf("change %d of %d",
			currentHunk(m.diffHunkRows, m.diffView.YOffset())+1, n))
	}
	return strings.Join(parts, " · ")
}

// diffPanes lays the file tree beside the diff, or gives the diff the whole
// width when the tree is hidden.
//
// The two are joined at the top rather than centered: they are both lists read
// from their first line down, and a shorter tree centered against a long diff
// would start halfway down the pane.
func (m Model) diffPanes() string {
	diff := m.diffView.View()
	treeW := m.diffTreeWidth()
	if treeW == 0 {
		return diff
	}

	tree := m.diffFiles.render(m.styles, treeW, m.diffPaneHeight(), m.diffTreeFocus)
	// The divider is drawn per row rather than as one tall glyph, so it stops
	// where the panes stop instead of being padded out by the join.
	rows := make([]string, m.diffPaneHeight())
	for i := range rows {
		rows[i] = m.styles.Faint.Render(" │ ")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, tree, strings.Join(rows, "\n"), diff)
}

// diffChips summarizes the facts you need while reading a diff: whether it
// merges, whether it passes, whether anything is uncommitted.
func (m Model) diffChips(s *core.Session) string {
	st := m.styles
	var chips []string

	add := func(text string, style lipgloss.Style) {
		chips = append(chips, style.Padding(0, 1).Render(text))
	}

	add(st.badgeText(s), st.badgeStyle(s))

	if s.HasPR() {
		prStyle := lipgloss.NewStyle().Foreground(st.P.Accent)
		switch s.PRState {
		case core.PRMerged:
			prStyle = lipgloss.NewStyle().Foreground(st.P.Success)
		case core.PRClosed:
			prStyle = lipgloss.NewStyle().Foreground(st.P.Danger)
		case core.PRDraft:
			prStyle = lipgloss.NewStyle().Foreground(st.P.Subtle)
		}
		add(fmt.Sprintf("PR #%d %s", s.PRNumber, s.PRState), prStyle)

		if s.PRQueued {
			add("◌ merge queue", lipgloss.NewStyle().Foreground(st.P.Accent))
		}

		// Mergeable first: conflicts matter most and matter earliest.
		switch s.PRMergeable {
		case core.MergeConflicts:
			add("⚠ conflicts", lipgloss.NewStyle().Foreground(st.P.Danger))
		case core.MergeClean:
			add("mergeable", lipgloss.NewStyle().Foreground(st.P.Success))
		}
		if s.PRCI != core.CINone {
			add("ci "+string(s.PRCI), st.ciStyle(s.PRCI))
		}
		switch s.PRReview {
		case core.ReviewApproved:
			add("approved", lipgloss.NewStyle().Foreground(st.P.Success))
		case core.ReviewChangesRequested:
			add("changes requested", lipgloss.NewStyle().Foreground(st.P.Warning))
		}
	} else {
		add("no PR", st.Faint)
	}

	add(fmt.Sprintf("+%d −%d", s.DiffAdded, s.DiffRemoved), st.Meta)
	if s.WorktreeDirty {
		add("uncommitted", lipgloss.NewStyle().Foreground(st.P.Warning))
	} else {
		add("clean", st.Faint)
	}
	if m.cfg.MultiRepo() {
		add(s.RepoID, st.RepoTag)
	}
	if s.Branch == "" {
		add("no branch", st.Faint)
	} else {
		add(s.Branch, st.Meta)
	}

	return strings.Join(chips, "")
}
