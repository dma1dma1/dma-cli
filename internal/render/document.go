package render

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Document is rendered pane content, read into rows that know what they stand
// for. See the package comment for why the pane holds one of these rather than
// the string it was built from.
type Document struct {
	Rows  []Row
	Hunks []Hunk
	// Layout is how the content was laid out, kept so a consumer can say why a
	// side-by-side row reports the numbers it does.
	Layout Layout
}

// Row is one line of the pane.
type Row struct {
	// Text is the row as the renderer produced it, color escapes and all. This
	// is what the viewport draws.
	Text string
	// Plain is Text with the escapes removed. Searching matches against this,
	// and it is computed once here rather than once per keystroke.
	Plain string
	// Old and New are the row's line numbers on each side of a patch, zero
	// where the row has no line on that side: a deleted line has no line in the
	// new file, an added one none in the old. A file document sets both to the
	// same number, since a file is its own only side.
	Old, New int
	// Body is the byte offset in Plain where content starts, past the
	// line-number margin. A search that matched the margin would turn every
	// line number on screen into a hit.
	Body int
	// Hunk indexes Document.Hunks, or is -1 for a row that is structure rather
	// than content: a file header, the rule standing in for a hunk header, the
	// blank rows between them.
	Hunk int
}

// isContent reports whether the row stands for a line of a file, which is what
// carrying a margin means. Files are numbered from one, so a row with a line on
// neither side has none.
func (r Row) isContent() bool { return r.Old > 0 || r.New > 0 }

// Content is the row's text without its margin, which is what a search runs
// against and what a grep hit is compared to.
func (r Row) Content() string { return r.Plain[min(r.Body, len(r.Plain)):] }

// Hunk is one contiguous run of content rows: the unit } and { step between.
type Hunk struct {
	// FirstRow and LastRow bound the hunk in the document, which is what it
	// takes to scroll to it.
	FirstRow, LastRow int
	// Start and End are the hunk's line range in the new file -- what a
	// path:line reference has to name for an agent to open the right place. A
	// hunk that only deletes occupies no lines in the new file, so it collapses
	// onto the line its deletion sat in front of.
	Start, End int
}

// Ref is the path:line-range an agent can be pointed at.
func (h Hunk) Ref(path string) string {
	if h.End > h.Start {
		return fmt.Sprintf("%s:%d-%d", path, h.Start, h.End)
	}
	return fmt.Sprintf("%s:%d", path, h.Start)
}

// Parse reads rendered content into a document.
//
// It never fails. A renderer whose margin this does not recognize -- a delta
// new enough to have changed its format strings, or a diff of a file git could
// not read -- yields rows that are all structure, which costs the features that
// need line numbers and costs the pane nothing: the text still draws.
func Parse(content string, layout Layout) *Document {
	doc := &Document{Layout: layout}
	if content == "" {
		return doc
	}
	lines := strings.Split(content, "\n")
	doc.Rows = make([]Row, len(lines))
	for i, text := range lines {
		plain := strip(text)
		m := readMargin(plain, layout)
		doc.Rows[i] = Row{
			Text: text, Plain: plain,
			Old: m.old, New: m.new, Body: m.body, Hunk: -1,
		}
		if !m.ok {
			// A structure row's content is the whole row: it has no margin to
			// skip past, and a hunk header carrying a function name is worth
			// finding with a search.
			doc.Rows[i].Body = 0
		}
	}
	doc.buildHunks()
	return doc
}

// buildHunks groups the content rows.
//
// A hunk is a maximal run of content rows. Both renderers put something between
// two hunks -- delta its own boxed header, the fallback the rule that replaces
// the @@ line -- and neither puts anything inside one, so the runs and the
// hunks are the same partition. A jump in the new-file line number splits a run
// as well, so a renderer that stopped drawing headers would degrade to hunks
// that are merely too fine rather than to one hunk swallowing a file.
func (d *Document) buildHunks() {
	start := -1
	for i := range d.Rows {
		content := d.Rows[i].isContent()
		if content && start >= 0 && d.contiguous(start, i) {
			continue
		}
		if start >= 0 {
			d.closeHunk(start, i-1)
		}
		start = -1
		if content {
			start = i
		}
	}
	if start >= 0 {
		d.closeHunk(start, len(d.Rows)-1)
	}
	for i := range d.Hunks {
		for r := d.Hunks[i].FirstRow; r <= d.Hunks[i].LastRow; r++ {
			d.Rows[r].Hunk = i
		}
	}
}

// contiguous reports whether row i continues the run that began at start.
//
// Only the new-file numbers are compared, and only rows that have one: a run of
// deletions has no new line at all, and reading its blank column as a jump
// would split every deletion into a hunk of its own.
func (d *Document) contiguous(start, i int) bool {
	prev := 0
	for r := i - 1; r >= start; r-- {
		if d.Rows[r].New > 0 {
			prev = d.Rows[r].New
			break
		}
	}
	cur := d.Rows[i].New
	return prev == 0 || cur == 0 || cur == prev+1
}

func (d *Document) closeHunk(first, last int) {
	h := Hunk{FirstRow: first, LastRow: last}
	for r := first; r <= last; r++ {
		if n := d.Rows[r].New; n > 0 {
			if h.Start == 0 {
				h.Start = n
			}
			h.End = n
		}
	}
	if h.Start == 0 {
		// Nothing but deletions. The change sits in front of the line the old
		// side was removed from, which is the closest thing to a place in the
		// new file that it has.
		for r := first; r <= last; r++ {
			if o := d.Rows[r].Old; o > 0 {
				h.Start, h.End = o, o
				break
			}
		}
	}
	d.Hunks = append(d.Hunks, h)
}

// Lines is the rows as the viewport wants them, which is one string per row
// with the colors left in.
func (d *Document) Lines() []string {
	out := make([]string, len(d.Rows))
	for i, r := range d.Rows {
		out[i] = r.Text
	}
	return out
}

// Text is the document as one string, for a caller that only wants to draw it.
func (d *Document) Text() string { return strings.Join(d.Lines(), "\n") }

// RowForLine is the row showing line n of the new file, which is how a grep hit
// or any other line reference is turned into somewhere to scroll.
//
// The nearest following row is taken when the exact line is not on screen: a
// diff shows the lines it changed and the few around them, so a hit inside a
// file is often on a line the patch never prints, and landing on the next line
// it does print beats not moving.
func (d *Document) RowForLine(n int) (int, bool) {
	best, found := 0, false
	for i, r := range d.Rows {
		if r.New == n {
			return i, true
		}
		if r.New > n && (!found || r.New < d.Rows[best].New) {
			best, found = i, true
		}
	}
	return best, found
}

// HunkAt is the hunk containing row, or the last one above it -- which is what
// "change 3 of 12" has to count while the pane is scrolled to the gap between
// two hunks.
func (d *Document) HunkAt(row int) int {
	current := 0
	for i, h := range d.Hunks {
		if h.FirstRow <= row {
			current = i
		}
	}
	return current
}

// NextHunkRow is the row to scroll to for a step of delta hunks from row. It
// reports false when there is nowhere to go, so a key can do nothing rather
// than pretend it did something.
func (d *Document) NextHunkRow(row, delta int) (int, bool) {
	if len(d.Hunks) == 0 {
		return 0, false
	}
	if delta > 0 {
		for _, h := range d.Hunks {
			if h.FirstRow > row {
				return h.FirstRow, true
			}
		}
		return 0, false
	}
	for i := len(d.Hunks) - 1; i >= 0; i-- {
		if d.Hunks[i].FirstRow < row {
			return d.Hunks[i].FirstRow, true
		}
	}
	return 0, false
}

// Match is one hit of a search, as byte offsets into the row's Plain form.
//
// Bytes rather than cells because that is what searching a string produces;
// Highlight converts to the cell positions the styler wants, which is the only
// place the difference matters.
type Match struct {
	Row        int
	Start, End int
}

// Search finds every occurrence of query in the document's content.
//
// The match is literal and smart-cased: a query typed in lower case ignores
// case, and one with a capital in it means that capital. Nobody types a regular
// expression into a find-in-file box by accident, and the failure mode of
// treating one as a pattern is an error message where the user expected a
// search for a bracket.
//
// The margin is excluded. Searching it would make every query of digits match
// the line numbers of the rows beside it.
func (d *Document) Search(query string) []Match {
	if query == "" {
		return nil
	}
	fold := query == strings.ToLower(query)
	needle := query
	var out []Match
	for i, r := range d.Rows {
		hay := r.Content()
		if fold {
			hay = strings.ToLower(hay)
		}
		from := 0
		for {
			at := strings.Index(hay[from:], needle)
			if at < 0 {
				break
			}
			start := r.Body + from + at
			out = append(out, Match{Row: i, Start: start, End: start + len(needle)})
			from += at + len(needle)
		}
	}
	return out
}

// Highlight draws the matches into the rows, returning the lines to hand the
// viewport.
//
// Only the rows that matched are rebuilt, so a document with three hits costs
// three restyled rows rather than a copy of the whole pane. current is the
// index into matches of the one being stepped to, drawn in its own style so it
// can be told apart from the rest; pass -1 for none.
func (d *Document) Highlight(matches []Match, current int, style, currentStyle lipgloss.Style) []string {
	lines := d.Lines()
	byRow := map[int][]lipgloss.Range{}
	for i, m := range matches {
		row := d.Rows[m.Row]
		// The styler works in display cells of the escape-free row, which is
		// not where a byte offset points the moment a line holds anything wider
		// or longer than ASCII.
		start := ansi.StringWidth(row.Plain[:m.Start])
		end := ansi.StringWidth(row.Plain[:m.End])
		s := style
		if i == current {
			s = currentStyle
		}
		byRow[m.Row] = append(byRow[m.Row], lipgloss.NewRange(start, end, s))
	}
	for row, ranges := range byRow {
		lines[row] = lipgloss.StyleRanges(lines[row], ranges...)
	}
	return lines
}
