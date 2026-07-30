package ui

import (
	"fmt"
	"strings"
	"time"

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
)

type mode int

const (
	modeBoard mode = iota
	modeDetail
	modeHelp
	modeCompose
	modePrompt
	modeConfirm
	modeRepos
)

// Model is the root Bubble Tea model.
type Model struct {
	cfg      *core.Config
	sessions []*core.Session
	styles   Styles

	mode   mode
	width  int
	height int

	layout     layout
	selectedID string
	collapsed  map[string]bool
	// repoFilter resets to empty on each launch rather than persisting: a
	// filter you forgot you set is a board that silently lies about what is
	// running.
	repoFilter string

	compose compose
	prompt  prompt
	confirm confirmState
	repos   repoPicker

	// activeRepo is the repo new sessions default to. It is seeded from the
	// directory dma was launched in, so standing in a repo is enough to work
	// in it.
	activeRepo string

	diffView   viewport.Model
	outputView viewport.Model
	diffMode   gitx.DiffMode

	hookEvents <-chan hooks.Event
	hookURL    string

	statusText string
	statusErr  bool
	statusAt   time.Time

	lastClickID string
	lastClickAt time.Time

	quitting bool
}

// doubleClickWindow is how close two clicks on the same card must be to count
// as opening it.
const doubleClickWindow = 400 * time.Millisecond

func (m Model) isDoubleClick() bool {
	return m.lastClickID == m.selectedID && time.Since(m.lastClickAt) < doubleClickWindow
}

func (m *Model) markClick() {
	m.lastClickID = m.selectedID
	m.lastClickAt = time.Now()
}

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

// New builds the root model.
func New(opt Options) Model {
	m := Model{
		cfg:        opt.Config,
		sessions:   opt.Sessions,
		styles:     newStyles(),
		mode:       modeBoard,
		collapsed:  map[string]bool{},
		hookEvents: opt.HookEvents,
		hookURL:    opt.HookURL,
		activeRepo: opt.LaunchRepo,
		diffView:   viewport.New(),
		outputView: viewport.New(),
		diffMode:   gitx.DiffUncommitted,
		width:      100,
		height:     30,
	}
	if opt.Notice != "" {
		m.statusText, m.statusAt = opt.Notice, time.Now()
	}
	// Put the picker cursor on the active repo, not on whatever is first.
	for i, r := range m.cfg.Repos {
		if r.ID == m.activeRepoID() {
			m.repos.cursor = i
		}
	}
	m.rebuild()
	if s := m.layout.first(); s != nil {
		m.selectedID = s.ID
	}
	return m
}

func (m *Model) rebuild() {
	m.layout = buildLayout(m.cfg, m.sessions, m.collapsed, m.repoFilter)
}

func (m Model) selected() *core.Session {
	return core.FindByID(m.sessions, m.selectedID)
}

func (m *Model) save() {
	if err := core.SaveSessions(m.sessions); err != nil {
		m.statusText = "save state: " + err.Error()
		m.statusErr = true
		m.statusAt = time.Now()
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		pollTickCmd(time.Duration(m.cfg.PollIntervalSecs)*time.Second),
		waitForHook(m.hookEvents),
		observeCmd(m.sessions),
		pollPRsCmd(m.cfg, m.sessions),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizePanes()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tickMsg:
		// Re-render so time in state stays honest.
		return m, tickCmd()

	case pollTickMsg:
		return m, tea.Batch(
			pollTickCmd(time.Duration(m.cfg.PollIntervalSecs)*time.Second),
			pollPRsCmd(m.cfg, m.sessions),
			observeCmd(m.sessions),
			m.refreshDetailPanes(),
		)

	case hookMsg:
		return m.handleHook(hooks.Event(msg))

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
		m.rebuild()
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
			m.rebuild()
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

	case captureMsg:
		if msg.id != m.selectedID {
			return m, nil
		}
		m.outputView.SetContent(msg.content)
		m.outputView.GotoBottom()
		return m, nil

	case statusMsg:
		m.statusText, m.statusErr, m.statusAt = msg.text, msg.isErr, time.Now()
		return m, nil

	case attachDoneMsg:
		if msg.err != nil {
			return m, errStatus(fmt.Errorf("attach: %w", msg.err))
		}
		return m, m.refreshDetailPanes()
	}

	return m, nil
}

func (m *Model) resizePanes() {
	w := max(m.width-4, 20)
	// Two panes share the detail body: header, chips, two titles, bottom bar.
	body := max(m.height-9, 6)
	diffH := body * 2 / 3
	outH := body - diffH
	m.diffView.SetWidth(w)
	m.diffView.SetHeight(max(diffH, 3))
	m.outputView.SetWidth(w)
	m.outputView.SetHeight(max(outH, 3))
	if m.compose.active {
		m.compose.input.SetWidth(composeInputWidth(m.width))
	}
}

// refreshDetailPanes reloads the diff and pane capture when the detail view is
// on screen. Nothing is fetched while the board is showing.
func (m Model) refreshDetailPanes() tea.Cmd {
	if m.mode != modeDetail {
		return nil
	}
	s := m.selected()
	if s == nil {
		return nil
	}
	return tea.Batch(diffCmd(s, m.diffMode), captureCmd(s))
}

// --- hook handling ---

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
	if ev.ClaudeSessionID != "" && s.ClaudeSessionID != ev.ClaudeSessionID {
		s.ClaudeSessionID = ev.ClaudeSessionID
	}

	was := s.AgentState
	changed := s.SetAgentState(out.State, out.Detail)

	var cmds []tea.Cmd
	cmds = append(cmds, next)

	// Notify on the transition into needs_you, not on every event while there.
	if changed && out.State == core.AgentNeedsYou && was != core.AgentNeedsYou {
		detail := out.Detail
		if detail == "" {
			detail = "needs your input"
		}
		notify.Notify(s.Title, detail)
	}

	// The Stop hook may advance lifecycle, but only when the user opted in:
	// an agent that resumes work would otherwise flap the card between columns.
	if out.Stopped && m.cfg.AutoAdvanceOnStop && s.Lifecycle == core.LifecycleActive {
		s.Lifecycle = core.LifecycleReview
	}
	if out.Ended {
		s.TmuxAlive = false
	}

	if changed {
		m.save()
		m.rebuild()
	}
	return m, tea.Batch(cmds...)
}

// --- PR sync ---

func (m Model) handlePRSync(msg prSyncMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// A failing repo reports itself but never blocks the UI or the other
		// repos' polls.
		return m, status(fmt.Sprintf("%s: %v", msg.repoID, msg.err))
	}

	// Index by (repo_id, branch). Matching on headRefName alone would
	// cross-assign PRs between repos that share a branch name.
	byKey := map[core.Key]ghx.PR{}
	for _, pr := range msg.prs {
		k := core.Key{RepoID: msg.repoID, Branch: pr.Branch}
		// Keep the highest-numbered PR for a branch, which is the current one.
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
		pr, ok := byKey[s.Key()]
		core.Touch(s)
		if !ok {
			// The list holds only open PRs. A tracked PR missing from it has
			// reached a terminal state, so resolve that one directly.
			if s.PRNumber > 0 && s.PRState != core.PRMerged && s.PRState != core.PRClosed {
				follow = append(follow, prDetailCmd(repo.Remote, s.ID, s.PRNumber))
			}
			continue
		}
		if s.PRNumber != pr.Number || s.PRState != pr.State || s.PRCI != pr.CI ||
			s.PRReview != pr.Review || s.PRMergeable != pr.Mergeable {
			dirty = true
		}
		hadPR := s.HasPR()
		s.PRNumber, s.PRState = pr.Number, pr.State
		s.PRCI, s.PRReview, s.PRMergeable = pr.CI, pr.Review, pr.Mergeable

		// Lifecycle auto-advances on exactly two durable events: a PR appearing
		// for the branch, and that PR becoming merged.
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
		m.rebuild()
	}
	if len(follow) > 0 {
		return m, tea.Batch(follow...)
	}
	return m, nil
}

// handlePRDetail applies the resolved state of a PR that left the open list.
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

	// Merged is the second and last durable event that advances lifecycle.
	// A PR closed without merging stays where it is and is shown as closed on
	// the card, rather than silently vanishing from the board.
	if msg.pr.State == core.PRMerged && s.Lifecycle != core.LifecycleMerged {
		s.Lifecycle = core.LifecycleMerged
	}
	m.save()
	m.rebuild()
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
	m.rebuild()
	m.selectedID = s.ID

	text := "created " + s.Branch
	if len(msg.res.Warnings) > 0 {
		text += " — " + strings.Join(msg.res.Warnings, "; ")
	}
	return m, tea.Batch(status(text), observeCmd(m.sessions))
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
		s.PRNumber = msg.number
		s.PRState = core.PROpen
		s.Lifecycle = core.LifecyclePROpen
	}
	m.save()
	m.rebuild()
	return m, tea.Batch(status(fmt.Sprintf("opened PR #%d", msg.number)),
		pollPRsCmd(m.cfg, m.sessions))
}

func (m Model) handleTeardown(msg teardownMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		s := core.FindByID(m.sessions, msg.id)
		switch e := msg.err.(type) {
		case *ops.DirtyError:
			return m.askConfirm(
				fmt.Sprintf("%s has uncommitted changes. Discard and prune?", nameOf(s)),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, true)
				})
		case *ops.BranchNotMergedError:
			return m.askConfirm(
				fmt.Sprintf("Branch %s is not fully merged. Delete anyway?", e.Branch),
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
	if m.selected() == nil {
		if s := m.layout.first(); s != nil {
			m.selectedID = s.ID
		} else {
			m.selectedID = ""
			m.mode = modeBoard
		}
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
	// Mouse tracking is declared in the view; it is disabled while attached
	// because tmux and the agent need the events instead.
	v.MouseMode = tea.MouseModeCellMotion
	if c := m.activeCursor(); c != nil {
		v.Cursor = c
	}
	return v
}

// activeCursor surfaces the text cursor while an input is focused.
func (m Model) activeCursor() *tea.Cursor {
	switch m.mode {
	case modeCompose:
		return m.compose.input.Cursor()
	case modePrompt:
		return m.prompt.input.Cursor()
	}
	return nil
}

func (m Model) render() string {
	if m.quitting {
		return ""
	}
	var body string
	switch m.mode {
	case modeHelp:
		body = m.viewHelp()
	case modeDetail:
		body = m.viewDetail()
	case modeRepos:
		body = m.viewRepos()
	default:
		body = m.viewBoard()
	}

	bar := m.viewBottomBar()
	statusLine := m.viewStatus()

	content := lipgloss.JoinVertical(lipgloss.Left, body, statusLine, bar)
	// Scan registers the zone positions from the rendered frame; without it,
	// mouse coordinates cannot be resolved back to components.
	return zone.Scan(content)
}

func (m Model) viewStatus() string {
	if m.statusText == "" || time.Since(m.statusAt) > 12*time.Second {
		return ""
	}
	st := m.styles.Status
	if m.statusErr {
		st = m.styles.Error
	}
	return st.Render("  " + truncate(m.statusText, max(m.width-2, 10)))
}

func (m Model) viewBottomBar() string {
	switch m.mode {
	case modeCompose:
		return m.viewCompose()
	case modePrompt:
		return m.viewPrompt()
	case modeConfirm:
		return m.styles.Dialog.Render(m.confirm.message + "   [y/n]")
	case modeRepos:
		return m.hintLine([]hint{
			{"j/k", "move"}, {"enter", "use for new sessions"},
			{"a", "add repo"}, {"x", "unregister"}, {"esc", "back"},
		})
	}

	hints := boardHints(m.cfg.MultiRepo())
	if m.mode == modeDetail {
		hints = detailHints()
	}
	line := m.hintLine(hints)
	if m.repoFilter != "" {
		line = m.styles.RepoTag.Render("[repo:"+m.repoFilter+"] ") + line
	}
	return lipgloss.NewStyle().Width(m.width).Render(truncate(line, m.width))
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
			st.KeyHint.Render(pad(row[1], 10)), st.KeyDesc.Render(row[2])))
	}
	b.WriteString("\n  " + st.Meta.Render("hook listener: "+m.hookURL) + "\n")
	b.WriteString("  " + st.Meta.Render("press any key to return") + "\n")
	return b.String()
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
