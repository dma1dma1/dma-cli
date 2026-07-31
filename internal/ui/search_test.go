package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// typeInto feeds a string to whichever field currently has the keyboard, one
// keypress at a time, the way a person does.
func typeInto(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.keyDiff(tea.KeyPressMsg{Code: r, Text: string(r)}, string(r))
		m = next.(Model)
	}
	return m
}

func pressDiff(m Model, key string) Model {
	next, _ := m.keyDiff(tea.KeyPressMsg{}, key)
	return next.(Model)
}

// --- find in the pane ---

// searchable is the review view with a pane holding four findable lines.
func searchable(t *testing.T) Model {
	t.Helper()
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("internal/ui/panel.go")
	m.setDiffContent(strings.Join([]string{
		" 1 ⋮  1 │ package ui",
		" 2 ⋮  2 │ ",
		" 3 ⋮  3 │ func needle() {}",
		" 4 ⋮  4 │ ",
		" 5 ⋮  5 │ func other() { needle() }",
		" 6 ⋮  6 │ var Needle = 1",
	}, "\n") + strings.Repeat("\nfiller", 200))
	return m
}

func TestFindHighlightsAndScrolls(t *testing.T) {
	m := pressDiff(searchable(t), "/")
	if !m.review.find.active {
		t.Fatal("/ did not open the find field")
	}
	m = typeInto(m, "needle")

	// Lower case ignores case, so the capitalized one counts too.
	if got := len(m.review.find.matches); got != 3 {
		t.Fatalf("found %d hits, want 3", got)
	}
	// The pane moved to the first hit rather than staying at the top.
	if got := m.review.view.YOffset(); got != m.review.find.matches[0].Row {
		t.Errorf("pane at row %d, want the first hit at %d", got, m.review.find.matches[0].Row)
	}
	// And the hits are drawn without disturbing the text.
	drawn := m.review.doc.Highlight(m.review.find.matches, m.review.find.current,
		m.styles.MatchHit, m.styles.MatchCurrent)
	row := m.review.find.matches[0].Row
	if ansi.Strip(drawn[row]) != m.review.doc.Rows[row].Plain {
		t.Errorf("highlighting changed the row: %q", ansi.Strip(drawn[row]))
	}
	if drawn[row] == m.review.doc.Rows[row].Text {
		t.Error("the hit was not marked")
	}
}

// The subtitle is what makes the mode visible, and n/N belong to the search for
// as long as it says so.
func TestFindShowsWhereYouAre(t *testing.T) {
	m := typeInto(pressDiff(searchable(t), "/"), "needle")
	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "1 of 3") {
		t.Errorf("subtitle = %q, want 1 of 3", got)
	}

	m = pressDiff(m, "enter") // keep the hits, hand the keyboard back
	if m.review.find.active {
		t.Error("enter left the field focused")
	}
	m = pressDiff(m, "n")
	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "2 of 3") {
		t.Errorf("after n, subtitle = %q, want 2 of 3", got)
	}
	if got := m.review.view.YOffset(); got != m.review.find.matches[1].Row {
		t.Errorf("n scrolled to %d, want %d", got, m.review.find.matches[1].Row)
	}
	m = pressDiff(m, "N")
	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "1 of 3") {
		t.Errorf("after N, subtitle = %q, want 1 of 3", got)
	}
}

// A search is a set of places, so stepping past the end comes back round.
func TestFindWraps(t *testing.T) {
	m := pressDiff(typeInto(pressDiff(searchable(t), "/"), "needle"), "enter")
	for i := 0; i < 3; i++ {
		m = pressDiff(m, "n")
	}
	if m.review.find.current != 0 {
		t.Errorf("after a full lap the cursor is on hit %d, want back at 0", m.review.find.current)
	}
}

// Escape puts the search down, and n goes back to meaning next file. This is
// the whole reason the mode has to be visible while it is on.
func TestEscapeClearsTheSearchBeforeLeavingTheView(t *testing.T) {
	m := pressDiff(typeInto(pressDiff(searchable(t), "/"), "needle"), "enter")
	was := m.review.files.selectedPath()

	m = pressDiff(m, "esc")
	if m.mode != modeDiff {
		t.Error("the first esc left the review view instead of clearing the search")
	}
	if len(m.review.find.matches) != 0 || m.review.find.query != "" {
		t.Error("esc left the search running")
	}
	if got := m.diffSubtitle(m.selected()); strings.Contains(got, "find") {
		t.Errorf("subtitle still advertises a search: %q", got)
	}

	// n is the next file again.
	m = pressDiff(m, "n")
	if m.review.files.selectedPath() == was {
		t.Error("n did not move to the next file once the search was put down")
	}

	// And a second esc now leaves.
	if pressDiff(m, "esc").mode != modeBoard {
		t.Error("esc with no search running did not leave the review view")
	}
}

// The hits are offsets into rows. A new document has different rows, so they
// have to be re-found rather than carried over.
func TestFindFollowsTheContent(t *testing.T) {
	m := pressDiff(typeInto(pressDiff(searchable(t), "/"), "needle"), "enter")
	if len(m.review.find.matches) != 3 {
		t.Fatalf("setup: got %d hits", len(m.review.find.matches))
	}
	m.setDiffContent(" 1 ⋮  1 │ nothing to find here\n")
	if len(m.review.find.matches) != 0 {
		t.Errorf("stale hits survived a new document: %+v", m.review.find.matches)
	}
	// The query survives, because asking it again of the next file is what
	// searching across files has to mean.
	if m.review.find.query != "needle" {
		t.Errorf("query = %q, want it kept", m.review.find.query)
	}
}

// --- reading a file instead of its diff ---

func TestToggleBetweenDiffAndContents(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("README.md")
	if m.review.source != sourceDiff {
		t.Fatal("the pane did not start on the diff")
	}

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "c")
	m = next.(Model)
	if m.review.source != sourceFile {
		t.Error("c did not switch to the file's contents")
	}
	if cmd == nil {
		t.Error("nothing was asked to render the file")
	}
	if got := m.diffSubtitle(m.selected()); !strings.Contains(got, "file") {
		t.Errorf("subtitle = %q, want it to say the pane holds a file", got)
	}
	// The key that got you here gets you back.
	if pressDiff(m, "c").review.source != sourceDiff {
		t.Error("c did not switch back to the diff")
	}
}

// A directory has no contents, so the key says so rather than emptying the pane.
func TestContentsKeyRefusesADirectory(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("internal/ui")

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "c")
	if got := next.(Model).review.source; got != sourceDiff {
		t.Error("c on a directory put the pane into file mode")
	}
	if cmd == nil {
		t.Error("c on a directory said nothing about why it did nothing")
	}
}

// Stepping from a file onto a directory falls back to the diff rather than
// leaving the pane asking for the contents of something that has none.
func TestFileSourceFallsBackOnADirectory(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("README.md")
	m = pressDiff(m, "c")
	if m.review.source != sourceFile {
		t.Fatal("setup: not in file mode")
	}
	m.review.files.setCursorByPath("internal/ui")
	m.showTreeSelection()
	if m.review.source != sourceDiff {
		t.Error("a directory row left the pane in file mode")
	}
	if m.review.filePath != "" {
		t.Errorf("the pane still names %q as its file", m.review.filePath)
	}
}

// A file that cannot be read answers in the pane rather than on the notice
// line: it is the answer to the question the pane was asked.
func TestUnreadableFileIsShownInThePane(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.files.setCursorByPath("README.md")
	m = pressDiff(m, "c")

	next, _ := m.update(fileMsg{id: "s1", key: m.diffKey(), path: "README.md",
		err: &stubErr{"README.md is a binary file"}})
	m = next.(Model)
	if !strings.Contains(m.review.doc.Text(), "binary file") {
		t.Errorf("the pane does not say why it is empty: %q", m.review.doc.Text())
	}
}

type stubErr struct{ s string }

func (e *stubErr) Error() string { return e.s }

// --- the file finder ---

func TestFuzzyFinderOpensAndPicks(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.paths = []string{
		"internal/ui/review.go", "internal/ui/panel.go", "docs/README.md", "go.mod",
	}

	m = pressDiff(m, "f")
	if m.review.picker.kind != pickerFiles {
		t.Fatal("f did not open the file finder")
	}
	// It opens on the whole list rather than on nothing.
	if len(m.review.picker.results) != 4 {
		t.Errorf("finder opened with %d rows, want all 4", len(m.review.picker.results))
	}

	m = typeInto(m, "revgo")
	if len(m.review.picker.results) == 0 {
		t.Fatal("no results for revgo")
	}
	if got := m.review.picker.results[0].path; got != "internal/ui/review.go" {
		t.Errorf("top result = %q, want internal/ui/review.go", got)
	}

	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "enter")
	m = next.(Model)
	if m.review.picker.open() {
		t.Error("picking left the finder open")
	}
	if m.review.source != sourceFile {
		t.Error("picking a file did not put the pane into file mode")
	}
	if cmd == nil {
		t.Error("nothing was asked to render the picked file")
	}
}

// The finder can be opened before the path list has landed -- it is asked for
// when the view opens, and f is one keystroke.
func TestFuzzyFinderFillsWhenThePathsArrive(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m = pressDiff(m, "f")
	if len(m.review.picker.results) != 0 {
		t.Fatal("setup: the finder already had rows")
	}
	next, _ := m.update(worktreeFilesMsg{id: "s1", paths: []string{"a.go", "b.go"}})
	m = next.(Model)
	if len(m.review.picker.results) != 2 {
		t.Errorf("the finder has %d rows after the paths landed, want 2", len(m.review.picker.results))
	}
}

func TestPickerEscapeCloses(t *testing.T) {
	m := pressDiff(diffModel(t, someFiles()...), "f")
	closed := pressDiff(m, "esc")
	if closed.review.picker.open() {
		t.Error("esc did not close the finder")
	}
	// And it does not also leave the review view in the same keystroke.
	if closed.mode != modeDiff {
		t.Error("esc closed the finder and left the view at once")
	}
}

// While the overlay is up it owns the keyboard: what it is is a text field, and
// a field that reserved half the alphabet for the view behind it would not be
// one.
func TestPickerOwnsTheKeyboard(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.paths = []string{"quit.go"}
	m = pressDiff(m, "f")
	m = typeInto(m, "q")

	if m.mode != modeDiff {
		t.Error("q typed into the finder quit the view")
	}
	if m.review.picker.query != "q" {
		t.Errorf("query = %q, want the q to have reached the field", m.review.picker.query)
	}
}

// --- grep ---

// Grep costs a process, so it waits for typing to stop, and every keystroke
// invalidates whatever was already in flight.
func TestGrepDebouncesAndDropsStaleAnswers(t *testing.T) {
	m := pressDiff(diffModel(t, someFiles()...), "g")
	if m.review.picker.kind != pickerGrep {
		t.Fatal("g did not open the search")
	}
	// Nothing has been searched for yet, so nothing is claimed to be missing.
	if got := m.review.picker.status(); !strings.Contains(got, "type to search") {
		t.Errorf("empty search says %q", got)
	}

	m = typeInto(m, "need")
	stale := m.review.picker.gen
	m = typeInto(m, "le")
	if m.review.picker.gen == stale {
		t.Fatal("typing did not invalidate the search already in flight")
	}
	current := m.review.picker.gen

	// The answer to the abandoned query is dropped on arrival.
	next, _ := m.update(grepMsg{id: "s1", gen: stale,
		hits: []gitx.Hit{{Path: "stale.go", Line: 1, Text: "stale"}}})
	m = next.(Model)
	if len(m.review.picker.results) != 0 {
		t.Errorf("a stale answer was drawn: %+v", m.review.picker.results)
	}

	// The answer to the query actually being asked is kept.
	next, _ = m.update(grepMsg{id: "s1", gen: current,
		hits: []gitx.Hit{{Path: "found.go", Line: 12, Text: "the needle"}}})
	m = next.(Model)
	if len(m.review.picker.results) != 1 || m.review.picker.results[0].path != "found.go" {
		t.Fatalf("the current answer was not drawn: %+v", m.review.picker.results)
	}

	// Picking a hit opens the file at the line it was found on.
	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "enter")
	m = next.(Model)
	if cmd == nil {
		t.Error("nothing was asked to render the file the hit is in")
	}
	if m.review.source != sourceFile {
		t.Error("a grep hit did not open the file")
	}
	if !m.review.pending.active || m.review.pending.line != 12 {
		t.Errorf("pending scroll = %+v, want line 12", m.review.pending)
	}
}

// The debounce only fires the search that is still being asked for.
func TestGrepDebounceIgnoresSupersededQueries(t *testing.T) {
	m := pressDiff(diffModel(t, someFiles()...), "g")
	m = typeInto(m, "a")
	stale := m.review.picker.gen
	m = typeInto(m, "b")

	if _, cmd := m.update(grepDebounceMsg{gen: stale}); cmd != nil {
		t.Error("a superseded query still reached the worktree")
	}
	next, cmd := m.update(grepDebounceMsg{gen: m.review.picker.gen})
	if cmd == nil {
		t.Error("the current query never reached the worktree")
	}
	if !next.(Model).review.picker.searching {
		t.Error("the list does not say a search is running")
	}
}

// Emptying the field puts the list back rather than leaving the last query's
// hits under a blank search box.
func TestGrepClearingTheQueryClearsTheResults(t *testing.T) {
	m := pressDiff(diffModel(t, someFiles()...), "g")
	m = typeInto(m, "x")
	next, _ := m.update(grepMsg{id: "s1", gen: m.review.picker.gen,
		hits: []gitx.Hit{{Path: "a.go", Line: 1, Text: "x"}}})
	m = next.(Model)
	if len(m.review.picker.results) != 1 {
		t.Fatal("setup: no results to clear")
	}
	m = typeKey(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if len(m.review.picker.results) != 0 {
		t.Errorf("clearing the query left %d results", len(m.review.picker.results))
	}
}

// typeKey delivers one non-printable keypress to the review view.
func typeKey(m Model, k tea.KeyPressMsg) Model {
	next, _ := m.keyDiff(k, k.String())
	return next.(Model)
}

// A grep hit names a line before the file has been rendered, and the render is
// a process away. The scroll has to survive that gap and land on the file the
// hit was in -- not on whatever the pane happened to be showing when it was
// picked.
func TestGrepHitScrollsTheFileThatArrivesLater(t *testing.T) {
	m := diffModel(t, someFiles()...)
	// The pane starts on some other file, long enough to have a line 12 of its
	// own -- which is exactly what must not be scrolled to.
	m.setDiffContent(strings.Join(marginLines(1, 200, "old file"), "\n"))
	before := m.review.view.YOffset()

	m = pressDiff(m, "g")
	m = typeInto(m, "x")
	next, _ := m.update(grepMsg{id: "s1", gen: m.review.picker.gen,
		hits: []gitx.Hit{{Path: "internal/gitx/git.go", Line: 12, Text: "hit"}}})
	m = next.(Model)
	next, cmd := m.keyDiff(tea.KeyPressMsg{}, "enter")
	m = next.(Model)

	if cmd == nil {
		t.Fatal("the file was never asked for")
	}
	// Nothing has moved yet: the document in the pane is still the other file.
	if got := m.review.view.YOffset(); got != before {
		t.Errorf("the previous file scrolled to %d before the hit's file arrived", got)
	}
	if !m.review.pending.active {
		t.Fatal("the request to scroll was spent on the wrong document")
	}

	// Now the file lands.
	next, _ = m.update(fileMsg{id: "s1", key: m.diffKey(), path: "internal/gitx/git.go",
		content: strings.Join(marginLines(1, 200, "the real file"), "\n")})
	m = next.(Model)

	row, ok := m.review.doc.RowForLine(12)
	if !ok {
		t.Fatal("line 12 is not in the rendered file")
	}
	if got := m.review.view.YOffset(); got != row {
		t.Errorf("pane at row %d, want the hit's line 12 at row %d", got, row)
	}
	if m.review.pending.active {
		t.Error("the scroll request was not consumed")
	}
}

// A request that cannot be honored is still consumed, or it fires at the next
// unrelated file.
func TestPendingScrollIsConsumedEvenWhenTheLineIsMissing(t *testing.T) {
	m := diffModel(t, someFiles()...)
	m.review.pending = pendingScroll{line: 900, active: true}
	m.setDiffContent(strings.Join(marginLines(1, 5, "short file"), "\n"))
	if m.review.pending.active {
		t.Error("a scroll to a line the file does not have stayed armed")
	}
}

// marginLines builds content in the format the pane parses: see package render.
func marginLines(from, to int, text string) []string {
	var out []string
	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf("%2d ⋮ %2d │ %s %d", i, i, text, i))
	}
	return out
}
