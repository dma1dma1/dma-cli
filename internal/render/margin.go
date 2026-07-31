// Package render turns rendered pane content into a Document: rows that know
// what they are, rather than a blob of text with colors in it.
//
// The review pane has always held one long string. Everything that needed to
// reason about it -- jumping between the changes in a file, telling an agent
// where you were looking -- had to find its way back to the structure by
// searching the rendered output for content it recognized. That works until it
// does not: delta's hunk headers are its own invention and carry no line
// numbers, side-by-side splits a long line across two columns, and a line
// appearing twice in a file is ambiguous by construction.
//
// The inversion is to stop reading whatever the renderer chose to emit and
// start telling it what to emit. Delta's line-number margin is configurable, so
// it is pinned to the format below; the fallback that renders without delta
// already writes a margin, so it is made to write the same one. Both are then
// read once, here, into rows carrying the line each one stands for -- and every
// feature downstream asks the rows instead of searching the text.
//
// This package is pure. Running git, running delta and reading files all stay
// where they were; what arrives here is the string they produced.
package render

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The margin is the contract between the renderers and this parser: a line
// number for the old side, a separator, a line number for the new side, a
// second separator, and one space before the content starts.
//
// Two different glyphs rather than one repeated: the parser has to find the end
// of the margin on a row whose content may itself contain either character, and
// a pair in a fixed order is far harder to counterfeit than a single mark. They
// are also what the no-delta renderer has always drawn, so pinning delta to
// them changes nothing on screen.
const (
	// MarginMid separates the old line number from the new one.
	MarginMid = '⋮'
	// MarginEnd closes the margin. One space follows it before the content.
	MarginEnd = '│'
)

// DeltaLeftFormat and DeltaRightFormat are what delta is told to draw, given
// the width of one number column. Delta writes the left format, the left
// column, the right format and the right column in that order -- which in a
// unified diff puts the two formats back to back and produces exactly the
// margin the fallback renderer draws, down to the spacing. Turning delta on or
// off must not move the text sideways.
func DeltaLeftFormat(width int) string {
	return "{nm:>" + itoa(width) + "} " + string(MarginMid) + " "
}

func DeltaRightFormat(width int) string {
	return "{np:>" + itoa(width) + "} " + string(MarginEnd) + " "
}

// Layout is how the renderer was asked to lay the content out, which decides
// how a row's margin reads: unified puts one margin at the head of the row,
// side-by-side puts the two halves of it around the left column's text.
type Layout int

const (
	// Unified is one column of content behind one margin.
	Unified Layout = iota
	// SideBySide is two columns, each with a line number of its own.
	SideBySide
)

// margin is what a row's line-number prefix says, and where the content after
// it begins.
type margin struct {
	old, new int
	// body is the byte offset in the plain row where the content starts, i.e.
	// past MarginEnd and the space after it.
	body int
	// ok is false for a row that carries no margin at all, which is how a
	// renderer says the row is structure rather than a line of a file: a file
	// header, the rule that replaces a hunk header, a blank separator.
	ok bool
}

// readMargin reads the margin off the front of a plain (escape-free) row.
//
// A row qualifies only if it opens with digits and spaces up to MarginMid, then
// digits and spaces up to MarginEnd, then a space. Content that happens to
// contain either glyph is therefore never mistaken for a margin, because
// content reaching this far would have to be made of nothing but digits and
// spaces first.
func readMargin(plain string, layout Layout) margin {
	mid := strings.IndexRune(plain, MarginMid)
	if mid < 0 || !digitsAndSpaces(plain[:mid]) {
		return margin{}
	}
	old, oldOK := readNumber(plain[:mid])

	// Side-by-side puts the left column's text between the two glyphs, so the
	// right number is read backwards from MarginEnd rather than forwards from
	// MarginMid. The caller knows which layout it asked for; guessing from the
	// row would misread a unified row whose content contains MarginEnd.
	rest := plain[mid+len(string(MarginMid)):]
	end := strings.IndexRune(rest, MarginEnd)
	if end < 0 {
		return margin{}
	}
	numField := rest[:end]
	if layout == SideBySide {
		numField = trailingNumberField(numField)
	} else if !digitsAndSpaces(numField) {
		return margin{}
	}
	newLine, newOK := readNumber(numField)
	if !oldOK && !newOK {
		// Both columns blank is not a line of anything. Delta draws no such row,
		// but a renderer that did would otherwise land every one of them on
		// line zero and make the whole document look like structure.
		return margin{}
	}

	// Where the content starts depends on how many columns there are. Unified
	// has one, behind the whole margin. Side-by-side has two, and the margin is
	// wrapped around the first of them -- so everything past the opening glyph
	// is content, the right column's line number included. A query of digits
	// can therefore match that inline number, which is the price of searching
	// both columns rather than only the second.
	body := mid + len(string(MarginMid))
	if layout == Unified {
		body += end + len(string(MarginEnd))
	}
	// The single space the format puts after the glyph belongs to the margin
	// rather than to the content, in either layout. Counting it as content
	// would put every match one column to the right of where it is.
	if body < len(plain) && plain[body] == ' ' {
		body++
	}
	return margin{old: old, new: newLine, body: body, ok: true}
}

// trailingNumberField is the run of digits and spaces at the end of a
// side-by-side row's left column, which is where its right-hand line number
// sits. Anything before that is the left column's content.
func trailingNumberField(s string) string {
	i := len(s)
	for i > 0 && (isDigit(s[i-1]) || s[i-1] == ' ') {
		i--
	}
	return s[i:]
}

// readNumber reads a right-aligned line number, reporting false for the blank
// column a row has on the side it does not exist -- a deleted line has no line
// in the new file, and an added one none in the old.
func readNumber(field string) (int, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(field); i++ {
		if !isDigit(field[i]) {
			return 0, false
		}
		n = n*10 + int(field[i]-'0')
	}
	return n, true
}

func digitsAndSpaces(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) && s[i] != ' ' {
			return false
		}
	}
	return true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// itoa avoids pulling strconv in for the two call sites that format a width.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// strip removes the color escapes from a row. Every row's plain form is worked
// out once, when the document is built, rather than per keystroke of a search.
func strip(s string) string { return ansi.Strip(s) }
