package ui

import "time"

// escalateAfter is how long a session may sit in needs_you before its badge
// escalates. Fifteen minutes of a blocked agent is real lost time.
const escalateAfter = 15 * time.Minute

// detachKey is the single reserved key in attached mode. Every other keystroke,
// Escape included, belongs to the agent. ctrl-q is chosen because almost
// nothing binds it.
const detachKey = "ctrl+q"

// hint is one entry in the bottom bar.
type hint struct{ key, desc string }

func boardHints(multiRepo bool) []hint {
	h := []hint{
		{"hjkl", "move"},
		{"enter", "open"},
		{"n", "new"},
		{"H/L", "column"},
		{"g", "collapse"},
		{"s", "push+PR"},
		{"m", "merge"},
		{"x", "prune"},
		{"R", "refresh"},
		{"r", "repos"},
	}
	if multiRepo {
		h = append(h, hint{"f", "filter"})
	}
	return append(h, hint{"?", "help"}, hint{"q", "quit"})
}

func detailHints() []hint {
	return []hint{
		{"a", "attach"},
		{"tab", "diff mode"},
		{"j/k", "prev/next"},
		{"d", "diff"},
		{"s", "push+PR"},
		{"m", "merge"},
		{"x", "prune"},
		{"esc", "board"},
	}
}

// helpText is the full keymap, shown on ?.
var helpText = [][3]string{
	{"Board", "", ""},
	{"", "h j k l", "move selection"},
	{"", "enter", "open session detail"},
	{"", "n", "new session (compose in bottom bar)"},
	{"", "H L", "move selected card to previous/next column"},
	{"", "g", "collapse/expand current group"},
	{"", "G", "change selected session's group"},
	{"", "d", "jump to diff for selected session"},
	{"", "s", "commit and push branch, open PR"},
	{"", "m", "merge PR"},
	{"", "x", "prune worktree and branch"},
	{"", "D", "kill session (worktree kept)"},
	{"", "R", "force PR refresh"},
	{"", "r", "repositories: switch, add, unregister"},
	{"", "f", "filter board to one repo, or clear"},
	{"", "?", "help"},
	{"", "q", "quit (sessions keep running)"},
	{"Session detail", "", ""},
	{"", "a", "attach (level 3)"},
	{"", "esc", "back to board"},
	{"", "j k", "previous/next session"},
	{"", "tab", "toggle uncommitted diff / branch diff"},
	{"", "d s m x", "same as board"},
	{"Repositories", "", ""},
	{"", "j k", "move"},
	{"", "enter", "use this repo for new sessions"},
	{"", "a", "add a repo by path (dependencies detected automatically)"},
	{"", "x", "unregister (the repo on disk is untouched)"},
	{"Attached", "", ""},
	{"", "ctrl-q", "detach back to session detail"},
	{"", "", "all other keys pass through to the agent"},
}
