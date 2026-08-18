package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// This file moves work in progress from one working tree into another, which is
// what attaching an already-running agent session needs: the conversation being
// resumed remembers editing files, and a worktree cut fresh from a commit does
// not have those edits in it.
//
// Nothing here writes to the source. A directory someone is working in --
// frequently their main checkout -- is read from and left exactly as it was
// found, so an attach that goes wrong costs a worktree and not the work.

// Carried reports what a carry moved, so the caller can say so rather than
// leaving the user to guess whether anything came across.
type Carried struct {
	// Modified counts tracked files whose changes were applied.
	Modified int
	// Added counts untracked files copied over.
	Added int
}

// Any reports whether anything was carried.
func (c Carried) Any() bool { return c.Modified > 0 || c.Added > 0 }

func (c Carried) String() string {
	switch {
	case !c.Any():
		return "nothing to carry"
	case c.Added == 0:
		return fmt.Sprintf("carried %s", plural(c.Modified, "modified file"))
	case c.Modified == 0:
		return fmt.Sprintf("carried %s", plural(c.Added, "new file"))
	}
	return fmt.Sprintf("carried %s and %s", plural(c.Modified, "modified file"), plural(c.Added, "new file"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Head is the commit a working tree is sitting on.
//
// Attaching cuts the new worktree from here rather than from the base branch,
// which is what makes the carry below a plain apply: the patch was produced
// against this commit, and the tree it lands in is that same commit, so there
// is nothing for git to merge and nothing that can conflict.
func Head(ctx context.Context, wt string) (string, error) {
	return Run(ctx, wt, "rev-parse", "HEAD")
}

// Carry copies the uncommitted state of src into dst: modifications to tracked
// files as a patch, and untracked files as themselves.
//
// Ignored files are not carried, which is what keeps this from copying a
// dependency tree or a build directory across. Those are the bootstrap's job
// and it has already done it by the time this runs.
//
// A carry that partly succeeds is still reported as an error, with whatever
// landed described in the Carried it returns: the caller keeps the session --
// the agent is about to come back up in it either way -- and tells the user
// which half is missing.
func Carry(ctx context.Context, src, dst string) (Carried, error) {
	var out Carried

	// --binary so an edited image or any other non-text change survives the
	// round trip instead of arriving as "Binary files differ" and being
	// dropped on apply.
	patch, err := RunRaw(ctx, src, "diff", "--binary", "HEAD")
	if err != nil {
		return out, fmt.Errorf("read uncommitted changes: %w", err)
	}
	if strings.TrimSpace(patch) != "" {
		if err := applyPatch(ctx, dst, patch); err != nil {
			return out, err
		}
		names, err := Run(ctx, src, "diff", "--name-only", "HEAD")
		if err == nil {
			out.Modified = countLinesIn(names)
		}
	}

	untracked, err := untrackedZ(ctx, src)
	if err != nil {
		return out, fmt.Errorf("list new files: %w", err)
	}
	for _, rel := range untracked {
		if err := copyInto(src, dst, rel); err != nil {
			return out, fmt.Errorf("copy %s: %w", rel, err)
		}
		out.Added++
	}
	return out, nil
}

// applyPatch feeds a diff to git apply in the destination worktree.
//
// It goes in on stdin rather than through a temporary file so nothing is left
// behind by a failure, and it is the one git call in this package that needs
// to write to the process it starts, which is why it does not go through Run.
func applyPatch(ctx context.Context, wt, patch string) error {
	args := []string{"-C", wt, "apply", "--whitespace=nowarn", "-"}
	cmd := gitCommand(ctx, args...)
	cmd.Stdin = strings.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return nil
}

// copyInto copies one worktree-relative path from src to dst, creating the
// directories above it and keeping its mode.
//
// Symlinks are recreated as links rather than followed. An agent's worktree is
// not a place to assume every link was meant to be a copy of what it points at,
// and following one out of the tree would be the same mistake ResolveInWorktree
// refuses to make.
func copyInto(src, dst, rel string) error {
	// Not ResolveInWorktree: that resolves the path it is given, which for a
	// symlink is the file at the other end of it, and copying that is the exact
	// thing this must not do. The containment check it exists for is kept, done
	// on the relative path rather than on what it points at.
	clean, err := relInWorktree(rel)
	if err != nil {
		return err
	}
	from, to := filepath.Join(src, clean), filepath.Join(dst, clean)
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}

	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		return os.Symlink(target, to)
	}
	if info.IsDir() {
		// git reports an untracked directory as a path when nothing inside it
		// is tracked. Making it is enough: everything in it is listed too.
		return os.MkdirAll(to, info.Mode().Perm())
	}

	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// relInWorktree cleans a worktree-relative path, refusing one that would leave
// the worktree it is relative to.
func relInWorktree(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("no path")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s is an absolute path", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the worktree", rel)
	}
	return clean, nil
}

// untrackedZ is UntrackedFiles with a NUL-delimited listing.
//
// The line-delimited form quotes any path holding a character git considers
// unusual -- a space is fine, a quote or a newline is not -- and a quoted path
// is not a path anything can open. That is a cosmetic problem where the list is
// only being shown, and a wrong answer here, where every entry is about to be
// copied.
func untrackedZ(ctx context.Context, wt string) ([]string, error) {
	out, err := RunRaw(ctx, wt, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func countLinesIn(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
