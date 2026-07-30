package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// Palette is a small semantic set. Colors carry meaning here -- danger means
// "this session is blocked on you" -- so they are named by role, not by hue.
type Palette struct {
	Text     color.Color
	Muted    color.Color
	Subtle   color.Color
	Accent   color.Color
	Success  color.Color
	Warning  color.Color
	Danger   color.Color
	Critical color.Color
	Border   color.Color
	Selected color.Color
}

func newPalette() Palette {
	return Palette{
		Text:     lipgloss.Color("252"),
		Muted:    lipgloss.Color("245"),
		Subtle:   lipgloss.Color("240"),
		Accent:   lipgloss.Color("111"),
		Success:  lipgloss.Color("78"),
		Warning:  lipgloss.Color("214"),
		Danger:   lipgloss.Color("203"),
		Critical: lipgloss.Color("197"),
		Border:   lipgloss.Color("238"),
		Selected: lipgloss.Color("117"),
	}
}

type Styles struct {
	P Palette

	ColumnHeader          lipgloss.Style
	ColumnHeaderFocused   lipgloss.Style
	GroupHeader           lipgloss.Style
	GroupRollup           lipgloss.Style
	Card                  lipgloss.Style
	CardSelected          lipgloss.Style
	CardAttention         lipgloss.Style
	CardAttentionSelected lipgloss.Style
	Title                 lipgloss.Style
	Meta                  lipgloss.Style
	RepoTag               lipgloss.Style
	KeyHint               lipgloss.Style
	KeyDesc               lipgloss.Style
	Error                 lipgloss.Style
	Status                lipgloss.Style
	DetailHeader          lipgloss.Style
	PaneTitle             lipgloss.Style
	AttachBanner          lipgloss.Style
	Dialog                lipgloss.Style
}

func newStyles() Styles {
	p := newPalette()
	base := lipgloss.NewStyle()

	return Styles{
		P: p,

		ColumnHeader: base.Foreground(p.Muted).Bold(true).Padding(0, 1),
		ColumnHeaderFocused: base.Foreground(p.Selected).Bold(true).Padding(0, 1).
			Underline(true),

		GroupHeader: base.Foreground(p.Text).Bold(true),
		GroupRollup: base.Foreground(p.Muted),

		Card: base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Border).
			Padding(0, 1),
		CardSelected: base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Selected).
			Padding(0, 1),
		// A needs_you card is identifiable by border alone, without reading text.
		CardAttention: base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Danger).
			Padding(0, 1),
		CardAttentionSelected: base.Border(lipgloss.ThickBorder()).BorderForeground(p.Critical).
			Padding(0, 1),

		Title:   base.Foreground(p.Text).Bold(true),
		Meta:    base.Foreground(p.Subtle),
		RepoTag: base.Foreground(p.Accent),

		KeyHint: base.Foreground(p.Accent).Bold(true),
		KeyDesc: base.Foreground(p.Subtle),

		Error:  base.Foreground(p.Danger),
		Status: base.Foreground(p.Muted),

		DetailHeader: base.Foreground(p.Text).Bold(true),
		PaneTitle:    base.Foreground(p.Muted).Bold(true),

		AttachBanner: base.Foreground(lipgloss.Color("232")).Background(p.Warning).Bold(true),
		Dialog: base.Border(lipgloss.RoundedBorder()).BorderForeground(p.Warning).
			Padding(1, 2),
	}
}

// agentColor maps an agent state to its badge color.
func (s Styles) agentColor(st core.AgentState) color.Color {
	switch st {
	case core.AgentWorking:
		return s.P.Warning
	case core.AgentNeedsYou:
		return s.P.Danger
	case core.AgentDone:
		return s.P.Success
	default:
		return s.P.Subtle
	}
}

// badgeText is the plain badge string: state plus time in state. "needs you"
// alone is not actionable; "needs you 8m" is.
func (s Styles) badgeText(sess *core.Session) string {
	return sess.AgentState.Badge() + " " + core.FormatDuration(sess.TimeInState())
}

// badgeStyle colors the badge. A session blocked for a long time is a different
// problem from one that just asked, so it escalates visually.
func (s Styles) badgeStyle(sess *core.Session) lipgloss.Style {
	st := lipgloss.NewStyle().Foreground(s.agentColor(sess.AgentState))
	if sess.AgentState == core.AgentNeedsYou && sess.TimeInState() >= escalateAfter {
		st = st.Bold(true).Foreground(s.P.Critical)
	}
	return st
}

// badge renders the styled badge.
func (s Styles) badge(sess *core.Session) string {
	return s.badgeStyle(sess).Render(s.badgeText(sess))
}

// ciStyle colors a CI verdict.
func (s Styles) ciStyle(ci core.CIState) lipgloss.Style {
	switch ci {
	case core.CIPass:
		return lipgloss.NewStyle().Foreground(s.P.Success)
	case core.CIFail:
		return lipgloss.NewStyle().Foreground(s.P.Danger)
	case core.CIPending:
		return lipgloss.NewStyle().Foreground(s.P.Warning)
	}
	return lipgloss.NewStyle().Foreground(s.P.Subtle)
}
