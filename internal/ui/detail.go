package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// viewDetail is zoom level 2: one session, framed by the application. The board
// and the diff pane compete for space, so only one is on screen at a time --
// switching is a single keypress.
func (m Model) viewDetail() string {
	s := m.selected()
	if s == nil {
		return m.styles.Status.Render("  No session selected.")
	}
	st := m.styles

	var b strings.Builder
	b.WriteString(m.detailHeader(s))
	b.WriteString("\n")
	b.WriteString(m.detailChips(s))
	b.WriteString("\n")

	paneWidth := m.width - 2
	diffTitle := "uncommitted diff"
	if m.diffMode == gitx.DiffBranch {
		diffTitle = fmt.Sprintf("%s...HEAD", s.BaseBranch)
	}

	b.WriteString(st.PaneTitle.Render("  diff — " + diffTitle + "  (tab to toggle)"))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Width(paneWidth).Render(m.diffView.View()))
	b.WriteString("\n")
	b.WriteString(st.PaneTitle.Render("  recent output — " + s.TmuxSession))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Width(paneWidth).Render(m.outputView.View()))
	b.WriteString("\n")

	return b.String()
}

func (m Model) detailHeader(s *core.Session) string {
	st := m.styles
	parts := []string{st.DetailHeader.Render(s.Title)}
	if m.cfg.MultiRepo() {
		parts = append(parts, st.RepoTag.Render(s.RepoID))
	}
	group := s.Group
	if group == "" {
		group = "ungrouped"
	}
	parts = append(parts,
		st.Meta.Render(group),
		st.Meta.Render(s.Branch),
		st.badge(s),
	)
	if s.AgentStateDetail != "" {
		parts = append(parts, st.Meta.Render("— "+s.AgentStateDetail))
	}
	return "  " + strings.Join(parts, st.Meta.Render(" │ "))
}

// detailChips renders status chips: PR state, CI, review, mergeable, diff stat,
// worktree cleanliness.
func (m Model) detailChips(s *core.Session) string {
	st := m.styles
	var chips []string

	chip := func(text string, fg any) string {
		style := lipgloss.NewStyle().Padding(0, 1)
		if c, ok := fg.(lipgloss.Style); ok {
			style = c.Padding(0, 1)
		}
		return style.Render(text)
	}

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
		chips = append(chips, chip(fmt.Sprintf("PR #%d %s", s.PRNumber, s.PRState), prStyle))

		// Mergeable first: conflicts matter most and matter earliest.
		switch s.PRMergeable {
		case core.MergeConflicts:
			chips = append(chips, chip("⚠ conflicts", lipgloss.NewStyle().Foreground(st.P.Danger)))
		case core.MergeClean:
			chips = append(chips, chip("mergeable", lipgloss.NewStyle().Foreground(st.P.Success)))
		}

		if s.PRCI != core.CINone {
			chips = append(chips, chip("ci "+string(s.PRCI), st.ciStyle(s.PRCI)))
		}
		switch s.PRReview {
		case core.ReviewApproved:
			chips = append(chips, chip("approved", lipgloss.NewStyle().Foreground(st.P.Success)))
		case core.ReviewChangesRequested:
			chips = append(chips, chip("changes requested", lipgloss.NewStyle().Foreground(st.P.Warning)))
		}
	} else {
		chips = append(chips, chip("no PR", lipgloss.NewStyle().Foreground(st.P.Subtle)))
	}

	chips = append(chips, chip(fmt.Sprintf("+%d −%d", s.DiffAdded, s.DiffRemoved),
		lipgloss.NewStyle().Foreground(st.P.Muted)))

	if s.WorktreeDirty {
		chips = append(chips, chip("dirty", lipgloss.NewStyle().Foreground(st.P.Warning)))
	} else {
		chips = append(chips, chip("clean", lipgloss.NewStyle().Foreground(st.P.Subtle)))
	}
	if !s.TmuxAlive {
		chips = append(chips, chip("tmux gone", lipgloss.NewStyle().Foreground(st.P.Danger)))
	}

	return "  " + strings.Join(chips, "")
}

// viewAttachBanner is the level-3 warning. Entry into attached mode must be
// visually unmistakable, because from there every keystroke belongs to the
// agent.
func (m Model) viewAttachBanner(s *core.Session) string {
	st := m.styles
	msg := fmt.Sprintf(" ATTACHED — %s — all keys go to the agent — %s to detach ",
		s.TmuxSession, detachKey)
	return st.AttachBanner.Width(m.width).Render(truncate(msg, m.width))
}
