package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// picker is the overlay the two searches across the worktree share: a query, a
// list of places to go, and a cursor on it.
//
// One widget for both because they differ only in where the rows come from and
// what picking one means. Find-in-pane is deliberately not here -- see find.
type picker struct {
	kind  pickerKind
	input textinput.Model
	query string

	results []pickResult
	cursor  int
	offset  int

	// gen is bumped on every keystroke and carried by the search it starts, so
	// a slow answer to an old query is dropped rather than drawn over a newer
	// one. The file finder is synchronous and does not need it; grep runs a
	// process per query and very much does.
	gen int
	// searching says a query is in flight, so an empty list can say "looking"
	// rather than "nothing".
	searching bool
	// capped says the search stopped at the limit, so the list does not read as
	// the whole answer.
	capped bool
}

type pickerKind int

const (
	pickerNone pickerKind = iota
	// pickerFiles is the fuzzy finder over every path in the worktree.
	pickerFiles
	// pickerGrep is the search for a string inside those files.
	pickerGrep
)

// pickResult is one row of the list.
type pickResult struct {
	path string
	// line is where in the file the hit was, zero for a file result.
	line int
	// text is the matching line, for a grep hit.
	text string
	// match is the byte offsets in path the query matched, which the row
	// underlines so the ranking can be seen rather than taken on trust.
	match []int
}

// pickLimit bounds both searches. Past a screenful the list stops being a way
// to choose and becomes a second thing to search, and an unbounded grep of a
// large repo is a process that writes for a while.
const pickLimit = 200

func (p picker) open() bool { return p.kind != pickerNone }

// start opens the overlay on one of the two searches, keeping nothing from the
// last one: a finder that opens on the previous query is a finder you have to
// clear before you can use it.
func (p *picker) start(kind pickerKind, width int) tea.Cmd {
	ti := textinput.New()
	ti.Prompt = ""
	// Two cells to the frame, two to its padding, two to the "› " marker, and
	// one the field keeps for its cursor. Short by any of them and the box
	// truncates its own query row.
	ti.SetWidth(max(width-7, 20))
	ti.SetVirtualCursor(true)
	*p = picker{kind: kind, input: ti}
	return p.input.Focus()
}

func (p *picker) close() { *p = picker{} }

func (p *picker) move(delta int) {
	if len(p.results) == 0 {
		return
	}
	p.cursor = clamp(p.cursor+delta, 0, len(p.results)-1)
}

func (p picker) selected() (pickResult, bool) {
	if p.cursor < 0 || p.cursor >= len(p.results) {
		return pickResult{}, false
	}
	return p.results[p.cursor], true
}

// syncScroll settles the offset so the cursor is on screen, moving as little as
// possible -- the same rule the file tree follows.
func (p *picker) syncScroll(height int) {
	if height <= 0 {
		p.offset = 0
		return
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+height {
		p.offset = p.cursor - height + 1
	}
	p.offset = clamp(p.offset, 0, max(len(p.results)-height, 0))
}

func (p picker) title() string {
	if p.kind == pickerGrep {
		return "search in files"
	}
	return "find file"
}

// status is the line under the query: how many places there are to go, or why
// there are none.
func (p picker) status() string {
	switch {
	case p.searching:
		return "searching…"
	case p.query == "" && p.kind == pickerGrep:
		return "type to search the worktree"
	case len(p.results) == 0:
		return "no matches"
	case p.capped:
		return fmt.Sprintf("first %d matches", len(p.results))
	case len(p.results) == 1:
		return "1 match"
	}
	return fmt.Sprintf("%d matches", len(p.results))
}

// --- rendering ---

const (
	zonePicker = "picker"
	// pickerRowsMax bounds the overlay's height so it never swallows the view
	// it is floating over: some of the diff behind it is what says where you
	// are about to land.
	pickerRowsMax = 14
)

func zonePickerRow(i int) string { return fmt.Sprintf("picker:row:%d", i) }

// render draws the overlay. It is returned as a block for the caller to place
// over the panes rather than drawn into them, so the view underneath does not
// have to know a search is open.
func (p *picker) render(st Styles, width int) string {
	rows := min(pickerRowsMax, max(len(p.results), 1))
	p.syncScroll(rows)

	inner := max(width-4, 20)
	var lines []string
	lines = append(lines,
		st.KeyHint.Render("› ")+p.input.View(),
		st.Faint.Render(strings.Repeat("─", inner)),
	)

	if len(p.results) == 0 {
		lines = append(lines, st.Faint.Render(p.status()))
	} else {
		for i := p.offset; i < len(p.results) && i-p.offset < rows; i++ {
			lines = append(lines,
				zone.Mark(zonePickerRow(i), p.renderRow(st, p.results[i], inner, i == p.cursor)))
		}
		lines = append(lines, st.Faint.Render(p.status()))
	}

	box := Box{
		Title:    p.title(),
		Subtitle: "enter open · esc cancel",
		Accent:   st.P.Accent,
		Border:   st.P.Focus,
		Width:    width,
		Focused:  true,
	}
	return zone.Mark(zonePicker, box.Render(strings.Join(lines, "\n")))
}

func (p *picker) renderRow(st Styles, r pickResult, width int, cursor bool) string {
	fill := func(style lipgloss.Style) lipgloss.Style {
		if !cursor {
			return style
		}
		return style.Background(st.P.Selection)
	}

	caret := "  "
	if cursor {
		caret = "▸ "
	}

	// The path first and the matching text after it, because you are choosing
	// between files far more often than between two hits in the same one. A
	// grep row spends half the width on the path and the rest on the line, so
	// both stay readable in a narrow pane.
	pathRoom := width - len(caret)
	if r.text != "" {
		pathRoom = max(pathRoom/2, 12)
	}
	label := r.path
	if r.line > 0 {
		label = fmt.Sprintf("%s:%d", r.path, r.line)
	}
	shown, dropped := truncatePathLeft(label, pathRoom)

	pathStyle := st.Meta
	if cursor {
		pathStyle = st.CardTitleSelected
	}
	line := fill(lipgloss.NewStyle().Foreground(st.P.Focus).Bold(true)).Render(caret) +
		highlightMatch(fill(pathStyle), fill(st.MatchHit), shown, r.match, dropped)

	if r.text != "" {
		gap := max(width-len(caret)-lipgloss.Width(shown)-2, 8)
		line += fill(st.Faint).Render("  " + truncate(strings.TrimSpace(r.text), gap))
	}
	if cursor {
		return padFill(line, width, lipgloss.NewStyle().Background(st.P.Selection))
	}
	return pad(line, width)
}

// truncatePathLeft cuts a path from its front, reporting how many bytes went,
// so the match offsets can be shifted to follow it. The tail of a path is what
// tells two of them apart -- see truncateLeft, which this is the measured form
// of.
func truncatePathLeft(s string, n int) (shown string, dropped int) {
	if n <= 1 || lipgloss.Width(s) <= n {
		return s, 0
	}
	// One cell goes to the ellipsis, so keep the last n-1 cells. Paths are the
	// output of git and are bytes-per-cell in every realistic case; a wide rune
	// here costs a column of padding, not a wrong offset, because the shift is
	// measured in the same bytes the offsets are.
	keep := n - 1
	if keep >= len(s) {
		return s, 0
	}
	return "…" + s[len(s)-keep:], len(s) - keep
}

// highlightMatch emphasizes the characters the query matched, so the order the
// list is in can be read off the rows rather than taken on trust.
//
// dropped is how many bytes truncation took off the front, since the offsets
// are into the whole path. Anything now off the left edge is simply not drawn:
// being one character's worth of emphasis short beats drawing it in the wrong
// place.
func highlightMatch(base, hit lipgloss.Style, s string, match []int, dropped int) string {
	if len(match) == 0 {
		return base.Render(s)
	}
	// The ellipsis occupies the first cell of a truncated string, so an offset
	// landing there has nothing of its own to mark.
	shift := dropped
	if dropped > 0 {
		shift -= len("…")
	}
	in := make(map[int]bool, len(match))
	for _, i := range match {
		if at := i - shift; at >= 0 && at < len(s) {
			in[at] = true
		}
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		style := base
		if in[i] {
			style = hit
		}
		b.WriteString(style.Render(string(s[i])))
	}
	return b.String()
}
