package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/clip"
	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/notify"
	"github.com/dma1dma1/dma-cli/internal/ops"
	"github.com/dma1dma1/dma-cli/internal/probe"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

type mode int

const (
	modeBoard mode = iota
	modeDiff
	modeHelp
	modeRepos
	modePrompt
	modeConfirm
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg      *core.Config
	sessions []*core.Session
	styles   Styles
	prober   *probe.Prober

	mode  mode
	focus focusArea

	width  int
	height int

	selectedID string
	// deselected says the empty selection was chosen rather than merely arrived
	// at, which rebuild has to know: it helpfully picks a card whenever nothing
	// is selected, and switching project would get its blank panel filled back in
	// on the next poll.
	deselected bool
	// repoFilter and projectFilter both reset on launch rather than persisting:
	// a filter you forgot you set is a board that silently lies about what is
	// running.
	repoFilter    string
	projectFilter string

	// activeRepo and agentChoice are what new sessions are built from. They are
	// seeded from the launch directory and the configured default.
	activeRepo  string
	agentChoice string

	input textarea.Model
	// pendingImages belong to the new-session composer. Their bytes stay in
	// memory until ops.Create has a worktree in which to stage them.
	pendingImages []clip.Image
	dropdown      dropdown
	prompt        prompt
	confirm       confirmState
	repos         repoPicker

	// preview is the selected session's recent terminal output, refreshed on a
	// timer. Display only.
	preview     string
	previewFull bool
	// previewCursor is where that frame's terminal cursor sat. It is kept beside
	// the content and only ever set together with it, so the caret cannot be
	// drawn onto a frame it does not belong to.
	previewCursor tmuxx.Cursor

	// echoUntil is how long the panel keeps re-reading the pane at echoInterval
	// after a forwarded keystroke; echoing says whether that ticker is running,
	// so a burst of typing extends the window instead of starting a ticker per
	// key.
	echoUntil time.Time
	echoing   bool

	// typedAt is when each session last had a keystroke forwarded to it. The
	// prober needs it to tell the user's typing apart from the agent's output:
	// both change the pane, and only one of them means the agent is working.
	typedAt map[string]time.Time

	diffView viewport.Model
	diffMode gitx.DiffMode

	hookEvents <-chan hooks.Event
	hookURL    string

	// notice is what the notice line is currently carrying: a failure, or the
	// one line a launch left behind. noticeErr picks the style between the two.
	notice    string
	noticeErr bool
	noticeAt  time.Time

	// lastPreviewCols/Rows are the size agents were last told to render at, so a
	// resize is only issued when it actually changes.
	lastPreviewCols int
	lastPreviewRows int

	lastClickID string
	lastClickAt time.Time

	quitting bool
}

const doubleClickWindow = 400 * time.Millisecond

type confirmState struct {
	active  bool
	message string
	action  func(m *Model) tea.Cmd
}

// Options configures the root model.
type Options struct {
	Config     *core.Config
	Sessions   []*core.Session
	HookEvents <-chan hooks.Event
	HookURL    string
	// LaunchRepo is the repo dma was started in, if any.
	LaunchRepo string
	// Notice is shown once on startup, e.g. that a repo was just adopted.
	Notice string
}

func New(opt Options) Model {
	styles := newStyles()

	m := Model{
		cfg:         opt.Config,
		sessions:    opt.Sessions,
		styles:      styles,
		prober:      probe.New(),
		typedAt:     map[string]time.Time{},
		mode:        modeBoard,
		focus:       focusBoard,
		hookEvents:  opt.HookEvents,
		hookURL:     opt.HookURL,
		activeRepo:  opt.LaunchRepo,
		agentChoice: opt.Config.DefaultProfile,
		input:       newTaskInput(styles),
		diffView:    viewport.New(),
		diffMode:    gitx.DiffUncommitted,
		width:       120,
		height:      36,
	}
	if opt.Notice != "" {
		m.notice, m.noticeAt = opt.Notice, time.Now()
	}
	for i, r := range m.cfg.Repos {
		if r.ID == m.activeRepoID() {
			m.repos.cursor = i
		}
	}
	if s := m.firstSession(); s != nil {
		m.selectedID = s.ID
	}
	m.layoutSizes()
	return m
}

func (m Model) selected() *core.Session {
	return core.FindByID(m.sessions, m.selectedID)
}

// selectSession aims the board's cursor and the panel at one session.
//
// The preview goes with it: it is the previous session's output, and left in
// place it draws into this session's panel until the next capture lands.
func (m *Model) selectSession(s *core.Session) {
	m.selectedID, m.deselected = s.ID, false
	m.preview, m.previewCursor = "", tmuxx.Cursor{}
}

// clearSelection empties the panel and keeps it empty until something is picked.
func (m *Model) clearSelection() {
	m.selectedID, m.deselected = "", true
	m.preview, m.previewCursor = "", tmuxx.Cursor{}
}

// dropSelectionIfHidden empties the panel when a filter has just taken the
// selected card off the board.
//
// The panel does not outlive the card it belongs to. Selection is held as an id
// and the filters do not touch it, so changing one used to leave the previous
// project or repo's agent running in the panel with no card above it to explain
// why -- which reads as the filter not having taken. Every filter change lands on
// the empty panel instead, the same place a board with no sessions starts from,
// and the next move or click picks from what the board is showing now.
//
// Widening a filter is not a change to land on: the card is still there, and
// blanking the panel under it would throw away the user's place for nothing.
func (m *Model) dropSelectionIfHidden() {
	if m.selectedID != "" && !m.findSelected().ok {
		m.clearSelection()
	}
}

func (m *Model) save() {
	if err := core.SaveSessions(m.sessions); err != nil {
		m.notice, m.noticeErr, m.noticeAt = "save state: "+err.Error(), true, time.Now()
	}
}

// rebuild exists so callers do not need to know that the layout is derived on
// demand rather than cached.
func (m *Model) rebuild() {
	if m.deselected {
		return
	}
	if m.selected() == nil {
		if s := m.firstSession(); s != nil {
			m.selectedID = s.ID
		} else {
			m.selectedID = ""
		}
	}
}

func (m *Model) layoutSizes() {
	// Prompt and ceiling before SetWidth: the field wraps its text against the
	// width left over after the prompt, and re-measures itself against the ceiling,
	// so SetWidth has to be the last of the three.
	m.setInputPrompt()
	m.input.MaxHeight = m.inputRowsMax()
	m.input.SetWidth(m.inputWidth())
	m.diffView.SetWidth(max(m.contentWidth()-4, 20))
	m.diffView.SetHeight(max(m.height-6, 5))
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		pollTickCmd(time.Duration(m.cfg.PollIntervalSecs)*time.Second),
		previewTickCmd(),
		probeTickCmd(),
		waitForHook(m.hookEvents),
		observeCmd(m.sessions),
		pollPRsCmd(m.cfg, m.sessions),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layoutSizes()
		return m, m.syncAgentSize()

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		return m.handlePaste(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tickMsg:
		// The panel grows and shrinks with the number of cards, not just with the
		// window, so the agent size is re-checked here too. syncAgentSize is a
		// no-op unless the dimensions actually changed, which is what keeps this
		// off the "never resize agents on a timer" rule.
		resize := m.syncAgentSize()
		return m, tea.Batch(tickCmd(), resize)

	case previewTickMsg:
		// Only the selected session is captured: the panel shows one at a time,
		// and capturing every pane every second would spawn a process per
		// session per tick for output nobody is looking at.
		if m.echoing {
			// The echo ticker is already capturing faster than this; a second
			// capture in the same moment buys nothing.
			return m, previewTickCmd()
		}
		return m, tea.Batch(previewTickCmd(), previewCmd(m.selected()))

	case echoTickMsg:
		if !now().Before(m.echoUntil) {
			m.echoing = false
			return m, nil
		}
		return m, tea.Batch(echoTickCmd(), previewCmd(m.selected()))

	case probeTickMsg:
		return m, tea.Batch(probeTickCmd(), probeCmd(m.prober, m.cfg, m.sessions, m.typedAt))

	case pollTickMsg:
		return m, tea.Batch(
			pollTickCmd(time.Duration(m.cfg.PollIntervalSecs)*time.Second),
			pollPRsCmd(m.cfg, m.sessions),
			observeCmd(m.sessions),
			m.refreshDiff(),
			// A resize normally arrives as a signal, but this program hands the
			// terminal to tmux and takes it back, so asking outright is the cheap
			// way to be sure a resize missed in between does not leave the board
			// laid out for a window that no longer exists.
			tea.RequestWindowSize,
		)

	case hookMsg:
		return m.handleHook(hooks.Event(msg))

	case probeMsg:
		return m.handleProbe(msg)

	case previewMsg:
		if msg.id == m.selectedID {
			m.preview, m.previewCursor = msg.content, msg.cursor
		}
		return m, nil

	case observeMsg:
		byID := map[string]ops.Observation{}
		for _, o := range msg.obs {
			byID[o.ID] = o
		}
		adopted := false
		for _, s := range m.sessions {
			if o, ok := byID[s.ID]; ok {
				s.TmuxAlive = o.Alive
				s.WorktreeDirty = o.Dirty
				s.DiffAdded, s.DiffRemoved = o.DiffAdded, o.DiffRemoved
				// Sessions start detached and the agent names its own branch;
				// picking it up here is what turns PR polling on. Only a real
				// name is taken: a worktree that has gone missing reports none,
				// and forgetting a branch over that would silently stop
				// tracking its PR.
				if o.Branch != "" && o.Branch != s.Branch {
					s.Branch = o.Branch
					adopted = true
				}
			}
		}
		if adopted {
			m.save()
		}
		m.rebuild()
		return m, nil

	case prSyncMsg:
		return m.handlePRSync(msg)

	case prDetailMsg:
		return m.handlePRDetail(msg)

	case prLinkMsg:
		if msg.err != nil {
			return m, errStatus(msg.err)
		}
		// Cached on the way past, so the next open or copy of this PR is local.
		if s := core.FindByID(m.sessions, msg.id); s != nil && s.PRURL != msg.url {
			s.PRURL = msg.url
			m.save()
		}
		return m, linkCmd(msg.url, msg.action)

	case adoptedMsg:
		if msg.err != nil {
			return m, errStatus(msg.err)
		}
		// Registering a repo makes it the one new sessions use, which is the same
		// statement the repo chip makes -- so a selected project learns from it
		// too, and a project added for a repo you were about to register does not
		// need binding by hand afterwards.
		m.setActiveRepo(msg.repo.ID)
		for i, r := range m.cfg.Repos {
			if r.ID == msg.repo.ID {
				m.repos.cursor = i
			}
		}
		// No confirmation: the repo is now in the list and active, which says it
		// better than a line of prose that costs the shortcut bar ten seconds.
		return m, nil

	case createdMsg:
		return m.handleCreated(msg)

	case shippedMsg:
		return m.handleShipped(msg)

	case mergedMsg:
		return m.handleMerged(msg)

	case prQueueMsg:
		return m.handlePRQueue(msg)

	case teardownMsg:
		return m.handleTeardown(msg)

	case killedMsg:
		if msg.err != nil {
			return m, errStatus(msg.err)
		}
		if s := core.FindByID(m.sessions, msg.id); s != nil {
			s.TmuxAlive = false
			s.SetAgentState(core.AgentIdle, "killed")
			m.save()
		}
		return m, nil

	case diffMsg:
		if msg.id != m.selectedID {
			return m, nil
		}
		content := msg.content
		if msg.err != nil {
			content = msg.err.Error()
		}
		if strings.TrimSpace(content) == "" {
			content = "(no changes)"
		}
		m.diffView.SetContent(content)
		return m, nil

	case noticeMsg:
		m.notice, m.noticeErr, m.noticeAt = msg.text, true, time.Now()
		return m, nil

	case clipboardMsg:
		return m.handleClipboard(msg)

	case attachDoneMsg:
		if msg.err != nil {
			return m, errStatus(fmt.Errorf("attach: %w", msg.err))
		}
		// Attaching let tmux resize the window to fill the real terminal; put it
		// back to the preview size now that nothing is attached.
		m.lastPreviewCols, m.lastPreviewRows = 0, 0
		return m, tea.Batch(m.syncAgentSize(), previewCmd(m.selected()))
	}

	return m, nil
}

// syncAgentSize tells every agent to render at the current preview size, when
// that size has changed. Agents reflow on resize, so this must not fire on a
// timer.
func (m *Model) syncAgentSize() tea.Cmd {
	cols, rows := m.previewDims()
	if cols == m.lastPreviewCols && rows == m.lastPreviewRows {
		return nil
	}
	m.lastPreviewCols, m.lastPreviewRows = cols, rows
	return resizeSessionsCmd(m.sessions, cols, rows)
}

func (m Model) refreshDiff() tea.Cmd {
	if m.mode != modeDiff {
		return nil
	}
	if s := m.selected(); s != nil {
		return diffCmd(s, m.diffMode)
	}
	return nil
}

// --- hooks and probing ---

func (m Model) handleHook(ev hooks.Event) (tea.Model, tea.Cmd) {
	next := waitForHook(m.hookEvents)

	out := hooks.Interpret(ev)
	if !out.Known {
		return m, next
	}
	s := hooks.Correlate(m.sessions, ev)
	if s == nil {
		return m, next
	}
	if ev.ClaudeSessionID != "" {
		s.ClaudeSessionID = ev.ClaudeSessionID
	}

	was := s.AgentState
	if s.SetAgentState(out.State, out.Detail) {
		m.notifyIfBlocked(s, was)
		m.save()
	}
	if out.Ended {
		s.TmuxAlive = false
	}
	return m, next
}

// handleProbe applies inferred state for agents that cannot report their own.
func (m Model) handleProbe(msg probeMsg) (tea.Model, tea.Cmd) {
	dirty := false
	for _, st := range msg.states {
		s := core.FindByID(m.sessions, st.SessionID)
		if s == nil {
			continue
		}
		s.TmuxAlive = st.Alive
		was := s.AgentState
		if s.SetAgentState(st.Agent, st.Detail) {
			m.notifyIfBlocked(s, was)
			dirty = true
		}
		// The probe already paid for a capture; reuse it as the preview.
		if st.Content != "" && s.ID == m.selectedID {
			m.preview, m.previewCursor = st.Content, st.Cursor
		}
	}
	if dirty {
		m.save()
	}
	return m, nil
}

// notifyIfBlocked raises a desktop notification on the transition into
// needs_you, not on every event while it stays there.
func (m Model) notifyIfBlocked(s *core.Session, was core.AgentState) {
	if s.AgentState != core.AgentNeedsYou || was == core.AgentNeedsYou {
		return
	}
	detail := s.AgentStateDetail
	if detail == "" {
		detail = "needs your input"
	}
	notify.Notify(s.Title, detail)
}

// --- PR sync ---

func (m Model) handlePRSync(msg prSyncMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, errText(fmt.Sprintf("%s: %v", msg.repoID, msg.err))
	}

	repo, _ := m.cfg.Repo(msg.repoID)
	dirty := false
	var follow []tea.Cmd

	// The poll is scoped to this repo and keyed by branch, so a branch name
	// shared with another repo cannot cross-assign a PR here.
	for _, s := range m.sessions {
		// A branch whose query failed is not in Answered. Skipping it leaves the
		// card showing its last known PR state, which beats inferring anything
		// from an answer that never came.
		if s.RepoID != msg.repoID || !msg.poll.Answered[s.Branch] {
			continue
		}
		core.Touch(s)
		pr, ok := msg.poll.Open[s.Branch]
		if !ok {
			// Only open PRs are polled, so a tracked PR the query no longer
			// returns has reached a terminal state; resolve that one directly.
			if s.PRNumber > 0 && s.PRState != core.PRMerged && s.PRState != core.PRClosed {
				follow = append(follow, prDetailCmd(repo.Remote, s.ID, s.PRNumber))
			}
			continue
		}
		hadPR := s.HasPR()
		if s.PRNumber != pr.Number || s.PRState != pr.State || s.PRCI != pr.CI ||
			s.PRReview != pr.Review || s.PRMergeable != pr.Mergeable || s.PRURL != pr.URL {
			dirty = true
		}
		s.PRNumber, s.PRURL, s.PRState = pr.Number, pr.URL, pr.State
		s.PRCI, s.PRReview, s.PRMergeable = pr.CI, pr.Review, pr.Mergeable

		// A queued PR is an open PR, so the poll cannot tell that the queue let
		// go of it. Ask, but only for the cards actually claiming to be queued --
		// on a board with no merge queue in sight that is never.
		if s.PRQueued {
			follow = append(follow, prQueueCmd(repo.Remote, s.ID, s.PRNumber))
		}

		// A PR appearing, and that PR merging, are the two durable events that
		// move a card into the git-owned columns.
		if !hadPR && s.HasPR() && s.Lifecycle != core.LifecycleMerged {
			s.Lifecycle = core.LifecyclePROpen
			dirty = true
		}
		if pr.State == core.PRMerged && s.Lifecycle != core.LifecycleMerged {
			s.Lifecycle = core.LifecycleMerged
			dirty = true
		}
		if pr.State == core.PRMerged || pr.State == core.PRClosed {
			s.PRQueued = false
		}
	}
	if dirty {
		m.save()
	}
	if len(follow) > 0 {
		return m, tea.Batch(follow...)
	}
	return m, nil
}

// handleMerged applies the result of pressing m.
//
// Only a merge that landed moves the card to the merged column. A pull request
// the merge queue accepted is still open work: the queue merges it when it
// reaches it, or drops it back out, and the poll is what resolves which.
func (m Model) handleMerged(msg mergedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, errStatus(msg.err)
	}
	s := core.FindByID(m.sessions, msg.id)
	if s == nil {
		return m, nil
	}
	switch msg.outcome {
	case ghx.MergeQueued, ghx.MergeAlreadyQueued:
		// Nothing is posted either way. The card stays in the PR column and now
		// reads "queued", which says both that the merge did not land and where
		// the PR went.
		s.PRQueued = true
		m.save()
		return m, nil
	}
	s.PRState = core.PRMerged
	s.Lifecycle = core.LifecycleMerged
	s.PRQueued = false
	m.save()
	return m, nil
}

// handlePRQueue applies a queue re-check. A failed check leaves the card as it
// was: the queue standing it is showing is the last one GitHub confirmed.
func (m Model) handlePRQueue(msg prQueueMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil
	}
	s := core.FindByID(m.sessions, msg.sessionID)
	if s == nil || s.PRQueued == msg.inQueue {
		return m, nil
	}
	s.PRQueued = msg.inQueue
	m.save()
	return m, nil
}

func (m Model) handlePRDetail(msg prDetailMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil
	}
	s := core.FindByID(m.sessions, msg.sessionID)
	if s == nil {
		return m, nil
	}
	s.PRState, s.PRCI = msg.pr.State, msg.pr.CI
	s.PRReview, s.PRMergeable = msg.pr.Review, msg.pr.Mergeable
	if msg.pr.URL != "" {
		s.PRURL = msg.pr.URL
	}
	// This resolves a PR that left the open set, so whatever the queue was going
	// to do with it, it has done.
	s.PRQueued = false
	core.Touch(s)
	// A PR closed without merging keeps its column and is labelled closed on the
	// card, rather than vanishing from the board.
	if msg.pr.State == core.PRMerged {
		s.Lifecycle = core.LifecycleMerged
	}
	m.save()
	return m, nil
}

// --- action results ---

func (m Model) handleCreated(msg createdMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, errStatus(msg.err)
	}
	s := msg.res.Session
	m.sessions = append(m.sessions, s)
	if s.Group != "" && m.cfg.AddProject(s.Group, s.RepoID) {
		_ = core.SaveConfig(m.cfg)
	}
	m.save()
	m.selectSession(s)

	// The new card is its own confirmation, so a successful start says nothing.
	// Warnings are the exception: a symlink or hook install that failed is not
	// visible anywhere on the board, and the session runs degraded until it is
	// fixed.
	var note tea.Cmd
	if len(msg.res.Warnings) > 0 {
		note = errText(strings.Join(msg.res.Warnings, "; "))
	}
	cols, rows := m.previewDims()
	return m, tea.Batch(note, observeCmd(m.sessions), previewCmd(s),
		resizeSessionsCmd([]*core.Session{s}, cols, rows))
}

func (m Model) handleShipped(msg shippedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, errStatus(msg.err)
	}
	s := core.FindByID(m.sessions, msg.id)
	if s == nil {
		return m, nil
	}
	if msg.branch != "" {
		s.Branch = msg.branch
	}
	if msg.number > 0 {
		s.PRNumber, s.PRState = msg.number, core.PROpen
		s.Lifecycle = core.LifecyclePROpen
		// gh prints the address on creation, so o and y work on a fresh PR
		// without waiting for the next poll.
		s.PRURL = msg.url
	}
	// The card moves to In Review and grows a PR number, so there is nothing a
	// message would add.
	m.save()
	return m, pollPRsCmd(m.cfg, m.sessions)
}

func (m Model) handleTeardown(msg teardownMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		s := core.FindByID(m.sessions, msg.id)
		// The recoveries below are per-session questions, and a bulk prune has
		// several teardowns behind it: asking one would hide the next, and the
		// answer would land on whichever card the confirm happened to hold. So a
		// session that needs a decision keeps its card, and x asks there.
		if msg.bulk {
			return m, errText(fmt.Sprintf("%s: %v", nameOf(s), msg.err))
		}
		switch e := msg.err.(type) {
		case *ops.DirtyError:
			return m.askConfirm(fmt.Sprintf("%s has uncommitted changes. Discard and prune?", nameOf(s)),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, true)
				})
		case *ops.UnnamedCommitsError:
			return m.askConfirm(fmt.Sprintf("%s has commits on no branch. Discard and prune?", nameOf(s)),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, true)
				})
		case *ops.BranchNotMergedError:
			return m.askConfirm(fmt.Sprintf("Branch %s is not fully merged. Delete anyway?", e.Branch),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, true)
				})
		}
		return m, errStatus(msg.err)
	}

	for i, s := range m.sessions {
		if s.ID == msg.id {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			break
		}
	}
	m.save()
	m.rebuild()
	m.preview = ""
	if m.selectedID == "" {
		m.mode = modeBoard
	}
	return m, nil
}

func nameOf(s *core.Session) string {
	if s == nil {
		return "session"
	}
	return s.Title
}

func (m Model) askConfirm(message string, action func(*Model) tea.Cmd) (tea.Model, tea.Cmd) {
	m.confirm = confirmState{active: true, message: message, action: action}
	m.mode = modeConfirm
	return m, nil
}

// --- view ---

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	if c := m.activeCursor(); c != nil {
		v.Cursor = c
	}
	return v
}

func (m Model) activeCursor() *tea.Cursor {
	switch {
	case m.mode == modePrompt:
		return m.prompt.input.Cursor()
	case m.mode == modeBoard && m.focus == focusInput:
		return m.input.Cursor()
	}
	return nil
}

func (m Model) render() string {
	if m.quitting {
		return ""
	}

	switch m.mode {
	case modeHelp:
		return zone.Scan(m.frame(m.viewHelp()))
	case modeRepos:
		return zone.Scan(m.frame(m.viewRepos()))
	case modeDiff:
		return zone.Scan(m.frame(m.viewDiff()))
	}

	// Board: columns on top, session panel pinned to the bottom. The panel is
	// always present, so the input is always one key away. While you are typing a
	// task the input steps out into a box of its own.
	boardH, panelH, inputH := m.splitHeights()

	var parts []string
	if boardH > 0 {
		parts = append(parts, m.viewBoard(boardH))
	}
	parts = append(parts, m.viewPanel(panelH))
	if inputH > 0 {
		parts = append(parts, m.viewInputBox(inputH))
	}
	parts = append(parts, m.footer()...)

	return zone.Scan(m.place(lipgloss.JoinVertical(lipgloss.Left, parts...)))
}

// frame wraps a full-screen sub-view with the footer, so every mode has the
// same one.
//
// The body is clipped to the rows and columns it was given. Sub-views lay
// themselves out to a comfortable size rather than to a tiny window, and content
// spilling past the edge would otherwise cost the status bar its line -- the one
// place that says which key gets you out.
func (m Model) frame(body string) string {
	lines := strings.Split(body, "\n")
	footer := m.footer()
	if avail := max(m.height-len(footer), 1); len(lines) > avail {
		lines = lines[:avail]
	}
	for i, l := range lines {
		lines[i] = truncate(l, m.contentWidth())
	}
	lines = append(lines, footer...)
	return m.place(strings.Join(lines, "\n"))
}

// maxContentWidth is where the UI stops widening. Four columns and an agent
// terminal gain nothing past it: a card line reaching a third of the way across
// an ultrawide display is a longer eye movement for the same information, and
// the agents pinned to the panel width start reflowing their output into shapes
// nobody designed them for.
const maxContentWidth = 200

// contentWidth is the width every part of the UI is laid out to.
func (m Model) contentWidth() int { return min(m.width, maxContentWidth) }

// place centers a rendered block on a screen wider than the content. Pinned to
// the left edge instead, the UI reads as a window that failed to resize.
func (m Model) place(block string) string {
	pad := (m.width - m.contentWidth()) / 2
	if pad <= 0 {
		return block
	}
	indent := strings.Repeat(" ", pad)
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		lines[i] = indent + l
	}
	return strings.Join(lines, "\n")
}

// noticeTTL is how long a notice stays on screen before the row it occupies
// goes back to the view above it.
const noticeTTL = 10 * time.Second

func (m Model) noticeActive() bool {
	return m.notice != "" && time.Since(m.noticeAt) < noticeTTL
}

// footer is the bottom of every mode: the shortcut bar, with the notice line
// above it when something has been posted.
//
// The two are separate rows on purpose. A message that borrows the bottom line
// takes the keymap off the screen for as long as it is up, and the keymap is
// what the user is looking at -- so the message pays for itself with a row of
// its own instead, out of the view above.
func (m Model) footer() []string {
	if !m.noticeActive() {
		return []string{m.statusBar()}
	}
	st := m.styles.Status
	if m.noticeErr {
		st = m.styles.Error
	}
	w := m.contentWidth()
	line := lipgloss.NewStyle().Width(w).Render(st.Render(truncate(" "+m.notice, w)))
	return []string{line, m.statusBar()}
}

// statusBar is the bottom line: the keys for the current focus, or the one
// question a confirm or prompt is waiting on.
func (m Model) statusBar() string {
	st := m.styles
	w := m.contentWidth()

	if m.mode == modeConfirm {
		// The question has to fit the one line the footer gets. A bordered box
		// here would be three lines tall, and the two it borrows come off the
		// bottom of the screen -- taking the answer keys with them.
		keys := st.KeyHint.Render("y") + st.KeyDesc.Render(" yes · ") +
			st.KeyHint.Render("n") + st.KeyDesc.Render(" no")
		msg := truncate(m.confirm.message, max(w-lipgloss.Width(keys)-5, 4))
		return lipgloss.NewStyle().Width(w).
			Render(" " + st.Dialog.Render(msg) + " " + keys)
	}
	if m.mode == modePrompt {
		line := st.KeyHint.Render(m.prompt.label+":") + " " + m.prompt.input.View() +
			st.KeyDesc.Render("   enter · esc")
		return lipgloss.NewStyle().Width(w).Render(truncate(line, w))
	}
	var line string
	if m.repoFilter != "" {
		line += st.RepoTag.Render("[repo:" + m.repoFilter + "] ")
	}
	line += m.hintLine(m.hints())
	return lipgloss.NewStyle().Width(w).Render(truncate(" "+line, w))
}

func (m Model) hintLine(hints []hint) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, m.styles.KeyHint.Render(h.key)+" "+m.styles.KeyDesc.Render(h.desc))
	}
	return strings.Join(parts, m.styles.KeyDesc.Render(" · "))
}

func (m Model) viewHelp() string {
	st := m.styles
	lines := []string{""}
	for _, row := range helpText {
		if row[0] != "" {
			lines = append(lines, "", "  "+st.Title.Render(row[0]))
			continue
		}
		lines = append(lines, fmt.Sprintf("    %s  %s",
			st.KeyHint.Render(padRight(row[1], 12)), st.KeyDesc.Render(row[2])))
	}
	tail := []string{
		"",
		"  " + st.Faint.Render("hook listener: "+m.hookURL),
		"  " + st.Faint.Render("press any key to return"),
	}
	// The keymap is longer than a short window. Clip the list rather than let the
	// frame clip the whole view, so the line naming the key that closes this
	// screen is never the one that falls off the bottom of it.
	if avail := max(m.height-len(m.footer())-len(tail), 1); len(lines) > avail {
		lines = lines[:avail-1]
		lines = append(lines, "  "+st.Faint.Render("… more keys below — grow the window to read them"))
	}
	return strings.Join(append(lines, tail...), "\n")
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
