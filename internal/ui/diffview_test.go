package ui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// diffModel is a board with one session, opened on the review view and given a
// file list, without going near git.
func diffModel(t *testing.T, files ...gitx.ChangedFile) Model {
	t.Helper()
	s := sess("s1", "", core.LifecycleIdle, core.AgentIdle, "r")
	s.Title = "rate limiter"
	s.BaseBranch = "main"
	m := testModel(nil, s)
	m.selectSession(s)
	m.mode = modeDiff
	m.review.treeFocus = true
	m.layoutSizes()
	m.review.files.setFiles(files)
	return m
}

func someFiles() []gitx.ChangedFile {
	return []gitx.ChangedFile{
		{Path: "internal/ui/panel.go", Status: gitx.ChangeModified, Added: 42, Removed: 8},
		{Path: "internal/ui/box.go", Status: gitx.ChangeModified, Added: 3, Removed: 1},
		{Path: "internal/gitx/git.go", Status: gitx.ChangeModified, Added: 210, Removed: 12},
		{Path: "README.md", Status: gitx.ChangeModified, Added: 6, Removed: 6},
		{Path: "internal/ui/filetree.go", Status: gitx.ChangeUntracked, Untracked: true, Added: 300},
	}
}

// The panes have to add up to the frame's interior exactly, or the box's right
// edge lands in a different column on every row.
func TestDiffPanesFillTheFrame(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.view.SetContent("a diff line\nanother line")

	inner := m.contentWidth() - 4
	if got := m.diffTreeWidth() + diffDividerCols + m.diffPaneWidth(); got != inner {
		t.Errorf("panes measure %d cells, want %d", got, inner)
	}

	for i, line := range strings.Split(m.diffPanes(), "\n") {
		if got := lipglossWidth(line); got != inner {
			t.Errorf("pane row %d is %d cells wide, want %d", i, got, inner)
		}
	}
}

// A window too narrow for both panes gives the whole width to the diff, rather
// than leaving two unreadable columns.
func TestNarrowWindowHidesTree(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.width, m.height = 80, 30
	m.layoutSizes()

	if m.diffTreeWidth() != 0 {
		t.Errorf("tree drawn at %d columns wide in an 80-column window", m.diffTreeWidth())
	}
	if got := m.diffPaneWidth(); got != m.contentWidth()-4 {
		t.Errorf("diff pane = %d cells, want the whole interior %d", got, m.contentWidth()-4)
	}
}

// e hands the tree's columns to the diff, which changes how the diff has to be
// rendered -- so it must ask for the file again rather than stretching what it
// already has.
func TestHideTreeRerendersAtTheNewWidth(t *testing.T) {
	m := diffModel(t, someFiles()...)
	before := m.diffKey()

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "e")
	m = next.(Model)
	if !m.review.treeHidden {
		t.Fatal("e did not hide the tree")
	}
	if m.review.treeFocus {
		t.Error("focus left on a tree that is no longer drawn")
	}
	if m.diffKey() == before {
		t.Error("the cache key did not change with the pane width")
	}
	if cmd == nil {
		t.Error("no re-render asked for after the width changed")
	}
}

// j/k belong to whichever pane has focus.
func TestKeysRouteByFocus(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("")

	next, _ := m.keyDiff(tea.KeyPressMsg{}, "j")
	m = next.(Model)
	if m.review.files.cursor != 1 {
		t.Errorf("j with the tree focused moved the cursor to %d, want 1", m.review.files.cursor)
	}

	// With the diff focused the same key must reach the viewport instead.
	m.review.treeFocus = false
	cursorWas := m.review.files.cursor
	m.review.view.SetContent(strings.Repeat("line\n", 200))
	next, _ = m.keyDiff(tea.KeyPressMsg{Code: 'j', Text: "j"}, "j")
	m = next.(Model)
	if m.review.files.cursor != cursorWas {
		t.Errorf("j with the diff focused moved the tree cursor to %d", m.review.files.cursor)
	}
	if m.review.view.YOffset() == 0 {
		t.Error("j with the diff focused did not scroll the diff")
	}
}

func TestArrowKeysScrollWideDiffHorizontally(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.treeFocus = false
	m.review.view.SetContent(strings.Repeat("x", m.diffPaneWidth()+40))

	next, _ := m.keyDiff(tea.KeyPressMsg{Code: tea.KeyRight}, "right")
	m = next.(Model)
	if m.review.view.XOffset() == 0 {
		t.Fatal("right arrow did not scroll the diff")
	}

	next, _ = m.keyDiff(tea.KeyPressMsg{Code: tea.KeyLeft}, "left")
	m = next.(Model)
	if got := m.review.view.XOffset(); got != 0 {
		t.Errorf("left arrow left the diff at column %d, want 0", got)
	}
}

func TestHorizontalWheelScrollsWideDiff(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.height = 10
	m.layoutSizes()
	m.review.view.SetContent(strings.Repeat("x", m.diffPaneWidth()+40))
	rendered(t, m, zoneDiffTree)
	z := zone.Get(zoneDiffTree)
	if z.IsZero() {
		t.Fatal("no zone recorded for the diff tree")
	}
	x, y := (z.StartX+z.EndX)/2, (z.StartY+z.EndY)/2

	// Ordinary vertical scrolling still belongs to the tree under the pointer.
	next, _ := m.diffWheel(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	m = next.(Model)
	if m.review.files.offset == 0 {
		t.Fatal("vertical wheel did not scroll the tree")
	}
	if got := m.review.view.XOffset(); got != 0 {
		t.Fatalf("vertical tree scroll moved the diff to column %d", got)
	}

	// A horizontal gesture belongs to the only pane which can use it, even if
	// the pointer is parked over the tree.
	next, _ = m.diffWheel(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelRight})
	m = next.(Model)
	if m.review.view.XOffset() == 0 {
		t.Fatal("horizontal wheel did not scroll the diff")
	}

	next, _ = m.diffWheel(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelLeft})
	m = next.(Model)
	if got := m.review.view.XOffset(); got != 0 {
		t.Errorf("horizontal wheel left the diff at column %d, want 0", got)
	}

	// Some terminals encode horizontal scrolling as Shift+vertical wheel.
	next, _ = m.diffWheel(tea.MouseWheelMsg{
		X: x, Y: y, Button: tea.MouseWheelDown, Mod: tea.ModShift,
	})
	m = next.(Model)
	if m.review.view.XOffset() == 0 {
		t.Fatal("shift+wheel did not scroll the diff")
	}
}

func TestFocusKeysSwitchPanes(t *testing.T) {
	m := diffModel(t, someFiles()...)

	next, _ := m.keyDiff(tea.KeyPressMsg{}, "l")
	if m = next.(Model); m.review.treeFocus {
		t.Error("l left focus on the tree")
	}
	next, _ = m.keyDiff(tea.KeyPressMsg{}, "h")
	if m = next.(Model); !m.review.treeFocus {
		t.Error("h did not focus the tree")
	}

	// With no tree drawn there is nowhere for h to go.
	m.width = 80
	m.layoutSizes()
	m.review.treeFocus = false
	next, _ = m.keyDiff(tea.KeyPressMsg{}, "h")
	if m = next.(Model); m.review.treeFocus {
		t.Error("h focused a tree that is not drawn")
	}
}

func TestNextFileSkipsDirectoryRows(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("")

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "n")
	m = next.(Model)
	if row, _ := m.review.files.selected(); row.dir {
		t.Errorf("n stopped on the directory row %q", row.path)
	}
	if cmd == nil {
		t.Error("n did not ask for the file it moved to")
	}
}

// The cache is keyed by everything that changes the output, and a diff that
// arrives late is filed rather than drawn over the file now on screen.
func TestDiffCacheKeyAndLateArrival(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("README.md")
	key := m.diffKey()

	next, _ := m.update(diffMsg{id: "s1", key: key, content: "readme diff"})
	m = next.(Model)
	if !strings.Contains(m.review.view.View(), "readme diff") {
		t.Error("the diff for the current row was not shown")
	}

	// Move on, then let a render for the row we left arrive.
	m.review.files.setCursorByPath("internal/gitx/git.go")
	cmd := m.showSelectedFile()
	if cmd == nil {
		t.Fatal("an unrendered file was not asked for")
	}
	next, _ = m.update(diffMsg{id: "s1", key: key, content: "stale readme diff"})
	m = next.(Model)
	if strings.Contains(m.review.view.View(), "stale readme diff") {
		t.Error("a late render was drawn over the file the cursor had moved to")
	}
	if m.review.cache[key] != "stale readme diff" {
		t.Error("a late render was thrown away instead of cached")
	}

	// Going back to a file already rendered spends no git at all. Its structure
	// comes out of the same cached text, so there is nothing else left to ask
	// for.
	m.review.files.setCursorByPath("README.md")
	if cmd := m.showSelectedFile(); cmd != nil {
		t.Error("a cached file was rendered again")
	}
}

// Switching the range invalidates every rendered diff: they describe the range
// they were rendered for.
func TestModeSwitchDropsCache(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.cache = map[string]string{m.diffKey(): "working tree diff"}

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "tab")
	m = next.(Model)
	if m.review.mode != gitx.DiffBranch {
		t.Fatal("tab did not switch the range")
	}
	if len(m.review.cache) != 0 {
		t.Errorf("cache survived the range switch: %v", m.review.cache)
	}
	if cmd == nil {
		t.Error("no re-list after the range switch")
	}
}

// Two columns come from delta, so the toggle has to say so rather than flipping
// a flag that changes nothing.
func TestSideBySideNeedsDelta(t *testing.T) {
	m := diffModel(t, someFiles()...)
	before := m.diffKey()

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "v")
	m = next.(Model)

	if !gitx.HasDelta() {
		if m.review.sideBySide {
			t.Error("side-by-side turned on with no delta to render it")
		}
		if cmd == nil {
			t.Error("no explanation posted when the key could not do anything")
		}
		return
	}
	if !m.review.sideBySide {
		t.Fatal("v did not turn side-by-side on")
	}
	// The layout is part of how the diff was rendered, so it is part of the key.
	if m.diffKey() == before {
		t.Error("the cache key ignores the layout")
	}
	if m.diffOpts().SideBySide != true {
		t.Error("the render options do not carry the layout")
	}
	if cmd == nil {
		t.Error("no re-render asked for after the layout changed")
	}
}

// Session stepping moved off j/k, which the tree now owns.
func TestBracketsStepSessions(t *testing.T) {
	a := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	b := sess("b", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(nil, a, b)
	m.selectSession(a)
	m.mode = modeDiff
	m.layoutSizes()

	next, _ := m.keyDiff(tea.KeyPressMsg{}, "]")
	m = next.(Model)
	if m.selectedID == "a" {
		t.Error("] did not step to another session")
	}
	next, _ = m.keyDiff(tea.KeyPressMsg{}, "[")
	if m = next.(Model); m.selectedID != "a" {
		t.Errorf("[ landed on %q, want a", m.selectedID)
	}
}

// The file list arriving for a range nobody is looking at any more must not
// replace the tree.
func TestChangedFilesForOtherModeIgnored(t *testing.T) {
	m := diffModel(t, someFiles()...)
	rowsWas := len(m.review.files.rows)

	next, _ := m.update(changedFilesMsg{id: "s1", mode: gitx.DiffBranch, files: nil})
	m = next.(Model)
	if len(m.review.files.rows) != rowsWas {
		t.Errorf("tree replaced by a list for another range: %d rows, want %d",
			len(m.review.files.rows), rowsWas)
	}
}

func TestDiffSubtitleNamesRangeAndRow(t *testing.T) {
	m := diffModel(t, someFiles()...)
	s := m.selected()

	if got := m.diffSubtitle(s); !strings.Contains(got, "working tree") || !strings.Contains(got, rootLabel) {
		t.Errorf("subtitle on the root row = %q", got)
	}

	m.review.files.setCursorByPath("README.md")
	m.review.mode = gitx.DiffBranch
	m.review.sideBySide = true
	got := m.diffSubtitle(s)
	for _, want := range []string{"main...HEAD", "side by side", "README.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("subtitle = %q, missing %q", got, want)
		}
	}
}

// DMA_DIFF_DUMP=1 go test ./internal/ui -run DumpDiffView prints the view, which
// is the quickest way to look at the layout without a real repo behind it.
func TestDumpDiffView(t *testing.T) {
	if os.Getenv("DMA_DIFF_DUMP") == "" {
		t.Skip("set DMA_DIFF_DUMP=1 to print the view")
	}
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("internal/ui/panel.go")
	m.review.view.SetContent(plainRender)
	t.Log("\n" + zoneMarker.ReplaceAllString(m.viewDiff(), ""))
}
