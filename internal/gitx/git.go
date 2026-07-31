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

// Diff renders a colored diff, piping through delta when it is on PATH.
// Parsing and re-rendering diffs ourselves would be a large amount of code for
// no gain over what git already does.
func Diff(ctx context.Context, wt, base string, mode DiffMode) (string, error) {
	tracked, err := diffTracked(ctx, wt, base, mode)
	if err != nil {
		return "", err
	}
	if mode == DiffBranch {
		// base...HEAD is committed history; untracked files are not part of it.
		return tracked, nil
	}
	return tracked + diffUntracked(ctx, wt), nil
}

// diffUntracked renders new files, which plain `git diff` omits entirely.
func diffUntracked(ctx context.Context, wt string) string {
	files, err := UntrackedFiles(ctx, wt)
	if err != nil || len(files) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range files {
		if i >= maxUntrackedInDiff {
			fmt.Fprintf(&b, "\n... and %d more new files\n", len(files)-maxUntrackedInDiff)
			break
		}
		// --no-index exits 1 when the files differ, which is the normal case
		// here, so its status is deliberately ignored.
		out, _ := RunRaw(ctx, wt, "-c", "color.ui=always", "diff", "--color=always",
			"--no-index", "--", os.DevNull, f)
		b.WriteString(out)
	}
	return b.String()
}

func diffTracked(ctx context.Context, wt, base string, mode DiffMode) (string, error) {
	args := []string{"-C", wt, "-c", "color.ui=always", "diff", "--color=always", "--stat-width=200"}
	if mode == DiffBranch {
		args = append(args, baseRef(ctx, wt, base)+"...HEAD")
	}
	git := exec.CommandContext(ctx, "git", args...)
	git.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if delta, err := exec.LookPath("delta"); err == nil {
		d := exec.CommandContext(ctx, delta, "--color-only", "--paging=never")
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
