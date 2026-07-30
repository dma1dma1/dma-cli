package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/notify"
	"github.com/dma1dma1/dma-cli/internal/ops"
	"github.com/dma1dma1/dma-cli/internal/probe"
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
	// repoFilter and projectFilter both reset on launch rather than persisting:
	// a filter you forgot you set is a board that silently lies about what is
	// running.
	repoFilter    string
	projectFilter string

	// activeRepo and agentChoice are what new sessions are built from. They are
	// seeded from the launch directory and the configured default.
	activeRepo  string
	agentChoice string

	input    textinput.Model
	dropdown dropdown
	prompt   prompt
	confirm  confirmState
	repos    repoPicker

	// preview is the selected session's recent terminal output, refreshed on a
	// timer. Display only.
	preview     string
	previewFull bool

	diffView viewport.Model
	diffMode gitx.DiffMode

	hookEvents <-chan hooks.Event
	hookURL    string

	statusText string
	statusErr  bool
	statusAt   time.Time

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
	ti := textinput.New()
	ti.Placeholder = "what should the agent do?"
	ti.SetVirtualCursor(true)

	m := Model{
		cfg:         opt.Config,
		sessions:    opt.Sessions,
		styles:      newStyles(),
		prober:      probe.New(),
		mode:        modeBoard,
		focus:       focusBoard,
		hookEvents:  opt.HookEvents,
		hookURL:     opt.HookURL,
		activeRepo:  opt.LaunchRepo,
		agentChoice: opt.Config.DefaultProfile,
		input:       ti,
		diffView:    viewport.New(),
		diffMode:    gitx.DiffUncommitted,
		width:       120,
		height:      36,
	}
	if opt.Notice != "" {
		m.statusText, m.statusAt = opt.Notice, time.Now()
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

func (m *Model) save() {
	if err := core.SaveSessions(m.sessions); err != nil {
		m.statusText, m.statusErr, m.statusAt = "save state: "+err.Error(), true, time.Now()
	}
}

// rebuild exists so callers do not need to know that the layout is derived on
// demand rather than cached.
func (m *Model) rebuild() {
	if m.selected() == nil {
		if s := m.firstSession(); s != nil {
			m.selectedID = s.ID
		} else {
			m.selectedID = ""
		}
	}
}

func (m *Model) layoutSizes() {
	m.input.SetWidth(max(m.width-10, 20))
	m.diffView.SetWidth(max(m.width-4, 20))
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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tickMsg:
		return m, tickCmd()

	case previewTickMsg:
		// Only the selected session is captured: the panel shows one at a time,
		// and capturing every pane every second would spawn a process per
		// session per tick for output nobody is looking at.
		return m, tea.Batch(previewTickCmd(), previewCmd(m.selected()))

	case probeTickMsg:
		return m, tea.Batch(probeTickCmd(), probeCmd(m.prober, m.cfg, m.sessions))

	case pollTickMsg:
		return m, tea.Batch(
			pollTickCmd(time.Duration(m.cfg.PollIntervalSecs)*time.Second),
			pollPRsCmd(m.cfg, m.sessions),
			observeCmd(m.sessions),
			m.refreshDiff(),
		)

	case hookMsg:
		return m.handleHook(hooks.Event(msg))

	case probeMsg:
		return m.handleProbe(msg)

	case previewMsg:
		if msg.id == m.selectedID {
			m.preview = msg.content
		}
		return m, nil

	case observeMsg:
		byID := map[string]ops.Observation{}
		for _, o := range msg.obs {
			byID[o.ID] = o
		}
		for _, s := range m.sessions {
			if o, ok := byID[s.ID]; ok {
				s.TmuxAlive = o.Alive
				s.WorktreeDirty = o.Dirty
				s.DiffAdded, s.DiffRemoved = o.DiffAdded, o.DiffRemoved
			}
		}
		m.rebuild()
		return m, nil

	case prSyncMsg:
		return m.handlePRSync(msg)

	case prDetailMsg:
		return m.handlePRDetail(msg)

	case adoptedMsg:
		if msg.err != nil {
			return m, errStatus(msg.err)
		}
		m.activeRepo = msg.repo.ID
		for i, r := range m.cfg.Repos {
			if r.ID == msg.repo.ID {
				m.repos.cursor = i
			}
		}
		if !msg.added {
			return m, status(msg.repo.ID + " was already registered — now active")
		}
		return m, status(fmt.Sprintf("added %s — %s", msg.repo.ID, ops.SummarizeBootstrap(msg.repo.Bootstrap)))

	case createdMsg:
		return m.handleCreated(msg)

	case shippedMsg:
		return m.handleShipped(msg)

	case mergedMsg:
		if msg.err != nil {
			return m, errStatus(msg.err)
		}
		if s := core.FindByID(m.sessions, msg.id); s != nil {
			s.PRState = core.PRMerged
			s.Lifecycle = core.LifecycleMerged
			m.save()
		}
		return m, status("merged")

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
		return m, status("session killed (worktree kept)")

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

	case statusMsg:
		m.statusText, m.statusErr, m.statusAt = msg.text, msg.isErr, time.Now()
		return m, nil

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
			m.preview = st.Content
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
		return m, status(fmt.Sprintf("%s: %v", msg.repoID, msg.err))
	}

	// Index by (repo_id, branch): matching on headRefName alone would
	// cross-assign PRs between repos that share a branch name.
	byKey := map[core.Key]ghx.PR{}
	for _, pr := range msg.prs {
		k := core.Key{RepoID: msg.repoID, Branch: pr.Branch}
		if prev, ok := byKey[k]; ok && prev.Number > pr.Number {
			continue
		}
		byKey[k] = pr
	}

	repo, _ := m.cfg.Repo(msg.repoID)
	dirty := false
	var follow []tea.Cmd

	for _, s := range m.sessions {
		if s.RepoID != msg.repoID {
			continue
		}
		core.Touch(s)
		pr, ok := byKey[s.Key()]
		if !ok {
			// Only open PRs are listed, so a tracked PR missing from the list
			// has reached a terminal state; resolve that one directly.
			if s.PRNumber > 0 && s.PRState != core.PRMerged && s.PRState != core.PRClosed {
				follow = append(follow, prDetailCmd(repo.Remote, s.ID, s.PRNumber))
			}
			continue
		}
		hadPR := s.HasPR()
		if s.PRNumber != pr.Number || s.PRState != pr.State || s.PRCI != pr.CI ||
			s.PRReview != pr.Review || s.PRMergeable != pr.Mergeable {
			dirty = true
		}
		s.PRNumber, s.PRState = pr.Number, pr.State
		s.PRCI, s.PRReview, s.PRMergeable = pr.CI, pr.Review, pr.Mergeable

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
	}
	if dirty {
		m.save()
	}
	if len(follow) > 0 {
		return m, tea.Batch(follow...)
	}
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
	if s.Group != "" && m.cfg.AddGroup(s.Group) {
		_ = core.SaveConfig(m.cfg)
	}
	m.save()
	m.selectedID = s.ID
	m.preview = ""

	text := "started " + s.Branch
	if len(msg.res.Warnings) > 0 {
		text += " — " + strings.Join(msg.res.Warnings, "; ")
	}
	cols, rows := m.previewDims()
	return m, tea.Batch(status(text), observeCmd(m.sessions), previewCmd(s),
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
	if msg.number > 0 {
		s.PRNumber, s.PRState = msg.number, core.PROpen
		s.Lifecycle = core.LifecyclePROpen
	}
	m.save()
	return m, tea.Batch(status(fmt.Sprintf("opened PR #%d", msg.number)),
		pollPRsCmd(m.cfg, m.sessions))
}

func (m Model) handleTeardown(msg teardownMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		s := core.FindByID(m.sessions, msg.id)
		switch e := msg.err.(type) {
		case *ops.DirtyError:
			return m.askConfirm(fmt.Sprintf("%s has uncommitted changes. Discard and prune?", nameOf(s)),
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
	return m, status("pruned")
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
	// always present, so the input is always one key away.
	panelH := m.panelHeight()
	statusH := 1
	boardH := m.height - panelH - statusH

	var parts []string
	if !m.previewFull && boardH >= minBoardRows {
		parts = append(parts, m.viewBoard(boardH))
	} else if !m.previewFull {
		// Too short for cards: keep the panel usable rather than rendering a
		// column frame with nothing inside it.
		panelH = m.height - statusH
	}
	parts = append(parts, m.viewPanel(panelH))
	parts = append(parts, m.statusBar())

	return zone.Scan(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// frame wraps a full-screen sub-view with the status bar, so every mode has the
// same footer.
func (m Model) frame(body string) string {
	return lipgloss.JoinVertical(lipgloss.Left, body, m.statusBar())
}

// statusBar is the single bottom line: a transient message when there is one,
// otherwise the keys for the current focus.
func (m Model) statusBar() string {
	st := m.styles

	if m.mode == modeConfirm {
		return lipgloss.NewStyle().Width(m.width).
			Render(truncate(st.Dialog.Render(m.confirm.message+"  [y/n]"), m.width))
	}
	if m.mode == modePrompt {
		line := st.KeyHint.Render(m.prompt.label+":") + " " + m.prompt.input.View() +
			st.KeyDesc.Render("   enter · esc")
		return lipgloss.NewStyle().Width(m.width).Render(truncate(line, m.width))
	}
	if m.statusText != "" && time.Since(m.statusAt) < 10*time.Second {
		text := st.Status
		if m.statusErr {
			text = st.Error
		}
		return lipgloss.NewStyle().Width(m.width).
			Render(text.Render(truncate(" "+m.statusText, m.width)))
	}

	var line string
	if m.repoFilter != "" {
		line += st.RepoTag.Render("[repo:" + m.repoFilter + "] ")
	}
	line += m.hintLine(m.hints())
	return lipgloss.NewStyle().Width(m.width).Render(truncate(" "+line, m.width))
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
	var b strings.Builder
	b.WriteString("\n")
	for _, row := range helpText {
		if row[0] != "" {
			b.WriteString("\n  " + st.Title.Render(row[0]) + "\n")
			continue
		}
		b.WriteString(fmt.Sprintf("    %s  %s\n",
			st.KeyHint.Render(padRight(row[1], 12)), st.KeyDesc.Render(row[2])))
	}
	b.WriteString("\n  " + st.Faint.Render("hook listener: "+m.hookURL) + "\n")
	b.WriteString("  " + st.Faint.Render("press any key to return") + "\n")
	return b.String()
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
