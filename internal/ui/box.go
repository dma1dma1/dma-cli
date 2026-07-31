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
	// Scroll, when set, draws a scrollbar down the right-hand padding column.
	Scroll *Scrollbar
}

// Scrollbar describes how much of a longer list a box is showing, so the frame
// can draw the position as a bar.
//
// It is the affordance that says the box scrolls at all: a body that simply
// stops at the fold reads as a body holding fewer items than it does, however
// carefully the ones off screen are counted in words.
type Scrollbar struct {
	// The three counts are in whatever unit the body scrolls by -- cards, for
	// the board's columns.
	Total   int
	Visible int
	Offset  int
	// Track colors the groove, Thumb the part of the list on screen.
	Track color.Color
	Thumb color.Color
}

// glyphs is the bar drawn down one cell, a glyph per interior row, or nil when
// the whole list is on screen and there is nothing to say.
//
// The thumb is as long as the share of the list on screen, floored at one cell
// so a very long column still shows something, and it reaches the bottom of the
// track exactly when the last item is drawn.
func (s *Scrollbar) glyphs(rows int) []string {
	if s == nil || rows <= 0 || s.Total <= 0 || s.Visible >= s.Total {
		return nil
	}
	length := clamp(rows*s.Visible/s.Total, 1, rows)
	start := 0
	// The thumb travels the rows it does not fill, over the items off screen.
	if span := s.Total - s.Visible; span > 0 {
		start = clamp((s.Offset*(rows-length)+span/2)/span, 0, rows-length)
	}

	// A dotted groove: a solid one sits a cell from the frame's own line and the
	// pair reads as a doubled border rather than as a scrollbar.
	track := lipgloss.NewStyle().Foreground(s.Track).Render("┊")
	thumb := lipgloss.NewStyle().Foreground(s.Thumb).Render("█")
	out := make([]string, rows)
	for i := range out {
		out[i] = track
		if i >= start && i < start+length {
			out[i] = thumb
		}
	}
	return out
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
	// The bar takes the right-hand padding cell rather than a column of its own,
	// so a scrolling box is exactly as wide as a still one and the cards inside
	// keep every cell they had.
	bar := b.Scroll.glyphs(len(lines))
	for i, l := range lines {
		right := " "
		if i < len(bar) {
			right = bar[i]
		}
		out = append(out, edge.Render(v)+" "+pad(l, inner)+right+edge.Render(v))
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

// padFill is pad with the padding itself styled, which is what a filled row
// needs: plain spaces would leave the fill ending at the text and the rest of the
// row showing through.
func padFill(s string, n int, style lipgloss.Style) string {
	wid := lipgloss.Width(s)
	if wid >= n {
		return pad(s, n)
	}
	return s + style.Render(strings.Repeat(" ", n-wid))
}

// lipglossWidth is exposed for tests, which need to assert exact cell widths.
func lipglossWidth(s string) int { return lipgloss.Width(s) }
