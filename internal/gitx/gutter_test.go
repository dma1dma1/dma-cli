package gitx

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// marginPattern picks a numbered row apart: the line it claims in the old file,
// the line it claims in the new one, and the text git wrote. Either number is
// blank on a row that exists in only one of the files.
var marginPattern = regexp.MustCompile(`^ *(\d*) ⋮ *(\d*) │ (.*)$`)

// row is one rendered row of a diff as a reader sees it: without the escapes, and
// with the margin read rather than counted.
type row struct {
	old, new string
	text     string
	numbered bool
}

func render(patch string) []row {
	lines := strings.Split(ansiSeq.ReplaceAllString(numberLines(patch), ""), "\n")
	out := make([]row, len(lines))
	for i, line := range lines {
		if m := marginPattern.FindStringSubmatch(line); m != nil {
			out[i] = row{old: m[1], new: m[2], text: m[3], numbered: true}
			continue
		}
		out[i] = row{text: line}
	}
	return out
}

func (r row) String() string {
	if !r.numbered {
		return fmt.Sprintf("(unnumbered) %q", r.text)
	}
	return fmt.Sprintf("%q ⋮ %q │ %q", r.old, r.new, r.text)
}

// Every row of a hunk names its line in each file, and the two sides walk out of
// step at the first change -- which is the whole reason to print both.
func TestNumberLinesNumbersEveryRow(t *testing.T) {
	rows := render(samplePatch)

	// The first hunk starts at line 4 of both files and adds two lines.
	want := map[int]row{
		5:  {old: "4", new: "4"},
		8:  {old: "", new: "7"},
		9:  {old: "", new: "8"},
		10: {old: "7", new: "9"},
		// The second hunk only deletes, which is the mirror of an addition: a line
		// in the old file and none at all in the new one.
		16: {old: "22", new: "24"},
		17: {old: "23", new: ""},
		18: {old: "24", new: "25"},
	}
	for at, w := range want {
		got := rows[at]
		if !got.numbered || got.old != w.old || got.new != w.new {
			t.Errorf("row %d = %s, want %q ⋮ %q", at, got, w.old, w.new)
		}
	}

	// The text is git's, untouched: the margin is a prefix and nothing else.
	lines := strings.Split(samplePatch, "\n")
	for at, r := range rows {
		if r.numbered && r.text != lines[at] {
			t.Errorf("row %d text = %q, want git's own %q", at, r.text, lines[at])
		}
	}
}

// A file header is not a line of a file, and neither is a diffstat: numbering
// them would be numbering nothing.
func TestNumberLinesLeavesTheHeaderAlone(t *testing.T) {
	stat := " panel.go | 3 ++-\n 1 file changed, 2 insertions(+), 1 deletion(-)\n\n"
	rows := render(stat + samplePatch)
	for i, want := range strings.Split(stat+samplePatch, "\n")[:7] {
		if rows[i].numbered || rows[i].text != want {
			t.Errorf("header row %d = %s, want it untouched: %q", i, rows[i], want)
		}
	}
}

// The @@ header becomes a rule carrying the enclosing function: the line
// arithmetic in it is what the margin now answers, on every row.
func TestNumberLinesReplacesHunkHeaders(t *testing.T) {
	if out := numberLines(samplePatch); strings.Contains(out, "@@") {
		t.Errorf("a hunk header survived:\n%s", out)
	}
	rules := 0
	for _, r := range render(samplePatch) {
		if !strings.HasPrefix(r.text, HunkRule) {
			continue
		}
		rules++
		if !strings.HasSuffix(r.text, "func (m Model) chips() string {") {
			t.Errorf("rule row = %s, want the enclosing function on it", r)
		}
	}
	// One per hunk, which is what makes counting them an exact way to find a hunk
	// in the rendered diff.
	if rules != 2 {
		t.Errorf("%d rule rows, want one per hunk (2)", rules)
	}
}

// Git colors what it writes, so the marker a row is classified by sits behind an
// escape -- and the row's own colors have to survive the trip.
func TestNumberLinesSeesThroughColor(t *testing.T) {
	colored := "\x1b[1mdiff --git a/panel.go b/panel.go\x1b[m\n" +
		"\x1b[36m@@ -4,2 +4,3 @@\x1b[m \x1b[mfunc run() {\x1b[m\n" +
		" \tline4\x1b[m\n" +
		"\x1b[32m+\tadded line\x1b[m\n" +
		" \tline5\x1b[m"

	if out := numberLines(colored); !strings.Contains(out, "\x1b[32m+\tadded line\x1b[m") {
		t.Errorf("git's own coloring was lost:\n%q", out)
	}
	rows := render(colored)
	if got := rows[2]; !got.numbered || got.old != "4" || got.new != "4" {
		t.Errorf("context row = %s, want 4 ⋮ 4", got)
	}
	if got := rows[3]; !got.numbered || got.old != "" || got.new != "5" {
		t.Errorf("added row = %s, want a number on the new side only", got)
	}
}

// A margin sized per hunk would step in and out as the diff is scrolled, and one
// sized for the first hunk would clip the numbers in the last.
func TestNumberLinesSizesTheMarginForTheWholeFile(t *testing.T) {
	patch := "diff --git a/big.go b/big.go\n" +
		"@@ -1,2 +1,2 @@\n context\n-old\n+new\n" +
		"@@ -1198,2 +1198,2 @@\n context\n+late\n"
	rows := render(patch)

	if got := rows[2]; got.old != "1" || got.new != "1" {
		t.Errorf("first row = %s, want 1 ⋮ 1", got)
	}
	if got := rows[6]; got.old != "1198" || got.new != "1198" {
		t.Errorf("late row = %s, want its numbers whole", got)
	}

	// One width top to bottom, so the text starts in the same column throughout.
	starts := map[int]bool{}
	for _, line := range strings.Split(ansiSeq.ReplaceAllString(numberLines(patch), ""), "\n") {
		if m := marginPattern.FindStringSubmatch(line); m != nil {
			starts[len([]rune(line))-len([]rune(m[3]))] = true
		}
	}
	if len(starts) != 1 {
		t.Errorf("text starts in %d different columns, want one: %v", len(starts), starts)
	}
}

// "\ No newline at end of file" is a note about the row above it. Numbering it
// would claim a line neither file has.
func TestNumberLinesSkipsTheNoNewlineNote(t *testing.T) {
	rows := render("diff --git a/x.txt b/x.txt\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n")

	if got := rows[3]; !got.numbered || got.old != "" || got.new != "" {
		t.Errorf("note row = %s, want a blank margin", got)
	}
	if got := rows[4]; got.new != "1" {
		t.Errorf("row after the note = %s, want line 1 of the new file", got)
	}
}

// A whole-tree diff is several patches in a row, and each file's --- and +++ rows
// open with the markers a hunk's lines use.
func TestNumberLinesRestartsAtEachFile(t *testing.T) {
	rows := render("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -7 +7 @@\n-old\n+new\n" +
		"diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-one\n+two\n")

	for _, i := range []int{1, 2, 7, 8} {
		if rows[i].numbered {
			t.Errorf("file header row %d = %s, want no margin on it", i, rows[i])
		}
	}
	if got := rows[4]; got.old != "7" {
		t.Errorf("first file's change = %s, want line 7", got)
	}
	if got := rows[10]; got.old != "1" {
		t.Errorf("second file's change = %s, want it back at line 1", got)
	}
}

// An unmerged file comes as a combined diff, whose @@@ headers this does not
// read. It has to pass through whole rather than have the file after it numbered
// from wherever the last hunk left off.
func TestNumberLinesLeavesCombinedDiffsAlone(t *testing.T) {
	combined := "diff --cc conflict.go\nindex 1111111,2222222..0000000\n--- a/conflict.go\n" +
		"+++ b/conflict.go\n@@@ -1,2 -1,2 +1,3 @@@\n  context\n++<<<<<<< HEAD\n"
	rows := render(combined + "diff --git a/after.go b/after.go\n@@ -9 +9 @@\n-old\n+new\n")

	for i, r := range rows[:7] {
		if r.numbered {
			t.Errorf("combined diff row %d = %s, want it left alone", i, r)
		}
	}
	if got := rows[9]; got.old != "9" {
		t.Errorf("the file after it = %s, want line 9", got)
	}
}

func TestNumberLinesEmptyPatch(t *testing.T) {
	if got := numberLines(""); got != "" {
		t.Errorf("numberLines of nothing = %q, want nothing", got)
	}
}
