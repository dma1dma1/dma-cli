package ops

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// Deleting a worktree is the slowest thing teardown does, and on a repo that
// clones dependency trees per worktree it is not close to anything else. A
// monorepo materializing 42 trees puts around 347k filesystem entries in each
// worktree, and unlinking that measured 25 seconds for the largest tree alone --
// against 0.21s for the status check and 0.03s for the registry work. Bulk prune
// runs teardowns one after another on purpose, so clearing a merged column of
// five cards spent three minutes deleting files nobody was waiting to read.
//
// So the files are moved instead of deleted: a rename into a sibling directory
// measured 4.8ms, and `git worktree prune` then clears the registry entry, which
// is what it is for once the recorded path is gone. The unlink happens afterwards
// in SweepTrash, off the keystroke.
//
// Deleting in parallel was measured and rejected: twelve workers over one
// 264k-entry tree finished in 25.0s against 24.9s for one. APFS serializes the
// metadata updates whatever the caller does, so the only time available to win is
// the user's, not the disk's.

// trashDirName holds discarded worktrees until a sweep unlinks them.
//
// It lives inside the worktree root so the rename stays on one filesystem --
// across a mount boundary rename fails outright, and a trash directory that
// forced a copy would cost more than the delete it replaced. The leading dot
// keeps it clear of session directories, which are named from slugs and so
// always start with a letter or digit.
const trashDirName = ".trash"

// TrashDir is where a repo's discarded worktrees wait to be deleted.
func TrashDir(worktreeRoot string) string {
	return filepath.Join(worktreeRoot, trashDirName)
}

// discardWorktree detaches a worktree from its repo and gets its files out of
// the way, leaving the unlink to a later sweep when it can.
//
// The registry prune runs either way: after a rename the entry git holds points
// at a path that no longer exists, and a stale entry there is what makes the
// following branch delete fail.
func discardWorktree(ctx context.Context, repo core.Repo, wtPath string, force bool) error {
	if _, err := os.Stat(wtPath); err == nil {
		if err := trashWorktree(repo, wtPath); err != nil {
			// The fast path was unavailable rather than wrong -- most plausibly a
			// worktree on a different filesystem from the root it was configured
			// under. Deleting in place is slow, not broken, so it stands in.
			if err := gitx.RemoveWorktree(ctx, repo.Path, wtPath, force); err != nil {
				return err
			}
		}
	}
	return gitx.PruneWorktrees(ctx, repo.Path)
}

// trashWorktree moves a worktree into its repo's trash directory.
func trashWorktree(repo core.Repo, wtPath string) error {
	if repo.WorktreeRoot == "" {
		return os.ErrInvalid
	}
	dir := TrashDir(repo.WorktreeRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Nothing reads the name back, so it only has to be unique: two sessions
	// started from the same title produce the same directory name and would
	// otherwise collide here, one silently landing inside the other.
	return os.Rename(wtPath, filepath.Join(dir, filepath.Base(wtPath)+"-"+core.NewID()))
}

var (
	sweepMu      sync.Mutex
	sweepRunning = map[string]bool{}
)

// SweepTrash unlinks the worktrees waiting in a repo's trash directory.
//
// It removes the entries and leaves the directory itself, so a teardown renaming
// into the trash while a sweep runs cannot fail on a directory that stopped
// being empty underneath it.
//
// One sweep per root at a time: a bulk prune fires a sweep per session torn down,
// and two of them walking one directory would meet each other's half-deleted
// trees. The one already running keeps going until the trash reads empty, which
// is what collects the trees the skipped callers were asking about.
func SweepTrash(ctx context.Context, worktreeRoot string) error {
	if worktreeRoot == "" {
		return nil
	}
	dir := TrashDir(worktreeRoot)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	sweepMu.Lock()
	if sweepRunning[dir] {
		sweepMu.Unlock()
		return nil
	}
	sweepRunning[dir] = true
	sweepMu.Unlock()
	defer func() {
		sweepMu.Lock()
		delete(sweepRunning, dir)
		sweepMu.Unlock()
	}()

	unlock, acquired, err := tryTrashLock(worktreeRoot)
	if err != nil {
		return err
	}
	if !acquired {
		// Another process already owns this cleanup pass. A late arrival remains
		// safely in .trash for the next sweep instead of starting a second metadata
		// storm against the same root.
		return nil
	}
	defer unlock()

	// failed is what keeps the drain loop finite. An entry that cannot be removed
	// -- a permission the sweep does not have, a file held open -- would otherwise
	// be reread and retried forever, since the directory never reaches empty.
	failed := map[string]bool{}
	var firstErr error
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return firstErr
			}
			return err
		}
		progress := false
		for _, e := range entries {
			if failed[e.Name()] {
				continue
			}
			// removeTree is a throttled child on macOS, where dependency clones
			// make this expensive enough to affect the interactive board. Other
			// platforms at least observe cancellation between trees.
			if err := ctx.Err(); err != nil {
				return err
			}
			progress = true
			if err := removeTree(ctx, filepath.Join(dir, e.Name())); err != nil {
				failed[e.Name()] = true
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if !progress {
			return firstErr
		}
	}
}
