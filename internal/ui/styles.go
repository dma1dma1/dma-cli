package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// Palette is named by role, not by hue: in this UI color carries meaning, and
// danger specifically means "a session is blocked on you".
type Palette struct {
	Text     color.Color
	Muted    color.Color
	Subtle   color.Color
	Faint    color.Color
	Accent   color.Color
	Success  color.Color
	Warning  color.Color
	Danger   color.Color
	Critical color.Color
	Border   color.Color
	Focus    color.Color
	Chip     color.Color
	ChipText color.Color
	// Selection fills the card the panel below is showing. A fill rather than a
	// brighter foreground because the cards already spend color on agent state:
	// another hue would compete with it, while a filled row reads as "this one"
	// at a glance and leaves every state color meaning what it meant.
	Selection color.Color
}

func newPalette() Palette {
	return Palette{
		Text:      lipgloss.Color("253"),
		Muted:     lipgloss.Color("246"),
		Subtle:    lipgloss.Color("242"),
		Faint:     lipgloss.Color("239"),
		Accent:    lipgloss.Color("111"),
		Success:   lipgloss.Color("78"),
		Warning:   lipgloss.Color("214"),
		Danger:    lipgloss.Color("203"),
		Critical:  lipgloss.Color("197"),
		Border:    lipgloss.Color("238"),
		Focus:     lipgloss.Color("117"),
		Chip:      lipgloss.Color("236"),
		ChipText:  lipgloss.Color("251"),
		Selection: lipgloss.Color("237"),
	}
}

type Styles struct {
	P Palette

	CardTitle         lipgloss.Style
	CardTitleSelected lipgloss.Style
	Meta              lipgloss.Style
	Faint             lipgloss.Style
	RepoTag           lipgloss.Style
	ProjectTag        lipgloss.Style

	KeyHint lipgloss.Style
	KeyDesc lipgloss.Style
	Error   lipgloss.Style
	Status  lipgloss.Style
	Title   lipgloss.Style

	Chip        lipgloss.Style
	ChipFocused lipgloss.Style
	ChipLabel   lipgloss.Style

	Prompt   lipgloss.Style
	Dialog   lipgloss.Style
	Selected lipgloss.Style

	// MatchHit marks the characters a query matched in a picker row, and
	// MatchCurrent the one hit a find is stepping through. Both are fills
	// rather than colors: they land on top of syntax highlighting, and a
	// foreground color there would be competing with the one the highlighter
	// already chose for the same characters.
	MatchHit     lipgloss.Style
	MatchCurrent lipgloss.Style
}

func newStyles() Styles {
	p := newPalette()
	base := lipgloss.NewStyle()

	return Styles{
		P: p,

		CardTitle:         base.Foreground(p.Text).Bold(true),
		CardTitleSelected: base.Foreground(p.Focus).Bold(true),
		Meta:              base.Foreground(p.Subtle),
		Faint:             base.Foreground(p.Faint),
		RepoTag:           base.Foreground(p.Accent),
		ProjectTag:        base.Foreground(lipgloss.Color("140")),

		KeyHint: base.Foreground(p.Accent).Bold(true),
		KeyDesc: base.Foreground(p.Subtle),
		Error:   base.Foreground(p.Danger),
		Status:  base.Foreground(p.Muted),
		Title:   base.Foreground(p.Text).Bold(true),

		Chip:        base.Foreground(p.ChipText).Background(p.Chip).Padding(0, 1),
		ChipFocused: base.Foreground(lipgloss.Color("232")).Background(p.Focus).Bold(true).Padding(0, 1),
		ChipLabel:   base.Foreground(p.Subtle),

		Prompt: base.Foreground(p.Success).Bold(true),
		// Reversed rather than bordered: the footer is one line, and a border
		// would need three. The fill carries the "answer this" weight instead.
		Dialog:   base.Foreground(lipgloss.Color("232")).Background(p.Warning).Bold(true).Padding(0, 1),
		Selected: base.Foreground(lipgloss.Color("232")).Background(p.Focus),

		MatchHit:     base.Foreground(lipgloss.Color("232")).Background(p.Accent),
		MatchCurrent: base.Foreground(lipgloss.Color("232")).Background(p.Warning).Bold(true),
	}
}

// agentColor maps an agent state to the color used for its badge and the card's
// accent bar.
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

// columnAccent colors a column title. Idle is the column you act on, so it is
// not styled as inactive.
func (s Styles) columnAccent(l core.Lifecycle) color.Color {
	switch l {
	case core.LifecycleIdle:
		return s.P.Text
	case core.LifecycleActive:
		return s.P.Warning
	case core.LifecyclePROpen:
		return s.P.Accent
	case core.LifecycleMerged:
		return s.P.Success
	}
	return s.P.Muted
}

// badgeText is the plain badge: state plus time in state. "needs you" alone is
// not actionable; "needs you 8m" is.
func (s Styles) badgeText(sess *core.Session) string {
	return sess.AgentState.Badge() + " " + core.FormatDuration(sess.TimeInState())
}

// badgeStyle colors the badge, escalating a session that has been blocked long
// enough for it to be a different problem.
func (s Styles) badgeStyle(sess *core.Session) lipgloss.Style {
	st := lipgloss.NewStyle().Foreground(s.agentColor(sess.AgentState))
	if sess.AgentState == core.AgentNeedsYou && sess.TimeInState() >= escalateAfter {
		st = st.Bold(true).Foreground(s.P.Critical)
	}
	return st
}

func (s Styles) badge(sess *core.Session) string {
	return s.badgeStyle(sess).Render(s.badgeText(sess))
}

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
