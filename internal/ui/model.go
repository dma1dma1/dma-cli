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
	"github.com/dma1dma1/dma-cli/internal/render"
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
	// projectFilter resets on launch rather than persisting: a filter you forgot
	// you set is a board that silently lies about what is running.
	projectFilter string

	// colScroll is the index of the first card each column draws, so a column
	// holding more cards than fit scrolls instead of growing.
	colScroll [4]int
	// scrollPinned marks the columns the wheel has parked. A pinned column is
	// left where the pointer put it, even when the cursor is somewhere off screen;
	// otherwise the offset chases the selected card, which is what keeps the cursor
	// visible while the agents reorder the cards underneath it. Moving the cursor
	// unpins, so the keyboard always wins the column back.
	scrollPinned [4]bool

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
	preview string
	// previewCursor is where that frame's terminal cursor sat. It is kept beside
	// the content and only ever set together with it, so the caret cannot be
	// drawn onto a frame it does not belong to.
	previewCursor tmuxx.Cursor
	// previewScroll is how many lines above the live pane the panel is showing.
	// It belongs to the preview rather than tmux copy mode: the panel is a
	// captured view, and scrolling it must not change how the real pane receives
	// the next forwarded key.
	previewScroll int
	// previewMouseSGR says the selected application asked for SGR mouse events.
	// Full-screen agents such as Claude own their scroll position and have no
	// tmux history, so their wheel events must go back to the application.
	previewMouseSGR bool
	// previewSelection is an application-owned drag selection. Terminal-native
	// selections cannot survive the board's live redraws, so the preview keeps a
	// snapshot and redraws the selected cells itself until the next interaction.
	previewSelection previewSelection

	// echoUntil is how long the panel keeps re-reading the pane at echoInterval
	// after a forwarded keystroke; echoing says whether that ticker is running,
	// so a burst of typing extends the window instead of starting a ticker per
	// key.
	echoUntil time.Time
	echoing   bool

	// touchedAt is when dma last did something to each session's terminal: sent a
	// keystroke or a paste, forwarded a wheel event, or resized it. The prober
	// needs it to tell the board's own doing apart from the agent's output -- both
	// change the pane, and only one of them means the agent is working.
	touchedAt map[string]time.Time

	// hookSeen marks the sessions that have reported a hook to this board since
	// it started, which is the difference between a state that was observed and
	// one that was read off disk. Only the second kind can be a transition the
	// board was not running to receive; see stranded.
	hookSeen map[string]bool

	// review is the full-screen review view: the file tree, the pane beside it,
	// and the searches that choose what the pane shows. See review.go.
	review review

	// helpQuery filters the help screen's keymap as it is typed. It is cleared
	// when the screen closes: a search you set last time is a keymap that looks
	// like it has lost half its keys.
	helpQuery string

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
		touchedAt:   map[string]time.Time{},
		hookSeen:    map[string]bool{},
		mode:        modeBoard,
		focus:       focusBoard,
		hookEvents:  opt.HookEvents,
		hookURL:     opt.HookURL,
		activeRepo:  opt.LaunchRepo,
		agentChoice: opt.Config.DefaultProfile,
		input:       newTaskInput(styles),
		review:      review{view: viewport.New(), mode: gitx.DiffUncommitted},
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
	m.syncColumnScroll()
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
	m.clearPreviewSelection()
	m.selectedID, m.deselected = s.ID, false
	m.preview, m.previewCursor = "", tmuxx.Cursor{}
	m.previewScroll, m.previewMouseSGR = 0, false
	m.unpinScroll()
}

// clearSelection empties the panel and keeps it empty until something is picked.
func (m *Model) clearSelection() {
	m.clearPreviewSelection()
	m.selectedID, m.deselected = "", true
	m.preview, m.previewCursor = "", tmuxx.Cursor{}
	m.previewScroll, m.previewMouseSGR = 0, false
}

// dropSelectionIfHidden empties the panel when a filter has just taken the
// selected card off the board.
//
// The panel does not outlive the card it belongs to. Selection is held as an id
// and the filter does not touch it, so changing it used to leave the previous
// project's agent running in the panel with no card above it to explain why --
// which reads as the filter not having taken. Every filter change lands on the
// empty panel instead, the same place a board with no sessions starts from, and
// the next move or click picks from what the board is showing now.
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
		// A card the board picked for you still has to be a card you can see, so a
		// column the wheel had parked gives way here too.
		m.unpinScroll()
	}
}

func (m *Model) layoutSizes() {
	// Prompt and ceiling before SetWidth: the field wraps its text against the
	// width left over after the prompt, and re-measures itself against the ceiling,
	// so SetWidth has to be the last of the three.
	m.setInputPrompt()
	m.input.MaxHeight = m.inputRowsMax()
	m.input.SetWidth(m.inputWidth())
	m.review.view.SetWidth(m.diffPaneWidth())
	m.review.view.SetHeight(m.diffPaneHeight())
}

// diffTreeWidth is the width of the file tree pane, zero when it is not drawn.
//
// A fixed width rather than a share of the window: the rows are file names, and
// a pane that grew with the terminal would spend the extra cells on whitespace
// the diff needs more. Below the cutoff neither pane would be readable, so the
// tree gives way entirely.
func (m Model) diffTreeWidth() int {
	if m.review.treeHidden || m.contentWidth() < diffTreeMinTotal {
		return 0
	}
	return diffTreeCols
}

const (
	// diffTreeCols is wide enough for a nested file name plus its counts.
	diffTreeCols = 32
	// diffTreeMinTotal is the narrowest window that still leaves a usable diff
	// beside the tree.
	diffTreeMinTotal = 100
)

// diffPaneWidth is the interior width of the diff pane, which is also what delta
// is told to render into: too wide and every line wraps one cell early, too
// narrow and the side-by-side columns do not line up with the frame.
func (m Model) diffPaneWidth() int {
	inner := m.contentWidth() - 4
	if treeW := m.diffTreeWidth(); treeW > 0 {
		inner -= treeW + diffDividerCols
	}
	return max(inner, 20)
}

// diffDividerCols is the width of the rule between the panes, " │ ".
const diffDividerCols = 3

func (m Model) diffPaneHeight() int {
	// Four rows go to the frame and the chip row, two more to the notice line
	// and the shortcut bar.
	return max(m.height-6, 5)
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
		// Whatever the last run left in the trash: a sweep the quit cut short, or
		// one that never ran because the process went away with the prune.
		sweepTrashCmd(m.cfg),
	)
}

// Update settles the column scroll after every message, whatever the message
// did.
//
// Scroll offsets are model state, so they cannot be fixed up during render, and
// almost anything can invalidate them: a resize, a filter, a moved cursor, or a
// poll that lands a new card. Doing it in one place here beats a call at the end
// of each of the dozens of paths through update, which is a call the next handler
// would forget.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if mm, ok := next.(Model); ok {
		mm.syncColumnScroll()
		return mm, cmd
	}
	return next, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		if msg.Width != m.width || msg.Height != m.height {
			m.clearPreviewSelection()
		}
		m.width, m.height = msg.Width, msg.Height
		m.layoutSizes()
		return m, m.syncAgentSize()

	case tea.KeyPressMsg:
		m.clearPreviewSelection()
		return m.handleKey(msg)

	case tea.PasteMsg:
		m.clearPreviewSelection()
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
		return m, tea.Batch(previewTickCmd(), previewCmdAt(m.selected(), m.previewScroll))

	case echoTickMsg:
		if !now().Before(m.echoUntil) {
			m.echoing = false
			return m, nil
		}
		return m, tea.Batch(echoTickCmd(), previewCmdAt(m.selected(), m.previewScroll))

	case probeTickMsg:
		return m, tea.Batch(probeTickCmd(), probeCmd(m.prober, m.cfg, m.sessions, m.touchedAt, m.hookSeen))

	case pollTickMsg:
		// Hoisted out of the batch: refreshDiff drops the rendered-diff cache, and
		// that has to happen to the model being returned rather than to a copy
		// made while the arguments were being evaluated.
		diff := m.refreshDiff()
		return m, tea.Batch(
			pollTickCmd(time.Duration(m.cfg.PollIntervalSecs)*time.Second),
			pollPRsCmd(m.cfg, m.sessions),
			observeCmd(m.sessions),
			diff,
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
		// Captures are asynchronous. A result requested before a later wheel
		// event must not pull the preview back to an older position.
		if msg.id == m.selectedID && msg.requestedScroll == m.previewScroll {
			m.preview, m.previewCursor = msg.content, msg.cursor
			m.previewMouseSGR = msg.mouseSGR
			m.previewScroll = msg.actualScroll
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

	case titledMsg:
		return m.handleTitled(msg)

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

	case changedFilesMsg:
		if msg.id != m.selectedID || msg.mode != m.review.mode {
			return m, nil
		}
		if msg.err != nil {
			return m, errStatus(msg.err)
		}
		m.review.files.setFiles(msg.files)
		return m, m.showTreeSelection()

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
		if m.review.cache == nil {
			m.review.cache = map[string]string{}
		}
		m.review.cache[msg.key] = content
		// A render that finished after the cursor moved on is worth keeping but
		// not worth showing: the pane already holds the file it moved to.
		if msg.key == m.diffKey() {
			m.setDiffContent(content)
		}
		return m, nil

	case fileMsg:
		return m.handleFile(msg)

	case worktreeFilesMsg:
		if msg.id != m.selectedID || msg.err != nil {
			return m, nil
		}
		m.review.paths = msg.paths
		// The finder may already be open on an empty list: the path list is
		// asked for when the view opens and f can be pressed before it lands.
		if m.review.picker.kind == pickerFiles {
			m.rankPaths()
		}
		return m, nil

	case grepDebounceMsg:
		// Anything typed since this was scheduled has started a newer one.
		if msg.gen != m.review.picker.gen || m.review.picker.kind != pickerGrep {
			return m, nil
		}
		s := m.selected()
		if s == nil || m.review.picker.query == "" {
			return m, nil
		}
		m.review.picker.searching = true
		return m, grepCmd(s, m.review.picker.query, msg.gen)

	case grepMsg:
		return m.handleGrep(msg)

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
	m.touchAll()
	return resizeSessionsCmd(m.sessions, cols, rows)
}

// attach hands the terminal to one session, recording the handover. Attaching
// releases the window to the real terminal and detaching pins it back to the
// preview size, and either reflow rewrites the pane -- which is a change the
// prober would otherwise have to read as the agent writing.
func (m Model) attach(s *core.Session) tea.Cmd {
	m.touchedAt[s.ID] = now()
	return attachCmd(s)
}

// touchAll records that dma is about to reflow every agent, so the prober reads
// the redraw that follows as the resize it is.
//
// A resize rewrites every line on a pane, which looks exactly like an agent that
// has just written a screenful. Sizing the terminals is the first thing the board
// does on startup, so without this every probe-driven card announced a turn on
// launch and finished it 25 seconds later.
func (m *Model) touchAll() {
	at := now()
	for _, s := range m.sessions {
		m.touchedAt[s.ID] = at
	}
}

// openDiff enters the review view with the file tree in focus, because picking
// which change to read is the first thing to do in it.
func (m *Model) openDiff() tea.Cmd {
	m.mode = modeDiff
	m.review.treeFocus = m.diffTreeWidth() > 0
	return m.refreshDiff()
}

// refreshDiff re-lists the files and re-renders the current one, discarding
// everything already rendered. It is what opening the view, switching session,
// and switching mode all go through.
func (m *Model) refreshDiff() tea.Cmd {
	if m.mode != modeDiff {
		return nil
	}
	s := m.selected()
	if s == nil {
		return nil
	}
	// The cache describes the worktree as it was; a refresh exists because that
	// may have changed underneath it. The path list goes with it: a file the
	// agent has just written is one you want the finder to offer.
	m.review.cache = map[string]string{}
	m.review.paths = nil
	m.setDiffContent("")
	return tea.Batch(
		changedFilesCmd(s, m.review.mode),
		worktreeFilesCmd(s),
		m.showTreeSelection(),
	)
}

// diffOpts is how the diff pane wants its content rendered.
func (m Model) diffOpts() gitx.DiffOpts {
	return gitx.DiffOpts{Width: m.diffPaneWidth(), SideBySide: m.review.sideBySide}
}

// diffKey identifies the render the pane should be showing. Everything that
// changes the output is in it: which session, which question, which row, and
// which layout.
//
// It doubles as the cache key, which is why it is a string rather than a
// struct: the two are the same question -- "is what the pane wants what the
// pane has" -- and a single answer cannot drift out of step with itself.
func (m Model) diffKey() string {
	if m.review.source == sourceFile {
		// A file's contents do not depend on the range or the columns, but they
		// do depend on the pane's width, since that is what they were wrapped
		// and numbered for.
		return fmt.Sprintf("%s|file|%s|%d", m.selectedID, m.filePath(), m.diffPaneWidth())
	}
	target := m.review.files.target()
	return fmt.Sprintf("%s|%d|%v|%v|%s|%d", m.selectedID, m.review.mode,
		m.review.sideBySide, target.Untracked, target.Path, m.diffPaneWidth())
}

// filePath is the path the pane shows the contents of.
func (m Model) filePath() string { return m.review.filePath }

// showTreeSelection renders whatever the tree cursor is on.
//
// Moving the cursor is a choice of path, so the pane's file goes with it: the
// tree and the pane must not end up naming two different files. A directory has
// no contents, so landing on one falls back to its diff rather than leaving the
// pane asking a question the row cannot answer.
func (m *Model) showTreeSelection() tea.Cmd {
	if row, ok := m.review.files.selected(); ok && !row.dir {
		m.review.filePath = row.path
	} else {
		m.review.filePath = ""
		m.review.source = sourceDiff
	}
	return m.showSelectedFile()
}

// showSelectedFile puts what the review view is pointed at into the pane,
// rendering it only if it has not been rendered already.
func (m *Model) showSelectedFile() tea.Cmd {
	s := m.selected()
	if s == nil {
		return nil
	}
	// Nothing to read the contents of falls back to the diff rather than
	// emptying the pane.
	if m.review.source == sourceFile && m.review.filePath == "" {
		m.review.source = sourceDiff
	}
	key := m.diffKey()
	if cached, ok := m.review.cache[key]; ok {
		m.setDiffContent(cached)
		return nil
	}
	if m.review.source == sourceFile {
		return fileCmd(s, m.filePath(), m.diffPaneWidth(), key)
	}
	return diffCmd(s, m.review.mode, m.review.files.target(), m.diffOpts(), key)
}

// showFileAt opens one path in the pane and scrolls to a line in it, which is
// what a grep hit and a picked file both land on. line zero means the top.
//
// The tree cursor follows when the tree is showing that path, so the two do not
// end up naming different files. A path the tree does not hold -- an unchanged
// file, which is most of the worktree -- leaves the cursor alone; the subtitle
// names the file either way, so the pane is never unlabelled.
func (m *Model) showFileAt(path string, line int) tea.Cmd {
	m.review.files.setCursorByPath(path)
	m.review.filePath = path
	m.review.source = sourceFile
	m.review.pending = pendingScroll{line: line, active: line > 0}
	// The scroll is not applied here. showSelectedFile either serves the file
	// from cache -- in which case setDiffContent has already honored it -- or
	// returns a command, and the document still in the pane is the *previous*
	// file. Scrolling that one to line 12 would both move the wrong file and
	// spend the request, leaving the right file at its top when it arrives.
	return m.showSelectedFile()
}

// applyPendingScroll puts the pane on the line a search asked for.
//
// It is a two-step because the render is asynchronous: the line is named when
// the hit is picked and can only be honored once there is content to find it
// in. Every route to new content runs it, and it is consumed whether or not the
// line was there -- a request left armed would fire at the next unrelated file.
func (m *Model) applyPendingScroll() {
	if !m.review.pending.active || m.review.doc == nil {
		return
	}
	if row, ok := m.review.doc.RowForLine(m.review.pending.line); ok {
		m.review.view.SetYOffset(row)
	}
	m.review.pending = pendingScroll{}
}

// setDiffContent puts a rendered diff in the pane, back at the top.
//
// Reading the content into a document is what the pane's structure now comes
// from: one pass over the rows it was already going to draw, rather than a
// second git process and a search through the rendered text for something to
// recognize.
func (m *Model) setDiffContent(content string) {
	m.review.doc = render.Parse(content, m.diffLayout())
	m.review.view.SetYOffset(0)
	// The hits belong to the rows of the document that has just been replaced,
	// so they are re-found rather than carried over. The query survives: it is
	// what the user asked, and asking it again of the new content is what
	// stepping to the next file during a search has to mean.
	m.review.find.run(m.review.doc)
	m.drawPane()
	// Last, so it overrides the reset to the top: a file opened at a grep hit
	// has to arrive showing the hit.
	m.applyPendingScroll()
}

// drawPane hands the document to the viewport, with the search hits drawn in.
//
// Highlighting is applied here rather than baked into the content, so the cache
// holds what was rendered and not what happened to be searched for at the time.
func (m *Model) drawPane() {
	if m.review.doc == nil {
		m.review.view.SetContentLines(nil)
		return
	}
	f := m.review.find
	if len(f.matches) == 0 {
		m.review.view.SetContentLines(m.review.doc.Lines())
		return
	}
	m.review.view.SetContentLines(
		m.review.doc.Highlight(f.matches, f.current, m.styles.MatchHit, m.styles.MatchCurrent))
}

// diffLayout is how the content in the pane was laid out, which is what it
// takes to read a row's margin back.
func (m Model) diffLayout() render.Layout {
	// A file is rendered here rather than by delta, and always in one column.
	if m.review.source == sourceFile {
		return render.Unified
	}
	if m.review.sideBySide && gitx.HasDelta() {
		return render.SideBySide
	}
	return render.Unified
}

// diffHunks is the changes in the pane, or none when nothing is loaded.
func (m Model) diffHunks() []render.Hunk {
	if m.review.doc == nil {
		return nil
	}
	return m.review.doc.Hunks
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
	// This session's state is now something this board watched happen rather than
	// something it inherited from the last one, so the pane has nothing to add and
	// the exact channel takes it back.
	m.hookSeen[s.ID] = true
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
		if st.Content != "" && s.ID == m.selectedID && m.previewScroll == 0 {
			m.preview, m.previewCursor = st.Content, st.Cursor
		}
	}
	if dirty {
		m.save()
	}
	return m, nil
}

// notifyFn is the desktop notifier, indirected so tests can assert on what the
// board would have raised without a notification landing on the screen of
// whoever is running them.
var notifyFn = notify.Notify

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
	notifyFn(s.Title, detail)
}

// notifyIfMergeable raises a desktop notification the first time an open PR has
// nothing left blocking it, for the same reason needs_you does: the board is not
// meant to be babysat, and a PR that has gone green is waiting on the user.
//
// It reports whether the session changed, since releasing a claim has to be
// persisted too -- a stale claim left on disk would swallow the notification for
// the next time the PR comes clean.
func (m Model) notifyIfMergeable(s *core.Session) (changed bool) {
	was := s.PRMergeableNotified
	if s.ClaimMergeableNotice() {
		notifyFn(s.Title, fmt.Sprintf("#%d ready to merge", s.PRNumber))
	}
	return s.PRMergeableNotified != was
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
			s.PRReview != pr.Review || s.PRMergeable != pr.Mergeable || s.PRURL != pr.URL ||
			s.PRAutoMerge != pr.AutoMerge {
			dirty = true
		}
		s.PRNumber, s.PRURL, s.PRState = pr.Number, pr.URL, pr.State
		s.PRCI, s.PRReview, s.PRMergeable = pr.CI, pr.Review, pr.Mergeable
		s.PRAutoMerge = pr.AutoMerge

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
			s.PRAutoMerge = false
		}

		// Last, so the verdict is read off the fully applied poll. A card that is
		// also waiting on a queue re-check is judged on the queue standing GitHub
		// last confirmed; if that check changes it, it notifies from there.
		if m.notifyIfMergeable(s) {
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

// handleMerged applies the result of pressing m.
//
// Only a merge that landed moves the card to the merged column. A pull request
// accepted by auto-merge or a merge queue is still open work: GitHub merges it
// later, or may stop waiting, and the poll is what resolves which.
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
		s.PRAutoMerge = false
		m.save()
		return m, nil
	case ghx.MergeAutoEnabled:
		// Auto-merge is still an open PR. The ordinary PR poll carries the
		// autoMergeRequest field, so it also notices if GitHub disables it later.
		s.PRQueued = false
		s.PRAutoMerge = true
		m.save()
		return m, nil
	}
	s.PRState = core.PRMerged
	s.Lifecycle = core.LifecycleMerged
	s.PRQueued = false
	s.PRAutoMerge = false
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
	// A PR the queue dropped back out is the user's problem again, and if it is
	// otherwise green that happened without any poll field changing.
	m.notifyIfMergeable(s)
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
	s.PRAutoMerge = false
	core.Touch(s)
	// A PR closed without merging keeps its column and is labelled closed on the
	// card, rather than vanishing from the board.
	if msg.pr.State == core.PRMerged {
		s.Lifecycle = core.LifecycleMerged
	}
	// This normally resolves a PR that has landed, which releases the claim. It
	// can also find one still open -- the poll and this query are two requests,
	// and a PR can rejoin the open set between them.
	m.notifyIfMergeable(s)
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
	// rebuild rather than select: a start never moves the panel to the new card.
	// It still fills an empty one -- a board whose panel is empty has nothing to be
	// pulled away from, so the first card to arrive may as well fill it. A panel
	// emptied on purpose is left empty, which rebuild already knows.
	m.rebuild()
	// watching is whether the panel ended up on the new session, which is what
	// decides both of the questions below.
	watching := m.selectedID == s.ID

	// A card that took the panel is its own confirmation, so it says nothing. Every
	// other start moved nothing on screen -- it is one more card in a column that
	// already had some -- so it says which session it was.
	//
	// Warnings outrank both: a symlink or hook install that failed is not visible
	// anywhere on the board, and the session runs degraded until it is fixed.
	var note tea.Cmd
	if !watching {
		m.notice, m.noticeErr, m.noticeAt = "started in the background: "+s.Title, false, time.Now()
	}
	if len(msg.res.Warnings) > 0 {
		note = errText(strings.Join(msg.res.Warnings, "; "))
	}
	cols, rows := m.previewDims()
	// The size this session is about to be given reflows whatever its agent has
	// already drawn, so the resize is recorded like every other one dma issues.
	m.touchedAt[s.ID] = now()
	var preview tea.Cmd
	if watching {
		// Only the session on the panel is ever captured, and a capture for any
		// other one is dropped on arrival.
		preview = previewCmd(s)
	}
	return m, tea.Batch(note, observeCmd(m.sessions), preview,
		resizeSessionsCmd([]*core.Session{s}, cols, rows),
		// The card starts out titled with the first line of the task; naming it
		// properly happens off to the side, now that there is something on
		// screen.
		titleCmd(s, summaryInput(msg, s)))
}

// summaryInput is the text a card's name is written from.
//
// It is the whole task, which only the create message still carries: the session
// record keeps just the first line of it, and the line that says what the work is
// is as often below the first one as in it. A caller that carried no task at all
// falls back to the title, which is the best text there is in that case.
func summaryInput(msg createdMsg, s *core.Session) string {
	if task := strings.TrimSpace(msg.task); task != "" {
		return task
	}
	return s.Title
}

// handleTitled renames a card once a summary of its task arrives.
//
// The rename is silent and unannounced. It lands a few seconds after the card
// does, and by then the eye is on the board rather than on the one card, so a
// status line about it would be noise about something already visible.
func (m Model) handleTitled(msg titledMsg) (tea.Model, tea.Cmd) {
	s := core.FindByID(m.sessions, msg.id)
	// Summarizing takes seconds and pruning takes one key, so the session this
	// title belongs to may be gone by now.
	if s == nil {
		return m, nil
	}
	title := strings.TrimSpace(msg.title)
	// An unchanged title is the common case for work described in a few words,
	// and an empty one means no model could be reached. Both leave the card as
	// it is; the board is never worse off for having asked.
	if title == "" || title == s.Title {
		return m, nil
	}
	s.Title = title
	m.save()
	return m, nil
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
					return teardownCmd(mm.cfg, s, ops.TeardownOptions{Force: true})
				})
		case *ops.UnnamedCommitsError:
			return m.askConfirm(fmt.Sprintf("%s has commits on no branch. Discard and prune?", nameOf(s)),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, ops.TeardownOptions{Force: true})
				})
		case *ops.BranchNotMergedError:
			return m.askConfirm(fmt.Sprintf("Branch %s is not fully merged. Delete anyway?", e.Branch),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, ops.TeardownOptions{Force: true})
				})
		// The pull request is still open and nothing has been removed yet. The
		// answer is the user's: leaving the PR open is a real choice offline, and
		// the alternative -- keeping the session until GitHub is reachable -- is
		// what declining does.
		case *ops.PRCloseError:
			return m.askConfirm(fmt.Sprintf("Could not close PR #%d (%v). Prune anyway and leave it open?", e.Number, e.Err),
				func(mm *Model) tea.Cmd {
					if s == nil {
						return nil
					}
					return teardownCmd(mm.cfg, s, ops.TeardownOptions{KeepPR: true})
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
	// The card is gone but its files are not: teardown renamed them into the
	// trash, and this is where the unlink gets paid for.
	return m, sweepTrashCmd(m.cfg)
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
	case m.mode == modeDiff && m.review.picker.open():
		return m.review.picker.input.Cursor()
	case m.mode == modeDiff && m.review.find.active:
		return m.review.find.input.Cursor()
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
	// The find field borrows the footer the same way a prompt does, rather than
	// taking a row off the pane it is searching. The pane is the thing you are
	// reading while you type into it, and shortening it would scroll the answer
	// out from under the question.
	if m.mode == modeDiff && m.review.find.active {
		line := st.KeyHint.Render("/") + m.review.find.input.View() +
			st.KeyDesc.Render("   ↑↓ step · enter keep · esc clear")
		return lipgloss.NewStyle().Width(w).Render(truncate(line, w))
	}
	line := m.hintLine(m.hints())
	return lipgloss.NewStyle().Width(w).Render(truncate(" "+line, w))
}

func (m Model) hintLine(hints []hint) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, m.styles.KeyHint.Render(h.key)+" "+m.styles.KeyDesc.Render(h.desc))
	}
	return strings.Join(parts, m.styles.KeyDesc.Render(" · "))
}
