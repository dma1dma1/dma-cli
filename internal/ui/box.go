package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Box draws a rounded panel with its title set into the top edge, which is how
// the columns and the session panel get labelled without spending an interior
// line on a heading.
type Box struct {
	Title    string
	Subtitle string
	// Accent colors the title; Border colors the frame.
	Accent color.Color
	Border color.Color
	Width  int
	Height int
	// Focused thickens the frame so the pane taking keystrokes is obvious.
	Focused bool
}

// Render frames body. Body lines are padded to the interior width and clipped
// to Height, so a box always occupies exactly the space it claims -- columns
// laid side by side must not drift.
func (b Box) Render(body string) string {
	w := max(b.Width, 8)
	inner := w - 4 // two frame cells, two padding cells

	edge := lipgloss.NewStyle().Foreground(b.Border)
	if b.Focused {
		edge = edge.Bold(true)
	}
	tl, tr, bl, br, h, v := "╭", "╮", "╰", "╯", "─", "│"
	if b.Focused {
		tl, tr, bl, br, h, v = "┏", "┓", "┗", "┛", "━", "┃"
	}

	var out []string
	out = append(out, b.topEdge(w, tl, tr, h, edge))

	lines := strings.Split(body, "\n")
	if b.Height > 0 {
		// Two rows go to the frame itself.
		avail := max(b.Height-2, 1)
		if len(lines) > avail {
			lines = lines[:avail]
		}
		for len(lines) < avail {
			lines = append(lines, "")
		}
	}
	for _, l := range lines {
		out = append(out, edge.Render(v)+" "+pad(l, inner)+" "+edge.Render(v))
	}
	out = append(out, edge.Render(bl+strings.Repeat(h, w-2)+br))
	return strings.Join(out, "\n")
}

// topEdge writes the title, and the subtitle if it fits, into the frame.
func (b Box) topEdge(w int, tl, tr, h string, edge lipgloss.Style) string {
	if b.Title == "" {
		return edge.Render(tl + strings.Repeat(h, w-2) + tr)
	}

	label := lipgloss.NewStyle().Foreground(b.Accent).Bold(true).Render(b.Title)
	labelW := lipgloss.Width(b.Title)

	// The subtitle is the first thing dropped when the column is narrow.
	sub := ""
	if b.Subtitle != "" {
		cand := " · " + b.Subtitle
		if 4+labelW+lipgloss.Width(cand) <= w-2 {
			sub = lipgloss.NewStyle().Foreground(b.Border).Render(cand)
			labelW += lipgloss.Width(cand)
		}
	}

	fill := w - 2 - 2 - labelW - 1
	if fill < 0 {
		fill = 0
	}
	return edge.Render(tl+h) + " " + label + sub + " " + edge.Render(strings.Repeat(h, fill)+tr)
}

// pad extends a possibly-styled line to n display cells, or clips it.
func pad(s string, n int) string {
	wid := lipgloss.Width(s)
	if wid == n {
		return s
	}
	if wid < n {
		return s + strings.Repeat(" ", n-wid)
	}
	return truncate(s, n)
}

// lipglossWidth is exposed for tests, which need to assert exact cell widths.
func lipglossWidth(s string) int { return lipgloss.Width(s) }
