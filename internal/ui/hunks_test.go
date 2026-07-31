package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// twoHunks is the structure of a file with two separate changes in it.
func twoHunks() []gitx.Hunk {
	return []gitx.Hunk{
		{Start: 4, End: 11, Added: 2, Anchor: "\tadded line"},
		{Start: 22, End: 28, Removed: 1, Anchor: "\tline20"},
	}
}

// plainRender is what git's own output looks like: the @@ markers survive, so the
// rows can be counted exactly.
const plainRender = `diff --git a/panel.go b/panel.go
--- a/panel.go
+++ b/panel.go
@@ -4,6 +4,8 @@ func (m Model) chips() string {
 	line3
+	added line
+	another added line
 	line4
@@ -20,7 +22,6 @@ func (m Model) chips() string {
 	line19
-	line20
 	line21`

// deltaRender is what delta's default hunk header looks like: its own decoration,
// with no @@ anywhere. The changed lines are still the changed lines, which is
// what the fallback leans on. The escapes are deliberate -- rendered output is
// colored, and a search over it has to see through that.
const deltaRender = "\x1b[34mpanel.go\x1b[0m\n" +
	"\x1b[33m───────────────────\x1b[0m\n" +
	"\x1b[36m4\x1b[0m: func (m Model) chips() string {\n" +
	" \tline3\n" +
	"\x1b[32m+\tadded line\x1b[0m\n" +
	"\x1b[32m+\tanother added line\x1b[0m\n" +
	" \tline4\n" +
	"\x1b[36m22\x1b[0m: func (m Model) chips() string {\n" +
	" \tline19\n" +
	"\x1b[31m-\tline20\x1b[0m\n" +
	" \tline21"

// With the @@ markers present the mapping is exact: hunk N is the Nth marker.
func TestHunkRowsFromHeaders(t *testing.T) {
	rows := hunkRows(plainRender, twoHunks())
	if len(rows) != 2 {
		t.Fatalf("mapped %d hunks, want 2: %v", len(rows), rows)
	}
	lines := strings.Split(plainRender, "\n")
	for i, row := range rows {
		if !strings.HasPrefix(lines[row], "@@") {
			t.Errorf("hunk %d mapped to row %d, which is %q", i, row, lines[row])
		}
	}
	if rows[0] != 3 || rows[1] != 8 {
		t.Errorf("rows = %v, want [3 8] (the two @@ rows)", rows)
	}
}

// Delta replaces the header with its own, so the fallback finds each hunk by the
// first line it changed -- through the color escapes around it.
func TestHunkRowsFallBackToAnchors(t *testing.T) {
	rows := hunkRows(deltaRender, twoHunks())
	if len(rows) != 2 {
		t.Fatalf("mapped %d hunks, want 2: %v", len(rows), rows)
	}
	lines := strings.Split(deltaRender, "\n")
	if got := plain(lines[rows[0]]); !strings.Contains(got, "added line") {
		t.Errorf("first hunk mapped to %q", got)
	}
	if got := plain(lines[rows[1]]); !strings.Contains(got, "line20") {
		t.Errorf("second hunk mapped to %q", got)
	}
	// Order matters as much as position: a later hunk must never map above an
	// earlier one, or } would scroll backwards.
	if rows[1] <= rows[0] {
		t.Errorf("rows = %v, which run backwards", rows)
	}
}

// Delta expands tabs, so a Go line aligned with them is spelled one way in the
// patch and another on screen. Matching has to see through that or every hunk in
// a struct or a var block would go unfound.
func TestHunkRowsSeeThroughExpandedTabs(t *testing.T) {
	// The patch keeps the tabs gofmt wrote; the rendered row has delta's spaces.
	hunks := []gitx.Hunk{{Anchor: "\tPath\tstring"}}
	rendered := "panel.go\n────────\n1: type ChangedFile struct {\n    Path    string\n    Status  ChangeStatus"

	rows := hunkRows(rendered, hunks)
	if len(rows) != 1 || rows[0] != 3 {
		t.Fatalf("rows = %v, want [3]: the tab-aligned line was not found", rows)
	}
}

// Side-by-side splits a long line between two columns, so the whole anchor may be
// on no single row. Its opening still places the hunk.
func TestHunkRowsMatchAnchorPrefix(t *testing.T) {
	hunks := []gitx.Hunk{{Anchor: "\tif err := doSomethingWithAVeryLongCallHere(ctx, path); err != nil {"}}
	rendered := "file.go\n───────\n1: func run() {\n  if err := doSomethingWith… │   if err := doSomethingWith…"

	rows := hunkRows(rendered, hunks)
	if len(rows) != 1 || rows[0] != 3 {
		t.Fatalf("rows = %v, want [3]: a line cut in half by the columns was not found", rows)
	}
}

func TestAnchorPrefixCutsOnAWordBoundary(t *testing.T) {
	// Cut at a space, so the fragment is not half a token a renderer may have
	// hyphenated, wrapped, or highlighted differently.
	if got := anchorPrefix("if err := doSomethingWithAVeryLongCall(ctx); err != nil {"); got != "if err :=" {
		t.Errorf("anchorPrefix = %q, want %q", got, "if err :=")
	}
	// Nothing to cut on: better a hard cut than no prefix at all.
	long := strings.Repeat("x", 40)
	if got := anchorPrefix(long); len(got) != 24 {
		t.Errorf("anchorPrefix of an unbroken run = %d chars, want 24", len(got))
	}
	// Short anchors are already their own prefix.
	if got := anchorPrefix("short"); got != "short" {
		t.Errorf("anchorPrefix = %q, want it unchanged", got)
	}
}

// An anchor that appears twice must not send the second hunk back to the first
// one's row.
func TestHunkRowsDoNotRewind(t *testing.T) {
	content := "context\n+same line\nmiddle\n+same line\ntail"
	hunks := []gitx.Hunk{{Anchor: "same line"}, {Anchor: "same line"}}
	rows := hunkRows(content, hunks)
	if rows[0] != 1 || rows[1] != 3 {
		t.Errorf("rows = %v, want [1 3]", rows)
	}
}

func TestHunkRowsEmptyInputs(t *testing.T) {
	if rows := hunkRows("", twoHunks()); rows != nil {
		t.Errorf("rows from no content = %v, want none", rows)
	}
	if rows := hunkRows(plainRender, nil); rows != nil {
		t.Errorf("rows from no hunks = %v, want none", rows)
	}
}

func TestCurrentHunkFollowsScroll(t *testing.T) {
	rows := []int{3, 7, 40}
	cases := map[int]int{0: 0, 3: 0, 6: 0, 7: 1, 39: 1, 40: 2, 100: 2}
	for offset, want := range cases {
		if got := currentHunk(rows, offset); got != want {
			t.Errorf("at row %d the current hunk is %d, want %d", offset, got, want)
		}
	}
}

func TestNextHunkRow(t *testing.T) {
	rows := []int{3, 7, 40}

	if got, ok := nextHunkRow(rows, 0, 1); !ok || got != 3 {
		t.Errorf("next from the top = %d (%v), want 3", got, ok)
	}
	if got, ok := nextHunkRow(rows, 3, 1); !ok || got != 7 {
		t.Errorf("next from the first change = %d (%v), want 7", got, ok)
	}
	if _, ok := nextHunkRow(rows, 40, 1); ok {
		t.Error("next from the last change went somewhere")
	}
	if got, ok := nextHunkRow(rows, 40, -1); !ok || got != 7 {
		t.Errorf("previous from the last change = %d (%v), want 7", got, ok)
	}
	if _, ok := nextHunkRow(rows, 3, -1); ok {
		t.Error("previous from the first change went somewhere")
	}
	if _, ok := nextHunkRow(nil, 0, 1); ok {
		t.Error("a file with no changes has somewhere to jump to")
	}
}

// } and { scroll the pane to the next and previous change.
func TestHunkKeysScrollThePane(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.diffFiles.setCursorByPath("internal/ui/panel.go")
	m.setDiffContent(plainRender + strings.Repeat("\nfiller", 200))
	m.diffHunks = twoHunks()
	m.syncHunkRows()

	next, _ := m.keyDiff(tea.KeyPressMsg{}, "}")
	m = next.(Model)
	if m.diffView.YOffset() != m.diffHunkRows[0] {
		t.Errorf("} scrolled to row %d, want %d", m.diffView.YOffset(), m.diffHunkRows[0])
	}
	if m.diffTreeFocus {
		t.Error("} left focus on the tree it just scrolled away from")
	}

	next, _ = m.keyDiff(tea.KeyPressMsg{}, "}")
	m = next.(Model)
	if m.diffView.YOffset() != m.diffHunkRows[1] {
		t.Errorf("second } scrolled to row %d, want %d", m.diffView.YOffset(), m.diffHunkRows[1])
	}

	next, _ = m.keyDiff(tea.KeyPressMsg{}, "{")
	m = next.(Model)
	if m.diffView.YOffset() != m.diffHunkRows[0] {
		t.Errorf("{ scrolled to row %d, want %d", m.diffView.YOffset(), m.diffHunkRows[0])
	}
}

// The header counts changes, and the count follows the scroll rather than the
// last jump.
func TestSubtitleCountsChanges(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.diffFiles.setCursorByPath("internal/ui/panel.go")
	// Padded out: a viewport clamps its offset to content taller than the pane,
	// so a nine-line diff cannot be scrolled to its second change at all.
	m.setDiffContent(plainRender + strings.Repeat("\nfiller", 200))
	m.diffHunks = twoHunks()
	m.syncHunkRows()

	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "change 1 of 2") {
		t.Errorf("subtitle = %q, want change 1 of 2", got)
	}
	m.diffView.SetYOffset(m.diffHunkRows[1])
	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "change 2 of 2") {
		t.Errorf("subtitle after scrolling = %q, want change 2 of 2", got)
	}

	// One change is not worth counting.
	m.diffHunks = m.diffHunks[:1]
	if got := m.diffSubtitle(m.selected()); strings.Contains(got, "change") {
		t.Errorf("subtitle = %q, want no count for a single change", got)
	}
}

// A directory has no hunks of its own: jumping between changes in a list of
// unrelated files is not a thing to want.
func TestDirectoryRowHasNoHunks(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.diffHunks = twoHunks()
	m.diffFiles.setCursorByPath("internal/ui")
	m.showSelectedFile()
	if m.diffHunks != nil {
		t.Errorf("directory row kept %d hunks", len(m.diffHunks))
	}
}

// The hunks of a file the cursor has left are filed, not drawn.
func TestLateHunksForAnotherFileIgnored(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.diffFiles.setCursorByPath("README.md")
	staleKey := m.hunkKey()
	m.diffFiles.setCursorByPath("internal/ui/panel.go")

	next, _ := m.update(hunksMsg{id: "s1", key: staleKey, hunks: twoHunks()})
	m = next.(Model)
	if len(m.diffHunks) != 0 {
		t.Error("hunks for another file were adopted by the file on screen")
	}
	if len(m.diffHunkCache[staleKey]) != 2 {
		t.Error("hunks for another file were thrown away instead of cached")
	}
}

// t points the agent at the change on screen and hands over the keyboard,
// without sending anything.
func TestTellAgentAboutHunk(t *testing.T) {
	m := diffModel(t, someFiles()...)
	s := m.selected()
	s.TmuxAlive = true
	m.diffFiles.setCursorByPath("internal/ui/panel.go")
	m.setDiffContent(plainRender)
	m.diffHunks = twoHunks()
	m.syncHunkRows()

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "t")
	m = next.(Model)
	if cmd == nil {
		t.Fatal("nothing was sent to the agent")
	}
	// Back on the board with the agent focused: what comes next is typing to it.
	if m.mode != modeBoard || m.focus != focusPreview {
		t.Errorf("mode %v focus %v, want the board with the agent focused", m.mode, m.focus)
	}

	// The reference names the hunk the pane is scrolled to.
	if got := m.diffHunks[currentHunk(m.diffHunkRows, m.diffView.YOffset())].
		Ref("internal/ui/panel.go"); got != "internal/ui/panel.go:4-11" {
		t.Errorf("reference = %q, want internal/ui/panel.go:4-11", got)
	}
}

// A dead terminal has nowhere to type, and a directory row has no lines to point
// at.
func TestTellAgentRefusesWhenThereIsNothingToSay(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.diffFiles.setCursorByPath("internal/ui")
	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "t")
	if mm := next.(Model); mm.mode != modeDiff {
		t.Error("t on a directory row left the review view")
	}
	if cmd != nil {
		t.Error("t on a directory row sent something")
	}

	m.diffFiles.setCursorByPath("README.md")
	m.selected().TmuxAlive = false
	next, cmd = m.keyDiff(tea.KeyPressMsg{}, "t")
	if cmd == nil {
		t.Error("t with a dead terminal said nothing about it")
	}
	if mm := next.(Model); mm.focus == focusPreview {
		t.Error("t focused a terminal that is not running")
	}
}
