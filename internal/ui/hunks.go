package ui

import (
	"regexp"
	"strings"

	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// This file maps the hunks of a diff onto the rows of the rendered diff, which
// is what it takes to jump between the changes in a file.
//
// The two are not the same thing. A hunk is a range of lines in a file; a row is
// a line of output that may have been through delta, wrapped, decorated, or laid
// out in two columns. Rather than model delta's output, the rows are found by
// looking for the hunk in it.

// sgrPattern matches the color escapes in rendered output, which sit between
// the characters any search would be looking for.
var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// hunkRows is the row each hunk starts on in content, one entry per hunk.
//
// Two rules, tried in order. Git's own output and delta's raw hunk headers keep
// the "@@" marker, and counting those is exact. Delta's default header is its own
// invention, so the fallback looks for the first line the hunk changed -- which
// survives every rendering, because it is the content itself.
func hunkRows(content string, hunks []gitx.Hunk) []int {
	if len(hunks) == 0 || content == "" {
		return nil
	}
	rows := strings.Split(content, "\n")
	flat := make([]string, len(rows))
	for i, row := range rows {
		flat[i] = normalizeSpace(sgrPattern.ReplaceAllString(row, ""))
	}

	var headers []int
	for i, row := range flat {
		if strings.HasPrefix(row, "@@") {
			headers = append(headers, i)
		}
	}
	if len(headers) == len(hunks) {
		return headers
	}

	// The anchors are searched in order and each search starts after the last
	// match, so a line that appears in two hunks cannot send the second jump
	// backwards.
	out := make([]int, len(hunks))
	next := 0
	for i, h := range hunks {
		out[i] = next
		anchor := normalizeSpace(h.Anchor)
		if anchor == "" {
			continue
		}
		row, ok := findRow(flat, anchor, next)
		if !ok {
			// Side-by-side splits a long line between two columns, so the whole
			// anchor may be nowhere on one row. Its opening is enough to place
			// the hunk, and being a few rows out beats not moving at all.
			if short := anchorPrefix(anchor); short != anchor {
				row, ok = findRow(flat, short, next)
			}
		}
		if ok {
			out[i], next = row, row+1
		}
	}
	return out
}

func findRow(rows []string, needle string, from int) (int, bool) {
	for row := from; row < len(rows); row++ {
		if strings.Contains(rows[row], needle) {
			return row, true
		}
	}
	return 0, false
}

// anchorPrefix is the opening of an anchor, cut on a word boundary so the
// fragment is one a renderer would not have broken up mid-token.
func anchorPrefix(anchor string) string {
	const want = 24
	if len(anchor) <= want {
		return anchor
	}
	cut := strings.LastIndex(anchor[:want], " ")
	if cut < 8 {
		cut = want
	}
	return anchor[:cut]
}

// normalizeSpace flattens every run of whitespace to a single space and trims the
// ends.
//
// Delta expands tabs -- four spaces by default -- so a Go line indented or
// aligned with tabs is not the same string in the patch and on the screen. Two
// lines that differ only in how their whitespace is spelled are the same line as
// far as finding a hunk goes.
func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// currentHunk is the hunk the pane is scrolled to: the last one starting at or
// above the top row. Derived from the scroll offset rather than remembered from
// the last jump, so scrolling by hand cannot make the count lie.
func currentHunk(rows []int, yOffset int) int {
	current := 0
	for i, row := range rows {
		if row <= yOffset {
			current = i
		}
	}
	return current
}

// nextHunkRow is the row to scroll to for a step of delta hunks from yOffset.
// It reports false when there is nowhere to go, so the key can do nothing rather
// than pretend.
func nextHunkRow(rows []int, yOffset, delta int) (int, bool) {
	if len(rows) == 0 {
		return 0, false
	}
	if delta > 0 {
		for _, row := range rows {
			if row > yOffset {
				return row, true
			}
		}
		return 0, false
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i] < yOffset {
			return rows[i], true
		}
	}
	return 0, false
}
