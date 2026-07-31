package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// plainRender is what a rendered diff looks like once it carries the margin:
// line numbers down the side and a rule where each hunk header was. Both
// renderers produce this shape -- it is the format delta is pinned to -- so it
// stands in for either of them here. See package render.
const plainRender = `diff --git a/panel.go b/panel.go
--- a/panel.go
+++ b/panel.go
┄┄┄┄┄┄┄┄┄ func (m Model) chips() string {
 4 ⋮  4 │  	line3
   ⋮  5 │ +	added line
   ⋮  6 │ +	another added line
 5 ⋮  7 │  	line4
┄┄┄┄┄┄┄┄┄ func (m Model) chips() string {
20 ⋮ 22 │  	line19
21 ⋮    │ -	line20
22 ⋮ 23 │  	line21`

// panelDiff is the review view showing panel.go, padded out so the pane can
// actually be scrolled: a viewport clamps its offset to content taller than
// itself, and a twelve-line diff cannot reach its second change at all.
func panelDiff(t *testing.T) Model {
	t.Helper()
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("internal/ui/panel.go")
	m.setDiffContent(plainRender + strings.Repeat("\nfiller", 200))
	return m
}

// The margin is what says where the changes are. Nothing here searches the
// rendered text for anything.
func TestDocumentFindsTheChanges(t *testing.T) {
	m := panelDiff(t)
	hunks := m.diffHunks()
	if len(hunks) != 2 {
		t.Fatalf("found %d changes, want 2: %+v", len(hunks), hunks)
	}
	if got := hunks[0]; got.Start != 4 || got.End != 7 {
		t.Errorf("first change = lines %d-%d, want 4-7", got.Start, got.End)
	}
	// The second only deletes at its end, so it runs to the last line it prints.
	if got := hunks[1]; got.Start != 22 || got.End != 23 {
		t.Errorf("second change = lines %d-%d, want 22-23", got.Start, got.End)
	}
}

func TestHunkKeysScrollThePane(t *testing.T) {
	m := panelDiff(t)
	first, second := m.diffHunks()[0].FirstRow, m.diffHunks()[1].FirstRow

	next, _ := m.keyDiff(tea.KeyPressMsg{}, "}")
	m = next.(Model)
	if m.review.view.YOffset() != first {
		t.Errorf("} scrolled to row %d, want %d", m.review.view.YOffset(), first)
	}
	if m.review.treeFocus {
		t.Error("} left focus on the tree it just scrolled away from")
	}

	next, _ = m.keyDiff(tea.KeyPressMsg{}, "}")
	m = next.(Model)
	if m.review.view.YOffset() != second {
		t.Errorf("second } scrolled to row %d, want %d", m.review.view.YOffset(), second)
	}

	next, _ = m.keyDiff(tea.KeyPressMsg{}, "{")
	m = next.(Model)
	if m.review.view.YOffset() != first {
		t.Errorf("{ scrolled to row %d, want %d", m.review.view.YOffset(), first)
	}
}

// The header counts changes, and the count follows the scroll rather than the
// last jump.
func TestSubtitleCountsChanges(t *testing.T) {
	m := panelDiff(t)

	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "change 1 of 2") {
		t.Errorf("subtitle = %q, want change 1 of 2", got)
	}
	m.review.view.SetYOffset(m.diffHunks()[1].FirstRow)
	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "change 2 of 2") {
		t.Errorf("subtitle after scrolling = %q, want change 2 of 2", got)
	}

	// One change is not worth counting.
	m.review.doc.Hunks = m.review.doc.Hunks[:1]
	if got := m.diffSubtitle(m.selected()); strings.Contains(got, "change") {
		t.Errorf("subtitle = %q, want no count for a single change", got)
	}
}

// A pane holding nothing with a line number in it -- an empty diff, a binary
// file, a rendering the margin could not be read out of -- has nowhere to jump.
func TestHunkKeysDoNothingWithoutAMargin(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("README.md")
	m.setDiffContent("Binary files a/logo.png and b/logo.png differ")

	if got := m.diffHunks(); len(got) != 0 {
		t.Fatalf("found %d changes in content with no margin", len(got))
	}
	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "}")
	if cmd != nil {
		t.Error("} did something with nowhere to go")
	}
	after := next.(Model)
	if after.review.view.YOffset() != 0 {
		t.Error("} scrolled a pane with no changes in it")
	}
}

// t points the agent at the change on screen and hands over the keyboard,
// without sending anything.
func TestTellAgentAboutHunk(t *testing.T) {
	m := panelDiff(t)
	m.selected().TmuxAlive = true

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "t")
	m = next.(Model)
	if cmd == nil {
		t.Fatal("nothing was sent to the agent")
	}
	// Back on the board with the agent focused: what comes next is typing to it.
	if m.mode != modeBoard || m.focus != focusPreview {
		t.Errorf("mode %v focus %v, want the board with the agent focused", m.mode, m.focus)
	}
}

// The reference names the change the pane is scrolled to, in lines of the file
// as it stands now -- which is what the agent has to open.
func TestAgentReferenceFollowsTheScroll(t *testing.T) {
	m := panelDiff(t)
	path := "internal/ui/panel.go"

	if got := m.diffHunks()[m.review.doc.HunkAt(m.review.view.YOffset())].Ref(path); got != path+":4-7" {
		t.Errorf("reference at the top = %q, want %s:4-7", got, path)
	}
	m.review.view.SetYOffset(m.diffHunks()[1].FirstRow)
	if got := m.diffHunks()[m.review.doc.HunkAt(m.review.view.YOffset())].Ref(path); got != path+":22-23" {
		t.Errorf("reference after scrolling = %q, want %s:22-23", got, path)
	}
}

// A dead terminal has nowhere to type, and a directory row has no one file to
// point at.
func TestTellAgentRefusesWhenThereIsNothingToSay(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("internal/ui")
	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "t")
	if mm := next.(Model); mm.mode != modeDiff {
		t.Error("t on a directory row left the review view")
	}
	if cmd != nil {
		t.Error("t on a directory row sent something")
	}

	m.review.files.setCursorByPath("README.md")
	m.selected().TmuxAlive = false
	next, cmd = m.keyDiff(tea.KeyPressMsg{}, "t")
	if cmd == nil {
		t.Error("t with a dead terminal said nothing about it")
	}
	if mm := next.(Model); mm.focus == focusPreview {
		t.Error("t focused a terminal that is not running")
	}
}
