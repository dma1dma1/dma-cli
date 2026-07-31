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

	// The preview owns the keyboard outright, before every binding below --
	// ctrl+c, tab and quit included. An agent needs all three: ctrl+c to
	// interrupt, tab to complete, and no key it might want reserved by the board.
	// This is the same bargain attached mode already makes, so it uses the same
	// single exit.
	if m.focus == focusPreview {
		return m.keyPreview(msg, key)
	}

	// ctrl+c always quits, wherever focus is; sessions keep running.
	if key == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	switch key {
	case "tab":
		return m.moveFocus(1)
	case "shift+tab":
		return m.moveFocus(-1)
	}

	switch m.focus {
	case focusInput:
		return m.keyInput(msg, key)
	case focusAgent, focusRepo, focusProject:
		return m.keyChip(key)
	}
	return m.keyBoard(key)
}

// moveFocus walks the focus ring, skipping stops that would do nothing.
func (m Model) moveFocus(dir int) (tea.Model, tea.Cmd) {
	i := indexOfFocus(m.focus)
	// One lap at most: if nothing is focusable we land back where we started.
	for range focusRing {
		i = wrap(i+dir, len(focusRing))
		if next := focusRing[i]; m.focusable(next) {
			m.focus = next
			return m, m.onFocusChange()
		}
	}
	return m, nil
}

// focusable reports whether a focus stop is worth landing on right now.
//
// The preview is the only conditional one: with no live terminal behind it there
// is nothing to type into, and tab parking there would look like the UI had
// stopped responding.
func (m Model) focusable(f focusArea) bool {
	if f != focusPreview {
		return true
	}
	s := m.selected()
	return s != nil && s.TmuxAlive
}

func indexOfFocus(f focusArea) int {
	for i, x := range focusRing {
		if x == f {
			return i
		}
	}
	return 0
}

// --- preview focus ---

// keyPreview forwards a keystroke to the selected session's terminal.
//
// detachKey is the one key held back, exactly as in attached mode. Escape in
// particular has to go through: it is how you interrupt a coding agent, and
// spending it on "leave the panel" would make the panel useless for the moment
// you most want it.
func (m Model) keyPreview(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if key == detachKeypress {
		m.focus = focusBoard
		// No status message: the footer switching back to the board's keymap already
		// says where keystrokes go, and a line saying it in words only covers the
		// keys it is describing.
		return m, nil
	}
	s := m.selected()
	if s == nil || !s.TmuxAlive {
		// The agent died under us. Hand the keyboard back rather than swallowing
		// keystrokes into a terminal that is gone.
		m.focus = focusBoard
		return m, errStatus(fmt.Errorf("terminal for this session is not running"))
	}
	fk, ok := tmuxKey(msg.Key())
	if !ok {
		return m, nil
	}
	// This keystroke is about to change the pane, and the prober must not read
	// that change as the agent producing output.
	m.typedAt[s.ID] = now()
	// Sequenced rather than inlined into the return: startEcho mutates m, and Go
	// does not order that against copying m into the return value.
	cmd := tea.Batch(sendKeyCmd(s, fk), m.startEcho())
	return m, cmd
}

// startEcho opens (or extends) the window during which the panel re-reads the
// pane at echoInterval, and returns the ticker only when one is not already
// running -- a ticker per keystroke would multiply the captures by the typing
// rate for no extra freshness.
func (m *Model) startEcho() tea.Cmd {
	m.echoUntil = now().Add(echoWindow)
	if m.echoing {
		return nil
	}
	m.echoing = true
	return echoTickCmd()
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

	case "t":
		// Type to the selected agent without leaving the board. This is the cheap
		// half of attaching: good for answering a prompt or steering a run, while
		// "a" remains the way to get a real terminal.
		s := m.selected()
		if s == nil {
			return m, nil
		}
		if !s.TmuxAlive {
			return m, errStatus(fmt.Errorf("terminal for %s is not running", s.Title))
		}
		m.focus = focusPreview
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
		// A picker rather than a text field: the projects that exist are the
		// answer nearly every time, and retyping one is how a board ends up with
		// "auth" and "Auth" as two projects.
		m.openMoveProject(m.selected())
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
		// The footer carries a [repo:x] tag while a filter is on, and the board
		// visibly changes, so the state is already on screen either way.
		if m.repoFilter != "" {
			m.repoFilter = ""
			m.rebuild()
			return m, nil
		}
		m.repoFilter = m.activeRepoID()
		m.rebuild()
		return m, nil

	case "R":
		return m, tea.Batch(pollPRsCmd(m.cfg, m.sessions), observeCmd(m.sessions),
			probeCmd(m.prober, m.cfg, m.sessions, m.typedAt), status("refreshing…"))
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

	case "o":
		return m, m.prLink(s, linkOpen)

	case "y":
		return m, m.prLink(s, linkCopy)

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

// prLink opens or copies the selected session's pull request, fetching the
// address first if the session does not already know it.
func (m Model) prLink(s *core.Session, action linkAction) tea.Cmd {
	if s == nil {
		return nil
	}
	url, remote, err := m.prLinkTarget(s)
	switch {
	case err != nil:
		return errStatus(err)
	case url != "":
		return linkCmd(url, action)
	}
	return tea.Batch(prLinkCmd(remote, s.ID, s.PRNumber, action), status("finding PR link…"))
}

// prLinkTarget says what to act on: the address the session already knows, or
// failing that the remote to ask GitHub for it.
func (m Model) prLinkTarget(s *core.Session) (url, remote string, err error) {
	if !s.HasPR() {
		return "", "", fmt.Errorf("no PR for %q yet — press s to push and open one", s.Title)
	}
	if s.PRURL != "" {
		return s.PRURL, "", nil
	}
	repo, ok := m.cfg.Repo(s.RepoID)
	if !ok || repo.Remote == "" {
		return "", "", fmt.Errorf("no remote for %s, so #%d has no link to follow", s.RepoID, s.PRNumber)
	}
	return "", repo.Remote, nil
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
	// The card is now in the other column, in front of the user.
	return m, nil
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
		req, err := m.newSessionRequest(task)
		if err != nil {
			return m, errStatus(err)
		}
		m.input.SetValue("")
		m.focus = focusBoard
		return m, tea.Batch(m.onFocusChange(), createCmd(m.cfg, req),
			status("starting "+req.Profile+" in "+req.RepoID+"…"))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// newSessionRequest describes the session the chips currently add up to.
func (m Model) newSessionRequest(task string) (ops.CreateRequest, error) {
	repo := m.activeRepoID()
	if repo == "" {
		return ops.CreateRequest{}, fmt.Errorf("no repo selected — press r to add one")
	}
	base := ""
	if r, ok := m.cfg.Repo(repo); ok {
		base = r.BaseBranch
	}
	cols, rows := m.previewDims()
	return ops.CreateRequest{
		Title:  task,
		RepoID: repo,
		Cols:   cols,
		Rows:   rows,
		// A new session joins the project the chip names, which is what
		// selecting one means when creating work. The chip's default is no
		// project, so that is where a session lands unless you say otherwise.
		Group:         m.projectFilter,
		Profile:       m.agentChoice,
		BaseBranch:    base,
		InitialPrompt: task,
		HookURL:       m.hookURL,
	}, nil
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
	case "x":
		// Only projects can be removed from a list: agents and repos have their
		// own screens for that, and a repo unregistered by a stray x here would
		// take the board's cards with it.
		if m.dropdown.area != focusProject {
			return m, nil
		}
		return m, m.removeProject()
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
		// Declining leaves everything as it was; the question closing is the
		// whole of the feedback.
		return m, nil
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

	// Clicking the agent's output aims the keyboard at it. The preview looks like
	// a terminal, so a click landing there and doing nothing was the single most
	// misleading thing about the panel.
	if z := zone.Get(zonePreview); z != nil && z.InBounds(msg) {
		if !m.focusable(focusPreview) {
			return m, nil
		}
		m.focus = focusPreview
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
