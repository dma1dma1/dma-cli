// Package gitx wraps the git CLI. Every invocation is explicitly scoped with
// `git -C <path>`; the TUI's own working directory is unrelated to any
// session's repo and is never relied upon.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Error carries the stderr of a failed git invocation, which is what the user
// actually needs to see.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), firstLine(msg))
}

func (e *Error) Unwrap() error { return e.Err }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Run executes git in dir and returns trimmed stdout.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Keep git non-interactive: a credential prompt inside a TUI would hang
	// the whole board with no visible cause.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return stdout.String(), &Error{Args: full, Stderr: stderr.String(), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// RunRaw is Run without trimming, for diff output where trailing bytes matter.
func RunRaw(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		return stdout.String(), &Error{Args: full, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// IsRepo reports whether path is inside a git working tree.
func IsRepo(ctx context.Context, path string) bool {
	out, err := Run(ctx, path, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// TopLevel returns the root of the working tree containing path.
func TopLevel(ctx context.Context, path string) (string, error) {
	return Run(ctx, path, "rev-parse", "--show-toplevel")
}

// DefaultBranch resolves the repo's default branch from origin's HEAD, falling
// back to main then master.
func DefaultBranch(ctx context.Context, repoPath string) string {
	if out, err := Run(ctx, repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if _, b, ok := strings.Cut(strings.TrimSpace(out), "/"); ok && b != "" {
			return b
		}
	}
	for _, cand := range []string{"main", "master"} {
		if _, err := Run(ctx, repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+cand); err == nil {
			return cand
		}
	}
	return "main"
}

// RemoteSlug derives "owner/name" from the origin URL, for `gh -R`. Handles
// both SSH (git@host:owner/name.git) and HTTPS forms.
func RemoteSlug(ctx context.Context, repoPath string) (string, error) {
	out, err := Run(ctx, repoPath, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return ParseRemote(strings.TrimSpace(out))
}

// ParseRemote extracts owner/name from a git remote URL.
func ParseRemote(url string) (string, error) {
	u := strings.TrimSpace(url)
	if u == "" {
		return "", fmt.Errorf("empty remote url")
	}
	u = strings.TrimSuffix(u, ".git")

	if i := strings.Index(u, "://"); i >= 0 {
		// URL forms: https://host/owner/name, ssh://git@host/owner/name
		u = u[i+3:]
	} else if at := strings.Index(u, "@"); at >= 0 {
		// scp-like syntax: git@github.com:owner/name
		if c := strings.Index(u[at:], ":"); c >= 0 {
			rest := strings.Trim(u[at+c+1:], "/")
			if parts := strings.Split(rest, "/"); len(parts) >= 2 {
				return strings.Join(parts[len(parts)-2:], "/"), nil
			}
			return "", fmt.Errorf("cannot parse remote %q", url)
		}
	}
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	parts := strings.Split(strings.Trim(u, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/"), nil
	}
	return "", fmt.Errorf("cannot parse remote %q", url)
}

// BranchExists reports whether a local branch of that name is present.
func BranchExists(ctx context.Context, repoPath, branch string) bool {
	_, err := Run(ctx, repoPath, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// RefExists reports whether a fully qualified ref resolves.
func RefExists(ctx context.Context, repoPath, ref string) bool {
	_, err := Run(ctx, repoPath, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// Fetch updates origin's tracking ref for one branch. Callers treat failure as
// a warning: a laptop offline in a train still has to be able to start a
// session, it just starts from whatever was last fetched.
func Fetch(ctx context.Context, repoPath, branch string) error {
	_, err := Run(ctx, repoPath, "fetch", "--quiet", "origin", branch)
	return err
}

// StartPoint is the ref a new worktree should be cut from: origin/<base> when
// that tracking ref exists, the local branch otherwise.
//
// The local branch is whatever the last pull happened to leave behind, which on
// a repo used mainly through this tool is nothing at all. Starting sessions
// there quietly puts every agent a few days behind.
func StartPoint(ctx context.Context, repoPath, base string) string {
	if base == "" {
		return base
	}
	if RefExists(ctx, repoPath, "refs/remotes/origin/"+base) {
		return "origin/" + base
	}
	return base
}

// AddDetachedWorktree creates a worktree at wtPath with HEAD detached at start.
//
// No branch is created here. Branch names are the agent's to choose once it
// knows what the work turned out to be; a name derived from the task title up
// front is a guess made at the moment of least information. The board adopts
// whatever branch it later finds in the worktree -- see ops.Observe.
func AddDetachedWorktree(ctx context.Context, repoPath, wtPath, start string) error {
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return err
	}
	_, err := Run(ctx, repoPath, "worktree", "add", "--detach", wtPath, start)
	return err
}

// CurrentBranch returns the branch a worktree is on, or "" when its HEAD is
// detached or the worktree is unreadable.
func CurrentBranch(ctx context.Context, wt string) string {
	out, err := Run(ctx, wt, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RemoveWorktree detaches a worktree from the repo. force is only ever passed
// after explicit user confirmation.
func RemoveWorktree(ctx context.Context, repoPath, wtPath string, force bool) error {
	args := []string{"worktree", "remove", wtPath}
	if force {
		args = append(args, "--force")
	}
	_, err := Run(ctx, repoPath, args...)
	return err
}

func PruneWorktrees(ctx context.Context, repoPath string) error {
	_, err := Run(ctx, repoPath, "worktree", "prune")
	return err
}

// DeleteBranch removes a local branch. force maps to -D and requires explicit
// confirmation upstream.
func DeleteBranch(ctx context.Context, repoPath, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := Run(ctx, repoPath, "branch", flag, branch)
	return err
}

// StatusPorcelain returns the raw porcelain status of a worktree.
func StatusPorcelain(ctx context.Context, wt string) (string, error) {
	return Run(ctx, wt, "status", "--porcelain")
}

// IsDirty reports whether a worktree has uncommitted changes.
func IsDirty(ctx context.Context, wt string) (bool, error) {
	out, err := StatusPorcelain(ctx, wt)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// UntrackedFiles lists files git is not tracking and not ignoring.
//
// A coding agent's most common output is a brand new file, which plain
// `git diff` does not show at all. Anything reporting "what has this agent
// done" has to account for them explicitly.
func UntrackedFiles(ctx context.Context, wt string) ([]string, error) {
	out, err := Run(ctx, wt, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// maxUntrackedInDiff bounds how many new files are rendered, so a stray
// build directory cannot produce a megabyte of diff.
const maxUntrackedInDiff = 50

// baseRef resolves the ref a session's work is measured against, preferring
// origin/<base> over the local branch of the same name.
//
// Worktrees are cut from the fetched remote tip, so a local base branch left
// behind sits *before* the point the session started from. Used as a merge
// base it would credit the session with every commit the local ref is missing,
// padding both the diff stat and the has-anything-to-push check.
func baseRef(ctx context.Context, wt, base string) string {
	if base == "" || strings.HasPrefix(base, "origin/") {
		return base
	}
	if RefExists(ctx, wt, "refs/remotes/origin/"+base) {
		return "origin/" + base
	}
	return base
}

// DiffStat sums added/removed lines for base...HEAD plus uncommitted changes,
// including untracked files.
func DiffStat(ctx context.Context, wt, base string) (added, removed int, err error) {
	base = baseRef(ctx, wt, base)
	ranges := [][]string{
		{"diff", "--numstat", base + "...HEAD"},
		{"diff", "--numstat", "HEAD"},
	}
	for _, args := range ranges {
		out, e := Run(ctx, wt, args...)
		if e != nil {
			// A missing base ref shouldn't blank the whole stat.
			continue
		}
		a, r := parseNumstat(out)
		added += a
		removed += r
	}

	files, _ := UntrackedFiles(ctx, wt)
	for i, f := range files {
		if i >= maxUntrackedInDiff {
			break
		}
		// --no-index against /dev/null counts a new file's lines without
		// touching the index.
		out, _ := Run(ctx, wt, "diff", "--numstat", "--no-index", "--", os.DevNull, f)
		a, r := parseNumstat(out)
		added += a
		removed += r
	}
	return added, removed, nil
}

func parseNumstat(out string) (added, removed int) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Binary files report "-" for both counts.
		if a, err := strconv.Atoi(fields[0]); err == nil {
			added += a
		}
		if r, err := strconv.Atoi(fields[1]); err == nil {
			removed += r
		}
	}
	return added, removed
}

// DiffMode selects which diff the detail pane renders.
type DiffMode int

const (
	// DiffUncommitted shows working-tree changes against HEAD.
	DiffUncommitted DiffMode = iota
	// DiffBranch shows the full branch contribution, base...HEAD.
	DiffBranch
)

// diffRange is the revision argument a mode measures against, empty when the
// diff needs none.
//
// Uncommitted work is compared against HEAD rather than the index, so a file an
// agent staged but did not commit still appears. Plain `git diff` would hide it,
// and a file listed in the tree whose diff came back empty reads as a bug. An
// unborn HEAD has nothing to compare against, so the range is dropped there.
func diffRange(ctx context.Context, wt, base string, mode DiffMode) string {
	if mode == DiffBranch {
		return baseRef(ctx, wt, base) + "...HEAD"
	}
	if RefExists(ctx, wt, "HEAD") {
		return "HEAD"
	}
	return ""
}

// ChangeStatus is what a diff did to a path.
type ChangeStatus byte

const (
	ChangeModified  ChangeStatus = 'M'
	ChangeAdded     ChangeStatus = 'A'
	ChangeDeleted   ChangeStatus = 'D'
	ChangeRenamed   ChangeStatus = 'R'
	ChangeCopied    ChangeStatus = 'C'
	ChangeUntracked ChangeStatus = '?'
)

func (c ChangeStatus) String() string {
	if c == 0 {
		return "M"
	}
	return string(rune(c))
}

// ChangedFile is one path in a session's diff: what happened to it, and how
// much of it moved.
type ChangedFile struct {
	Path string
	// OldPath is where a rename or copy came from.
	OldPath string
	Status  ChangeStatus
	Added   int
	Removed int
	// Binary marks a file whose lines git will not count.
	Binary bool
	// Untracked marks a file git does not know about, which needs --no-index
	// against /dev/null to diff at all.
	Untracked bool
}

// ChangedFiles lists the paths a session's diff touches, with the counts the
// file tree labels them by.
//
// The list and the per-file diffs come from the same range, so a row can never
// name a file whose diff renders empty.
func ChangedFiles(ctx context.Context, wt, base string, mode DiffMode) ([]ChangedFile, error) {
	rev := diffRange(ctx, wt, base, mode)

	numArgs := []string{"diff", "--numstat", "-z"}
	nameArgs := []string{"diff", "--name-status", "-z"}
	if rev != "" {
		numArgs = append(numArgs, rev)
		nameArgs = append(nameArgs, rev)
	}

	numOut, err := RunRaw(ctx, wt, numArgs...)
	if err != nil {
		return nil, err
	}
	files := parseNumstatZ(numOut)

	// The status letter is a label on a row that already exists. Losing it is
	// worth less than losing the row, so a failure here is not fatal.
	if nameOut, err := RunRaw(ctx, wt, nameArgs...); err == nil {
		applyNameStatus(files, parseNameStatusZ(nameOut))
	}

	if mode == DiffUncommitted {
		// base...HEAD is committed history; untracked files are not part of it.
		untracked, _ := UntrackedFiles(ctx, wt)
		for _, p := range untracked {
			files = append(files, untrackedChange(wt, p))
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// parseNumstatZ reads `git diff --numstat -z`, whose records are
// "<add>\t<del>\t<path>\0" -- except for a rename, where the path field is
// empty and the two paths follow as their own NUL-terminated fields.
func parseNumstatZ(out string) []ChangedFile {
	fields := strings.Split(out, "\x00")
	var files []ChangedFile
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		f := ChangedFile{Path: parts[2]}
		// Binary files report "-" for both counts.
		a, aErr := strconv.Atoi(parts[0])
		r, rErr := strconv.Atoi(parts[1])
		f.Added, f.Removed = a, r
		f.Binary = aErr != nil && rErr != nil
		if f.Path == "" {
			// The record terminates with a NUL of its own, so the field list
			// always ends in an empty string -- a truncated rename reaches the
			// bounds check with room to spare and has to be caught by the empty
			// path it produces instead.
			if i+2 >= len(fields) || fields[i+1] == "" || fields[i+2] == "" {
				continue
			}
			f.OldPath, f.Path = fields[i+1], fields[i+2]
			f.Status = ChangeRenamed
			i += 2
		}
		files = append(files, f)
	}
	return files
}

// nameStatus is one record of `git diff --name-status -z`.
type nameStatus struct {
	path    string
	oldPath string
	status  ChangeStatus
}

// parseNameStatusZ reads `git diff --name-status -z`, where a status field is
// followed by one path -- or by two, old then new, when it is a rename or copy.
func parseNameStatusZ(out string) []nameStatus {
	fields := strings.Split(out, "\x00")
	var records []nameStatus
	for i := 0; i < len(fields); i++ {
		code := fields[i]
		if code == "" {
			continue
		}
		status := ChangeStatus(code[0])
		// R and C carry a similarity score, e.g. R075, and two paths. An empty
		// path means the record was cut short: the trailing NUL of the last
		// record leaves an empty final field, so bounds alone cannot tell.
		if status == ChangeRenamed || status == ChangeCopied {
			if i+2 >= len(fields) || fields[i+1] == "" || fields[i+2] == "" {
				return records
			}
			records = append(records, nameStatus{oldPath: fields[i+1], path: fields[i+2], status: status})
			i += 2
			continue
		}
		if i+1 >= len(fields) || fields[i+1] == "" {
			return records
		}
		records = append(records, nameStatus{path: fields[i+1], status: status})
		i++
	}
	return records
}

// applyNameStatus labels the numstat rows, which carry counts but no status.
func applyNameStatus(files []ChangedFile, records []nameStatus) {
	byPath := make(map[string]nameStatus, len(records))
	for _, r := range records {
		byPath[r.path] = r
	}
	for i := range files {
		r, ok := byPath[files[i].Path]
		if !ok {
			continue
		}
		files[i].Status = r.status
		if r.oldPath != "" {
			files[i].OldPath = r.oldPath
		}
	}
}

// untrackedChange describes a brand new file, counting its lines by reading it
// rather than spending a git process per file. `git diff --no-index` would
// report the same number for a file whose every line is an addition.
func untrackedChange(wt, path string) ChangedFile {
	f := ChangedFile{Path: path, Status: ChangeUntracked, Untracked: true}
	data, err := os.ReadFile(filepath.Join(wt, path))
	if err != nil {
		return f
	}
	f.Added, f.Binary = countLines(data)
	return f
}

// countLines counts a new file's lines and reports whether it looks binary. A
// NUL byte early in the file is how git makes the same call.
func countLines(data []byte) (lines int, binary bool) {
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true
	}
	if len(data) == 0 {
		return 0, false
	}
	lines = bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines, false
}

// DiffOpts tunes how a diff is rendered.
type DiffOpts struct {
	// Width is the column count the diff has to fit. It is handed to delta so
	// its wrapping and its two columns match the pane the output lands in.
	Width int
	// SideBySide asks for two columns. Only delta can produce them; plain git
	// has no such mode, so this is ignored when delta is not installed.
	SideBySide bool
}

// DiffTarget narrows a diff to part of the tree. The zero value is the whole
// diff, which is what the pane shows before a file is picked.
type DiffTarget struct {
	// Path is a file, or a directory prefix for a whole subtree. Empty means
	// everything.
	Path string
	// Untracked marks a path git is not tracking. It has no diff of its own,
	// so it is compared against /dev/null instead.
	Untracked bool
}

// Diff renders a colored diff, piping through delta when it is on PATH.
// Parsing and re-rendering diffs ourselves would be a large amount of code for
// no gain over what git already does.
func Diff(ctx context.Context, wt, base string, mode DiffMode, t DiffTarget, opts DiffOpts) (string, error) {
	if t.Untracked {
		return diffUntrackedPath(ctx, wt, t.Path, opts), nil
	}
	tracked, err := diffTracked(ctx, wt, base, mode, t.Path, opts)
	if err != nil {
		return "", err
	}
	if mode == DiffBranch {
		// base...HEAD is committed history; untracked files are not part of it.
		return tracked, nil
	}
	return tracked + diffUntracked(ctx, wt, t.Path, opts), nil
}

// diffUntracked renders the new files under prefix, which plain `git diff`
// omits entirely. An empty prefix takes them all.
func diffUntracked(ctx context.Context, wt, prefix string, opts DiffOpts) string {
	files, err := UntrackedFiles(ctx, wt)
	if err != nil || len(files) == 0 {
		return ""
	}
	if prefix != "" {
		files = withinPrefix(files, prefix)
	}
	var b strings.Builder
	for i, f := range files {
		if i >= maxUntrackedInDiff {
			fmt.Fprintf(&b, "\n... and %d more new files\n", len(files)-maxUntrackedInDiff)
			break
		}
		b.WriteString(diffUntrackedPath(ctx, wt, f, opts))
	}
	return b.String()
}

// withinPrefix keeps the paths inside a directory, matching on a path boundary
// so `internal/ui` cannot claim `internal/uix`.
func withinPrefix(paths []string, prefix string) []string {
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	var out []string
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	return out
}

func diffUntrackedPath(ctx context.Context, wt, path string, opts DiffOpts) string {
	// --no-index exits 1 when the files differ, which is the normal case here,
	// so its status is deliberately ignored.
	out, _ := renderDiff(ctx, []string{"-C", wt, "-c", "color.ui=always", "diff", "--color=always",
		"--no-index", "--", os.DevNull, path}, opts)
	return out
}

func diffTracked(ctx context.Context, wt, base string, mode DiffMode, path string, opts DiffOpts) (string, error) {
	// --stat-width implies --stat, and --stat on its own *replaces* the patch
	// with a summary -- so --patch has to be asked for explicitly to get both.
	args := []string{"-C", wt, "-c", "color.ui=always", "diff", "--color=always",
		"--stat", "--stat-width=200", "--patch"}
	if rev := diffRange(ctx, wt, base, mode); rev != "" {
		args = append(args, rev)
	}
	if path != "" {
		args = append(args, "--", path)
	}
	return renderDiff(ctx, args, opts)
}

// renderDiff runs git with args and pipes the result through delta when it is
// installed.
func renderDiff(ctx context.Context, args []string, opts DiffOpts) (string, error) {
	git := exec.CommandContext(ctx, "git", args...)
	git.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if delta, err := exec.LookPath("delta"); err == nil {
		d := exec.CommandContext(ctx, delta, deltaArgs(opts)...)
		d.Env = append(os.Environ(), "DELTA_PAGER=cat")
		pipe, err := git.StdoutPipe()
		if err != nil {
			return "", err
		}
		d.Stdin = pipe
		var out, derr bytes.Buffer
		d.Stdout = &out
		d.Stderr = &derr
		if err := git.Start(); err != nil {
			return "", err
		}
		if err := d.Start(); err != nil {
			return "", err
		}
		gitErr := git.Wait()
		if err := d.Wait(); err != nil && out.Len() == 0 {
			return "", fmt.Errorf("delta: %s", strings.TrimSpace(derr.String()))
		}
		if out.Len() == 0 && gitErr != nil {
			return "", gitErr
		}
		return out.String(), nil
	}

	var stdout, stderr bytes.Buffer
	git.Stdout = &stdout
	git.Stderr = &stderr
	if err := git.Run(); err != nil && stdout.Len() == 0 {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// HasDelta reports whether delta is installed. Side-by-side is the one thing the
// review view cannot fall back on git for, so it has to be able to say why the
// key did nothing.
func HasDelta() bool {
	_, err := exec.LookPath("delta")
	return err == nil
}

// deltaArgs is how delta is asked to render into a pane of a known width.
//
// Flags given here beat the user's [delta] gitconfig, but only additively:
// side-by-side turned on there cannot be turned back off from the command line,
// so a user who has set it sees two columns whatever the view says.
func deltaArgs(opts DiffOpts) []string {
	args := []string{"--paging=never"}
	if opts.Width > 0 {
		args = append(args, fmt.Sprintf("-w=%d", opts.Width))
	}
	// Delta's defaults clip a long line before the pane ever gets the chance to
	// scroll it. Let it wrap instead, so the whole line stays reachable.
	args = append(args, "--max-line-length=0", "--wrap-max-lines=unlimited")
	if opts.SideBySide {
		args = append(args, "--side-by-side")
	}
	return args
}

// HasCommits reports whether the worktree has any commit beyond base.
func HasCommits(ctx context.Context, wt, base string) bool {
	out, err := Run(ctx, wt, "rev-list", "--count", baseRef(ctx, wt, base)+"..HEAD")
	if err != nil {
		return false
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n > 0
}

// CommitAll stages everything in the worktree and commits. It is a no-op when
// there is nothing staged.
func CommitAll(ctx context.Context, wt, message string) error {
	if _, err := Run(ctx, wt, "add", "-A"); err != nil {
		return err
	}
	out, err := Run(ctx, wt, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	_, err = Run(ctx, wt, "commit", "-m", message)
	return err
}

// Push publishes the branch and sets upstream.
func Push(ctx context.Context, wt, branch string) error {
	_, err := Run(ctx, wt, "push", "-u", "origin", branch)
	return err
}

// AddLocalExclude appends patterns to the repo's .git/info/exclude, which is
// local-only and never committed.
//
// Bootstrap artifacts and tool settings are infrastructure, not the user's
// work. Left untracked they would make every worktree read as dirty, which
// would block teardown and make the clean/dirty chip meaningless.
func AddLocalExclude(ctx context.Context, worktree string, patterns ...string) error {
	if len(patterns) == 0 {
		return nil
	}
	dir, err := Run(ctx, worktree, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(worktree, dir)
	}
	path := filepath.Join(dir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	existing := map[string]bool{}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			existing[strings.TrimSpace(line)] = true
		}
	}
	var add []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || existing[p] {
			continue
		}
		existing[p] = true
		add = append(add, p)
	}
	if len(add) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("\n# added by dma\n" + strings.Join(add, "\n") + "\n"); err != nil {
		return err
	}
	return nil
}
