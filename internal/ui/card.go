package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// zoneCard is the zone id prefix for a card, used for mouse hit-testing.
func zoneCard(id string) string { return "card:" + id }
func zoneGroup(g string) string { return "group:" + g }
func zoneColumn(i int) string   { return fmt.Sprintf("col:%d", i) }

// renderCard draws one session card at the given inner width.
//
// Content order is fixed: title, repo (multi-repo only), branch or PR, agent
// badge, diff stat. The repo handle is omitted entirely when only one repo is
// registered, so the common case stays uncluttered.
func (m Model) renderCard(s *core.Session, selected bool, width int) string {
	st := m.styles
	inner := width - 4 // border + padding
	if inner < 6 {
		inner = 6
	}

	var lines []string

	lines = append(lines, st.Title.Render(truncate(s.Title, inner)))

	if m.cfg.MultiRepo() {
		lines = append(lines, st.RepoTag.Render(truncate(s.RepoID, inner)))
	}

	lines = append(lines, st.Meta.Render(truncate(m.branchOrPR(s), inner)))
	lines = append(lines, st.badgeStyle(s).Render(truncate(st.badgeText(s), inner)))

	if d := m.detailLine(s); d != "" {
		lines = append(lines, st.Meta.Render(truncate(d, inner)))
	}

	if s.DiffAdded > 0 || s.DiffRemoved > 0 {
		add := lipgloss.NewStyle().Foreground(st.P.Success).
			Render(fmt.Sprintf("+%d", s.DiffAdded))
		rem := lipgloss.NewStyle().Foreground(st.P.Danger).
			Render(fmt.Sprintf("−%d", s.DiffRemoved))
		lines = append(lines, add+" "+rem)
	}

	if !s.TmuxAlive {
		lines = append(lines, lipgloss.NewStyle().Foreground(st.P.Subtle).
			Render(truncate("⚠ no tmux session", inner)))
	}

	body := strings.Join(lines, "\n")

	style := st.Card
	switch {
	case s.AgentState == core.AgentNeedsYou && selected:
		style = st.CardAttentionSelected
	case s.AgentState == core.AgentNeedsYou:
		style = st.CardAttention
	case selected:
		style = st.CardSelected
	}

	return zone.Mark(zoneCard(s.ID), style.Width(width-2).Render(body))
}

// branchOrPR shows the PR number and CI result once a PR exists, and the branch
// name before that.
func (m Model) branchOrPR(s *core.Session) string {
	if !s.HasPR() {
		return s.Branch
	}
	label := fmt.Sprintf("#%d", s.PRNumber)
	// Field priority when space is tight: mergeable first -- conflicts matter
	// most and matter earliest -- then CI, then review.
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
		return label + " • ci"
	case core.CIPass:
		if s.PRReview == core.ReviewChangesRequested {
			return label + " ✓ ci ⟲ changes"
		}
		if s.PRReview == core.ReviewApproved {
			return label + " ✓ ci ✓ approved"
		}
		return label + " ✓ ci"
	}
	if s.PRState == core.PRDraft {
		return label + " draft"
	}
	return label
}

// detailLine surfaces what the agent is blocked on, which is the whole point of
// the needs_you badge.
func (m Model) detailLine(s *core.Session) string {
	if s.AgentState != core.AgentNeedsYou {
		return ""
	}
	return s.AgentStateDetail
}

// truncate cuts a display string to n cells, appending an ellipsis.
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
