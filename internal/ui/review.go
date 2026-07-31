package ui

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/fuzzy"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/render"
)

// review is the state of the full-screen review view: a tree of paths on the
// left, one thing rendered on the right, and the searches that choose what that
// thing is.
//
// It lives in a struct of its own rather than on the Model because it is a
// screen's worth of state -- what is on the pane, where the pane is scrolled,
// what has been rendered already, which of three searches is open -- and none
// of it means anything anywhere else in the program. The Model holds the board.
type review struct {
	// view is the pane the content is drawn in. It scrolls both ways: content
	// is never wrapped, so a long line is reached by scrolling to it rather
	// than by being folded onto rows that would break the line numbering
	// everything else depends on.
	view viewport.Model
	// doc is what the pane is showing, read into rows that carry the line each
	// one stands for. See package render.
	doc *render.Document

	// mode is which range a diff is measured over.
	mode gitx.DiffMode
	// source is whether the pane is showing a diff or a file. The tree names a
	// path; this says which question is being asked about it.
	source paneSource
	// files is the tree beside the pane. The pane renders one row of it at a
	// time, so a session with fifty changed files costs one file's worth of git
	// rather than fifty.
	files fileTree
	// filePath is the path the pane shows the contents of.
	//
	// It is held here rather than read off the tree cursor, because the tree
	// lists what the session changed and the searches reach the whole worktree:
	// a file the agent never touched has no row to put a cursor on. Moving the
	// tree cursor sets this, so the common case still reads as one selection --
	// see showTreeSelection.
	filePath string

	// cache holds rendered content by key. Stepping back to something already
	// read should not spend a process on it again; the cache is dropped whole
	// on a refresh, so it can never outlive the work it describes.
	cache map[string]string

	// treeFocus routes j/k to the tree rather than the pane.
	treeFocus bool
	// treeHidden gives the whole width to the pane. A narrow window starts out
	// this way, since a tree and a diff cannot both be legible in 80 columns.
	treeHidden bool
	// sideBySide asks delta for two columns. Without delta there is only one
	// layout git can produce, so the toggle says so rather than silently
	// failing.
	sideBySide bool

	// find is the search within whatever the pane is showing.
	find find
	// picker is the overlay the two searches across the worktree share.
	picker picker
	// paths is the worktree's file list, for the fuzzy finder. It is read once
	// per refresh rather than per keystroke: on a large repo it is the one
	// expensive part of opening the finder, and it does not change while a
	// query is being typed.
	paths []string

	// pending is a line the pane should scroll to once it has content to find
	// it in. A grep hit names a line before the file has been rendered, and the
	// render is a process away.
	pending pendingScroll
}

// pendingScroll is a line asked for before there was a document to look it up
// in.
type pendingScroll struct {
	line   int
	active bool
}

// paneSource is what the review pane is rendering.
//
// A diff answers "what changed here". A file answers "what is here" -- which is
// the question a path with no diff can only answer, and the one a changed file
// is often better read as anyway, since a patch shows three lines of context
// and a review question rarely stops there.
type paneSource int

const (
	sourceDiff paneSource = iota
	sourceFile
)

// --- results arriving ---

// handleFile puts a rendered file in the pane.
//
// A file that could not be read is shown rather than posted to the notice line:
// "this is a binary file" is the answer to the question the pane was asked, and
// an answer belongs where the question was.
func (m Model) handleFile(msg fileMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.selectedID {
		return m, nil
	}
	content := msg.content
	if msg.err != nil {
		content = m.styles.Faint.Render("  " + msg.err.Error())
	}
	if m.review.cache == nil {
		m.review.cache = map[string]string{}
	}
	m.review.cache[msg.key] = content
	if msg.key == m.diffKey() {
		m.setDiffContent(content)
	}
	return m, nil
}

// handleGrep puts one search's hits in the picker, if it is still the search
// being asked.
func (m Model) handleGrep(msg grepMsg) (tea.Model, tea.Cmd) {
	if msg.id != m.selectedID || msg.gen != m.review.picker.gen {
		return m, nil
	}
	m.review.picker.searching = false
	if msg.err != nil {
		m.review.picker.results = nil
		return m, errStatus(msg.err)
	}
	results := make([]pickResult, 0, len(msg.hits))
	for _, h := range msg.hits {
		results = append(results, pickResult{path: h.Path, line: h.Line, text: h.Text})
	}
	m.review.picker.results = results
	m.review.picker.capped = len(results) >= pickLimit
	m.review.picker.cursor, m.review.picker.offset = 0, 0
	return m, nil
}

// rankPaths refills the file finder from the query. It runs on every keystroke:
// ranking a repo's worth of paths is a scan of strings already in memory, so
// there is nothing here to debounce or to wait for.
func (m *Model) rankPaths() {
	ranked := fuzzy.Rank(m.review.picker.query, m.review.paths, pickLimit)
	results := make([]pickResult, 0, len(ranked))
	for _, r := range ranked {
		results = append(results, pickResult{path: r.Text, match: r.Match})
	}
	m.review.picker.results = results
	m.review.picker.capped = len(results) >= pickLimit
	m.review.picker.cursor, m.review.picker.offset = 0, 0
}

// --- keys ---

// keyPicker routes a keystroke while one of the two worktree searches is open.
// The overlay owns the keyboard outright, the way the board's dropdown does:
// every printable key belongs to the query.
func (m Model) keyPicker(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		m.review.picker.close()
		return m, nil

	case "up", "ctrl+p":
		m.review.picker.move(-1)
		return m, nil

	case "down", "ctrl+n":
		m.review.picker.move(1)
		return m, nil

	case "enter":
		r, ok := m.review.picker.selected()
		if !ok {
			return m, nil
		}
		m.review.picker.close()
		cmd := m.showFileAt(r.path, r.line)
		return m, cmd
	}

	before := m.review.picker.input.Value()
	var cmd tea.Cmd
	m.review.picker.input, cmd = m.review.picker.input.Update(msg)
	after := m.review.picker.input.Value()
	if after == before {
		return m, cmd
	}
	m.review.picker.query = after

	if m.review.picker.kind == pickerFiles {
		m.rankPaths()
		return m, cmd
	}
	// Grep costs a process, so it waits for typing to stop. The generation is
	// bumped here rather than when the search fires, so every keystroke
	// invalidates whatever was already in flight.
	m.review.picker.gen++
	if after == "" {
		m.review.picker.results, m.review.picker.searching = nil, false
		return m, cmd
	}
	return m, tea.Batch(cmd, grepDebounceCmd(m.review.picker.gen))
}

// keyFind routes a keystroke while the find field has the keyboard.
func (m Model) keyFind(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		// Escape abandons the search outright, hits and all -- which is what
		// makes it the way out of a highlighted pane.
		m.review.find.clear()
		m.drawPane()
		return m, nil

	case "enter":
		// Enter keeps the hits and hands the keyboard back, so n and N step
		// them. This is the pager bargain, and it is why the query stays in the
		// subtitle afterwards: the mode is still on.
		m.review.find.close()
		return m, nil

	case "up", "down":
		delta := 1
		if key == "up" {
			delta = -1
		}
		return m.stepFind(delta)
	}

	before := m.review.find.input.Value()
	var cmd tea.Cmd
	m.review.find.input, cmd = m.review.find.input.Update(msg)
	after := m.review.find.input.Value()
	if after == before {
		return m, cmd
	}
	// Incremental: every keystroke re-searches and moves to the first hit at or
	// after where the pane already is, so a search started halfway down a file
	// does not throw away where you were.
	m.review.find.query = after
	m.review.find.run(m.review.doc)
	if match, ok := m.review.find.nearest(m.review.view.YOffset()); ok {
		m.review.view.SetYOffset(match.Row)
	}
	m.drawPane()
	return m, cmd
}

// stepFind moves to the next or previous hit and scrolls to it.
func (m Model) stepFind(delta int) (tea.Model, tea.Cmd) {
	match, ok := m.review.find.step(delta)
	if !ok {
		return m, nil
	}
	m.review.view.SetYOffset(match.Row)
	m.review.treeFocus = false
	m.drawPane()
	return m, nil
}
