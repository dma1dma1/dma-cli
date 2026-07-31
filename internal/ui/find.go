package ui

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/render"
)

// find is the search within whatever the pane is showing.
//
// It is deliberately not the picker: the two searches across the worktree
// produce a list of places to go, and this one produces a position in
// something already open. A list of hits in the file you are looking at would
// be a second thing to read instead of an answer about the first.
type find struct {
	// active is whether the query field has the keyboard. The matches outlive
	// it: enter closes the field and leaves the hits in place, so n and N keep
	// stepping them the way they do in a pager.
	active bool
	input  textinput.Model
	query  string
	// matches are the hits in the document, in the order they appear.
	matches []render.Match
	// current is the one n and N are stepping through, an index into matches.
	current int
}

// open starts a search, or reopens the field over the query already run.
func (f *find) open(width int) tea.Cmd {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetWidth(max(width, 20))
	ti.SetValue(f.query)
	ti.SetVirtualCursor(true)
	ti.CursorEnd()
	f.active, f.input = true, ti
	return f.input.Focus()
}

// close puts the keyboard back without dropping the hits.
func (f *find) close() { f.active = false }

// clear ends the search outright, which is what esc means and what a new
// document means: the hits are offsets into rows that no longer exist.
func (f *find) clear() {
	*f = find{}
}

// run re-searches the document. It is called on every keystroke; a document is
// already split into rows with their escapes stripped, so this is a scan of
// strings already in hand rather than anything that needs a process or a tick.
func (f *find) run(doc *render.Document) {
	f.matches = nil
	f.current = 0
	if doc == nil || f.query == "" {
		return
	}
	f.matches = doc.Search(f.query)
}

// step moves to the next or previous hit, wrapping. Wrapping rather than
// stopping because a search is a set of places rather than a list read
// downward, and every pager in the world wraps.
func (f *find) step(delta int) (render.Match, bool) {
	if len(f.matches) == 0 {
		return render.Match{}, false
	}
	f.current = wrap(f.current+delta, len(f.matches))
	return f.matches[f.current], true
}

// nearest puts the cursor on the first hit at or after row, so that opening a
// search does not throw away where the pane was already scrolled to.
func (f *find) nearest(row int) (render.Match, bool) {
	if len(f.matches) == 0 {
		return render.Match{}, false
	}
	for i, m := range f.matches {
		if m.Row >= row {
			f.current = i
			return m, true
		}
	}
	f.current = 0
	return f.matches[0], true
}

// label is what the subtitle says about the search: the query and where in the
// hits you are, or that there are none.
func (f find) label() string {
	if f.query == "" {
		return ""
	}
	if len(f.matches) == 0 {
		return fmt.Sprintf("find %q · no matches", f.query)
	}
	return fmt.Sprintf("find %q · %d of %d", f.query, f.current+1, len(f.matches))
}
