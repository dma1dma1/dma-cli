package ops

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// Bootstrap prepares a fresh worktree so it is immediately usable: shared
// dependency trees and caches are symlinked, per-worktree config is copied.
//
// Without this step every new worktree needs a fresh dependency install and a
// hand-copied env file, and the worktree gets skipped under time pressure --
// which is the whole failure mode this tool exists to prevent.
//
// Individual path failures are collected and returned as warnings rather than
// aborting: a missing .env in one repo should not block session creation.
//
// A canceled context is the exception, and is returned as an error. It is not a
// per-path failure: every path still queued will fail the same way, and what is
// on disk is a dependency tree truncated at an arbitrary point, which reads as
// complete to every installer that looks at it. That is worth abandoning the
// session over, so the caller can roll the worktree back.
func Bootstrap(ctx context.Context, repo core.Repo, worktree string) ([]string, error) {
	return bootstrapWithProgress(ctx, repo, worktree, nil)
}

func bootstrapWithProgress(ctx context.Context, repo core.Repo, worktree string, onProgress func(CreateProgress)) ([]string, error) {
	var warnings []string
	var created []string

	total := len(repo.Bootstrap.Symlink) + len(repo.Bootstrap.Clone) + len(repo.Bootstrap.Copy)
	current := 0

	report := func(action, rel string) {
		current++
		if onProgress != nil {
			onProgress(CreateProgress(fmt.Sprintf("%s %s (%d/%d)", action, rel, current, total)))
		}
	}

	for _, rel := range repo.Bootstrap.Symlink {
		report("linking", rel)
		if err := linkPath(repo.Path, worktree, rel); err != nil {
			warnings = append(warnings, fmt.Sprintf("symlink %s: %v", rel, err))
			continue
		}
		created = append(created, rel)
	}
	for _, rel := range repo.Bootstrap.Clone {
		report("cloning", rel)
		if err := clonePath(ctx, repo.Path, worktree, rel); err != nil {
			if ctx.Err() != nil {
				return warnings, fmt.Errorf("clone %s: %w", rel, ctx.Err())
			}
			warnings = append(warnings, fmt.Sprintf("clone %s: %v", rel, err))
			continue
		}
		created = append(created, rel)
	}
	for _, rel := range repo.Bootstrap.Copy {
		report("copying", rel)
		if err := copyPath(repo.Path, worktree, rel); err != nil {
			warnings = append(warnings, fmt.Sprintf("copy %s: %v", rel, err))
			continue
		}
		created = append(created, rel)
	}

	// Bootstrapped paths are infrastructure the tool put there. If they are not
	// already ignored by the repo, exclude them locally so the worktree does
	// not permanently read as dirty.
	_ = gitx.AddLocalExclude(ctx, worktree, untracked(ctx, worktree, created)...)

	return warnings, nil
}

// untracked filters to the paths git would actually report, so already-ignored
// entries are not appended to the exclude file for no reason.
func untracked(ctx context.Context, worktree string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	ignored := ignoredSet(ctx, worktree, paths)
	var out []string
	for _, p := range paths {
		if ignored[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// safeJoin resolves rel against root and refuses anything that escapes it.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("must be a relative path")
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the repo")
	}
	return filepath.Join(root, clean), nil
}

// linkPath creates worktree/rel as a symlink to repo/rel.
func linkPath(repoPath, worktree, rel string) error {
	src, err := safeJoin(repoPath, rel)
	if err != nil {
		return err
	}
	dst, err := safeJoin(worktree, rel)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(src); err != nil {
		return fmt.Errorf("not present in main checkout")
	}
	// The worktree may already carry a tracked file at this path.
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(src, dst)
}

// clonePath gives the worktree its own copy of repo/rel, preferring a
// filesystem clone and falling back to a plain recursive copy.
//
// The fallback is slow for a dependency tree, but a slow correct worktree beats
// a fast one whose node_modules is the same directory as its neighbour's.
func clonePath(ctx context.Context, repoPath, worktree, rel string) error {
	src, err := safeJoin(repoPath, rel)
	if err != nil {
		return err
	}
	dst, err := safeJoin(worktree, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("not present in main checkout")
	}
	// The worktree may already carry a tracked file at this path.
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := cloneTree(ctx, src, dst); err == nil {
		return nil
	}
	// A partial clone would read as a complete tree to every installer that
	// looks for one, so it goes before the copy retries.
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	// A canceled start must not turn into an uncancelable recursive copy just
	// because its clone worker was the first operation to notice the deadline.
	if err := ctx.Err(); err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

// copyPath duplicates repo/rel into the worktree, recursing into directories.
func copyPath(repoPath, worktree, rel string) error {
	src, err := safeJoin(repoPath, rel)
	if err != nil {
		return err
	}
	dst, err := safeJoin(worktree, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("not present in main checkout")
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(s)
			if err != nil {
				return err
			}
			_ = os.Symlink(target, d)
		case e.IsDir():
			if err := copyDir(s, d); err != nil {
				return err
			}
		default:
			if err := copyFile(s, d, info.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
