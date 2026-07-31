package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// samplePatch is `git diff` output as git actually writes it -- two hunks in one
// file, the second of which only deletes. Copied from a real repo rather than
// written by hand: a patch's line counts have to agree with its lines, and the
// parser is the thing that notices when they do not.
const samplePatch = `diff --git a/panel.go b/panel.go
index d8e5452..6465bb5 100644
--- a/panel.go
+++ b/panel.go
@@ -4,6 +4,8 @@ func (m Model) chips() string {
 	line1
 	line2
 	line3
+	added line
+	another added line
 	line4
 	line5
 	line6
@@ -20,7 +22,6 @@ func (m Model) chips() string {
 	line17
 	line18
 	line19
-	line20
 	line21
 	line22
 	line23
`

func TestParseHunks(t *testing.T) {
	hunks, err := parseHunks(samplePatch)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 2 {
		t.Fatalf("parsed %d hunks, want 2: %+v", len(hunks), hunks)
	}

	first := hunks[0]
	if first.Start != 4 || first.End != 4+8-1 {
		t.Errorf("first hunk spans %d-%d, want 4-11", first.Start, first.End)
	}
	if first.Added != 2 || first.Removed != 0 {
		t.Errorf("first hunk = +%d -%d, want +2 -0", first.Added, first.Removed)
	}
	// The header is the context git puts after @@, which is what says where you
	// are without counting lines.
	if first.Header != "func (m Model) chips() string {" {
		t.Errorf("first hunk header = %q", first.Header)
	}
	// The anchor has to be a *changed* line: a context line is by definition
	// somewhere else in the file too, so searching for one finds the wrong row.
	if first.Anchor != "\tadded line" {
		t.Errorf("first hunk anchor = %q, want the first added line", first.Anchor)
	}

	second := hunks[1]
	if second.Start != 22 {
		t.Errorf("second hunk starts at %d, want 22", second.Start)
	}
	if second.Removed != 1 || second.Added != 0 {
		t.Errorf("second hunk = +%d -%d, want +0 -1", second.Added, second.Removed)
	}
	if second.Anchor != "\tline20" {
		t.Errorf("second hunk anchor = %q, want the deleted line", second.Anchor)
	}
}

// A deletion occupies no lines in the new file, so its range has to collapse
// rather than run backwards.
func TestParseHunksWholeFileDeletion(t *testing.T) {
	patch := `diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 1111111..0000000
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-one
-two
`
	hunks, err := parseHunks(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) != 1 {
		t.Fatalf("parsed %d hunks, want 1", len(hunks))
	}
	if h := hunks[0]; h.End < h.Start {
		t.Errorf("deletion spans %d-%d, which runs backwards", h.Start, h.End)
	}
}

func TestParseHunksEmpty(t *testing.T) {
	hunks, err := parseHunks("")
	if err != nil || hunks != nil {
		t.Errorf("empty patch = %+v, %v; want no hunks and no error", hunks, err)
	}
}

func TestHunkRef(t *testing.T) {
	multi := Hunk{Start: 328, End: 355}
	if got := multi.Ref("internal/ui/panel.go"); got != "internal/ui/panel.go:328-355" {
		t.Errorf("Ref = %q", got)
	}
	// A one-line change reads better without a range that repeats itself.
	single := Hunk{Start: 42, End: 42}
	if got := single.Ref("a.go"); got != "a.go:42" {
		t.Errorf("Ref = %q", got)
	}
}

// Hunks has to work on a file git is not tracking, which has no diff of its own
// at all.
func TestHunksOnRealRepo(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()

	write(t, dir, "keep.txt", "alpha\nbravo\ncharlie\ndingo\n")
	tracked, err := Hunks(ctx, dir, "main", DiffUncommitted, DiffTarget{Path: "keep.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tracked) != 1 {
		t.Fatalf("tracked file has %d hunks, want 1: %+v", len(tracked), tracked)
	}
	if tracked[0].Added != 1 {
		t.Errorf("hunk = +%d, want +1", tracked[0].Added)
	}

	write(t, dir, "new.txt", "fresh\nlines\n")
	untracked, err := Hunks(ctx, dir, "main", DiffUncommitted, DiffTarget{Path: "new.txt", Untracked: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(untracked) != 1 {
		t.Fatalf("untracked file has %d hunks, want 1: %+v", len(untracked), untracked)
	}
	if untracked[0].Added != 2 || untracked[0].Start != 1 {
		t.Errorf("untracked hunk = +%d from line %d, want +2 from 1",
			untracked[0].Added, untracked[0].Start)
	}

	// A file with nothing to say has no hunks, and that is not an error.
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("alpha\nbravo\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	none, err := Hunks(ctx, dir, "main", DiffUncommitted, DiffTarget{Path: "keep.txt"})
	if err != nil {
		t.Fatalf("unchanged file errored: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unchanged file has %d hunks, want none", len(none))
	}
}
