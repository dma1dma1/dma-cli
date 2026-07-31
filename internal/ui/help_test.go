package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The help screen is a search box over the keymap. What the tests below hold
// down: what a query matches, what it must not, and the fact that typing into
// the screen can no longer dismiss it by accident.

// typeHelp runs a string of printable keys through the help screen, the way the
// key router would.
func typeHelp(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(Model)
	}
	return m
}

func descs(hits []helpHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.desc)
	}
	return out
}

func hasDesc(hits []helpHit, want string) bool {
	for _, h := range hits {
		if strings.Contains(h.desc, want) {
			return true
		}
	}
	return false
}

func TestEmptyQueryShowsTheWholeKeymap(t *testing.T) {
	if got, want := len(filterHelp("")), len(helpRows()); got != want {
		t.Errorf("empty query kept %d of %d rows", got, want)
	}
	if got, want := len(filterHelp("   ")), len(helpRows()); got != want {
		t.Errorf("whitespace query kept %d of %d rows", got, want)
	}
}

// The point of the feature: a word you remember finds the key you do not.
func TestSearchFindsRowsByDescription(t *testing.T) {
	hits := filterHelp("prune")
	if len(hits) == 0 {
		t.Fatal("no rows matched \"prune\"")
	}
	for _, h := range hits {
		joined := h.section + " " + h.keys + " " + h.desc
		if !strings.Contains(strings.ToLower(joined), "prun") {
			t.Errorf("row %q matched \"prune\" on nothing", joined)
		}
	}
}

// Fuzzy is the fallback, not the rule. A word the keymap actually contains is
// read literally, or "prune" comes back holding "previous/next column" -- which
// it does spell, p-r-...-u-n-e, and which is not what was asked for.
func TestALiteralWordIsNotReadFuzzily(t *testing.T) {
	hits := filterHelp("prune")
	if hasDesc(hits, "previous/next column") {
		t.Errorf("\"prune\" fuzzy-matched a row it shares no word with: %v", descs(hits))
	}
	if !hasDesc(hits, "prune the worktree and branch") {
		t.Errorf("\"prune\" lost the row it names: %v", descs(hits))
	}
}

// Fuzzy, not exact: the letters have to appear in order, not together.
func TestSearchIsASubsequenceMatch(t *testing.T) {
	if !hasDesc(filterHelp("sbs"), "side-by-side") {
		t.Errorf("\"sbs\" did not reach side-by-side: %v", descs(filterHelp("sbs")))
	}
	if hasDesc(filterHelp("zzq"), "side-by-side") {
		t.Error("\"zzq\" matched a row it shares no letters with")
	}
}

// Words are ANDed, so a second word narrows rather than widens.
func TestEachWordMustMatch(t *testing.T) {
	one := filterHelp("pr")
	two := filterHelp("pr browser")
	if len(two) >= len(one) {
		t.Errorf("adding a word kept %d rows, was %d", len(two), len(one))
	}
	if !hasDesc(two, "open the PR in your browser") {
		t.Errorf("\"pr browser\" lost the row it names: %v", descs(two))
	}
}

// A section name carries its whole block through: "diff" is how you ask for the
// review keys, and only two of them say "diff" in their own text.
func TestSectionNameMatchesItsRows(t *testing.T) {
	hits := filterHelp("diff")
	if !hasDesc(hits, "next/previous file, skipping directories") {
		t.Errorf("\"diff\" did not carry the Diff section: %v", descs(hits))
	}
}

// Search must not be case-sensitive: the keymap is lowercase and the query
// arrives however it was typed.
func TestSearchIgnoresCase(t *testing.T) {
	if len(filterHelp("MERGE")) != len(filterHelp("merge")) {
		t.Error("uppercase query matched a different set")
	}
}

// A contiguous hit is highlighted as the word it is, not as scattered letters:
// the first m of "merge the PR" starts a run of five.
func TestHighlightPrefersTheContiguousRun(t *testing.T) {
	idx, ok := fuzzyMatch("merge the PR, or queue it", "merge")
	if !ok {
		t.Fatal("no match")
	}
	want := []int{0, 1, 2, 3, 4}
	if len(idx) != len(want) {
		t.Fatalf("hit %v, want %v", idx, want)
	}
	for i := range want {
		if idx[i] != want[i] {
			t.Fatalf("hit %v, want %v", idx, want)
		}
	}
}

// Positions index runes, not bytes: the keymap has arrows and ellipses in it,
// and a byte offset would highlight the middle of one.
func TestHighlightPositionsAreRuneOffsets(t *testing.T) {
	idx, ok := fuzzyMatch("← → change", "change")
	if !ok {
		t.Fatal("no match")
	}
	if idx[0] != 4 {
		t.Errorf("first hit at rune %d, want 4", idx[0])
	}
	if got := highlight("← → change", idx, m0().styles.KeyDesc, m0().styles.Match); !strings.Contains(got, "change") {
		t.Errorf("highlight mangled the string: %q", got)
	}
}

func m0() Model { return testModel(nil) }

// Typing has to reach the query. Before the search this screen closed on any
// key, so every letter of a search would have dropped it back to the board.
func TestTypingFiltersInsteadOfClosingHelp(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp

	m = typeHelp(m, "q merge")

	if m.mode != modeHelp {
		t.Fatal("help closed while being typed into")
	}
	if m.helpQuery != "q merge" {
		t.Errorf("query = %q", m.helpQuery)
	}
	if body := m.viewHelp(); !strings.Contains(body, "queue") {
		t.Errorf("filtered view lost the matching row:\n%s", body)
	}
}

// Escape backs out one layer at a time, so a narrowed list is never a dead end
// you have to leave the screen to escape.
func TestEscapeClearsTheSearchBeforeClosing(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp
	m = typeHelp(m, "merge")

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(Model)
	if m.mode != modeHelp || m.helpQuery != "" {
		t.Fatalf("first esc: mode %v, query %q", m.mode, m.helpQuery)
	}

	next, _ = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m := next.(Model); m.mode != modeBoard {
		t.Errorf("second esc left mode %v", m.mode)
	}
}

func TestQuestionMarkTogglesHelpClosed(t *testing.T) {
	m := testModel(nil)
	next, _ := m.handleKey(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(Model)
	if m.mode != modeHelp {
		t.Fatal("? did not open help")
	}

	next, _ = m.handleKey(tea.KeyPressMsg{Code: '?', Text: "?"})
	if m := next.(Model); m.mode != modeBoard {
		t.Errorf("? did not close help, mode %v", m.mode)
	}
}

func TestBackspaceAndClearEditTheQuery(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp
	m = typeHelp(m, "merge")

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m = next.(Model)
	if m.helpQuery != "merg" {
		t.Errorf("after backspace query = %q", m.helpQuery)
	}

	next, _ = m.handleKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m := next.(Model); m.helpQuery != "" {
		t.Errorf("ctrl-u left %q", m.helpQuery)
	}
}

// ctrl-w drops the last word, which is how a second search term is retried.
func TestCtrlWDropsTheLastWord(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp
	m = typeHelp(m, "pr browser")

	next, _ := m.handleKey(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if m := next.(Model); m.helpQuery != "pr " {
		t.Errorf("ctrl-w left %q, want %q", m.helpQuery, "pr ")
	}
}

// A query that finds nothing still has to say what to do about it, and must not
// leave the screen looking broken.
func TestEmptyResultSaysSo(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp
	m = typeHelp(m, "zzzz")

	body := m.viewHelp()
	if !strings.Contains(body, "no keys match") {
		t.Errorf("empty result gave no explanation:\n%s", body)
	}
	if !strings.Contains(body, "search") {
		t.Error("search line disappeared with the results")
	}
}

// Reopening starts clean: a filter left over from last time is a keymap that
// looks like it has lost most of its keys.
func TestHelpReopensWithAnEmptyQuery(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp
	m = typeHelp(m, "merge")

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.handleKey(tea.KeyPressMsg{Code: '?', Text: "?"})
	m = next.(Model)

	if m.helpQuery != "" {
		t.Errorf("reopened with query %q", m.helpQuery)
	}
}

// ctrl-c quits from here as it does everywhere else; it used to merely close
// the screen, which is the one keystroke nobody means as "go back".
func TestCtrlCQuitsFromHelp(t *testing.T) {
	m := testModel(nil)
	m.mode = modeHelp
	next, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !next.(Model).quitting || cmd == nil {
		t.Error("ctrl-c did not quit")
	}
}
