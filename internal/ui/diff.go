package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// viewDiff is the full-screen review of one session's changes. The board panel
// already shows live output, so this view exists purely for the diff -- the two
// compete for space and are never rendered together.
func (m Model) viewDiff() string {
	s := m.selected()
	if s == nil {
		return m.styles.Faint.Render("  no session selected")
	}
	st := m.styles

	label := "working tree"
	if m.diffMode == gitx.DiffBranch {
		label = s.BaseBranch + "...HEAD"
	}

	body := lipgloss.JoinVertical(lipgloss.Left,
		m.diffChips(s),
		"",
		m.diffView.View(),
	)

	b := Box{
		Title:    s.Title,
		Subtitle: "diff · " + label + " · tab to switch",
		Accent:   st.P.Accent,
		Border:   st.P.Border,
		Width:    m.contentWidth(),
		Height:   m.height - 1,
		Focused:  true,
	}
	return b.Render(body)
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
