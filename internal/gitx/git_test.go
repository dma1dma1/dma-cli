package gitx

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]string{
		"git@github.com:owner/name.git":         "owner/name",
		"git@github.com:owner/name":             "owner/name",
		"https://github.com/owner/name.git":     "owner/name",
		"https://github.com/owner/name":         "owner/name",
		"ssh://git@github.com/owner/name.git":   "owner/name",
		"git@ssh.github.com:443/owner/name.git": "owner/name",
	}
	for in, want := range cases {
		got, err := ParseRemote(in)
		if err != nil {
			t.Errorf("ParseRemote(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseRemote(%q) = %q, want %q", in, got, want)
		}
	}

	if _, err := ParseRemote(""); err == nil {
		t.Error("empty remote should error")
	}
}

func TestParseNumstat(t *testing.T) {
	out := "10\t2\tfile.go\n5\t0\tother.go\n-\t-\timage.png\n"
	added, removed := parseNumstat(out)
	if added != 15 || removed != 2 {
		t.Fatalf("parseNumstat = +%d -%d, want +15 -2", added, removed)
	}
}

func TestParseNumstatIgnoresGarbage(t *testing.T) {
	added, removed := parseNumstat("not a numstat line\n\n")
	if added != 0 || removed != 0 {
		t.Fatalf("parseNumstat = +%d -%d, want zero", added, removed)
	}
}

// The -z forms are what the file tree is built from, and their rename records
// break the one-record-per-field rule, so they are worth pinning exactly. These
// strings are real `git diff` output.
func TestParseNumstatZ(t *testing.T) {
	out := "1\t0\tadded.txt\x001\t0\tkeep.txt\x00-\t-\timage.png\x004\t1\t\x00old.txt\x00new.txt\x00"
	files := parseNumstatZ(out)
	if len(files) != 4 {
		t.Fatalf("parsed %d files, want 4: %+v", len(files), files)
	}

	if files[0].Path != "added.txt" || files[0].Added != 1 || files[0].Removed != 0 {
		t.Errorf("first file = %+v", files[0])
	}
	if !files[2].Binary {
		t.Errorf("image.png should be binary: %+v", files[2])
	}

	renamed := files[3]
	if renamed.Path != "new.txt" || renamed.OldPath != "old.txt" {
		t.Errorf("rename = %q from %q, want new.txt from old.txt", renamed.Path, renamed.OldPath)
	}
	if renamed.Status != ChangeRenamed {
		t.Errorf("rename status = %s, want R", renamed.Status)
	}
	if renamed.Added != 4 || renamed.Removed != 1 {
		t.Errorf("rename counts = +%d -%d, want +4 -1", renamed.Added, renamed.Removed)
	}
}

func TestParseNameStatusZ(t *testing.T) {
	out := "A\x00added.txt\x00M\x00keep.txt\x00R075\x00old.txt\x00new.txt\x00D\x00gone.txt\x00"
	records := parseNameStatusZ(out)
	if len(records) != 4 {
		t.Fatalf("parsed %d records, want 4: %+v", len(records), records)
	}
	want := []nameStatus{
		{path: "added.txt", status: ChangeAdded},
		{path: "keep.txt", status: ChangeModified},
		{path: "new.txt", oldPath: "old.txt", status: ChangeRenamed},
		{path: "gone.txt", status: ChangeDeleted},
	}
	for i, w := range want {
		if records[i] != w {
			t.Errorf("record %d = %+v, want %+v", i, records[i], w)
		}
	}
}

// A truncated record must not index past the end of the field list.
func TestParseZTruncated(t *testing.T) {
	if files := parseNumstatZ("1\t0\t\x00old.txt\x00"); len(files) != 0 {
		t.Errorf("truncated rename parsed as %+v", files)
	}
	if records := parseNameStatusZ("R075\x00old.txt\x00"); len(records) != 0 {
		t.Errorf("truncated rename parsed as %+v", records)
	}
	if records := parseNameStatusZ("M\x00"); len(records) != 0 {
		t.Errorf("statusless path parsed as %+v", records)
	}
}

func TestApplyNameStatus(t *testing.T) {
	files := []ChangedFile{{Path: "added.txt"}, {Path: "keep.txt"}, {Path: "untouched.txt"}}
	applyNameStatus(files, []nameStatus{
		{path: "added.txt", status: ChangeAdded},
		{path: "keep.txt", status: ChangeModified},
	})
	if files[0].Status != ChangeAdded {
		t.Errorf("added.txt status = %s, want A", files[0].Status)
	}
	// A row no status record mentions keeps the zero value, which reads as M.
	if files[2].Status.String() != "M" {
		t.Errorf("unlabelled row = %s, want M", files[2].Status)
	}
}

func TestCountLines(t *testing.T) {
	cases := []struct {
		name   string
		data   string
		lines  int
		binary bool
	}{
		{"trailing newline", "a\nb\nc\n", 3, false},
		{"no trailing newline", "a\nb\nc", 3, false},
		{"empty", "", 0, false},
		{"one line", "only\n", 1, false},
		{"binary", "png\x00\x01data", 0, true},
	}
	for _, c := range cases {
		lines, binary := countLines([]byte(c.data))
		if lines != c.lines || binary != c.binary {
			t.Errorf("%s: countLines = %d lines, binary %v; want %d, %v",
				c.name, lines, binary, c.lines, c.binary)
		}
	}
}

func TestWithinPrefix(t *testing.T) {
	paths := []string{"internal/ui/panel.go", "internal/uix/other.go", "internal/ui/box.go", "cmd/main.go"}
	got := withinPrefix(paths, "internal/ui")
	want := []string{"internal/ui/panel.go", "internal/ui/box.go"}
	if len(got) != len(want) {
		t.Fatalf("withinPrefix = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("withinPrefix = %v, want %v", got, want)
		}
	}
}

// testRepo builds a real repo with one commit. The -z parsers are unit tested
// above; this is here so the wiring around them -- which range each mode asks
// for, and whether untracked files come along -- is exercised against real git.
func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		if _, err := Run(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	// Distinctive words rather than a, b, c: the diff assertions below look for
	// these lines in rendered output, and single letters turn up inside file names.
	write(t, dir, "keep.txt", "alpha\nbravo\ncharlie\n")
	write(t, dir, "old.txt", "one\ntwo\n")
	write(t, dir, "gone.txt", "bye\n")
	if _, err := Run(ctx, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "commit", "-q", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ansiSeq matches the color escapes git writes, which sit between the +/- marker
// and the line content and so break a naive substring check.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func changedByPath(files []ChangedFile) map[string]ChangedFile {
	byPath := make(map[string]ChangedFile, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	return byPath
}

func TestChangedFilesUncommitted(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()

	write(t, dir, "keep.txt", "alpha\nbravo\ncharlie\ndingo\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new.txt", "fresh\nlines\n")
	// Staged but not committed: the range is HEAD precisely so this still shows.
	write(t, dir, "staged.txt", "staged\n")
	if _, err := Run(ctx, dir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}

	files, err := ChangedFiles(ctx, dir, "main", DiffUncommitted)
	if err != nil {
		t.Fatal(err)
	}
	byPath := changedByPath(files)

	if got := byPath["keep.txt"]; got.Added != 1 || got.Status != ChangeModified {
		t.Errorf("keep.txt = %+v, want +1 M", got)
	}
	if got := byPath["gone.txt"]; got.Status != ChangeDeleted {
		t.Errorf("gone.txt = %+v, want D", got)
	}
	if got, ok := byPath["staged.txt"]; !ok || got.Status != ChangeAdded {
		t.Errorf("staged.txt = %+v (present %v), want A", got, ok)
	}
	newFile, ok := byPath["new.txt"]
	if !ok || !newFile.Untracked || newFile.Status != ChangeUntracked || newFile.Added != 2 {
		t.Errorf("new.txt = %+v (present %v), want untracked +2", newFile, ok)
	}

	// The list is sorted, so a tree built from it is stable between refreshes.
	for i := 1; i < len(files); i++ {
		if files[i-1].Path > files[i].Path {
			t.Fatalf("files not sorted: %q before %q", files[i-1].Path, files[i].Path)
		}
	}
}

func TestChangedFilesBranchSeesRenamesNotUntracked(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()

	if _, err := Run(ctx, dir, "checkout", "-q", "-b", "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "mv", "old.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "commit", "-q", "-am", "rename"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "untracked.txt", "not committed\n")

	files, err := ChangedFiles(ctx, dir, "main", DiffBranch)
	if err != nil {
		t.Fatal(err)
	}
	byPath := changedByPath(files)

	renamed, ok := byPath["renamed.txt"]
	if !ok || renamed.Status != ChangeRenamed || renamed.OldPath != "old.txt" {
		t.Errorf("renamed.txt = %+v (present %v), want R from old.txt", renamed, ok)
	}
	// Committed history has no room for a file git is not tracking.
	if _, ok := byPath["untracked.txt"]; ok {
		t.Error("untracked file listed in the branch diff")
	}
}

// A file listed in the tree must render a diff, otherwise the row is a dead end.
func TestDiffPerFile(t *testing.T) {
	dir := testRepo(t)
	ctx := context.Background()
	write(t, dir, "keep.txt", "alpha\nbravo\ncharlie\ndingo\n")
	write(t, dir, "new.txt", "fresh\n")

	tracked, err := Diff(ctx, dir, "main", DiffUncommitted, DiffTarget{Path: "keep.txt"}, DiffOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// The patch itself, not just the summary: git's --stat-width implies --stat,
	// which replaces the patch unless --patch is asked for too.
	//
	// Asserted on the file's *content* rather than on "+d" or "@@": with delta
	// installed the output has been through it, and delta renders an addition as a
	// background color with no marker and replaces the hunk header with its own
	// decoration. A diffstat, on the other hand, never contains the lines.
	body := stripANSI(tracked)
	for _, line := range []string{"alpha", "bravo", "charlie", "dingo"} {
		if !strings.Contains(body, line) {
			t.Errorf("keep.txt diff is missing line %q, so it is a stat and not a diff:\n%s", line, tracked)
		}
	}
	if strings.Contains(tracked, "new.txt") {
		t.Errorf("keep.txt diff leaked another file:\n%s", tracked)
	}

	untracked, err := Diff(ctx, dir, "main", DiffUncommitted, DiffTarget{Path: "new.txt", Untracked: true}, DiffOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(untracked, "fresh") {
		t.Errorf("untracked diff missing its content:\n%s", untracked)
	}

	// The whole diff is what the pane shows before a file is picked, and it has
	// to include the new file that plain `git diff` omits.
	all, err := Diff(ctx, dir, "main", DiffUncommitted, DiffTarget{}, DiffOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "keep.txt") || !strings.Contains(all, "new.txt") {
		t.Errorf("whole diff missing a file:\n%s", all)
	}
}

// A rendered diff has to arrive with its lines numbered whichever renderer drew
// it: git puts the numbers in the hunk header and nowhere else, so the margin is
// added on the way out.
func TestDiffCarriesLineNumbers(t *testing.T) {
	if HasDelta() {
		t.Skip("delta draws the margin itself")
	}
	dir := testRepo(t)
	write(t, dir, "keep.txt", "alpha\nbravo\ncharlie\ndingo\n")

	out, err := Diff(context.Background(), dir, "main", DiffUncommitted, DiffTarget{Path: "keep.txt"}, DiffOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if !strings.HasSuffix(line, "+dingo") {
			continue
		}
		// The fourth line of the new file, and no line at all in the old one.
		if m := marginPattern.FindStringSubmatch(line); m == nil || m[1] != "" || m[2] != "4" {
			t.Errorf("added row = %q, want it numbered as line 4 of the new file", line)
		}
		return
	}
	t.Errorf("the added line is not in the rendered diff:\n%s", out)
}

func TestDeltaArgs(t *testing.T) {
	args := strings.Join(deltaArgs(DiffOpts{Width: 80}), " ")
	if !strings.Contains(args, "-w=80") {
		t.Errorf("width not passed to delta: %s", args)
	}
	if strings.Contains(args, "--side-by-side") {
		t.Errorf("side-by-side asked for when it was not wanted: %s", args)
	}
	if !strings.Contains(args, "--max-line-length=0") {
		t.Errorf("long lines would be clipped: %s", args)
	}
	// The margin is the whole point of asking delta rather than reading a patch,
	// and a hunk header that repeats what the margin says is noise.
	if !strings.Contains(args, "--line-numbers") {
		t.Errorf("no line numbers asked for: %s", args)
	}
	if !strings.Contains(args, "--hunk-header-style=syntax") {
		t.Errorf("hunk headers would still carry line numbers: %s", args)
	}

	sbs := strings.Join(deltaArgs(DiffOpts{Width: 80, SideBySide: true}), " ")
	if !strings.Contains(sbs, "--side-by-side") {
		t.Errorf("side-by-side not passed to delta: %s", sbs)
	}

	// No width means no -w, rather than -w=0, which delta would read as a
	// zero-column pane.
	if strings.Contains(strings.Join(deltaArgs(DiffOpts{}), " "), "-w=") {
		t.Error("width flag emitted without a width")
	}
}
