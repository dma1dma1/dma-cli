package render

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// File renders a file's contents into a document, with the same margin a diff
// gets so that everything downstream reads one format.
//
// Highlighting is done in process rather than by shelling out to bat. The diff
// pane can lean on whatever the user has installed because a diff without delta
// is still a diff; a file without highlighting is a wall of one color, and
// making that the common case for anyone who has not installed a second tool is
// not a bargain worth repeating. In process it also renders the same way on
// every machine, which is what makes the tests mean anything.
//
// name decides the language: chroma matches on the file name first and sniffs
// the content only when the name tells it nothing.
func File(name string, src []byte, width int) *Document {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	// A trailing newline is the end of the last line, not an empty line after
	// it. Splitting without accounting for that puts a phantom row at the foot
	// of every well-formed file.
	text = strings.TrimSuffix(text, "\n")

	lines := highlight(name, text)
	if width < 1 {
		width = numberWidth(len(lines))
	} else {
		width = max(width, numberWidth(len(lines)))
	}

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		// A file is its own only side, so both columns carry the same number.
		// Keeping the shape means the pane, the search and the line references
		// do not need to know which of the two things they are looking at.
		b.WriteString(fileGutter(width, i+1) + line)
	}
	return Parse(b.String(), Unified)
}

// highlight colors each line, falling back to the plain text. A file whose
// language chroma does not know, or one it chokes on, is still a file worth
// reading -- the margin and the content are what the pane is for, and the
// colors are a bonus.
func highlight(name, text string) []string {
	plain := strings.Split(text, "\n")

	lexer := lexers.Match(name)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		return plain
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, text)
	if err != nil {
		return plain
	}
	var buf strings.Builder
	if err := formatters.TTY16m.Format(&buf, styles.Get("monokai"), it); err != nil {
		return plain
	}
	colored := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	// A highlighter that returned a different number of lines than it was given
	// has done something this cannot reconcile with the line numbers, and the
	// numbers are the part that matters.
	if len(colored) != len(plain) {
		return plain
	}
	return colored
}

// fileGutter is the margin for one line of a file, in the format the parser
// reads back. See margin.go.
func fileGutter(width, line int) string {
	n := fmt.Sprintf("%*d", width, line)
	return sgrFaint + n + " " + string(MarginMid) + " " + n + " " + string(MarginEnd) + sgrReset + " "
}

// The margin is drawn faint for the same reason the diff's is: the content
// beside it is already colored, in a palette the line numbers have no business
// joining.
const (
	sgrFaint = "\x1b[2m"
	sgrReset = "\x1b[0m"
)

// numberWidth is how many columns the line numbers need, measured once for the
// whole file so the margin does not step in and out as it is scrolled.
func numberWidth(lines int) int {
	const min = 2
	w := 0
	for n := lines; n > 0; n /= 10 {
		w++
	}
	if w < min {
		return min
	}
	return w
}
