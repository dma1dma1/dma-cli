package gitx

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file puts line numbers down the side of a diff git rendered on its own.
//
// A hunk header answers "where in the file am I" once, at the top of the hunk,
// in a form you have to count from to use. Numbers in the margin answer it on
// every row, which is what an editor does and what makes a diff readable without
// opening the file beside it. Delta does this itself when it is installed, so
// this is the same experience for the diff git renders when it is not.

// HunkRule opens the rule that replaces a hunk's @@ header. Finding a hunk in
// the rendered diff means finding these rows, which is why it is exported; it is
// dashed rather than solid so it cannot be confused with the solid rules delta
// draws around headers of its own.
const HunkRule = "┄"

// The margin is faint rather than colored: the diff beside it is already colored,
// by git, in a palette this has no business joining.
const (
	sgrFaint = "\x1b[2m"
	sgrReset = "\x1b[0m"
)

// hunkHeader matches an @@ header: where the hunk starts and how long it runs in
// each file, then the context git puts after the marker -- usually the enclosing
// function, and the one part of the header worth keeping.
var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

// sgrPattern matches the color escapes git writes around each line, which sit in
// front of the marker the line has to be classified by.
var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// numberLines rewrites a patch so every row carries the line it is: its line in
// the old file, then its line in the new one.
//
// Rows are classified and prefixed, nothing more. The colors, the markers and the
// text stay git's own -- re-rendering a diff ourselves would be a lot of code for
// no gain over what git already produces.
func numberLines(patch string) string {
	if patch == "" {
		return patch
	}
	lines := strings.Split(patch, "\n")
	width := numberWidth(lines)

	var b strings.Builder
	b.Grow(len(patch) + len(lines)*(2*width+8))
	oldLine, newLine, inHunk := 0, 0, false
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		plain := sgrPattern.ReplaceAllString(line, "")
		header := hunkHeader.FindStringSubmatch(plain)
		switch {
		// A patch is a diffstat, then a file header, then hunks, and only the last
		// of those holds lines of a file. An empty row is never one of them: git
		// writes a context line as a space and its content, so even a blank line
		// of the file arrives with its marker.
		case plain == "":
			b.WriteString(line)
		case strings.HasPrefix(plain, "diff "):
			// The next file's header. Its --- and +++ rows open with markers a
			// hunk's lines use, so they must not be read as content. Matching
			// "diff " rather than "diff --git" catches the combined diff git
			// writes for an unmerged file, whose @@@ headers this does not read --
			// such a file goes by unnumbered rather than mis-numbered.
			inHunk = false
			b.WriteString(line)
		case header != nil:
			oldLine, newLine, inHunk = atoi(header[1]), atoi(header[3]), true
			b.WriteString(hunkRule(width, header[5]))
		case !inHunk:
			b.WriteString(line)
		case strings.HasPrefix(plain, "+"):
			// An added line exists only in the new file and a removed one only in
			// the old, so one column of the pair is blank on every changed row.
			b.WriteString(gutter(width, 0, newLine) + line)
			newLine++
		case strings.HasPrefix(plain, "-"):
			b.WriteString(gutter(width, oldLine, 0) + line)
			oldLine++
		case strings.HasPrefix(plain, `\`):
			// "\ No newline at end of file" is a note about the row above it rather
			// than a line of either file.
			b.WriteString(gutter(width, 0, 0) + line)
		default:
			b.WriteString(gutter(width, oldLine, newLine) + line)
			oldLine++
			newLine++
		}
	}
	return b.String()
}

// gutter is the margin for one row: its line in each file, then the rule that
// separates the numbers from the text.
func gutter(width, oldLine, newLine int) string {
	return sgrFaint + fmt.Sprintf("%s ⋮ %s │", pad(oldLine, width), pad(newLine, width)) + sgrReset + " "
}

// pad right-aligns a line number, and leaves the column blank for a row that has
// no line on that side.
func pad(n, width int) string {
	if n <= 0 {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%*d", width, n)
}

// hunkRule is what a hunk header becomes: a rule across the margin, carrying the
// context git put after the marker and none of the line arithmetic the margin now
// does for every row. It runs up to where the text starts, so the context lines
// up with the code under it.
func hunkRule(width int, context string) string {
	rule := strings.Repeat(HunkRule, 2*width+5)
	if context = strings.TrimSpace(context); context != "" {
		return sgrFaint + rule + " " + context + sgrReset
	}
	return sgrFaint + rule + sgrReset
}

// numberWidth is how wide a number column has to be, from the largest line number
// the patch's headers reach. Measured once for the whole patch so the margin does
// not step in and out as the diff is scrolled.
func numberWidth(lines []string) int {
	const min = 2
	widest := 0
	for _, line := range lines {
		m := hunkHeader.FindStringSubmatch(sgrPattern.ReplaceAllString(line, ""))
		if m == nil {
			continue
		}
		// A header without a count covers a single line, which is what git leaves
		// out. The last line of a hunk is its start plus its length, less one.
		widest = max(widest, atoi(m[1])+span(m[2])-1, atoi(m[3])+span(m[4])-1)
	}
	return max(len(strconv.Itoa(widest)), min)
}

// span is the number of lines a header claims, defaulting to the one line git
// omits the count for.
func span(count string) int {
	if count == "" {
		return 1
	}
	return atoi(count)
}

// atoi reads a number the hunk-header pattern has already proved is one.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
