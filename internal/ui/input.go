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
		return m.keyHelp(msg, key)
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
	// Typing means returning to the live terminal, just as leaving scrollback in
	// an attached tmux client does before interacting with the application.
	m.previewScroll = 0
	// This keystroke is about to change the pane, and the prober must not read
	// that change as the agent producing output.
	m.touchedAt[s.ID] = now()
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
		m.helpQuery = ""
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
		return m, m.attach(s)

	case "enter", "d":
		if m.selected() == nil {
			return m, nil
		}
		cmd := m.openDiff()
		return m, cmd

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
			probeCmd(m.prober, m.cfg, m.sessions, m.touchedAt, m.hookSeen))
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
		m.touchedAt[s.ID] = now()
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
		} else if s.PRCI == core.CIPending {
			prompt = fmt.Sprintf("Enable auto-merge for PR #%d when CI passes?", s.PRNumber)
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
		return m.startTask(false)

	case "ctrl+enter":
		// Start it and stay where you are. This mirrors enter's submit action
		// while the modifier says not to move the panel to the new session.
		return m.startTask(true)

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

	case "ctrl+u":
		// Start the task over in one keystroke, images included: what the box
		// holds is one composed task, and half-clearing it would leave an
		// attachment to notice and remove separately. The composer wraps onto as
		// many rows as the task needs, so the shell's kill-to-line-start is not
		// the same thing -- a pasted task has to be cleared row by row otherwise.
		if m.input.Value() == "" && len(m.pendingImages) == 0 {
			return m, nil
		}
		m.input.SetValue("")
		m.pendingImages = nil
		m.layoutSizes()
		return m, nil

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

// startTask hands the composed task to a new session and returns to the board.
//
// background says the board's cursor stays where it is rather than following the
// new card. Deciding to start work is not the same as deciding to watch it: the
// worktree, the fetch, and the agent's first frame take seconds, so a foreground
// start moves the panel off whatever you went back to reading in the meantime,
// which is the one moment you did not ask for it to move.
//
// An empty composer closes either way. There is no task to start and nothing to
// keep the box open for.
func (m Model) startTask(background bool) (tea.Model, tea.Cmd) {
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
	return m, tea.Batch(m.onFocusChange(), createCmd(m.cfg, req, background))
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
		m.previewScroll = 0
		m.touchedAt[s.ID] = now()
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

// keyDiff routes a key by which pane has focus: the tree walks rows, the diff
// scrolls lines.
//
// Stepping between sessions moves off j/k to [ and ], which is the one piece of
// muscle memory the file tree costs. It is unavoidable: j/k in a pane of rows
// cannot also mean "a different agent's work".
func (m Model) keyDiff(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	// The two overlays own the keyboard outright while they are up, before every
	// binding below: what they are is a text field, and a field that reserved
	// half the alphabet for the view behind it would not be one.
	if m.review.picker.open() {
		return m.keyPicker(msg, key)
	}
	if m.review.find.active {
		return m.keyFind(msg, key)
	}

	switch key {
	case "esc", "d", "q":
		// A finished search is still a mode: its hits are on the screen and n
		// still steps them, so escape puts that down before it leaves the view.
		if key == "esc" && m.review.find.query != "" {
			m.review.find.clear()
			m.drawPane()
			return m, nil
		}
		m.mode = modeBoard
		return m, nil

	case "/":
		cmd := m.review.find.open(m.diffPaneWidth() - 8)
		return m, cmd

	case "f":
		cmd := m.review.picker.start(pickerFiles, m.pickerWidth())
		m.rankPaths()
		return m, cmd

	case "g":
		cmd := m.review.picker.start(pickerGrep, m.pickerWidth())
		return m, cmd

	case "c":
		// The same file, asked the other question.
		//
		// Going back to the diff is always the tree's diff, since a diff is
		// inherently about what changed. Going to the contents prefers the row
		// under the cursor, falling back to whatever a search left open -- that
		// is the case where the file is not in the tree at all.
		if m.review.source == sourceFile {
			m.review.source = sourceDiff
			cmd := m.showSelectedFile()
			return m, cmd
		}
		if row, ok := m.review.files.selected(); ok && !row.dir {
			m.review.filePath = row.path
		}
		if m.review.filePath == "" {
			return m, errStatus(fmt.Errorf("%s is a directory — pick a file to read it", rootLabel))
		}
		m.review.source = sourceFile
		cmd := m.showSelectedFile()
		return m, cmd

	case "tab":
		if m.review.mode == gitx.DiffUncommitted {
			m.review.mode = gitx.DiffBranch
		} else {
			m.review.mode = gitx.DiffUncommitted
		}
		cmd := m.refreshDiff()
		return m, cmd

	case "h", "l":
		// The tree is to the left of the diff, so the keys that move between
		// cards on the board move between panes here.
		if m.diffTreeWidth() == 0 {
			return m, nil
		}
		m.review.treeFocus = key == "h"
		return m, nil

	case "e":
		// Hiding the tree changes the diff pane's width, which is part of how the
		// diff was rendered, so the pane has to be asked for again.
		m.review.treeHidden = !m.review.treeHidden
		if m.diffTreeWidth() == 0 {
			m.review.treeFocus = false
		}
		m.layoutSizes()
		cmd := m.showSelectedFile()
		return m, cmd

	case "}", "{":
		// Jump between the separate changes in this file. Scrolling to a row
		// rather than to a line number: the pane may hold delta's rendering of
		// the patch, whose rows and the file's lines are not the same thing.
		dir := 1
		if key == "{" {
			dir = -1
		}
		if m.review.doc == nil {
			return m, nil
		}
		row, ok := m.review.doc.NextHunkRow(m.review.view.YOffset(), dir)
		if !ok {
			return m, nil
		}
		m.review.view.SetYOffset(row)
		m.review.treeFocus = false
		return m, nil

	case "t":
		return m.tellAgentAboutHunk()

	case "v":
		// Two columns are delta's doing, not git's, so without it there is
		// nothing to switch to -- and a key that silently does nothing reads as
		// a bug.
		if !gitx.HasDelta() {
			return m, errStatus(fmt.Errorf("side-by-side needs delta on PATH"))
		}
		m.review.sideBySide = !m.review.sideBySide
		cmd := m.showSelectedFile()
		return m, cmd

	case "[", "]":
		dir := 1
		if key == "[" {
			dir = -1
		}
		if s := m.stepSession(dir); s != nil {
			m.selectSession(s)
			cmd := m.refreshDiff()
			return m, tea.Batch(cmd, previewCmd(s))
		}
		return m, nil

	case "n", "N":
		// While a search has hits on screen, n and N step them -- the pager
		// bargain, and the reason escape is advertised as the way to put the
		// search down. With no search running, n is the next file it has always
		// been.
		if len(m.review.find.matches) > 0 {
			dir := 1
			if key == "N" {
				dir = -1
			}
			return m.stepFind(dir)
		}
		if key == "N" {
			return m, nil
		}
		if !m.review.files.moveFile(1) {
			return m, nil
		}
		cmd := m.showTreeSelection()
		return m, cmd

	case "p":
		if !m.review.files.moveFile(-1) {
			return m, nil
		}
		cmd := m.showTreeSelection()
		return m, cmd

	case "enter", " ":
		// On a directory this opens or closes it; on a file there is nothing to
		// open, so it hands focus to the diff that is already beside it.
		if m.review.files.toggle() {
			cmd := m.showTreeSelection()
			return m, cmd
		}
		m.review.treeFocus = false
		return m, nil

	case "j", "down", "k", "up":
		if !m.review.treeFocus {
			break // the viewport scrolls the diff
		}
		dir := 1
		if key == "k" || key == "up" {
			dir = -1
		}
		m.review.files.move(dir)
		cmd := m.showTreeSelection()
		return m, cmd

	case "a":
		s := m.selected()
		if s == nil || !s.TmuxAlive {
			return m, nil
		}
		return m, m.attach(s)
	}

	if mm, cmd := m.sessionAction(key); cmd != nil {
		return mm, cmd
	}
	var cmd tea.Cmd
	m.review.view, cmd = m.review.view.Update(msg)
	return m, cmd
}

// tellAgentAboutHunk types a reference to the change on screen into the agent's
// terminal and hands it the keyboard, without pressing Enter.
//
// This is the point of knowing where the hunks are. Reviewing an agent's work
// and steering it are the same activity interrupted by a context switch: finding
// the file again in the agent's pane, and typing out where you were looking. The
// reference is left unsent so the sentence is still yours to finish.
func (m Model) tellAgentAboutHunk() (tea.Model, tea.Cmd) {
	s := m.selected()
	row, ok := m.review.files.selected()
	if s == nil || !ok || row.dir {
		return m, nil
	}
	if !s.TmuxAlive {
		return m, errStatus(fmt.Errorf("terminal for %s is not running", s.Title))
	}

	// The whole file when the pane holds nothing with a line number in it -- a
	// binary file, a rendering this could not read: a path on its own is still a
	// better handle than nothing.
	ref := row.path
	if hunks := m.diffHunks(); len(hunks) > 0 {
		ref = hunks[m.review.doc.HunkAt(m.review.view.YOffset())].Ref(row.path)
	}

	// Back to the board with the agent focused, since what follows is typing to
	// it: the review view forwards nothing.
	m.mode = modeBoard
	m.focus = focusPreview
	return m, tea.Batch(sendPasteCmd(s, "look at "+ref+" "), m.onFocusChange())
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
	case tea.MouseMotionMsg:
		return m.dragPreviewSelection(msg)
	case tea.MouseReleaseMsg:
		return m.releasePreviewSelection(msg)
	case tea.MouseWheelMsg:
		m.clearPreviewSelection()
		// Scroll routes by zone: in the review view, whichever of the two panes
		// the pointer is over; in a focused session panel, the agent's history;
		// otherwise the column under the pointer.
		if m.mode == modeDiff {
			return m.diffWheel(msg)
		}
		if m.focus == focusPreview {
			if z := zone.Get(zonePreview); z != nil && z.InBounds(msg) {
				return m.previewWheel(msg)
			}
		}
		return m.handleWheel(msg)
	case tea.MouseClickMsg:
		if m.beginPreviewSelection(msg) {
			return m, nil
		}
		m.clearPreviewSelection()
		if m.mode == modeDiff {
			return m.diffClick(msg)
		}
		return m.handleClick(msg)
	}
	return m, nil
}

// previewWheel gives a full-screen application the wheel when it requested SGR
// mouse input; otherwise it moves through tmux history. Claude owns an
// alternate-screen viewport and takes the first path, while Codex renders its
// transcript inline and takes the second.
func (m Model) previewWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	const linesPerNotch = 3
	delta := 0
	up := false
	switch msg.Mouse().Button {
	case tea.MouseWheelUp:
		delta = linesPerNotch
		up = true
	case tea.MouseWheelDown:
		delta = -linesPerNotch
	default:
		return m, nil
	}
	// Either way this is a gesture at a session, so the prober is told before the
	// branch: whether the wheel ends up reaching the application is a detail of
	// how the agent draws, and an agent that scrolls its own viewport repaints the
	// pane for a reason that has nothing to do with a turn. Capability is
	// re-checked at send time as well, so the branch not taken here is not proof
	// the pane was left alone.
	if s := m.selected(); s != nil {
		m.touchedAt[s.ID] = now()
	}
	if m.previewMouseSGR {
		s := m.selected()
		if s == nil || !s.TmuxAlive {
			return m, nil
		}
		z := zone.Get(zonePreview)
		x, y := msg.Mouse().X-z.StartX, msg.Mouse().Y-z.StartY
		m.previewScroll = 0
		return m, tea.Batch(sendWheelCmd(s, up, x, y), m.startEcho())
	}
	m.previewScroll = max(m.previewScroll+delta, 0)
	return m, previewCmdAt(m.selected(), m.previewScroll)
}

// diffWheel scrolls the file tree when the pointer is over it, and the diff
// otherwise. Like the board's wheel, scrolling the tree does not move its
// cursor: looking around must not change which file is rendered.
func (m Model) diffWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// The wheel moves the search's cursor while it is open, rather than
	// scrolling the pane out from under a list floating on top of it.
	if m.review.picker.open() {
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			m.review.picker.move(-1)
		case tea.MouseWheelDown:
			m.review.picker.move(1)
		}
		return m, nil
	}
	if z := zone.Get(zoneDiffTree); z != nil && z.InBounds(msg) {
		mouse := msg.Mouse()
		// The tree has only a vertical axis. Let horizontal wheel events pass
		// through to the diff even when the pointer happens to be over the tree;
		// otherwise a trackpad's sideways gesture is silently swallowed there.
		// Bubble Tea also treats Shift+wheel as horizontal scrolling, so preserve
		// that fallback for terminals which cannot report wheel-left/right.
		if !mouse.Mod.Contains(tea.ModShift) {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.review.files.scroll(-1, m.diffPaneHeight())
				return m, nil
			case tea.MouseWheelDown:
				m.review.files.scroll(1, m.diffPaneHeight())
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.review.view, cmd = m.review.view.Update(msg)
	return m, cmd
}

// diffClick picks the file row under the pointer. One click rather than two: a
// tree row is a much bigger target than a card, and picking one only renders a
// diff.
func (m Model) diffClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// An open search owns the clicks inside its box, and a click outside it
	// dismisses it -- the same bargain the board's dropdown makes.
	if m.review.picker.open() {
		for i := range m.review.picker.results {
			z := zone.Get(zonePickerRow(i))
			if z == nil || !z.InBounds(msg) {
				continue
			}
			m.review.picker.cursor = i
			r := m.review.picker.results[i]
			m.review.picker.close()
			cmd := m.showFileAt(r.path, r.line)
			return m, cmd
		}
		if z := zone.Get(zonePicker); z == nil || !z.InBounds(msg) {
			m.review.picker.close()
		}
		return m, nil
	}

	for i := range m.review.files.rows {
		z := zone.Get(zoneDiffRow(i))
		if z == nil || !z.InBounds(msg) {
			continue
		}
		m.review.files.cursor = i
		m.review.treeFocus = true
		cmd := m.showTreeSelection()
		return m, cmd
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
			cmd := m.openDiff()
			return m, cmd
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
	if m.focus != focusBoard {
		m.focus = focusBoard
		// Not inlined into the return: onFocusChange blurs the input through a
		// pointer, and Go does not order that against copying m into the result.
		cmd := m.onFocusChange()
		return m, cmd
	}
	return m, nil
}
