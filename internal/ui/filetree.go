package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// fileTree is the changed-file list the diff view navigates.
//
// A diff arrives as one long stream of text, which is the wrong shape for the
// question being asked of it -- "what did this agent touch, and which part do I
// want to read". The tree turns the same information into rows that can be
// picked, so the diff pane only ever has to render one file.
type fileTree struct {
	files []gitx.ChangedFile
	// rows is the flattened, currently visible tree. It is rebuilt whenever the
	// files or the collapsed set change, so drawing never has to walk nodes.
	rows   []treeRow
	cursor int
	// collapsed holds the directories the user has closed, keyed by their full
	// prefix. Kept outside rows so a refresh that re-lists the files does not
	// spring every directory back open.
	collapsed map[string]bool
	// offset is the first row drawn, for a tree holding more rows than fit.
	offset int
}

// treeRow is one drawn line.
type treeRow struct {
	// path is a file's full path, or a directory's prefix. The root row's path
	// is empty, which is also what targets the whole diff.
	path string
	// label is what the row shows: a file's name, or the directory chain a
	// collapse merged into one row (internal/ui).
	label string
	depth int
	dir   bool
	// file is the change this row stands for, zero for directories.
	file gitx.ChangedFile
	// added and removed sum the subtree for a directory, so a closed directory
	// still says how much moved inside it.
	added   int
	removed int
}

// rootLabel names the row that stands for every change at once. It is always
// present: a session with two hundred changed files still needs somewhere to
// see the whole diff, and a session with none needs a row to sit on.
const rootLabel = "all changes"

// setFiles rebuilds the tree, keeping the cursor on the path it was on. The
// files list is refreshed on every diff-mode switch and on demand, and a cursor
// that jumped to an unrelated file each time would make the view unusable.
func (t *fileTree) setFiles(files []gitx.ChangedFile) {
	was := t.selectedPath()
	t.files = files
	t.rebuild()
	t.setCursorByPath(was)
}

func (t *fileTree) rebuild() {
	if t.collapsed == nil {
		t.collapsed = map[string]bool{}
	}
	t.rows = flattenTree(buildTree(t.files), t.collapsed)
	t.cursor = clamp(t.cursor, 0, max(len(t.rows)-1, 0))
}

// treeNode is the nested form the rows are flattened from.
type treeNode struct {
	name     string
	path     string
	dir      bool
	file     gitx.ChangedFile
	children []*treeNode
	added    int
	removed  int
}

// buildTree nests the changed files by directory, then collapses and sorts.
func buildTree(files []gitx.ChangedFile) *treeNode {
	root := &treeNode{dir: true}
	for _, f := range files {
		// git always reports forward slashes, whatever the host separator is, so
		// the split is on "/" rather than on filepath.Separator.
		parts := strings.Split(f.Path, "/")
		node := root
		for i, part := range parts {
			if i == len(parts)-1 {
				node.children = append(node.children, &treeNode{
					name: part, path: f.Path, file: f,
					added: f.Added, removed: f.Removed,
				})
				break
			}
			node = childDir(node, part)
		}
	}
	collapseChains(root)
	sortTree(root)
	sumTree(root)
	return root
}

// childDir finds or creates the directory child named name.
func childDir(parent *treeNode, name string) *treeNode {
	for _, c := range parent.children {
		if c.dir && c.name == name {
			return c
		}
	}
	path := name
	if parent.path != "" {
		path = parent.path + "/" + name
	}
	child := &treeNode{name: name, path: path, dir: true}
	parent.children = append(parent.children, child)
	return child
}

// collapseChains merges a directory that holds nothing but one directory into
// its child, so internal/ui/panel.go costs one row rather than three. A
// directory with two children is a real fork in the tree and stays.
func collapseChains(node *treeNode) {
	for _, c := range node.children {
		collapseChains(c)
	}
	if !node.dir || node.path == "" {
		return
	}
	for len(node.children) == 1 && node.children[0].dir {
		only := node.children[0]
		node.name += "/" + only.name
		node.path = only.path
		node.children = only.children
	}
}

// sortTree orders directories before files, each alphabetically, so a refresh
// draws the same tree twice and the eye can find a path by where it sat.
func sortTree(node *treeNode) {
	sort.SliceStable(node.children, func(i, j int) bool {
		a, b := node.children[i], node.children[j]
		if a.dir != b.dir {
			return a.dir
		}
		return a.name < b.name
	})
	for _, c := range node.children {
		sortTree(c)
	}
}

// sumTree totals each directory's subtree.
func sumTree(node *treeNode) (added, removed int) {
	if !node.dir {
		return node.added, node.removed
	}
	for _, c := range node.children {
		a, r := sumTree(c)
		added += a
		removed += r
	}
	node.added, node.removed = added, removed
	return added, removed
}

// flattenTree walks the nodes into drawable rows, stopping at closed
// directories. The root row comes first and is never hidden.
func flattenTree(root *treeNode, collapsed map[string]bool) []treeRow {
	rows := []treeRow{{label: rootLabel, dir: true, added: root.added, removed: root.removed}}
	if collapsed[""] {
		return rows
	}
	var walk func(nodes []*treeNode, depth int)
	walk = func(nodes []*treeNode, depth int) {
		for _, n := range nodes {
			rows = append(rows, treeRow{
				path: n.path, label: n.name, depth: depth, dir: n.dir,
				file: n.file, added: n.added, removed: n.removed,
			})
			if n.dir && !collapsed[n.path] {
				walk(n.children, depth+1)
			}
		}
	}
	walk(root.children, 1)
	return rows
}

// --- cursor ---

func (t fileTree) selected() (treeRow, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return treeRow{}, false
	}
	return t.rows[t.cursor], true
}

// selectedPath is the path the cursor is on, empty on the root row.
func (t fileTree) selectedPath() string {
	row, ok := t.selected()
	if !ok {
		return ""
	}
	return row.path
}

// target is what the diff pane should render for the current row: a file, a
// whole subtree, or everything.
func (t fileTree) target() gitx.DiffTarget {
	row, ok := t.selected()
	if !ok {
		return gitx.DiffTarget{}
	}
	return gitx.DiffTarget{Path: row.path, Untracked: !row.dir && row.file.Untracked}
}

// move steps the cursor, clamped rather than wrapped: a tree is a list you read
// downward, and wrapping from the last file back to the root reads as a jump.
func (t *fileTree) move(delta int) {
	t.cursor = clamp(t.cursor+delta, 0, max(len(t.rows)-1, 0))
}

// moveFile steps to the next or previous file row, skipping directories. It
// reports whether it moved, so a key can fall through when there is nowhere to
// go.
func (t *fileTree) moveFile(delta int) bool {
	for i := t.cursor + delta; i >= 0 && i < len(t.rows); i += delta {
		if !t.rows[i].dir {
			t.cursor = i
			return true
		}
	}
	return false
}

// toggle opens or closes the directory under the cursor. It reports whether the
// row was a directory, so the caller can treat a file row as "show me this".
func (t *fileTree) toggle() bool {
	row, ok := t.selected()
	if !ok || !row.dir {
		return false
	}
	if t.collapsed == nil {
		t.collapsed = map[string]bool{}
	}
	t.collapsed[row.path] = !t.collapsed[row.path]
	// Rebuilding can leave the cursor past the end of a shortened tree, and the
	// row it was on may now be hidden, so put it back on the directory itself.
	t.rebuild()
	t.setCursorByPath(row.path)
	return true
}

// setCursorByPath puts the cursor back on a path, leaving it where it is when
// the path is gone -- clamped, so a shorter tree cannot strand it off the end.
func (t *fileTree) setCursorByPath(path string) {
	for i, row := range t.rows {
		if row.path == path {
			t.cursor = i
			return
		}
	}
	t.cursor = clamp(t.cursor, 0, max(len(t.rows)-1, 0))
}

// syncScroll settles the offset so the cursor is on screen, moving as little as
// possible: stepping down a long tree should advance a row at a time rather than
// re-centering under the cursor.
func (t *fileTree) syncScroll(height int) {
	if height <= 0 {
		t.offset = 0
		return
	}
	maxOffset := max(len(t.rows)-height, 0)
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+height {
		t.offset = t.cursor - height + 1
	}
	t.offset = clamp(t.offset, 0, maxOffset)
}

// scroll moves the tree by delta rows without moving the cursor, which is what
// the mouse wheel does.
func (t *fileTree) scroll(delta, height int) {
	t.offset = clamp(t.offset+delta, 0, max(len(t.rows)-height, 0))
}

// --- rendering ---

// statusStyle colors a row by what happened to the file. Additions read as
// progress, deletions as loss, and an untracked file is the one case where the
// agent made something git has never seen.
func (s Styles) statusStyle(status gitx.ChangeStatus) lipgloss.Style {
	base := lipgloss.NewStyle()
	switch status {
	case gitx.ChangeAdded, gitx.ChangeUntracked:
		return base.Foreground(s.P.Success)
	case gitx.ChangeDeleted:
		return base.Foreground(s.P.Danger)
	case gitx.ChangeRenamed, gitx.ChangeCopied:
		return base.Foreground(s.P.Accent)
	}
	return base.Foreground(s.P.Muted)
}

// render draws the visible rows into a pane of the given size. The cursor row is
// filled rather than recolored, matching how the board marks the selected card:
// the status colors are already spent on meaning.
func (t *fileTree) render(st Styles, width, height int, focused bool) string {
	t.syncScroll(height)
	if len(t.rows) == 0 {
		return st.Faint.Render("(no changes)")
	}

	var out []string
	for i := t.offset; i < len(t.rows) && i-t.offset < height; i++ {
		// Marked per row so a click picks the file it landed on, the same way a
		// click picks a card on the board.
		out = append(out, zone.Mark(zoneDiffRow(i), t.renderRow(st, t.rows[i], width, i == t.cursor, focused)))
	}
	// Short trees leave blank rows, which still belong to the tree as far as the
	// wheel is concerned.
	for len(out) < height {
		out = append(out, strings.Repeat(" ", width))
	}
	return zone.Mark(zoneDiffTree, strings.Join(out, "\n"))
}

func (t *fileTree) renderRow(st Styles, row treeRow, width int, cursor, focused bool) string {
	// Every segment of the cursor row carries the fill itself; wrapping the
	// finished line in one background style would stop at the first reset. Same
	// reason as cardLines.
	fill := func(style lipgloss.Style) lipgloss.Style {
		if !cursor {
			return style
		}
		return style.Background(st.P.Selection)
	}

	counts := ""
	switch {
	case row.file.Binary:
		counts = "bin"
	case row.added > 0 || row.removed > 0:
		counts = fmt.Sprintf("+%d −%d", row.added, row.removed)
	}

	// A closed directory keeps its twisty pointing right, which is the only clue
	// that rows are hidden under it. The root row stands for the whole diff
	// rather than a directory to walk into, so it has none.
	marker := " "
	if row.dir {
		switch {
		case t.collapsed[row.path]:
			marker = "▸"
		case row.path != "":
			marker = "▾"
		}
	}

	name := row.label
	if row.dir && row.path != "" {
		name += "/"
	}
	status := " "
	if !row.dir {
		status = row.file.Status.String()
	}

	// The caret repeats the cursor mark in shape as well as fill, so the row
	// stays findable in a terminal ignoring colors -- and it reserves its cells
	// on every row, so moving the cursor does not shift any text sideways.
	caret := "  "
	if cursor {
		caret = "▸ "
	}
	head := caret + strings.Repeat("  ", row.depth) + marker + " " + status + " "

	avail := max(width-lipgloss.Width(head)-lipgloss.Width(counts)-1, 4)
	name = truncateLeft(name, avail)

	statusStyle, nameStyle := st.statusStyle(row.file.Status), st.Meta
	if row.dir {
		statusStyle, nameStyle = st.Faint, st.Faint
	}
	if cursor {
		nameStyle = st.CardTitle
		if focused {
			nameStyle = st.CardTitleSelected
		}
	}

	caretStyle := fill(lipgloss.NewStyle().Foreground(st.P.Focus).Bold(true))
	line := caretStyle.Render(caret) +
		fill(st.Faint).Render(strings.Repeat("  ", row.depth)+marker+" ") +
		fill(statusStyle).Render(status) + fill(lipgloss.NewStyle()).Render(" ") +
		fill(nameStyle).Render(name)
	if counts != "" {
		gap := max(width-lipgloss.Width(head)-lipgloss.Width(name)-lipgloss.Width(counts), 1)
		line += fill(lipgloss.NewStyle()).Render(strings.Repeat(" ", gap)) + fill(st.Faint).Render(counts)
	}
	if cursor {
		return padFill(line, width, lipgloss.NewStyle().Background(st.P.Selection))
	}
	return pad(line, width)
}

// truncateLeft cuts a name from its front, which is the opposite of truncate.
// The tail of a path is what tells two of them apart -- "…/ui/panel.go" is
// legible where "internal/ui/pa…" is not.
func truncateLeft(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	runes := []rune(s)
	for i := range runes {
		cand := "…" + string(runes[i+1:])
		if lipgloss.Width(cand) <= n {
			return cand
		}
	}
	return "…"
}
