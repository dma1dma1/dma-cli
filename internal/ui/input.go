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

	// Image paste is useful as a one-shot action on the selected live session:
	// it should not require entering panel focus first. The agent still owns the
	// actual clipboard interpretation, exactly as it does while attached.
	if m.focus == focusBoard && key == "ctrl+v" {
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
		// Nothing is posted: the footer switching back to the board's keymap already
		// says where keystrokes go.
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
	send := sendKeyCmd(s, fk)
	if m.focus != focusPreview {
		// A paste aimed at the board's selected session never passed through panel
		// focus, so nothing has readied a modal composer for it -- and in vim's
		// normal mode ctrl+v starts a block selection instead of pasting. Sequence,
		// not batch: the mode has to change before the paste arrives.
		send = tea.Sequence(insertModeCmd(s), send)
	}
	// Sequenced rather than inlined into the return: startEcho mutates m, and Go
	// does not order that against copying m into the return value.
	cmd := tea.Batch(send, m.startEcho())
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

// onFocusChange manages the text cursor, which must only blink in the input, and
// readies the agent's composer when the keyboard is handed to it.
func (m *Model) onFocusChange() tea.Cmd {
	if m.focus == focusInput {
		return m.input.Focus()
	}
	m.input.Blur()
	if m.focus == focusPreview {
		// Every route into panel focus comes through here -- "t", tab, and a click
		// on the preview -- so a modal composer is readied once per handover
		// rather than once per entry point.
		return insertModeCmd(m.selected())
	}
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
			m.selectSession(s)
			return m, previewCmd(s)
		}
		return m, nil

	case "j", "down", "k", "up":
		dir := 1
		if key == "k" || key == "up" {
			dir = -1
		}
		if s := m.moveV(dir); s != nil {
			m.selectSession(s)
			return m, previewCmd(s)
		}
		return m, nil

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

	case "A":
		// Shifted because "a" attaches. Switching agent is a per-task decision
		// made right before pressing i, so it needs to be one key from the board
		// rather than a tab walk or a click on the chip.
		m.focus = focusAgent
		m.openDropdown(focusAgent)
		return m, nil

	case "X":
		return m.pruneMerged()

	case "R":
		return m, tea.Batch(pollPRsCmd(m.cfg, m.sessions), observeCmd(m.sessions),
			probeCmd(m.prober, m.cfg, m.sessions, m.typedAt))
	}

	return m.sessionAction(key)
}

// pruneMerged clears out the merged column in one keystroke. Merged work is
// the one state where teardown is routine rather than a judgement call, and
// clearing it a card at a time is the most repetitive thing on the board.
//
// It follows the filters, so what X prunes is the merged column as it is on
// screen -- pruning cards a repo or project filter is hiding would be a
// surprise no confirm could undo.
func (m Model) pruneMerged() (tea.Model, tea.Cmd) {
	var merged []*core.Session
	for _, s := range m.visible() {
		if s.Lifecycle == core.LifecycleMerged {
			merged = append(merged, s)
		}
	}
	if len(merged) == 0 {
		return m, errStatus(fmt.Errorf("no merged sessions to prune"))
	}
	noun := "sessions"
	if len(merged) == 1 {
		noun = "session"
	}
	return m.askConfirm(fmt.Sprintf("Prune worktrees and branches for %d merged %s?", len(merged), noun),
		func(mm *Model) tea.Cmd { return teardownAllCmd(mm.cfg, merged) })
}

// sessionAction handles keys that mean the same thing wherever you are.
func (m Model) sessionAction(key string) (tea.Model, tea.Cmd) {
	s := m.selected()
	if s == nil {
		return m, nil
	}
	switch key {
	case "s":
		if !s.TmuxAlive {
			return m, errStatus(fmt.Errorf("terminal for this session is not running"))
		}
		// The agent is about to work and repaint, and the prober must not read
		// that as output it produced on its own.
		m.typedAt[s.ID] = now()
		// The request never passes through panel focus, so nothing has readied a
		// modal composer for it. Sequence, not batch: the mode has to change
		// before the request arrives.
		send := tea.Sequence(insertModeCmd(s), askShipCmd(s))
		// Sequenced rather than inlined into the return: startEcho mutates m, and
		// Go does not order that against copying m into the return value.
		cmd := tea.Batch(send, m.startEcho())
		return m, cmd

	case "o":
		return m, m.prLink(s, linkOpen)

	case "y":
		return m, m.prLink(s, linkCopy)

	case "m":
		if !s.HasPR() {
			return m, errStatus(fmt.Errorf("no PR to merge"))
		}
		// A queued PR is asked about rather than refused: the merge itself
		// re-checks the queue, so pressing m on a card whose queue standing has
		// gone stale re-queues it instead of waiting for the next poll.
		prompt := fmt.Sprintf("Merge PR #%d (%s)?", s.PRNumber, s.Title)
		if s.PRQueued {
			prompt = fmt.Sprintf("PR #%d is in the merge queue. Queue it again?", s.PRNumber)
		}
		return m.askConfirm(prompt,
			func(mm *Model) tea.Cmd { return mergeCmd(mm.cfg, s) })

	case "x":
		// Pruning a session with a live pull request closes it too, and that
		// half of the action reaches other people's review queues -- so the
		// confirm names the PR rather than letting it go quietly.
		prompt := fmt.Sprintf("Prune worktree and branch for %q?", s.Title)
		if s.HasOpenPR() {
			prompt = fmt.Sprintf("Close PR #%d and prune worktree and branch for %q?", s.PRNumber, s.Title)
		}
		return m.askConfirm(prompt,
			func(mm *Model) tea.Cmd { return teardownCmd(mm.cfg, s, ops.TeardownOptions{}) })

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
	return prLinkCmd(remote, s.ID, s.PRNumber, action)
}

// prLinkTarget says what to act on: the address the session already knows, or
// failing that the remote to ask GitHub for it.
func (m Model) prLinkTarget(s *core.Session) (url, remote string, err error) {
	if !s.HasPR() {
		return "", "", fmt.Errorf("no PR for %q yet — press s to ask the agent to open one", s.Title)
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
		m.pendingImages = nil
		m.layoutSizes()
		m.focus = focusBoard
		return m, tea.Batch(m.onFocusChange(), createCmd(m.cfg, req))

	case "shift+enter", "alt+enter", "ctrl+j":
		// Enter is spent on starting the agent, so a task written over several
		// lines needs a second key that means newline. The field's own binding is
		// enter, which never reaches it, so the newline is inserted here.
		//
		// Three spellings for one key: shift+enter is the one people reach for, but
		// a terminal only reports it as its own keypress when it speaks the kitty
		// keyboard protocol or modifyOtherKeys -- everywhere else it arrives as a
		// plain enter and starts the agent. alt+enter and ctrl+j are the fallbacks
		// that survive a terminal without either.
		m.input.InsertRune('\n')
		return m, nil

	case "ctrl+v":
		return m, readClipboardCmd()

	case "backspace":
		// The very start of the task, which in a field that wraps means the first
		// row as well as the first column -- backspace anywhere else in the text is
		// still deleting text.
		if len(m.pendingImages) > 0 && m.input.Line() == 0 && m.input.Column() == 0 {
			m.pendingImages = m.pendingImages[:len(m.pendingImages)-1]
			m.layoutSizes()
			return m, nil
		}
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
	images := make([]ops.ImageAttachment, len(m.pendingImages))
	for i, image := range m.pendingImages {
		images[i] = ops.ImageAttachment{PNG: append([]byte(nil), image.PNG...)}
	}
	return ops.CreateRequest{
		// A task that arrived with line breaks in it still names one session: the
		// first line is the title a card can show and a branch can be slugged
		// from, and the whole of it is what the agent is started on. The title is
		// a placeholder either way -- a summary of the whole task replaces it a
		// few seconds later, see titleCmd.
		Title:  firstLine(task),
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
		InitialImages: images,
		HookURL:       m.hookURL,
	}, nil
}

// handlePaste routes terminal-owned bracketed paste. Bubble Tea v2 reports it
// separately from keypresses, so it must be handed to the focused component
// explicitly.
func (m Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.mode == modePrompt {
		var cmd tea.Cmd
		m.prompt.input, cmd = m.prompt.input.Update(msg)
		return m, cmd
	}
	if m.mode != modeBoard {
		return m, nil
	}
	switch m.focus {
	case focusInput:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	case focusBoard, focusPreview:
		s := m.selected()
		if s == nil || !s.TmuxAlive {
			if m.focus == focusPreview {
				m.focus = focusBoard
			}
			return m, errStatus(fmt.Errorf("terminal for this session is not running"))
		}
		m.typedAt[s.ID] = now()
		return m, tea.Batch(sendPasteCmd(s, msg.Content), m.startEcho())
	}
	return m, nil
}

func (m Model) handleClipboard(msg clipboardMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, errStatus(msg.err)
	}
	if msg.content.Image != nil {
		// Nothing is posted: the input row's own image summary is the receipt.
		m.pendingImages = append(m.pendingImages, *msg.content.Image)
		m.layoutSizes()
		return m, nil
	}
	if msg.content.Text != "" {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.PasteMsg{Content: msg.content.Text})
		return m, cmd
	}
	return m, nil
}

// firstLine is the first line of a task with anything on it.
//
// The input wraps rather than scrolls, so a task can be several lines long --
// pasted, most of the time. Where only one line fits, a leading blank line is
// not the one to show: it would title a card with nothing, and the empty title
// would be refused on the way to creating the session.
func firstLine(task string) string {
	for _, l := range strings.Split(task, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
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
			m.selectSession(s)
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
		// Scroll routes by zone: the diff when it is open, otherwise the column
		// under the pointer.
		if m.mode == modeDiff {
			var cmd tea.Cmd
			m.diffView, cmd = m.diffView.Update(msg)
			return m, cmd
		}
		return m.handleWheel(msg)
	case tea.MouseClickMsg:
		return m.handleClick(msg)
	}
	return m, nil
}

// handleWheel scrolls the column the pointer is over, a card at a time.
//
// Scrolling deliberately leaves the selection alone: the panel below is a live
// agent terminal, and a gesture for looking around the board must not swap out
// what is running in it. The cursor comes back into view the moment a key moves
// it.
func (m Model) handleWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	delta := 0
	switch msg.Mouse().Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return m, nil
	}
	for i := range core.Columns {
		if z := zone.Get(zoneColumn(i)); z != nil && z.InBounds(msg) {
			m.scrollColumn(i, delta)
			return m, nil
		}
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
		// Clicking outside an open list dismisses it, and dismissing it leaves the
		// board in charge -- the same place committing a choice lands.
		m.dropdown = dropdown{}
		m.focus = focusBoard
		cmd := m.onFocusChange()
		return m, cmd
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
		m.selectSession(s)
		m.lastClickID, m.lastClickAt = s.ID, now()
		return m, previewCmd(s)
	}

	// A click that hit none of the targets above -- the header, the status bar,
	// the empty rows under a column's last card -- still says something: the
	// keyboard is no longer aimed where it was. Clicking into the panel is the
	// gesture that hands keystrokes to the agent, so clicking away has to be the
	// gesture that takes them back. Leaving focus where it was means every key
	// keeps going to the agent from a pointer that has visibly left it, and
	// ctrl-q becomes the only way out of a mode the user already tried to leave.
	//
	// Not while the panel is expanded: there is no board on screen to click back
	// to, so the only cells that miss every target are the frame and the chip
	// row, and a click landing there is a miss rather than a departure.
	if m.focus != focusBoard && !m.previewFull {
		m.focus = focusBoard
		// Not inlined into the return: onFocusChange blurs the input through a
		// pointer, and Go does not order that against copying m into the result.
		cmd := m.onFocusChange()
		return m, cmd
	}
	return m, nil
}
