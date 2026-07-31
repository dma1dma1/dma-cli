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
		m.overlayPicker(m.diffPanes()),
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
	if m.review.mode == gitx.DiffBranch {
		return s.BaseBranch + "...HEAD"
	}
	return "working tree"
}

// diffSubtitle says what question the pane is answering, about what, and where
// in the answer you are.
func (m Model) diffSubtitle(s *core.Session) string {
	var parts []string
	if m.review.source == sourceFile {
		parts = append(parts, "file")
	} else {
		parts = append(parts, "diff", m.diffModeLabel(s))
		if m.review.sideBySide {
			parts = append(parts, "side by side")
		}
	}
	if path := m.review.files.selectedPath(); path != "" {
		parts = append(parts, path)
	} else {
		parts = append(parts, rootLabel)
	}
	// A search is a mode -- n and N belong to it while it has hits -- so it has
	// to be visible for as long as that is true, not just while the field is up.
	if label := m.review.find.label(); label != "" {
		parts = append(parts, label)
	} else if n := len(m.diffHunks()); n > 1 && m.review.source == sourceDiff {
		// Where you are in the file, in changes rather than in lines: a scroll
		// bar says how far down a wall of text you are, which is not the
		// question.
		parts = append(parts, fmt.Sprintf("change %d of %d",
			m.review.doc.HunkAt(m.review.view.YOffset())+1, n))
	}
	return strings.Join(parts, " · ")
}

// pickerWidth is how wide the search overlay is drawn: most of the view, but
// short of the frame on either side so it reads as floating over the panes
// rather than as having replaced them.
func (m Model) pickerWidth() int {
	return clamp(m.contentWidth()-12, 40, 110)
}

// overlayPicker draws the search box over the panes, if one is open.
//
// It is composited over the rendered panes rather than drawn instead of them:
// what is behind the box is the diff you are searching from, and covering it
// entirely would lose the context that makes a result list mean anything.
func (m Model) overlayPicker(panes string) string {
	if !m.review.picker.open() {
		return panes
	}
	p := m.review.picker
	box := p.render(m.styles, m.pickerWidth())

	rows := strings.Split(panes, "\n")
	overlay := strings.Split(box, "\n")
	// Two rows down from the top of the panes: far enough not to sit on the
	// chips, close enough that the eye is already there from typing.
	const top = 1
	left := max((lipgloss.Width(panes)-lipgloss.Width(box))/2, 0)
	pad := strings.Repeat(" ", left)
	for i, line := range overlay {
		r := top + i
		if r >= len(rows) {
			rows = append(rows, "")
		}
		// The box is opaque: whatever the pane had on that row is replaced
		// rather than blended, so a colored diff underneath cannot bleed
		// through the border.
		rows[r] = pad + line
	}
	return strings.Join(rows, "\n")
}

// diffPanes lays the file tree beside the diff, or gives the diff the whole
// width when the tree is hidden.
//
// The two are joined at the top rather than centered: they are both lists read
// from their first line down, and a shorter tree centered against a long diff
// would start halfway down the pane.
func (m Model) diffPanes() string {
	diff := m.review.view.View()
	treeW := m.diffTreeWidth()
	if treeW == 0 {
		return diff
	}

	tree := m.review.files.render(m.styles, treeW, m.diffPaneHeight(), m.review.treeFocus)
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
