package render

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// deltaOutput is what delta 0.19 emits for a two-hunk patch, with the margin
// pinned to the format this package specifies and the colors taken out. It is
// recorded rather than generated so the parser can be tested on a machine with
// no delta installed; TestParseAgainstRealDelta in package gitx is what catches
// a delta that has changed its mind about the format.
const deltaOutput = `
a.go
──────────────────────────────────────────

 1 ⋮  1 │ package main
 2 ⋮  2 │
 3 ⋮  3 │ func alpha() int {
 4 ⋮    │         return 1
   ⋮  4 │         return 42
 5 ⋮  5 │ }
 6 ⋮  6 │
 7 ⋮  7 │ func beta() string {

──────────────────────┐
 func beta() string { │
──────────────────────┘
 9 ⋮  9 │ }
10 ⋮ 10 │
11 ⋮ 11 │ func gamma() {
12 ⋮    │         println("g")
   ⋮ 12 │         println("changed here")
   ⋮ 13 │         println("and more")
13 ⋮ 14 │ }`

func TestParseSeparatesContentFromStructure(t *testing.T) {
	doc := Parse(deltaOutput, Unified)

	// The file name, the rules, the boxed hunk header and the rows between them
	// are structure: nothing in them stands for a line of a file. A blank line
	// *of the file* is content -- it has a number, and a search that skipped it
	// would report the wrong count.
	structure := []string{
		"", "a.go", "──────────────────────────────────────────",
		"──────────────────────┐", " func beta() string { │", "──────────────────────┘",
	}
	isStructure := map[string]bool{}
	for _, s := range structure {
		isStructure[s] = true
	}
	content := 0
	for i, r := range doc.Rows {
		want := !isStructure[r.Plain]
		if r.isContent() != want {
			t.Errorf("row %d %q: content=%v, want %v", i, r.Plain, r.isContent(), want)
		}
		if want {
			content++
		}
	}
	// Fifteen lines of the file are printed across the two hunks.
	if content != 15 {
		t.Errorf("counted %d content rows, want 15", content)
	}
}

func TestParseReadsLineNumbers(t *testing.T) {
	doc := Parse(deltaOutput, Unified)

	// Keyed by the line numbers rather than by the text: "}" closes two of the
	// three functions, so the body alone does not name a row.
	want := map[[2]int]string{
		{1, 1}:   "package main",
		{4, 0}:   "        return 1",
		{0, 4}:   "        return 42",
		{0, 12}:  `        println("changed here")`,
		{13, 14}: "}",
		{5, 5}:   "}",
		{2, 2}:   "",
	}
	got := map[[2]int]string{}
	for _, r := range doc.Rows {
		if r.isContent() {
			got[[2]int{r.Old, r.New}] = r.Content()
		}
	}
	for lines, body := range want {
		if got[lines] != body {
			t.Errorf("line %d/%d = %q, want %q", lines[0], lines[1], got[lines], body)
		}
	}
}

// The whole point of the margin: two hunks, found without searching the text
// for anything.
func TestParseFindsHunks(t *testing.T) {
	doc := Parse(deltaOutput, Unified)
	if len(doc.Hunks) != 2 {
		t.Fatalf("got %d hunks, want 2: %+v", len(doc.Hunks), doc.Hunks)
	}
	if got := doc.Hunks[0]; got.Start != 1 || got.End != 7 {
		t.Errorf("first hunk = lines %d-%d, want 1-7", got.Start, got.End)
	}
	if got := doc.Hunks[1]; got.Start != 9 || got.End != 14 {
		t.Errorf("second hunk = lines %d-%d, want 9-14", got.Start, got.End)
	}
	// Every content row belongs to one of them, and no structure row does.
	for i, r := range doc.Rows {
		if r.isContent() == (r.Hunk < 0) {
			t.Errorf("row %d %q: content=%v hunk=%d", i, strings.TrimSpace(r.Plain), r.isContent(), r.Hunk)
		}
	}
}

func TestNextHunkRowSteps(t *testing.T) {
	doc := Parse(deltaOutput, Unified)
	first, second := doc.Hunks[0].FirstRow, doc.Hunks[1].FirstRow

	if got, ok := doc.NextHunkRow(0, 1); !ok || got != first {
		t.Errorf("forward from the top = %d (%v), want %d", got, ok, first)
	}
	if got, ok := doc.NextHunkRow(first, 1); !ok || got != second {
		t.Errorf("forward from the first hunk = %d (%v), want %d", got, ok, second)
	}
	if _, ok := doc.NextHunkRow(second, 1); ok {
		t.Error("forward from the last hunk went somewhere, want nowhere to go")
	}
	if got, ok := doc.NextHunkRow(second, -1); !ok || got != first {
		t.Errorf("back from the second hunk = %d (%v), want %d", got, ok, first)
	}
	if _, ok := doc.NextHunkRow(first, -1); ok {
		t.Error("back from the first hunk went somewhere, want nowhere to go")
	}
}

// The reference an agent is pointed at names lines in the new file, which is
// what it has to open.
func TestHunkRef(t *testing.T) {
	doc := Parse(deltaOutput, Unified)
	if got := doc.Hunks[1].Ref("a.go"); got != "a.go:9-14" {
		t.Errorf("ref = %q, want a.go:9-14", got)
	}
	single := Hunk{Start: 12, End: 12}
	if got := single.Ref("a.go"); got != "a.go:12" {
		t.Errorf("one-line ref = %q, want a.go:12", got)
	}
}

func TestRowForLine(t *testing.T) {
	doc := Parse(deltaOutput, Unified)

	row, ok := doc.RowForLine(12)
	if !ok {
		t.Fatal("line 12 not found")
	}
	if got := doc.Rows[row].Content(); got != `        println("changed here")` {
		t.Errorf("line 12 landed on %q", got)
	}
	// Line 8 is inside the gap the patch does not print. The nearest line it
	// does print is the next one, which beats not moving at all.
	row, ok = doc.RowForLine(8)
	if !ok || doc.Rows[row].New != 9 {
		t.Errorf("line 8 landed on new line %d, want the next printed line 9", doc.Rows[row].New)
	}
}

func TestSearchSkipsTheMargin(t *testing.T) {
	doc := Parse(deltaOutput, Unified)

	// "13" is the old-side line number of one row and the new-side number of
	// another. Neither is content, so neither is a hit.
	if got := doc.Search("13"); len(got) != 0 {
		for _, m := range got {
			t.Errorf("matched the margin: row %q", doc.Rows[m.Row].Plain)
		}
	}
	matches := doc.Search("println")
	if len(matches) != 3 {
		t.Fatalf("got %d hits for println, want 3", len(matches))
	}
	for _, m := range matches {
		if got := doc.Rows[m.Row].Plain[m.Start:m.End]; got != "println" {
			t.Errorf("hit spans %q, want println", got)
		}
	}
}

// Lower case means "I do not care"; a capital means "I meant that capital".
func TestSearchIsSmartCased(t *testing.T) {
	doc := Parse("1 ⋮ 1 │ Needle and needle\n", Unified)
	if got := doc.Search("needle"); len(got) != 2 {
		t.Errorf("lower-case query found %d, want both", len(got))
	}
	if got := doc.Search("Needle"); len(got) != 1 {
		t.Errorf("capitalized query found %d, want only the capitalized one", len(got))
	}
}

// The pane holds syntax-highlighted text. A highlight that shifted the content
// or dropped a color would be worse than no highlight at all.
func TestHighlightPreservesTheRow(t *testing.T) {
	colored := " 1 ⋮  1 │ \x1b[36mfunc\x1b[0m \x1b[32mneedle\x1b[0m() {"
	doc := Parse(colored, Unified)

	matches := doc.Search("needle")
	if len(matches) != 1 {
		t.Fatalf("got %d hits, want 1", len(matches))
	}
	lines := doc.Highlight(matches, 0,
		lipgloss.NewStyle().Reverse(true), lipgloss.NewStyle().Reverse(true))

	if got := ansi.Strip(lines[0]); got != doc.Rows[0].Plain {
		t.Errorf("text changed:\n got %q\nwant %q", got, doc.Rows[0].Plain)
	}
	if got, want := ansi.StringWidth(lines[0]), ansi.StringWidth(colored); got != want {
		t.Errorf("width changed: %d, want %d", got, want)
	}
	if lines[0] == colored {
		t.Error("nothing was highlighted")
	}
}

// A row wider than ASCII is where byte offsets and screen columns part company,
// and the styler wants columns.
func TestHighlightHandlesWideRunes(t *testing.T) {
	doc := Parse(" 1 ⋮  1 │ 日本語 needle here\n", Unified)
	matches := doc.Search("needle")
	if len(matches) != 1 {
		t.Fatalf("got %d hits, want 1", len(matches))
	}
	style := lipgloss.NewStyle().Reverse(true)
	lines := doc.Highlight(matches, 0, style, style)

	plain := doc.Rows[0].Plain
	if got := ansi.Strip(lines[0]); got != plain {
		t.Errorf("text changed:\n got %q\nwant %q", got, plain)
	}
	// The reversed span must be the word, not something shifted left by the
	// difference between the bytes and the cells of the runes before it.
	body := strings.Index(lines[0], "\x1b[7m")
	if body < 0 {
		t.Fatal("no highlight emitted")
	}
	if !strings.HasPrefix(ansi.Strip(lines[0][body:]), "needle") {
		t.Errorf("highlight starts at %q, want needle", ansi.Strip(lines[0][body:]))
	}
}

// A renderer this does not recognize must cost the features that need line
// numbers and nothing else. The text still has to draw.
func TestParseDegradesOnAnUnknownMargin(t *testing.T) {
	unknown := "1| package main\n2| func main() {\n"
	doc := Parse(unknown, Unified)

	if len(doc.Hunks) != 0 {
		t.Errorf("got %d hunks from an unreadable margin, want none", len(doc.Hunks))
	}
	if got := doc.Text(); got != unknown {
		t.Errorf("text = %q, want it drawn unchanged", got)
	}
	// And it is still searchable, because the whole row counts as content.
	if got := doc.Search("package"); len(got) != 1 {
		t.Errorf("got %d hits, want the text still searchable", len(got))
	}
}

// matchStyle is the emphasis the tests apply, kept trivial: what is being
// checked is that applying any style leaves the row intact.
func matchStyle() lipgloss.Style { return lipgloss.NewStyle().Reverse(true) }
