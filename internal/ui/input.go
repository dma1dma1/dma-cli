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

	switch m.mode {
	case modeCompose:
		return m.keyCompose(msg, key)
	case modePrompt:
		return m.keyPrompt(msg, key)
	case modeConfirm:
		return m.keyConfirm(key)
	case modeHelp:
		m.mode = modeBoard
		return m, nil
	case modeDetail:
		return m.keyDetail(msg, key)
	case modeRepos:
		return m.keyRepos(key)
	}
	return m.keyBoard(key)
}

// --- board ---

func (m Model) keyBoard(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "ctrl+c":
		// Sessions keep running: quitting the board does not stop the agents.
		m.quitting = true
		return m, tea.Quit

	case "?":
		m.mode = modeHelp
		return m, nil

	case "r":
		m.mode = modeRepos
		return m, nil

	case "n":
		m.startCompose()
		m.mode = modeCompose
		return m, nil

	case "h", "left", "l", "right":
		dir := -1
		if key == "l" || key == "right" {
			dir = 1
		}
		if s := m.layout.moveHorizontal(m.layout.find(m.selectedID), dir); s != nil {
			m.selectedID = s.ID
		}
		return m, nil

	case "j", "down", "k", "up":
		dir := 1
		if key == "k" || key == "up" {
			dir = -1
		}
		if s := m.layout.moveVertical(m.layout.find(m.selectedID), dir, m.collapsed); s != nil {
			m.selectedID = s.ID
		}
		return m, nil

	case "enter":
		if m.selected() == nil {
			return m, nil
		}
		m.mode = modeDetail
		m.resizePanes()
		return m, m.refreshDetailPanes()

	case "H", "L":
		return m.moveCard(key == "L")

	case "g":
		g := m.currentGroup()
		m.collapsed[g] = !m.collapsed[g]
		m.rebuild()
		return m, nil

	case "G":
		s := m.selected()
		if s == nil {
			return m, nil
		}
		m.startPrompt(promptGroup, "group", s.Group, s.ID)
		m.mode = modePrompt
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
		m.startPrompt(promptRepoFilter, "filter repo", "", "")
		m.mode = modePrompt
		return m, nil

	case "d":
		if m.selected() == nil {
			return m, nil
		}
		m.mode = modeDetail
		m.resizePanes()
		return m, m.refreshDetailPanes()

	case "R":
		return m, tea.Batch(
			pollPRsCmd(m.cfg, m.sessions),
			observeCmd(m.sessions),
			status("refreshing…"),
		)
	}

	return m.sharedAction(key)
}

// sharedAction handles the bindings that mean the same thing on the board and
// in the detail view.
func (m Model) sharedAction(key string) (tea.Model, tea.Cmd) {
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
		return m.askConfirm(
			fmt.Sprintf("Prune worktree and branch for %q?", s.Title),
			func(mm *Model) tea.Cmd { return teardownCmd(mm.cfg, s, false) })

	case "D":
		return m.askConfirm(fmt.Sprintf("Kill agent session for %q? (worktree kept)", s.Title),
			func(mm *Model) tea.Cmd { return killCmd(s) })
	}
	return m, nil
}

// moveCard is the manual lifecycle override. Only user actions and durable
// git/PR events move cards between columns.
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
	m.rebuild()
	return m, status("moved to " + s.Lifecycle.Title())
}

// --- detail ---

func (m Model) keyDetail(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.mode = modeBoard
		return m, nil

	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "?":
		m.mode = modeHelp
		return m, nil

	case "a":
		s := m.selected()
		if s == nil {
			return m, nil
		}
		if !s.TmuxAlive {
			return m, errStatus(fmt.Errorf("tmux session %s is not running", s.TmuxSession))
		}
		return m, attachCmd(s)

	case "tab":
		if m.diffMode == gitx.DiffUncommitted {
			m.diffMode = gitx.DiffBranch
		} else {
			m.diffMode = gitx.DiffUncommitted
		}
		return m, m.refreshDetailPanes()

	case "j", "down", "k", "up":
		// j/k step to the next session without going back up to the board.
		dir := 1
		if key == "k" || key == "up" {
			dir = -1
		}
		if s := m.nextSession(dir); s != nil {
			m.selectedID = s.ID
			return m, m.refreshDetailPanes()
		}
		return m, nil

	case "R":
		return m, tea.Batch(m.refreshDetailPanes(), pollPRsCmd(m.cfg, m.sessions))
	}

	if mm, cmd := m.sharedAction(key); cmd != nil {
		return mm, cmd
	}
	// Anything left over scrolls the diff pane.
	var cmd tea.Cmd
	m.diffView, cmd = m.diffView.Update(msg)
	return m, cmd
}

// nextSession walks the flattened board order, so j/k in the detail view visits
// every session exactly once.
func (m Model) nextSession(dir int) *core.Session {
	var flat []*core.Session
	for _, l := range m.layout.Lanes {
		for c := 0; c < 4; c++ {
			flat = append(flat, l.Columns[c]...)
		}
	}
	if len(flat) == 0 {
		return nil
	}
	idx := -1
	for i, s := range flat {
		if s.ID == m.selectedID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return flat[0]
	}
	next := idx + dir
	if next < 0 || next >= len(flat) {
		return nil
	}
	return flat[next]
}

// --- compose ---

func (m Model) keyCompose(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.compose = compose{}
		m.mode = modeBoard
		return m, nil

	case "tab":
		m.compose.cycle(1)
		return m, nil

	case "shift+tab":
		m.compose.cycle(-1)
		return m, nil

	case "left", "right":
		// On the repo field the arrows pick a repo; elsewhere they move the
		// text cursor as usual.
		if m.compose.focusedField() == fieldRepo {
			dir := 1
			if key == "left" {
				dir = -1
			}
			m.compose.cycleRepo(m.cfg.Repos, dir)
			return m, nil
		}

	case "enter":
		m.compose.syncField()
		c := m.compose
		task := strings.TrimSpace(c.get(fieldTask))
		if task == "" {
			return m, errStatus(fmt.Errorf("a task description is required"))
		}
		req := ops.CreateRequest{
			Title:         task,
			RepoID:        c.get(fieldRepo),
			Group:         c.get(fieldGroup),
			Profile:       c.get(fieldProfile),
			BaseBranch:    c.get(fieldBase),
			InitialPrompt: task,
			HookURL:       m.hookURL,
		}
		m.compose = compose{}
		m.mode = modeBoard
		return m, tea.Batch(createCmd(m.cfg, m.sessions, req), status("creating session…"))
	}

	m.compose.consumeFresh(isPrintable(msg))
	var cmd tea.Cmd
	m.compose.input, cmd = m.compose.input.Update(msg)
	return m, cmd
}

// isPrintable reports whether a key press inserts a character, as opposed to
// navigating or editing.
func isPrintable(msg tea.KeyPressMsg) bool {
	return msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0
}

// --- prompt ---

func (m Model) keyPrompt(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.prompt = prompt{}
		m.mode = modeBoard
		return m, nil

	case "enter":
		p := m.prompt
		val := strings.TrimSpace(p.input.Value())
		m.prompt = prompt{}
		m.mode = modeBoard

		switch p.kind {
		case promptGroup:
			s := core.FindByID(m.sessions, p.target)
			if s == nil {
				return m, nil
			}
			s.Group = val
			// Typing a label that does not exist creates it.
			if m.cfg.AddGroup(val) {
				_ = core.SaveConfig(m.cfg)
			}
			m.save()
			m.rebuild()
			return m, status("group set")

		case promptRepoFilter:
			if val != "" {
				if _, ok := m.cfg.Repo(val); !ok {
					return m, errStatus(fmt.Errorf("no repo with id %q", val))
				}
			}
			m.repoFilter = val
			m.rebuild()
			if s := m.selected(); s == nil {
				if f := m.layout.first(); f != nil {
					m.selectedID = f.ID
				}
			}
			return m, status("repo filter: " + val)

		case promptAddRepo:
			if val == "" {
				return m, nil
			}
			return m, tea.Batch(adoptCmd(m.cfg, val), status("registering "+val+"…"))

		case promptPRTitle:
			s := core.FindByID(m.sessions, p.target)
			if s == nil {
				return m, nil
			}
			if val == "" {
				val = s.Title
			}
			return m, tea.Batch(shipCmd(m.cfg, s, val), status("pushing and opening PR…"))
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.prompt.input, cmd = m.prompt.input.Update(msg)
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
	// Mouse is meaningless while a modal input owns the bottom bar.
	if m.mode == modeConfirm {
		return m, nil
	}

	switch msg.(type) {
	case tea.MouseWheelMsg:
		// Scroll routes by zone: the diff in the detail view, swimlanes on the
		// board.
		if m.mode == modeDetail {
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
	// Group headers toggle collapse.
	for _, l := range m.layout.Lanes {
		if z := zone.Get(zoneGroup(l.Group)); z != nil && z.InBounds(msg) {
			m.collapsed[l.Group] = !m.collapsed[l.Group]
			m.rebuild()
			return m, nil
		}
	}

	for _, s := range m.sessions {
		z := zone.Get(zoneCard(s.ID))
		if z == nil || !z.InBounds(msg) {
			continue
		}
		// A single click only selects. Misclicks on a dense board are frequent,
		// so opening a session takes a double click or enter.
		if m.selectedID == s.ID && m.isDoubleClick() {
			m.selectedID = s.ID
			m.mode = modeDetail
			m.resizePanes()
			return m, m.refreshDetailPanes()
		}
		m.selectedID = s.ID
		m.markClick()
		return m, nil
	}
	return m, nil
}
