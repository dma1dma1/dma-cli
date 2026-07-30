package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// focusArea is which part of the UI keystrokes go to. The input bar is always
// on screen, so focus has to be explicit -- otherwise j and k would type
// letters instead of moving the cursor.
type focusArea int

const (
	focusBoard focusArea = iota
	focusInput
	focusAgent
	focusRepo
	focusProject
)

// focusRing is the order tab walks.
var focusRing = []focusArea{focusBoard, focusInput, focusAgent, focusRepo, focusProject}

const (
	zoneAgentChip   = "chip:agent"
	zoneRepoChip    = "chip:repo"
	zoneProjectChip = "chip:project"
	zonePreview     = "panel:preview"
	zoneInput       = "panel:input"
)

func zoneCard(id string) string { return "card:" + id }
func zoneColumn(i int) string   { return fmt.Sprintf("col:%d", i) }
func zoneOption(i int) string   { return fmt.Sprintf("opt:%d", i) }

// panelHeight is how much vertical space the session panel takes. It is all of
// the screen when expanded.
//
// Otherwise it takes about two fifths: an agent UI has its own input box and
// footer, so a third of a normal window leaves it too few rows to be readable,
// while the columns stay legible with the remainder.
func (m Model) panelHeight() int {
	if m.previewFull {
		return m.height - 1
	}
	return clamp(m.height*2/5, 12, 26)
}

// previewDims is the size the agent's terminal should be rendered at: the
// panel's interior, minus the rows the chips, rule and input bar occupy.
//
// Agents are sized to this rather than to some default, because a tmux session
// with no client has no natural size and would otherwise draw into 80 columns.
func (m Model) previewDims() (cols, rows int) {
	h := m.panelHeight()
	return max(m.width-4, 20), max(h-2-3, 3)
}

// viewPanel renders the persistent bottom panel: the three selectors, the live
// session output, and the input bar.
func (m Model) viewPanel(height int) string {
	st := m.styles
	inner := max(m.width-4, 20)

	var rows []string
	rows = append(rows, m.chipRow(inner))
	rows = append(rows, st.Faint.Render(strings.Repeat("─", inner)))

	// The dropdown opens into the body rather than floating over it, so there
	// is no compositing to get wrong and the list is never clipped.
	bodyRows := max(height-2-3, 1) // frame, chips, rule, input
	if m.dropdown.open {
		rows = append(rows, m.viewDropdown(bodyRows, inner)...)
	} else {
		rows = append(rows, m.previewBody(bodyRows, inner)...)
	}

	rows = append(rows, m.inputRow(inner))

	title, subtitle := m.panelTitle()
	b := Box{
		Title:    title,
		Subtitle: subtitle,
		Accent:   st.P.Accent,
		Border:   st.P.Border,
		Width:    m.width,
		Height:   height,
		Focused:  m.focus != focusBoard,
	}
	return b.Render(strings.Join(rows, "\n"))
}

func (m Model) panelTitle() (string, string) {
	s := m.selected()
	if s == nil {
		return "session", "nothing selected"
	}
	sub := s.TmuxSession
	if !s.TmuxAlive {
		sub += " (not running)"
	}
	if m.previewFull {
		sub += " · e to shrink"
	}
	return s.Title, sub
}

// chipRow renders the agent / repo / project selectors. Repo and project sit
// where the mockup put them: the two things you change per task on the left,
// the board-wide filter on the right.
func (m Model) chipRow(width int) string {
	st := m.styles

	chip := func(label, value string, area focusArea, z string) string {
		style := st.Chip
		if m.focus == area {
			style = st.ChipFocused
		}
		if value == "" {
			value = "—"
		}
		body := st.ChipLabel.Render(label+" ") + value
		if m.focus == area {
			body = label + " " + value
		}
		return zone.Mark(z, style.Render("▾ "+body))
	}

	left := lipgloss.JoinHorizontal(lipgloss.Top,
		chip("agent", m.agentChoice, focusAgent, zoneAgentChip), " ",
		chip("repo", m.activeRepoID(), focusRepo, zoneRepoChip),
	)

	project := m.projectFilter
	if project == "" {
		project = "all projects"
	}
	right := chip("project", project, focusProject, zoneProjectChip)

	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", gap) + right
}

// previewBody renders recent output from the selected session's terminal.
//
// This is display only. Agent state comes from hooks, or from the prober for
// agents without them -- never from reading this text here.
func (m Model) previewBody(rows, width int) []string {
	st := m.styles
	s := m.selected()
	if s == nil {
		return centered(rows, width, st.Faint.Render("no session selected — type a task below to start one"))
	}
	if strings.TrimSpace(m.preview) == "" {
		hint := "waiting for output…"
		if !s.TmuxAlive {
			hint = "this session's terminal is gone (press D to clear, x to prune)"
		}
		return centered(rows, width, st.Faint.Render(hint))
	}

	lines := strings.Split(strings.TrimRight(m.preview, "\n"), "\n")
	// The tail is the interesting part of a terminal.
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	out := make([]string, 0, rows)
	for _, l := range lines {
		out = append(out, zone.Mark(zonePreview, pad(l, width)))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

func centered(rows, width int, msg string) []string {
	out := make([]string, rows)
	mid := rows / 2
	for i := range out {
		if i == mid {
			out[i] = lipgloss.PlaceHorizontal(width, lipgloss.Center, msg)
			continue
		}
		out[i] = ""
	}
	return out
}

// inputRow is the always-present task input. It stays on screen even when the
// board has focus, so starting work is never more than one key away.
func (m Model) inputRow(width int) string {
	st := m.styles
	if m.focus == focusInput {
		return zone.Mark(zoneInput, st.Prompt.Render("❯ ")+m.input.View())
	}
	hint := "press i to describe a task for a new agent session"
	if s := m.selected(); s != nil && m.input.Value() != "" {
		hint = m.input.Value()
	}
	return zone.Mark(zoneInput, st.Faint.Render("❯ "+truncate(hint, max(width-2, 4))))
}

// --- dropdowns ---

// dropdown is an open selector. Options are resolved when it opens so the list
// cannot shift under the cursor mid-selection.
type dropdown struct {
	open    bool
	area    focusArea
	options []string
	labels  []string
	cursor  int
}

func (m *Model) openDropdown(area focusArea) {
	d := dropdown{open: true, area: area}
	switch area {
	case focusAgent:
		for _, p := range m.cfg.AgentProfiles {
			d.options = append(d.options, p.Name)
			state := "state via pane activity"
			if p.Hooks {
				state = "state via hooks"
			}
			d.labels = append(d.labels, fmt.Sprintf("%-10s %s   %s", p.Name, p.Command, state))
		}
		d.cursor = indexOf(d.options, m.agentChoice)
	case focusRepo:
		for _, r := range m.cfg.Repos {
			d.options = append(d.options, r.ID)
			remote := r.Remote
			if remote == "" {
				remote = "no remote"
			}
			d.labels = append(d.labels, fmt.Sprintf("%-16s %s", r.ID, remote))
		}
		d.cursor = indexOf(d.options, m.activeRepoID())
	case focusProject:
		// The empty option is the unfiltered board, and it comes first because
		// it is the state you return to.
		d.options = append(d.options, "")
		d.labels = append(d.labels, fmt.Sprintf("%-16s %d session(s)", "all projects", len(m.sessions)))
		for _, g := range m.projects() {
			n := 0
			for _, s := range m.sessions {
				if s.Group == g {
					n++
				}
			}
			d.options = append(d.options, g)
			d.labels = append(d.labels, fmt.Sprintf("%-16s %d session(s)", g, n))
		}
		d.cursor = indexOf(d.options, m.projectFilter)
	}
	m.dropdown = d
}

func (m Model) viewDropdown(rows, width int) []string {
	st := m.styles
	out := make([]string, 0, rows)

	for i, label := range m.dropdown.labels {
		if len(out) >= rows-1 {
			break
		}
		line := "  " + label
		if i == m.dropdown.cursor {
			line = st.Selected.Render(pad("▸ "+label, width))
		} else {
			line = pad(line, width)
		}
		out = append(out, zone.Mark(zoneOption(i), line))
	}
	if len(out) < rows {
		out = append(out, st.Faint.Render("  ↑↓ choose · enter select · esc cancel"))
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

// applyDropdown commits the highlighted option.
func (m *Model) applyDropdown() tea.Cmd {
	d := m.dropdown
	m.dropdown = dropdown{}
	if d.cursor < 0 || d.cursor >= len(d.options) {
		return nil
	}
	choice := d.options[d.cursor]

	switch d.area {
	case focusAgent:
		m.agentChoice = choice
		m.cfg.DefaultProfile = choice
		_ = core.SaveConfig(m.cfg)
		return status("new sessions will use " + choice)
	case focusRepo:
		m.activeRepo = choice
		return status("new sessions will use " + choice)
	case focusProject:
		m.projectFilter = choice
		if m.selected() == nil {
			if f := m.firstSession(); f != nil {
				m.selectedID = f.ID
			}
		}
		if choice == "" {
			return status("showing all projects")
		}
		return status("filtered to " + choice)
	}
	return nil
}

// cycleChip changes a selector without opening it, for quick left/right nudges.
func (m *Model) cycleChip(area focusArea, dir int) tea.Cmd {
	switch area {
	case focusAgent:
		names := m.cfg.ProfileNames()
		if len(names) == 0 {
			return nil
		}
		m.agentChoice = names[wrap(indexOf(names, m.agentChoice)+dir, len(names))]
		m.cfg.DefaultProfile = m.agentChoice
		_ = core.SaveConfig(m.cfg)
	case focusRepo:
		if len(m.cfg.Repos) == 0 {
			return nil
		}
		ids := make([]string, 0, len(m.cfg.Repos))
		for _, r := range m.cfg.Repos {
			ids = append(ids, r.ID)
		}
		m.activeRepo = ids[wrap(indexOf(ids, m.activeRepoID())+dir, len(ids))]
	case focusProject:
		opts := append([]string{""}, m.projects()...)
		m.projectFilter = opts[wrap(indexOf(opts, m.projectFilter)+dir, len(opts))]
		if m.selected() == nil {
			if f := m.firstSession(); f != nil {
				m.selectedID = f.ID
			}
		}
	}
	return nil
}

func indexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return 0
}

func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
