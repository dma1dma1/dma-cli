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
		return []hint{{"enter", "start agent"}, {"tab", "selectors"}, {"esc", "board"}}
	case focusAgent, focusRepo, focusProject:
		return []hint{{"←→", "change"}, {"enter", "open list"}, {"tab", "next"}, {"esc", "board"}}
	}

	switch m.mode {
	case modeDiff:
		return []hint{
			{"tab", "diff mode"}, {"j/k", "prev/next session"}, {"a", "attach"},
			{"s", "push+PR"}, {"o/y", "PR open/copy"}, {"m", "merge"}, {"x", "prune"},
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
		{"e", "expand"},
		{"d", "diff"},
		{"s", "push+PR"},
		// One entry for the pair: the bar is already long enough that a second
		// PR-shaped hint would push something else off the end.
		{"o/y", "PR open/copy"},
		{"m", "merge"},
		{"x", "prune"},
		{"r", "repos"},
		{"p", "project"},
	}
	if m.cfg.MultiRepo() {
		h = append(h, hint{"f", "repo filter"})
	}
	return append(h, hint{"?", "help"}, hint{"q", "quit"})
}

// helpText is the full keymap, shown on ?.
var helpText = [][3]string{
	{"Board", "", ""},
	{"", "h j k l", "move between cards and columns"},
	{"", "i / n", "focus the task input at the bottom"},
	{"", "t", "type to the selected agent in the panel"},
	{"", "tab", "cycle board → agent panel → input → agent → repo → project"},
	{"", "a", "attach to the selected session's terminal"},
	{"", "e", "expand the session panel to full screen"},
	{"", "enter / d", "review the diff"},
	{"", "H L", "move a card to the previous/next column"},
	{"", "G", "move the selected session to a project"},
	{"", "s", "commit and push the branch, open a PR"},
	{"", "o", "open the PR in your browser"},
	{"", "y", "copy the PR link to the clipboard"},
	{"", "m", "merge the PR"},
	{"", "x", "prune the worktree and branch"},
	{"", "D", "kill the agent, keep the worktree"},
	{"", "R", "refresh PR and session state now"},
	{"", "r", "repositories: switch, add, unregister"},
	{"", "p", "pick the project: filters the board, and new sessions join it"},
	{"", "f", "filter to the active repo, or clear"},
	{"", "?", "this help"},
	{"", "q", "quit — agents keep running"},

	{"Task input", "", ""},
	{"", "enter", "start an agent with the chosen agent/repo/project"},
	{"", "esc", "return to the board"},

	{"Selectors", "", ""},
	{"", "← →", "change the value in place"},
	{"", "enter", "open the full list"},
	{"", "p", "jump straight to the project selector"},

	{"Projects", "", ""},
	{"", "", "sessions start in no project; pick one and new sessions join it"},
	{"", "+ new project…", "the last row of any project list — type a name"},
	{"", "x", "remove the highlighted project (must hold no sessions)"},

	{"Diff", "", ""},
	{"", "tab", "toggle working tree / branch diff"},
	{"", "j k", "previous/next session"},
	{"", "esc", "back to the board"},

	{"Repositories", "", ""},
	{"", "enter", "use this repo for new sessions"},
	{"", "a", "add a repo by path (dependencies detected automatically)"},
	{"", "x", "unregister (the repo on disk is untouched)"},

	{"Session panel", "", ""},
	{"", "t / click", "aim the keyboard at the agent in the panel"},
	{"", "ctrl-q", "hand the keyboard back to the board"},
	{"", "", "every other key, Escape and ctrl-c included, goes to the agent"},
	{"", "", "for full mouse and redraw fidelity, attach with a instead"},

	{"Attached", "", ""},
	{"", "ctrl-q", "detach back to the board"},
	{"", "", "every other key, Escape included, goes to the agent"},
}
