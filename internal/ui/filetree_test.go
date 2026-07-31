package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// changed is the shorthand these tests build diffs out of.
func changed(path string, added, removed int) gitx.ChangedFile {
	return gitx.ChangedFile{Path: path, Status: gitx.ChangeModified, Added: added, Removed: removed}
}

// treeOf builds the tree a set of changed files produces.
func treeOf(files ...gitx.ChangedFile) *fileTree {
	t := &fileTree{}
	t.setFiles(files)
	return t
}

// labels is the drawn shape of the tree: indent depth and label per row, which
// is what the assertions are about.
func labels(t *fileTree) []string {
	out := make([]string, 0, len(t.rows))
	for _, row := range t.rows {
		out = append(out, strings.Repeat("  ", row.depth)+row.label)
	}
	return out
}

func wantLabels(t *testing.T, tree *fileTree, want ...string) {
	t.Helper()
	got := labels(tree)
	if len(got) != len(want) {
		t.Fatalf("tree has %d rows, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %q, want %q\nfull tree:\n%s", i, got[i], want[i], strings.Join(got, "\n"))
		}
	}
}

// A directory holding nothing but one directory costs one row, not two: the
// whole point of collapsing is that internal/ui/panel.go does not spend three
// rows on two directories that fork nowhere.
func TestBuildTreeCollapsesChains(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/panel.go", 10, 2),
		changed("internal/ui/box.go", 1, 0),
		changed("cmd/dma/main.go", 3, 1),
		changed("README.md", 5, 5),
	)
	// Directories first, each alphabetical, files after them.
	wantLabels(t, tree,
		rootLabel,
		"  cmd/dma",
		"    main.go",
		"  internal/ui",
		"    box.go",
		"    panel.go",
		"  README.md",
	)
}

// A directory with two children is a real fork and has to stay a row of its own,
// otherwise the two subtrees would read as siblings.
func TestBuildTreeKeepsForks(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/panel.go", 1, 0),
		changed("internal/ops/session.go", 2, 0),
	)
	wantLabels(t, tree,
		rootLabel,
		"  internal",
		"    ops",
		"      session.go",
		"    ui",
		"      panel.go",
	)
}

func TestTreeCountsSumSubtrees(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/panel.go", 10, 2),
		changed("internal/ui/box.go", 5, 3),
	)
	if root := tree.rows[0]; root.added != 15 || root.removed != 5 {
		t.Errorf("root = +%d −%d, want +15 −5", root.added, root.removed)
	}
	if dir := tree.rows[1]; dir.added != 15 || dir.removed != 5 {
		t.Errorf("internal/ui = +%d −%d, want +15 −5", dir.added, dir.removed)
	}
}

// Closing a directory hides its files and leaves the cursor on the directory
// itself, so the same key reopens what it just closed.
func TestToggleClosesAndReopens(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/panel.go", 1, 0),
		changed("internal/ui/box.go", 1, 0),
		changed("README.md", 1, 0),
	)
	tree.cursor = 1 // internal/ui
	if !tree.toggle() {
		t.Fatal("toggle on a directory reported no directory")
	}
	wantLabels(t, tree, rootLabel, "  internal/ui", "  README.md")
	if got := tree.selectedPath(); got != "internal/ui" {
		t.Errorf("cursor moved to %q, want internal/ui", got)
	}

	tree.toggle()
	wantLabels(t, tree, rootLabel, "  internal/ui", "    box.go", "    panel.go", "  README.md")
	if got := tree.selectedPath(); got != "internal/ui" {
		t.Errorf("cursor moved to %q on reopen, want internal/ui", got)
	}
}

func TestToggleOnFileReportsFalse(t *testing.T) {
	tree := treeOf(changed("README.md", 1, 0))
	tree.cursor = 1
	if tree.toggle() {
		t.Error("toggle on a file row reported a directory")
	}
}

func TestMoveFileSkipsDirectories(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/panel.go", 1, 0),
		changed("cmd/dma/main.go", 1, 0),
	)
	// From the root row, the next *file* is main.go -- the two directory rows in
	// between are not somewhere n should stop.
	if !tree.moveFile(1) {
		t.Fatal("no next file from the root row")
	}
	if got := tree.selectedPath(); got != "cmd/dma/main.go" {
		t.Errorf("next file = %q, want cmd/dma/main.go", got)
	}
	if !tree.moveFile(1) {
		t.Fatal("no next file from main.go")
	}
	if got := tree.selectedPath(); got != "internal/ui/panel.go" {
		t.Errorf("next file = %q, want internal/ui/panel.go", got)
	}
	// Past the last file it reports that it did not move, so the key can be a
	// no-op rather than wrapping around to the top.
	if tree.moveFile(1) {
		t.Error("moveFile went past the last file")
	}
}

func TestMoveClampsRatherThanWraps(t *testing.T) {
	tree := treeOf(changed("a.go", 1, 0))
	tree.move(-5)
	if tree.cursor != 0 {
		t.Errorf("cursor = %d after moving up from the top, want 0", tree.cursor)
	}
	tree.move(99)
	if tree.cursor != len(tree.rows)-1 {
		t.Errorf("cursor = %d after moving past the end, want %d", tree.cursor, len(tree.rows)-1)
	}
}

// A refresh re-lists the files. The cursor has to stay on the file it was on,
// and the directories the user closed have to stay closed.
func TestSetFilesKeepsCursorAndCollapse(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/panel.go", 1, 0),
		changed("internal/ui/box.go", 1, 0),
	)
	tree.cursor = 1
	tree.toggle() // close internal/ui
	tree.setFiles([]gitx.ChangedFile{
		changed("internal/ui/panel.go", 4, 1),
		changed("internal/ui/box.go", 1, 0),
		changed("new.go", 1, 0),
	})
	if got := tree.selectedPath(); got != "internal/ui" {
		t.Errorf("cursor = %q after refresh, want internal/ui", got)
	}
	if !tree.collapsed["internal/ui"] {
		t.Error("refresh sprang a closed directory back open")
	}
}

// A file that disappeared between refreshes must not strand the cursor off the
// end of a shorter tree.
func TestSetFilesSurvivesVanishedPath(t *testing.T) {
	tree := treeOf(changed("a.go", 1, 0), changed("b.go", 1, 0), changed("c.go", 1, 0))
	tree.cursor = 3 // c.go
	tree.setFiles([]gitx.ChangedFile{changed("a.go", 1, 0)})
	if tree.cursor >= len(tree.rows) {
		t.Fatalf("cursor %d is past the end of a %d-row tree", tree.cursor, len(tree.rows))
	}
	if _, ok := tree.selected(); !ok {
		t.Error("no row under the cursor after a shrinking refresh")
	}
}

func TestTargetByRow(t *testing.T) {
	untracked := gitx.ChangedFile{Path: "new.go", Status: gitx.ChangeUntracked, Untracked: true, Added: 3}
	tree := treeOf(changed("internal/ui/panel.go", 1, 0), untracked)

	// The root row renders the whole diff, which is the zero target.
	if got := tree.target(); got.Path != "" || got.Untracked {
		t.Errorf("root target = %+v, want the whole diff", got)
	}

	tree.setCursorByPath("internal/ui")
	if got := tree.target(); got.Path != "internal/ui" || got.Untracked {
		t.Errorf("directory target = %+v, want the internal/ui subtree", got)
	}

	tree.setCursorByPath("new.go")
	if got := tree.target(); got.Path != "new.go" || !got.Untracked {
		t.Errorf("untracked target = %+v, want new.go marked untracked", got)
	}
}

// Rows sit beside a diff pane, so a row that is one cell too wide would push the
// frame out of line for the whole view.
func TestRenderRowsAreExactlyPaneWidth(t *testing.T) {
	tree := treeOf(
		changed("internal/ui/a-very-long-file-name-that-will-not-fit.go", 1200, 340),
		gitx.ChangedFile{Path: "img.png", Status: gitx.ChangeAdded, Binary: true},
		changed("a.go", 0, 0),
	)
	const width = 30
	out := tree.render(newStyles(), width, 10, true)
	for i, line := range strings.Split(out, "\n") {
		if got := lipglossWidth(line); got != width {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, got, width, line)
		}
	}
}

func TestRenderMarksCursorAndCounts(t *testing.T) {
	tree := treeOf(changed("a.go", 12, 3))
	tree.setCursorByPath("a.go")
	out := plain(tree.render(newStyles(), 40, 10, true))
	line := strings.Split(out, "\n")[1]
	if !strings.Contains(line, "▸") {
		t.Errorf("cursor row has no caret: %q", line)
	}
	if !strings.Contains(line, "+12 −3") {
		t.Errorf("cursor row missing its counts: %q", line)
	}
	if !strings.Contains(line, "M") {
		t.Errorf("cursor row missing its status letter: %q", line)
	}
}

func TestRenderEmptyTree(t *testing.T) {
	tree := &fileTree{}
	tree.setFiles(nil)
	// The root row stands alone, so there is always something to sit on.
	if len(tree.rows) != 1 {
		t.Fatalf("empty diff produced %d rows, want 1", len(tree.rows))
	}
	if out := plain(tree.render(newStyles(), 30, 5, true)); !strings.Contains(out, rootLabel) {
		t.Errorf("empty tree rendered %q", out)
	}
}

// The offset follows the cursor by the least it can, so stepping down a long
// tree advances a row at a time instead of re-centering.
func TestSyncScrollFollowsCursorMinimally(t *testing.T) {
	files := make([]gitx.ChangedFile, 0, 40)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		files = append(files, changed(name+".go", 1, 0))
	}
	tree := &fileTree{}
	tree.setFiles(files)

	const height = 4
	tree.cursor = 6
	tree.syncScroll(height)
	if tree.offset != 3 {
		t.Errorf("offset = %d, want 3 (the least that shows row 6)", tree.offset)
	}

	tree.cursor = 2
	tree.syncScroll(height)
	if tree.offset != 2 {
		t.Errorf("offset = %d after moving back up, want 2", tree.offset)
	}
}

func TestScrollWithoutCursor(t *testing.T) {
	files := make([]gitx.ChangedFile, 0, 10)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		files = append(files, changed(name+".go", 1, 0))
	}
	tree := &fileTree{}
	tree.setFiles(files)

	tree.scroll(3, 4)
	if tree.offset != 3 {
		t.Errorf("offset = %d after a 3-row wheel, want 3", tree.offset)
	}
	if tree.cursor != 0 {
		t.Errorf("the wheel moved the cursor to %d, want 0", tree.cursor)
	}
	// It cannot scroll past the last screenful.
	tree.scroll(99, 4)
	if tree.offset != len(tree.rows)-4 {
		t.Errorf("offset = %d, want %d", tree.offset, len(tree.rows)-4)
	}
}

func TestTruncateLeft(t *testing.T) {
	cases := []struct{ in, want string }{
		{"panel.go", "panel.go"},
		{"a-long-file-name.go", "…ile-name.go"},
	}
	for _, c := range cases {
		if got := truncateLeft(c.in, 12); got != c.want {
			t.Errorf("truncateLeft(%q, 12) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := truncateLeft("panel.go", 0); got != "" {
		t.Errorf("truncateLeft to no room = %q, want empty", got)
	}
}
