package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pane is reachable from a fuzzy finder and a search box, so "every path
// came from git" is a property that holds only until someone adds a fourth way
// in. These are the paths that must never resolve.
func TestResolveInWorktreeRefusesEscapes(t *testing.T) {
	wt := t.TempDir()
	write(t, wt, "keep.txt", "inside\n")

	for _, path := range []string{
		"../outside.txt",
		"../../etc/passwd",
		"a/../../outside.txt",
		"/etc/passwd",
		"",
	} {
		if got, err := ResolveInWorktree(wt, path); err == nil {
			t.Errorf("%q resolved to %q, want it refused", path, got)
		}
	}

	// And the ordinary case still works, including a path that walks out of a
	// subdirectory and back in.
	for _, path := range []string{"keep.txt", "./keep.txt", "sub/../keep.txt"} {
		got, err := ResolveInWorktree(wt, path)
		if err != nil {
			t.Errorf("%q was refused: %v", path, err)
			continue
		}
		if filepath.Base(got) != "keep.txt" {
			t.Errorf("%q resolved to %q", path, got)
		}
	}
}

// A symlink out of the worktree is refused rather than followed. An agent's
// worktree is not a place to assume every link was put there on purpose.
func TestResolveInWorktreeRefusesSymlinkOut(t *testing.T) {
	wt := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(wt, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, err := ResolveInWorktree(wt, "link.txt"); err == nil {
		t.Errorf("a link out of the worktree resolved to %q, want it refused", got)
	}
}

func TestReadFile(t *testing.T) {
	wt := t.TempDir()
	write(t, wt, "hello.txt", "one\ntwo\n")

	got, err := ReadFile(wt, "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Errorf("read %q", got)
	}

	// A binary file has no lines to show, and says so as its own error rather
	// than arriving in the pane as mojibake.
	if err := os.WriteFile(filepath.Join(wt, "logo.png"), []byte("\x89PNG\x00\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	var binErr *BinaryError
	if _, err := ReadFile(wt, "logo.png"); !errors.As(err, &binErr) {
		t.Errorf("binary file gave %v, want a BinaryError", err)
	}

	// A file too big to review is refused whole rather than truncated: a prefix
	// read as if it were the file is the worse failure.
	big := make([]byte, maxFileBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(wt, "huge.txt"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	var bigErr *FileTooBigError
	if _, err := ReadFile(wt, "huge.txt"); !errors.As(err, &bigErr) {
		t.Errorf("huge file gave %v, want a FileTooBigError", err)
	}

	if _, err := ReadFile(wt, "nope.txt"); err == nil {
		t.Error("a missing file read without error")
	}
}

// The finder offers what is in the repo, not what the repo builds. A file
// finder that offers node_modules is a file finder nobody uses twice.
func TestListFilesRespectsGitignore(t *testing.T) {
	dir := testRepo(t)
	write(t, dir, ".gitignore", "build/\n*.log\n")
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "build/output.bin", "generated\n")
	write(t, dir, "noisy.log", "chatter\n")
	write(t, dir, "brand-new.go", "package main\n")

	files, err := ListFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, f := range files {
		has[f] = true
	}
	// Tracked, and untracked-but-not-ignored.
	for _, want := range []string{"keep.txt", "brand-new.go", ".gitignore"} {
		if !has[want] {
			t.Errorf("%s missing from the list: %v", want, files)
		}
	}
	for _, unwanted := range []string{"build/output.bin", "noisy.log"} {
		if has[unwanted] {
			t.Errorf("%s was offered despite being ignored", unwanted)
		}
	}
}

func TestParseGrep(t *testing.T) {
	out := strings.Join([]string{
		"internal/ui/model.go:42:\tfunc (m Model) render() string {",
		"internal/ui/diff.go:7:import \"strings\"",
		// The matching text routinely contains colons; only the first two
		// fields are ours.
		"README.md:3:see http://example.com:8080/docs",
		"", // trailing newline
	}, "\n")

	hits := parseGrep(out, 10)
	if len(hits) != 3 {
		t.Fatalf("parsed %d hits, want 3: %+v", len(hits), hits)
	}
	if hits[0].Path != "internal/ui/model.go" || hits[0].Line != 42 {
		t.Errorf("first hit = %+v", hits[0])
	}
	if hits[2].Text != "see http://example.com:8080/docs" {
		t.Errorf("a colon in the text was eaten: %q", hits[2].Text)
	}
}

// A row whose line number will not parse is dropped rather than guessed at: a
// result list that sends you to the wrong line is worse than one row short.
func TestParseGrepDropsUnparseableRows(t *testing.T) {
	hits := parseGrep("weird:notanumber:text\ngood.go:1:fine\n", 10)
	if len(hits) != 1 || hits[0].Path != "good.go" {
		t.Errorf("got %+v, want only the parseable row", hits)
	}
}

func TestParseGrepHonorsTheLimit(t *testing.T) {
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "a.go:1:match")
	}
	if got := parseGrep(strings.Join(lines, "\n"), 3); len(got) != 3 {
		t.Errorf("got %d hits, want the limit of 3", len(got))
	}
}

// Whichever tool is installed, the answer has to be the same shape -- and
// "nothing matched" has to be an answer rather than a failure, since both tools
// exit 1 to say it.
func TestGrepOnRealRepo(t *testing.T) {
	dir := testRepo(t)
	write(t, dir, "findme.go", "package main\n\nfunc Needle() {}\n")

	hits, err := Grep(context.Background(), dir, "Needle", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	if hits[0].Path != "findme.go" || hits[0].Line != 3 {
		t.Errorf("hit = %+v, want findme.go:3", hits[0])
	}

	// Lower case ignores case; a capital means that capital.
	if got, err := Grep(context.Background(), dir, "needle", 50); err != nil || len(got) != 1 {
		t.Errorf("smart-case lower query got %d hits, err %v", len(got), err)
	}
	if got, err := Grep(context.Background(), dir, "NEEDLE", 50); err != nil || len(got) != 0 {
		t.Errorf("capitalized query got %d hits, want none; err %v", len(got), err)
	}

	// No matches is an answer, not an error.
	got, err := Grep(context.Background(), dir, "nothinghasthisstring", 50)
	if err != nil {
		t.Errorf("a search with no matches failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d hits for a string that is not there", len(got))
	}

	// The query is taken literally, so regex metacharacters are just
	// characters rather than a syntax error in front of someone who typed a
	// function signature.
	if _, err := Grep(context.Background(), dir, "func Needle(", 50); err != nil {
		t.Errorf("a query with an unbalanced bracket failed: %v", err)
	}
}

// A search stops as soon as it has the rows the picker can show, which means
// killing the tool mid-write. The risk in that is the abandoned process: a
// non-zero exit from a search that was cut short on purpose says nothing about
// the search, and must not turn a full result list into an error.
func TestGrepStopsAtTheLimitWithoutFailing(t *testing.T) {
	dir := testRepo(t)
	// Enough matches, spread over enough files, that the limit is reached well
	// before either tool has finished walking.
	for i := 0; i < 40; i++ {
		var b strings.Builder
		for j := 0; j < 40; j++ {
			b.WriteString("needle\n")
		}
		write(t, dir, fmt.Sprintf("f%d.txt", i), b.String())
	}

	const limit = 25
	hits, err := Grep(context.Background(), dir, "needle", limit)
	if err != nil {
		t.Fatalf("a search that was cut short reported an error: %v", err)
	}
	if len(hits) != limit {
		t.Fatalf("got %d hits, want exactly the limit of %d", len(hits), limit)
	}
	// The rows still have to be whole ones: a row assembled from a half-written
	// line would send the pane to the wrong place.
	for _, h := range hits {
		if h.Path == "" || h.Line == 0 || h.Text != "needle" {
			t.Errorf("truncated row: %+v", h)
		}
	}
}
