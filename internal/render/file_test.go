package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const goSource = `package main

import "fmt"

func main() {
	fmt.Println("needle")
}
`

// A file arrives in the pane wearing the same margin a diff does, so everything
// downstream reads one format.
func TestFileCarriesTheMargin(t *testing.T) {
	doc := File("main.go", []byte(goSource), 0)

	lines := strings.Split(strings.TrimSuffix(goSource, "\n"), "\n")
	if len(doc.Rows) != len(lines) {
		t.Fatalf("got %d rows for a %d-line file", len(doc.Rows), len(lines))
	}
	for i, want := range lines {
		row := doc.Rows[i]
		if row.New != i+1 || row.Old != i+1 {
			t.Errorf("row %d numbered %d/%d, want %d on both sides", i, row.Old, row.New, i+1)
		}
		if row.Content() != want {
			t.Errorf("row %d = %q, want %q", i, row.Content(), want)
		}
	}
}

// A trailing newline ends the last line; it does not add an empty one after it.
func TestFileHasNoPhantomLastLine(t *testing.T) {
	doc := File("x.txt", []byte("one\ntwo\n"), 0)
	if len(doc.Rows) != 2 {
		t.Errorf("got %d rows for a two-line file: %q", len(doc.Rows), doc.Text())
	}
	// And a file with no trailing newline is still two lines.
	if got := File("x.txt", []byte("one\ntwo"), 0); len(got.Rows) != 2 {
		t.Errorf("got %d rows without a trailing newline", len(got.Rows))
	}
}

func TestFileIsHighlighted(t *testing.T) {
	doc := File("main.go", []byte(goSource), 0)
	body := doc.Text()
	if !strings.Contains(body, "\x1b[") {
		t.Error("no color anywhere in a highlighted Go file")
	}
	// Whatever the highlighter did, the text underneath is unchanged -- the
	// colors are a bonus and the content is the point.
	for i, row := range doc.Rows {
		if strings.Contains(row.Plain, "\x1b") {
			t.Errorf("row %d kept an escape in its plain form: %q", i, row.Plain)
		}
	}
}

// A language chroma does not know is still a file worth reading.
func TestFileFallsBackForUnknownLanguages(t *testing.T) {
	src := "some words\nand some more\n"
	doc := File("notes.zzzz", []byte(src), 0)
	if len(doc.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(doc.Rows))
	}
	if doc.Rows[0].Content() != "some words" {
		t.Errorf("row 0 = %q", doc.Rows[0].Content())
	}
}

// Windows line endings are line endings, not content.
func TestFileNormalizesLineEndings(t *testing.T) {
	doc := File("x.txt", []byte("one\r\ntwo\r\n"), 0)
	if len(doc.Rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(doc.Rows))
	}
	if strings.Contains(doc.Text(), "\r") {
		t.Error("a carriage return survived into the pane")
	}
}

// The margin is measured once for the whole file, so the text starts in the
// same column on line 9 and line 1000.
func TestFileMarginDoesNotStepInAndOut(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 1200; i++ {
		b.WriteString("line\n")
	}
	doc := File("x.txt", []byte(b.String()), 0)

	starts := map[int]bool{}
	for _, row := range doc.Rows {
		starts[row.Body] = true
	}
	if len(starts) != 1 {
		t.Errorf("content starts at %d different offsets, want one: %v", len(starts), starts)
	}
	// And the numbers are all readable.
	if got := doc.Rows[1199].New; got != 1200 {
		t.Errorf("last row numbered %d, want 1200", got)
	}
}

// Everything the pane can do with a file goes through the document, so the
// searching and line lookup a grep hit needs have to work on one.
func TestFileIsSearchableAndAddressable(t *testing.T) {
	doc := File("main.go", []byte(goSource), 0)

	matches := doc.Search("needle")
	if len(matches) != 1 {
		t.Fatalf("got %d hits for needle, want 1", len(matches))
	}
	row := doc.Rows[matches[0].Row]
	if row.New != 6 {
		t.Errorf("needle found on line %d, want 6", row.New)
	}
	if got := row.Plain[matches[0].Start:matches[0].End]; got != "needle" {
		t.Errorf("hit spans %q", got)
	}

	// A grep hit names a line; the pane has to turn that into a row.
	at, ok := doc.RowForLine(6)
	if !ok || at != matches[0].Row {
		t.Errorf("line 6 resolved to row %d, want %d", at, matches[0].Row)
	}
}

// The margin must not be searchable, or every query of digits lights up the
// line numbers beside it.
func TestFileSearchSkipsTheMargin(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		b.WriteString("nothing here\n")
	}
	doc := File("x.txt", []byte(b.String()), 0)
	if got := doc.Search("42"); len(got) != 0 {
		t.Errorf("matched %d line numbers", len(got))
	}
}

// The colors are chroma's, so highlighting a search hit on top of them has to
// leave the row the same width and the same text.
func TestFileHighlightSurvivesSyntaxColors(t *testing.T) {
	doc := File("main.go", []byte(goSource), 0)
	matches := doc.Search("needle")
	if len(matches) != 1 {
		t.Fatal("setup: needle not found")
	}
	before := doc.Rows[matches[0].Row]
	lines := doc.Highlight(matches, 0, matchStyle(), matchStyle())
	got := lines[matches[0].Row]

	if ansi.Strip(got) != before.Plain {
		t.Errorf("text changed:\n got %q\nwant %q", ansi.Strip(got), before.Plain)
	}
	if ansi.StringWidth(got) != ansi.StringWidth(before.Text) {
		t.Errorf("width changed: %d, want %d", ansi.StringWidth(got), ansi.StringWidth(before.Text))
	}
}
