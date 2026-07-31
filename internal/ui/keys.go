package ui

import "time"

// escalateAfter is how long a session may sit in needs_you before its badge
// escalates. Fifteen minutes of a blocked agent is real lost time.
const escalateAfter = 15 * time.Minute

// detachKey is the single reserved key wherever the agent has the keyboard --
// attached, or focused in the session panel. Every other keystroke, Escape
// included, belongs to the agent. ctrl-q is chosen because almost nothing binds
// it.
//
// The same key needs three spellings, and they must not be confused: this one is
// for display, detachTmuxKey is what tmux bind-key wants, and detachKeypress is
// what Bubble Tea reports when it is pressed.
const detachKey = "ctrl-q"

// detachKeypress is the Bubble Tea spelling of detachKey.
const detachKeypress = "ctrl+q"

// hint is one entry in the status bar.
type hint struct{ key, desc string }

// hints are chosen per focus: the status bar should describe the keys that will
// actually do something right now, not the union of every mode.
func (m Model) hints() []hint {
	if m.dropdown.open {
		if m.dropdown.area == focusProject {
			return []hint{{"↑↓", "choose"}, {"enter", "select"}, {"x", "remove"}, {"esc", "cancel"}}
		}
		return []hint{{"↑↓", "choose"}, {"enter", "select"}, {"esc", "cancel"}}
	}

	switch m.focus {
	case focusPreview:
		// Deliberately the shortest hint row in the UI: every key named here except
		// the first belongs to the agent, so there is nothing else of ours to
		// advertise.
		return []hint{{detachKey, "leave · every other key goes to the agent"}}
	case focusInput:
		return []hint{
			{"ctrl-v", "paste image/text"}, {"ctrl-u", "clear"},
			{"enter", "start agent"}, {"shift-enter", "newline"},
			{"tab", "selectors"}, {"esc", "board"},
		}
	case focusAgent, focusRepo, focusProject:
		return []hint{{"←→", "change"}, {"enter", "open list"}, {"tab", "next"}, {"esc", "board"}}
	}

	switch m.mode {
	case modeDiff:
		return []hint{
			{"j/k", "move"}, {"h/l", "tree/diff"}, {"n/p", "next/prev file"},
			{"}/{", "next/prev change"}, {"t", "point the agent here"},
			{"tab", "diff mode"}, {"v", "side by side"}, {"[/]", "prev/next session"},
			{"e", "hide tree"}, {"a", "attach"}, {"s", "ship"}, {"m", "merge"},
			{"esc", "board"},
		}
	case modeRepos:
		return []hint{
			{"j/k", "move"}, {"enter", "use for new sessions"},
			{"a", "add repo"}, {"x", "unregister"}, {"esc", "back"},
		}
	}

	h := []hint{
		{"hjkl", "move"},
		{"i", "new task"},
		{"t", "type to agent"},
		{"a", "attach"},
		{"d", "diff"},
		{"s", "ship"},
		// One entry for the pair: the bar is already long enough that a second
		// PR-shaped hint would push something else off the end.
		{"o/y", "PR open/copy"},
		{"m", "merge"},
		// The pair again: X is the merged column's bulk form of x, and the two
		// only make sense read together.
		{"x/X", "prune one/merged"},
		// One entry for the three selectors, in chip order: three separate hints
		// would cost more of the bar than the pair of PR keys already does.
		{"A/r/p", "agent/repo/project"},
	}
	return append(h, hint{"?", "help"}, hint{"q", "quit"})
}

// helpText is the full keymap, shown on ?.
var helpText = [][3]string{
	{"Board", "", ""},
	{"", "h j k l", "move between cards and columns"},
	{"", "i / n", "focus the task input at the bottom"},
	{"", "t", "type to the selected agent in the panel"},
	{"", "ctrl-v", "paste to the selected live agent without attaching"},
	{"", "tab", "cycle board → agent panel → input → agent → repo → project"},
	{"", "a", "attach to the selected session's terminal"},
	{"", "enter / d", "review the diff"},
	{"", "H L", "move a card to the previous/next column"},
	{"", "G", "move the selected session to a project"},
	{"", "s", "ask the agent to commit, push, and open a PR"},
	{"", "o", "open the PR in your browser"},
	{"", "y", "copy the PR link to the clipboard"},
	{"", "m", "merge the PR, or queue it where the base branch has a merge queue"},
	{"", "x", "prune the worktree and branch, closing the PR if it is open"},
	{"", "X", "prune every merged session on the board"},
	{"", "D", "kill the agent, keep the worktree"},
	{"", "R", "refresh PR and session state now"},
	{"", "A", "pick the agent new sessions start with"},
	{"", "r", "repositories: switch, add, unregister"},
	{"", "p", "pick the project: filters the board, and new sessions join it"},
	{"", "?", "this help"},
	{"", "q", "quit — agents keep running"},

	{"Task input", "", ""},
	{"", "ctrl-v", "add a clipboard image, or paste clipboard text"},
	{"", "ctrl-u", "clear the task, images included"},
	{"", "backspace", "at the start, remove the last image"},
	{"", "enter", "start an agent with the chosen agent/repo/project"},
	{"", "shift-enter", "newline — alt-enter or ctrl-j where the terminal eats it"},
	{"", "esc", "return to the board"},

	{"Selectors", "", ""},
	{"", "← →", "change the value in place"},
	{"", "enter", "open the full list"},
	{"", "A / p", "jump straight to the agent / project selector"},

	{"Projects", "", ""},
	{"", "", "sessions start in no project; pick one and new sessions join it"},
	{"", "+ new project…", "the last row of any project list — type a name"},
	{"", "x", "remove the highlighted project (must hold no sessions)"},

	{"Diff", "", ""},
	{"", "j k", "move in the focused pane: tree rows, or diff lines"},
	{"", "h l", "focus the file tree / the diff"},
	{"", "n p", "next/previous file, skipping directories"},
	{"", "} {", "next/previous change within the file"},
	{"", "t", "type \"look at <file>:<lines>\" to the agent, unsent"},
	{"", "enter", "open or close a directory; on a file, focus the diff"},
	{"", "e", "hide the file tree and give the diff the whole width"},
	{"", "tab", "toggle working tree / branch diff"},
	{"", "v", "toggle side-by-side columns (needs delta)"},
	{"", "[ ]", "previous/next session"},
	{"", "← →", "scroll a wide diff sideways"},
	{"", "esc / d / q", "back to the board"},

	{"Repositories", "", ""},
	{"", "enter", "use this repo for new sessions"},
	{"", "a", "add a repo by path (dependencies detected automatically)"},
	{"", "x", "unregister (the repo on disk is untouched)"},

	{"Session panel", "", ""},
	{"", "t / click", "aim the keyboard at the agent in the panel"},
	{"", "ctrl-q", "hand the keyboard back to the board (or click off the panel)"},
	{"", "wheel", "scroll the agent's history"},
	{"", "", "every other key, Escape and ctrl-c included, goes to the agent"},
	{"", "", "for full mouse and redraw fidelity, attach with a instead"},

	{"Attached", "", ""},
	{"", "ctrl-q", "detach back to the board"},
	{"", "", "every other key, Escape included, goes to the agent"},
	{"", "wheel", "scroll the agent's history (hold shift to select text)"},
}
