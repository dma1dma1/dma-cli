package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/ops"
)

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Modal states own the keyboard outright.
	switch m.mode {
	case modeConfirm:
		return m.keyConfirm(key)
	case modePrompt:
		return m.keyPrompt(msg, key)
	case modeHelp:
		m.mode = modeBoard
		return m, nil
	case modeRepos:
		return m.keyRepos(key)
	case modeDiff:
		return m.keyDiff(msg, key)
	}

	if m.dropdown.open {
		return m.keyDropdown(key)
	}

	// ctrl+c always quits, wherever focus is; sessions keep running.
	if key == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	switch key {
	case "tab":
		m.focus = focusRing[wrap(indexOfFocus(m.focus)+1, len(focusRing))]
		return m, m.onFocusChange()
	case "shift+tab":
		m.focus = focusRing[wrap(indexOfFocus(m.focus)-1, len(focusRing))]
		return m, m.onFocusChange()
	}

	switch m.focus {
	case focusInput:
		return m.keyInput(msg, key)
	case focusAgent, focusRepo, focusProject:
		return m.keyChip(key)
	}
	return m.keyBoard(key)
}

func indexOfFocus(f focusArea) int {
	for i, x := range focusRing {
		if x == f {
			return i
		}
	}
	return 0
}

// onFocusChange manages the text cursor, which must only blink in the input.
func (m *Model) onFocusChange() tea.Cmd {
	if m.focus == focusInput {
		return m.input.Focus()
	}
	m.input.Blur()
	return nil
}

// --- board focus ---

func (m Model) keyBoard(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		m.quitting = true
		return m, tea.Quit

	case "?":
		m.mode = modeHelp
		return m, nil

	case "i", "n":
		m.focus = focusInput
		return m, m.onFocusChange()

	case "h", "left", "l", "right":
		dir := -1
		if key == "l" || key == "right" {
			dir = 1
		}
		if s := m.moveH(dir); s != nil {
			m.selectedID, m.preview = s.ID, ""
			return m, previewCmd(s)
		}
		return m, nil

	case "j", "down", "k", "up":
		dir := 1
		if key == "k" || key == "up" {
			dir = -1
		}
		if s := m.moveV(dir); s != nil {
			m.selectedID, m.preview = s.ID, ""
			return m, previewCmd(s)
		}
		return m, nil

	case "e":
		// Expand the session panel to the whole screen and back. The agents are
		// resized to match, so the expanded view is genuinely more room rather
		// than the same narrow render in a bigger frame.
		m.previewFull = !m.previewFull
		return m, tea.Batch(m.syncAgentSize(), previewCmd(m.selected()))

	case "a":
		s := m.selected()
		if s == nil {
			return m, nil
		}
		if !s.TmuxAlive {
			return m, errStatus(fmt.Errorf("terminal for %s is not running", s.Title))
		}
		return m, attachCmd(s)

	case "enter", "d":
		if m.selected() == nil {
			return m, nil
		}
		m.mode = modeDiff
		return m, m.refreshDiff()

	case "H", "L":
		return m.moveCard(key == "L")

	case "G":
		s := m.selected()
		if s == nil {
			return m, nil
		}
		m.startPrompt(promptGroup, "project", s.Group, s.ID)
		m.mode = modePrompt
		return m, nil

	case "r":
		m.mode = modeRepos
		return m, nil

	case "p":
		m.focus = focusProject
		m.openDropdown(focusProject)
		return m, nil

	case "f":
		if !m.cfg.MultiRepo() {
			return m, nil
		}
		if m.repoFilter != "" {
			m.repoFilter = ""
			m.rebuild()
			return m, status("repo filter cleared")
		}
		m.repoFilter = m.activeRepoID()
		m.rebuild()
		return m, status("showing only " + m.repoFilter)

	case "R":
		return m, tea.Batch(pollPRsCmd(m.cfg, m.sessions), observeCmd(m.sessions),
			probeCmd(m.prober, m.cfg, m.sessions), status("refreshing…"))
	}

	return m.sessionAction(key)
}

// sessionAction handles keys that mean the same thing wherever you are.
func (m Model) sessionAction(key string) (tea.Model, tea.Cmd) {
	s := m.selected()
	if s == nil {
		return m, nil
	}
	switch key {
	case "s":
		m.startPrompt(promptPRTitle, "PR title", s.Title, s.ID)
		m.mode = modePrompt
		return m, nil

	case "m":
		if !s.HasPR() {
			return m, errStatus(fmt.Errorf("no PR to merge"))
		}
		return m.askConfirm(fmt.Sprintf("Merge PR #%d (%s)?", s.PRNumber, s.Title),
			func(mm *Model) tea.Cmd { return mergeCmd(mm.cfg, s) })

	case "x":
		return m.askConfirm(fmt.Sprintf("Prune worktree and branch for %q?", s.Title),
			func(mm *Model) tea.Cmd { return teardownCmd(mm.cfg, s, false) })

	case "D":
		return m.askConfirm(fmt.Sprintf("Kill the agent for %q? (worktree kept)", s.Title),
			func(mm *Model) tea.Cmd { return killCmd(s) })
	}
	return m, nil
}

// moveCard is the manual column override. It is most useful for the PR-owned
// columns; the idle/active pair is reclaimed by the agent on its next report.
func (m Model) moveCard(right bool) (tea.Model, tea.Cmd) {
	s := m.selected()
	if s == nil {
		return m, nil
	}
	idx := s.Lifecycle.ColumnIndex()
	if right {
		idx++
	} else {
		idx--
	}
	if idx < 0 || idx >= len(core.Columns) {
		return m, nil
	}
	s.Lifecycle = core.Columns[idx]
	m.save()
	return m, status("moved to " + s.Lifecycle.Title())
}

// --- input focus ---

func (m Model) keyInput(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.focus = focusBoard
		return m, m.onFocusChange()

	case "enter":
		task := strings.TrimSpace(m.input.Value())
		if task == "" {
			m.focus = focusBoard
			return m, m.onFocusChange()
		}
		repo := m.activeRepoID()
		if repo == "" {
			return m, errStatus(fmt.Errorf("no repo selected — press r to add one"))
		}
		base := ""
		if r, ok := m.cfg.Repo(repo); ok {
			base = r.BaseBranch
		}
		cols, rows := m.previewDims()
		req := ops.CreateRequest{
			Title:  task,
			RepoID: repo,
			Cols:   cols,
			Rows:   rows,
			// A new session joins whatever project the board is filtered to,
			// which is what the project selector means when creating work.
			Group:         m.projectFilter,
			Profile:       m.agentChoice,
			BaseBranch:    base,
			InitialPrompt: task,
			HookURL:       m.hookURL,
		}
		m.input.SetValue("")
		m.focus = focusBoard
		return m, tea.Batch(m.onFocusChange(), createCmd(m.cfg, m.sessions, req),
			status("starting "+m.agentChoice+" in "+repo+"…"))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// --- chip focus ---

func (m Model) keyChip(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.focus = focusBoard
		return m, nil
	case "enter", " ", "down":
		m.openDropdown(m.focus)
		return m, nil
	case "left", "h":
		return m, m.cycleChip(m.focus, -1)
	case "right", "l":
		return m, m.cycleChip(m.focus, 1)
	case "q":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// --- dropdown ---

func (m Model) keyDropdown(key string) (tea.Model, tea.Cmd) {
	n := len(m.dropdown.options)
	switch key {
	case "esc", "q":
		m.dropdown = dropdown{}
		return m, nil
	case "j", "down":
		m.dropdown.cursor = wrap(m.dropdown.cursor+1, n)
		return m, nil
	case "k", "up":
		m.dropdown.cursor = wrap(m.dropdown.cursor-1, n)
		return m, nil
	case "enter", " ":
		cmd := m.applyDropdown()
		m.focus = focusBoard
		return m, cmd
	}
	return m, nil
}

// --- diff view ---

func (m Model) keyDiff(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "d", "q":
		m.mode = modeBoard
		return m, nil

	case "tab":
		if m.diffMode == gitx.DiffUncommitted {
			m.diffMode = gitx.DiffBranch
		} else {
			m.diffMode = gitx.DiffUncommitted
		}
		return m, m.refreshDiff()

	case "j", "down", "k", "up":
		dir := 1
		if key == "k" || key == "up" {
			dir = -1
		}
		if s := m.stepSession(dir); s != nil {
			m.selectedID, m.preview = s.ID, ""
			return m, tea.Batch(m.refreshDiff(), previewCmd(s))
		}
		return m, nil

	case "a":
		s := m.selected()
		if s == nil || !s.TmuxAlive {
			return m, nil
		}
		return m, attachCmd(s)
	}

	if mm, cmd := m.sessionAction(key); cmd != nil {
		return mm, cmd
	}
	var cmd tea.Cmd
	m.diffView, cmd = m.diffView.Update(msg)
	return m, cmd
}

// --- confirm ---

func (m Model) keyConfirm(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "y", "Y":
		action := m.confirm.action
		m.confirm = confirmState{}
		m.mode = modeBoard
		if action == nil {
			return m, nil
		}
		mm := m
		return m, action(&mm)
	case "n", "N", "esc", "q":
		m.confirm = confirmState{}
		m.mode = modeBoard
		return m, status("cancelled")
	}
	return m, nil
}

// --- mouse ---

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeConfirm || m.mode == modePrompt {
		return m, nil
	}

	switch msg.(type) {
	case tea.MouseWheelMsg:
		// Scroll routes by zone: the diff when it is open, otherwise nothing --
		// the columns are not scrollable yet.
		if m.mode == modeDiff {
			var cmd tea.Cmd
			m.diffView, cmd = m.diffView.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.MouseClickMsg:
		return m.handleClick(msg)
	}
	return m, nil
}

func (m Model) handleClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// An open dropdown owns the clicks inside the panel.
	if m.dropdown.open {
		for i := range m.dropdown.options {
			if z := zone.Get(zoneOption(i)); z != nil && z.InBounds(msg) {
				m.dropdown.cursor = i
				cmd := m.applyDropdown()
				m.focus = focusBoard
				return m, cmd
			}
		}
		m.dropdown = dropdown{}
		return m, nil
	}

	for _, c := range []struct {
		z    string
		area focusArea
	}{
		{zoneAgentChip, focusAgent},
		{zoneRepoChip, focusRepo},
		{zoneProjectChip, focusProject},
	} {
		if z := zone.Get(c.z); z != nil && z.InBounds(msg) {
			m.focus = c.area
			m.openDropdown(c.area)
			return m, nil
		}
	}

	if z := zone.Get(zoneInput); z != nil && z.InBounds(msg) {
		m.focus = focusInput
		return m, m.onFocusChange()
	}

	for _, s := range m.sessions {
		z := zone.Get(zoneCard(s.ID))
		if z == nil || !z.InBounds(msg) {
			continue
		}
		// A single click selects; opening the diff takes a double click or enter,
		// because misclicks on a dense board are frequent.
		if m.selectedID == s.ID && m.lastClickID == s.ID &&
			timeSince(m.lastClickAt) < doubleClickWindow {
			m.mode = modeDiff
			return m, m.refreshDiff()
		}
		m.focus = focusBoard
		m.selectedID, m.preview = s.ID, ""
		m.lastClickID, m.lastClickAt = s.ID, now()
		return m, previewCmd(s)
	}
	return m, nil
}
