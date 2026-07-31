package render

import "testing"

func TestReadMarginUnified(t *testing.T) {
	tests := []struct {
		name     string
		row      string
		old, new int
		body     string
		ok       bool
	}{
		{
			name: "context line has a number on both sides",
			row:  " 1 ⋮  1 │ package main",
			old:  1, new: 1, body: "package main", ok: true,
		},
		{
			name: "a deleted line has no line in the new file",
			row:  " 4 ⋮    │         return 1",
			old:  4, body: "        return 1", ok: true,
		},
		{
			name: "an added line has none in the old",
			row:  "   ⋮  4 │         return 42",
			new:  4, body: "        return 42", ok: true,
		},
		{
			name: "wide numbers keep the columns aligned",
			row:  "1198 ⋮ 1198 │ tail",
			old:  1198, new: 1198, body: "tail", ok: true,
		},
		{
			name: "the rule standing in for a hunk header is structure",
			row:  "┄┄┄┄┄┄┄┄┄ func beta() string {",
		},
		{
			name: "delta's boxed hunk header is structure, despite the glyph",
			row:  " func beta() string { │",
		},
		{
			name: "a file header is structure",
			row:  "diff --git a/a.go b/a.go",
		},
		{
			name: "content holding the glyphs is not a margin",
			row:  `        sep := "a ⋮ b │ c"`,
		},
		{
			name: "a blank row is structure",
			row:  "",
		},
		{
			name: "both columns blank is not a line of anything",
			row:  "   ⋮    │ ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := readMargin(tc.row, Unified)
			if m.ok != tc.ok {
				t.Fatalf("ok = %v, want %v", m.ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if m.old != tc.old || m.new != tc.new {
				t.Errorf("lines = %d/%d, want %d/%d", m.old, m.new, tc.old, tc.new)
			}
			if got := tc.row[m.body:]; got != tc.body {
				t.Errorf("body = %q, want %q", got, tc.body)
			}
		})
	}
}

// Side-by-side wraps the margin around the left column instead of putting it
// all at the head of the row, so the right-hand number is read backwards from
// the closing glyph rather than forwards from the opening one.
func TestReadMarginSideBySide(t *testing.T) {
	row := " 1 ⋮ package main                     1 │ package main"
	m := readMargin(row, SideBySide)
	if !m.ok {
		t.Fatal("side-by-side row read as structure")
	}
	if m.old != 1 || m.new != 1 {
		t.Errorf("lines = %d/%d, want 1/1", m.old, m.new)
	}
	// Both columns are content: a search must reach the left one, which a body
	// starting after the closing glyph would skip entirely.
	if got := row[m.body:]; got != "package main                     1 │ package main" {
		t.Errorf("body = %q, want both columns", got)
	}
}

// Reading the same row under the wrong layout must not invent line numbers. The
// caller knows which layout it asked delta for; this is the check that the
// parser is not quietly guessing.
func TestReadMarginWrongLayoutIsRefused(t *testing.T) {
	row := " 1 ⋮ package main                     1 │ package main"
	if m := readMargin(row, Unified); m.ok {
		t.Errorf("side-by-side row read as unified: old=%d new=%d", m.old, m.new)
	}
}
