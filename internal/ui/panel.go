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
	// focusPreview is the running agent in the panel: keystrokes are forwarded to
	// its terminal. focusAgent, confusingly close, is the chip that picks which
	// agent *new* sessions launch -- it has nothing to do with a live one.
	focusPreview
	focusInput
	focusAgent
	focusRepo
	focusProject
)

// focusRing is the order tab walks.
var focusRing = []focusArea{focusBoard, focusPreview, focusInput, focusAgent, focusRepo, focusProject}

const (
	zoneAgentChip   = "chip:agent"
	zoneRepoChip    = "chip:repo"
	zoneProjectChip = "chip:project"
	zonePreview     = "panel:preview"
	zoneInput       = "panel:input"
)

// projectNew is the sentinel row that invents a project rather than selecting
// one. Labels are free text and a dropdown cannot take text, so this row hands
// off to the prompt; it is a sentinel rather than an empty string because ""
// already means "no project".
const projectNew = "\x00new-project"

const newProjectLabel = "+ new project…"

func zoneCard(id string) string { return "card:" + id }
func zoneColumn(i int) string   { return fmt.Sprintf("col:%d", i) }
func zoneOption(i int) string   { return fmt.Sprintf("opt:%d", i) }

// minPanelHeight is the fewest rows worth giving the panel: its frame, chips,
// rule and input bar, plus enough of the agent's output to be worth reading.
const minPanelHeight = 12

// inputBoxHeight is what the task input claims once it draws its own frame: two
// rows of border around the one row you type on.
const inputBoxHeight = 3

// inputDetached reports whether the task input gets a frame of its own below the
// panel.
//
// It does while you are typing there. Sharing the panel's frame with the live
// agent puts your caret two rows under the agent's own prompt, which reads as
// one crowded input into the session rather than the separate thing it is.
func (m Model) inputDetached() bool {
	if m.focus != focusInput {
		return false
	}
	// On a window too short for both, the input stays in the panel: a frame of
	// its own is not worth the rows it takes off the agent's output.
	return m.height-1 >= inputBoxHeight+minPanelHeight
}

// splitHeights divides the screen between the columns, the panel and the task
// input's own box, above the footer.
//
// The board takes only the rows its cards need and the panel takes the rest.
// Sizing the two by a fixed ratio instead looks right on a laptop and wrong on
// anything taller: the columns stretch into a screenful of empty frame while the
// agent's output stays in the same short window it had at 30 rows.
func (m Model) splitHeights() (boardH, panelH, inputH int) {
	boardH, panelH = m.baseHeights()
	if m.inputDetached() {
		// The panel gives up the row the input bar was using, and the two the new
		// frame adds are borrowed.
		inputH = inputBoxHeight
		panelH--
		boardH, panelH = borrowRows(boardH, panelH, inputBoxHeight-1)
	}
	if m.noticeActive() {
		boardH, panelH = borrowRows(boardH, panelH, 1)
	}
	return boardH, panelH, inputH
}

// borrowRows takes n rows off the board where it has them to spare, and off the
// panel where it does not.
//
// The board is asked first because its rows are the cheap ones: a column short
// of a card scrolls, while the panel losing rows is the agent's output getting
// shorter. baseHeights, and so the size the agents are rendered at, deliberately
// does not see any of this -- an agent reflowed every time a notice appears and
// again ten seconds later would be a worse cost than the row.
func borrowRows(boardH, panelH, n int) (int, int) {
	fromBoard := min(n, max(boardH-minBoardRows, 0))
	return boardH - fromBoard, panelH - (n - fromBoard)
}

// baseHeights is the board/panel split with the task input inside the panel,
// which is the layout the agents are sized to.
func (m Model) baseHeights() (boardH, panelH int) {
	avail := max(m.height-1, 3)
	if m.previewFull {
		return 0, avail
	}
	// The panel holds the input and the live output, so on a screen too short
	// for both it is the board that goes.
	maxBoard := avail - minPanelHeight
	if maxBoard < minBoardRows {
		return 0, avail
	}
	// Past halfway the board stops growing and the columns scroll instead. Letting
	// it keep taking rows was worse than it sounds: one busy column would push the
	// agent's output down to minPanelHeight, so a board doing its job cost you sight
	// of the session you were actually working in.
	maxBoard = clamp(avail/2, minBoardRows, maxBoard)
	boardH = clamp(m.boardContentHeight(), minBoardRows, maxBoard)
	return boardH, avail - boardH
}

// previewDims is the size the agent's terminal should be rendered at: the
// panel's interior, minus the rows the chips, rule and input bar occupy.
//
// Agents are sized to this rather than to some default, because a tmux session
// with no client has no natural size and would otherwise draw into 80 columns.
//
// It deliberately ignores the input's own box: a board already down to its
// minimum has no rows to lend, so sizing to the shrunken panel would reflow every
// agent each time you focus the input. Showing two fewer lines of a terminal that
// kept its size costs nothing -- the panel prints the tail, which is the part
// being written.
func (m Model) previewDims() (cols, rows int) {
	_, h := m.baseHeights()
	return max(m.contentWidth()-4, 20), max(h-panelChromeRows, 3)
}

// panelChromeRows is what the panel spends on everything that is not the agent's
// output: two rows of frame, the chip row, the rule, and the input bar.
const panelChromeRows = 5

// panelChrome is that count for the panel as it currently stands, which is one
// row less once the input has its own box.
func (m Model) panelChrome() int {
	if m.inputDetached() {
		return panelChromeRows - 1
	}
	return panelChromeRows
}

// viewPanel renders the persistent bottom panel: the three selectors, the live
// session output, and the input bar.
func (m Model) viewPanel(height int) string {
	st := m.styles
	inner := max(m.contentWidth()-4, 20)

	var rows []string
	rows = append(rows, m.chipRow(inner))
	rows = append(rows, st.Faint.Render(strings.Repeat("─", inner)))

	// The dropdown opens into the body rather than floating over it, so there
	// is no compositing to get wrong and the list is never clipped.
	bodyRows := max(height-m.panelChrome(), 1)
	if m.dropdown.open {
		rows = append(rows, m.viewDropdown(bodyRows, inner)...)
	} else {
		rows = append(rows, m.previewBody(bodyRows, inner)...)
	}

	if !m.inputDetached() {
		rows = append(rows, m.inputRow(inner))
	}

	title, subtitle := m.panelTitle()
	b := Box{
		Title:    title,
		Subtitle: subtitle,
		// The same color the selected card's title wears, so the card and the
		// panel showing it are visibly one pair rather than two blues that happen
		// to be near each other.
		Accent: st.P.Focus,
		Border: st.P.Border,
		Width:  m.contentWidth(),
		Height: height,
		// While the input has its own box that box is the focused one; two bold
		// frames stacked would say keystrokes go to both.
		Focused: m.focus != focusBoard && !m.inputDetached(),
	}
	return b.Render(strings.Join(rows, "\n"))
}

// viewInputBox renders the task input in its own frame, which is what it gets
// while you are typing into it.
func (m Model) viewInputBox(height int) string {
	st := m.styles
	b := Box{
		Title:    "new session",
		Subtitle: m.newSessionTarget(),
		Accent:   st.P.Success,
		Border:   st.P.Border,
		Width:    m.contentWidth(),
		Height:   height,
		Focused:  true,
	}
	return b.Render(m.inputRow(max(m.contentWidth()-4, 20)))
}

// newSessionTarget names what pressing enter would start. The chips say the same
// thing, but they are in the other box now, and this is the moment it matters.
func (m Model) newSessionTarget() string {
	repo := m.activeRepoID()
	if repo == "" {
		return "no repo — press r to add one"
	}
	target := m.agentChoice + " in " + repo
	if m.projectFilter != "" {
		target += " ◆ " + m.projectFilter
	}
	return target
}

func (m Model) panelTitle() (string, string) {
	s := m.selected()
	if s == nil {
		return "session", "nothing selected"
	}
	// The move list is the filter list with a different meaning, and nothing in
	// the rows themselves says which one is open. The frame does.
	if m.dropdown.open && m.dropdown.target != "" {
		return s.Title, "move to a project"
	}
	sub := s.TmuxSession
	if !s.TmuxAlive {
		sub += " (not running)"
	}
	if m.previewFull {
		sub += " · e to shrink"
	}
	// While the agent has the keyboard the way out is the one thing the frame must
	// say: every other key is going to the agent, so a user who wants the board
	// back has nothing else to guess from.
	if m.focus == focusPreview {
		sub += " · typing to agent · " + detachKey + " to leave"
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
	// Drawn before the tail is taken, while a line's index is still its pane row:
	// that is the only coordinate space the cursor tmux reported makes sense in.
	lines = placeCursor(lines, m.previewCursor, rows, width)
	// The tail is the interesting part of a terminal.
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	out := make([]string, 0, rows)
	for _, l := range lines {
		// pad truncates, so a cursor drawn past the panel's width is clipped
		// away here rather than widening the row.
		out = append(out, pad(l, width))
	}
	// One mark around the block, not one per line: paired markers describe a
	// rectangle, so per-line marks would collapse the zone onto the last line.
	out = strings.Split(zone.Mark(zonePreview, strings.Join(out, "\n")), "\n")
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

// newSessionGlyph marks the board's task input.
//
// It is deliberately not "❯": this row sits directly beneath a live agent that
// draws its own "❯" prompt, and two prompt carets stacked read as two inputs
// into the same session. This one only ever starts a new session, so it is
// marked as the action it is.
const newSessionGlyph = "+ "

// inputRow is the always-present task input. It stays on screen even when the
// board has focus, so starting work is never more than one key away.
func (m Model) inputRow(width int) string {
	st := m.styles
	glyph := st.Prompt.Render(newSessionGlyph)
	summary := m.imageSummary()
	if m.focus == focusInput {
		image := st.RepoTag.Render(summary)
		return zone.Mark(zoneInput, pad(glyph+image+m.input.View(), width))
	}
	// Naming the session as new is what keeps the row from being mistaken for
	// the agent's own input; the keystroke alone would not say where it goes.
	hint := "new session — press i to describe a task"
	if v := m.input.Value(); v != "" {
		hint = v
	}
	hint = summary + hint
	body := len(newSessionGlyph)
	// Padded so the whole row is clickable, not just the width of the hint text.
	return zone.Mark(zoneInput, pad(glyph+st.Faint.Render(truncate(hint, max(width-body, 4))), width))
}

func (m Model) imageSummary() string {
	switch len(m.pendingImages) {
	case 0:
		return ""
	case 1:
		image := m.pendingImages[0]
		return fmt.Sprintf("[image %d×%d] ", image.Width, image.Height)
	default:
		return fmt.Sprintf("[images ×%d] ", len(m.pendingImages))
	}
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
	// target is the session a project choice applies to. Empty means the choice
	// aims the chip instead -- which filters the board and sets the project new
	// sessions join.
	target string
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
		d.labels = append(d.labels, projectLabel("all projects", "", len(m.sessions)))
		m.appendProjects(&d)
		d.cursor = indexOf(d.options, m.projectFilter)
	}
	m.dropdown = d
}

// openMoveProject opens the project list as a move rather than a filter:
// choosing a label refiles this session. It is deliberately the same list the
// chip shows, down to the row that invents a project, so a session can be moved
// somewhere that does not exist yet without first going to the chip.
func (m *Model) openMoveProject(s *core.Session) {
	if s == nil {
		return
	}
	d := dropdown{open: true, area: focusProject, target: s.ID}
	// No project is where sessions start, so it is the first row rather than a
	// way of clearing one -- moving a card back out of a project is as ordinary
	// as moving it in.
	d.options = append(d.options, "")
	d.labels = append(d.labels, projectLabel("no project", "", m.projectSize("")))
	m.appendProjects(&d)
	d.cursor = indexOf(d.options, s.Group)
	m.dropdown = d
}

// appendProjects adds a row per known project and then the row that creates
// one. The labels come from config as well as from sessions, so a project added
// by hand is selectable before anything is running in it.
//
// Each row names the repo the project works in, because choosing a project now
// chooses a repo too -- a selector that silently changes another one has to say
// so before it is used, not after.
func (m Model) appendProjects(d *dropdown) {
	for _, g := range m.projects() {
		repo := m.cfg.ProjectRepo(g)
		if repo == "" {
			repo = "any repo"
		}
		d.options = append(d.options, g)
		d.labels = append(d.labels, projectLabel(g, repo, m.projectSize(g)))
	}
	d.options = append(d.options, projectNew)
	d.labels = append(d.labels, newProjectLabel)
}

func (m Model) projectSize(name string) int {
	n := 0
	for _, s := range m.sessions {
		if s.Group == name {
			n++
		}
	}
	return n
}

func projectLabel(name, repo string, n int) string {
	return fmt.Sprintf("%-16s %-16s %d session(s)", name, repo, n)
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
		hint := "  ↑↓ choose · enter select · esc cancel"
		if m.dropdown.area == focusProject {
			hint = "  ↑↓ choose · enter select · x remove · esc cancel"
		}
		out = append(out, st.Faint.Render(hint))
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

	// None of these confirm themselves: the selector the user just closed is
	// sitting above the board showing the choice they made.
	switch d.area {
	case focusAgent:
		m.agentChoice = choice
		m.cfg.DefaultProfile = choice
		_ = core.SaveConfig(m.cfg)
	case focusRepo:
		m.setActiveRepo(choice)
	case focusProject:
		if choice == projectNew {
			// The label has to be typed. The prompt carries the session the list
			// was opened for, so naming a project from a card both creates it and
			// moves the card into it.
			m.startPrompt(promptNewProject, "new project", "", d.target)
			m.mode = modePrompt
			return nil
		}
		if d.target != "" {
			m.setSessionProject(d.target, choice)
			return nil
		}
		m.selectProject(choice)
	}
	return nil
}

// selectProject aims the chip at a label, which both filters the board and is
// the project new sessions join. The empty label is the default: an unfiltered
// board, and new sessions in no project.
func (m *Model) selectProject(name string) {
	m.projectFilter = name
	// The repo comes along. Switching project is switching what you are working
	// on, and the repo that work happens in is part of that -- having to change
	// it separately is the step this binding exists to remove.
	//
	// A project with no repo leaves the chip where it is rather than clearing
	// it: the last repo used is a better guess than none at all.
	if repo := m.cfg.ProjectRepo(name); repo != "" {
		m.aimRepo(repo)
	}
	// A project the selected session is not in has just hidden its card, and the
	// panel goes with it.
	m.dropSelectionIfHidden()
}

// setActiveRepo aims new sessions at a repo, and takes it as the answer for the
// selected project too.
//
// A binding nothing can teach is a binding that goes stale: projects that
// predate the feature have none, and work does move between repos. Changing the
// repo while a project is selected is the plainest way to say where that
// project's work happens now, and it costs a keystroke that was already spent.
func (m *Model) setActiveRepo(id string) {
	m.aimRepo(id)
	if m.projectFilter == "" {
		return
	}
	if m.cfg.BindProject(m.projectFilter, id) {
		_ = core.SaveConfig(m.cfg)
	}
}

// aimRepo points the chip at a repo, taking an active repo filter along with
// it. The filter is set from the chip in the first place (f), so leaving it
// behind on a repo you have just switched away from empties the board and reads
// as sessions having disappeared.
func (m *Model) aimRepo(id string) {
	if m.repoFilter != "" && m.repoFilter == m.activeRepoID() {
		m.repoFilter = id
	}
	m.activeRepo = id
	// A filter that travelled has just refiltered the board, so the panel may be
	// showing work from the repo left behind.
	m.dropSelectionIfHidden()
}

// setSessionProject refiles one session, registering the project if it is new.
//
// A project first seen here is bound to the session's repo, which is the only
// evidence available about where its work happens.
func (m *Model) setSessionProject(id, name string) {
	s := core.FindByID(m.sessions, id)
	if s == nil {
		return
	}
	s.Group = name
	if m.cfg.AddProject(name, s.RepoID) {
		_ = core.SaveConfig(m.cfg)
	}
	m.save()
	m.rebuild()
}

// removeProject forgets the highlighted label and reopens the list without it.
//
// Manually added projects would otherwise be a one-way door: a label typed by
// mistake has no sessions to prune and so no way out of the picker.
func (m *Model) removeProject() tea.Cmd {
	d := m.dropdown
	if d.cursor < 0 || d.cursor >= len(d.options) {
		return nil
	}
	name := d.options[d.cursor]
	if name == "" || name == projectNew {
		return nil
	}
	// Sessions are never silently unfiled: emptying the project first is the
	// user's decision to make, one card at a time.
	if n := m.projectSize(name); n > 0 {
		return errStatus(fmt.Errorf("%s still holds %d session(s) — move them out first", name, n))
	}
	if m.cfg.RemoveProject(name) {
		_ = core.SaveConfig(m.cfg)
	}
	if m.projectFilter == name {
		m.selectProject("")
	}
	// Rebuilt rather than spliced, so the counts and the cursor come from the
	// same code path that opened it.
	if d.target != "" {
		m.openMoveProject(core.FindByID(m.sessions, d.target))
		return nil
	}
	m.openDropdown(focusProject)
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
		m.setActiveRepo(ids[wrap(indexOf(ids, m.activeRepoID())+dir, len(ids))])
	case focusProject:
		// Cycling walks the projects that exist; inventing one is a keystroke
		// with a text field behind it, so it stays in the open list.
		opts := append([]string{""}, m.projects()...)
		m.selectProject(opts[wrap(indexOf(opts, m.projectFilter)+dir, len(opts))])
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
