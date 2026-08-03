package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

// previewPoint is a cell in the preview body's coordinate space.
type previewPoint struct{ row, col int }

// previewSelection holds both the gesture and the frame it began on. The
// snapshot is the important part: a Codex spinner or the board's one-second
// clock can redraw while the mouse is down without changing the selected text.
type previewSelection struct {
	anchor, head previewPoint
	pressed      bool
	active       bool
	lines        []string
}

func (s previewSelection) held() bool { return s.pressed || s.active }

func (m *Model) clearPreviewSelection() { m.previewSelection = previewSelection{} }

// beginPreviewSelection defers the ordinary click action until release. A
// motion in between turns the same gesture into selection instead of briefly
// focusing the agent and sending its modal composer into insert mode.
func (m *Model) beginPreviewSelection(msg tea.MouseMsg) bool {
	if m.mode != modeBoard || msg.Mouse().Button != tea.MouseLeft || m.dropdown.open {
		return false
	}
	z := zone.Get(zonePreview)
	if z == nil || !z.InBounds(msg) || !m.focusable(focusPreview) {
		return false
	}
	p := previewMousePoint(msg, z)
	rows, width := z.EndY-z.StartY+1, z.EndX-z.StartX+1
	m.previewSelection = previewSelection{
		anchor: p, head: p, pressed: true,
		lines: m.previewLines(rows, width),
	}
	return true
}

func (m Model) dragPreviewSelection(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.previewSelection.pressed {
		return m, nil
	}
	z := zone.Get(zonePreview)
	if z == nil {
		return m, nil
	}
	m.previewSelection.head = previewMousePoint(msg, z)
	m.previewSelection.active = m.previewSelection.active ||
		m.previewSelection.head != m.previewSelection.anchor
	return m, nil
}

func (m Model) releasePreviewSelection(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.previewSelection.pressed {
		return m, nil
	}
	z := zone.Get(zonePreview)
	if z != nil {
		m.previewSelection.head = previewMousePoint(msg, z)
		m.previewSelection.active = m.previewSelection.active ||
			m.previewSelection.head != m.previewSelection.anchor
	}
	m.previewSelection.pressed = false
	if m.previewSelection.active {
		text := m.previewSelection.text()
		if text == "" {
			m.clearPreviewSelection()
			return m, nil
		}
		return m, copyTextCmd(text)
	}

	// It was a click, not a drag. Preserve the existing promise that clicking
	// the preview aims the keyboard at the agent.
	m.clearPreviewSelection()
	return m.handleClick(tea.MouseClickMsg(msg.Mouse()))
}

// previewMousePoint clamps drags that leave the panel to its nearest cell, the
// same way graphical text selections continue to the edge of a text area.
func previewMousePoint(msg tea.MouseMsg, z *zone.ZoneInfo) previewPoint {
	return previewPoint{
		row: clamp(msg.Mouse().Y-z.StartY, 0, z.EndY-z.StartY),
		col: clamp(msg.Mouse().X-z.StartX, 0, z.EndX-z.StartX),
	}
}

func (s previewSelection) bounds() (previewPoint, previewPoint) {
	a, b := s.anchor, s.head
	if b.row < a.row || (b.row == a.row && b.col < a.col) {
		return b, a
	}
	return a, b
}

func (s previewSelection) render(style lipgloss.Style) []string {
	lines := append([]string(nil), s.lines...)
	if !s.active {
		return lines
	}
	start, end := s.bounds()
	for row := start.row; row <= end.row && row < len(lines); row++ {
		left, right := 0, ansi.StringWidth(lines[row])
		if row == start.row {
			left = start.col
		}
		if row == end.row {
			right = end.col + 1
		}
		if right > left {
			lines[row] = lipgloss.StyleRanges(lines[row], lipgloss.NewRange(left, right, style))
		}
	}
	return lines
}

func (s previewSelection) text() string {
	if !s.active {
		return ""
	}
	start, end := s.bounds()
	selected := make([]string, 0, end.row-start.row+1)
	for row := start.row; row <= end.row && row < len(s.lines); row++ {
		left, right := 0, ansi.StringWidth(s.lines[row])
		if row == start.row {
			left = start.col
		}
		if row == end.row {
			right = end.col + 1
		}
		line := ansi.Strip(ansi.Cut(s.lines[row], left, right))
		selected = append(selected, strings.TrimRight(line, " "))
	}
	return strings.Join(selected, "\n")
}
