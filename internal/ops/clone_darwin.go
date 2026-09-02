package ops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// cloneChunkEntries bounds how much of a tree one clonefile(2) call is allowed
// to cover.
//
// Cloning a directory hands the whole hierarchy to the kernel as a single
// uninterruptible syscall. On a 278k-entry node_modules that call ran for
// close to a minute, and for that minute the rest of the desktop stalled:
// WindowServer logged GUI clients failing to drain events, Slack froze, and
// the clone could not be cancelled. clonefile(2)'s own manual says cloning
// directories this way is strongly discouraged.
//
// Measured on this hardware, one call costs roughly 100us per entry, so a
// chunk this size holds the kernel for well under half a second before other
// processes get the filesystem back and the context is checked again. It is
// large enough that a typical pnpm package or Python site-package still lands
// in one call, so the total number of syscalls stays in the low thousands.
const cloneChunkEntries = 4096

// cloneTree copies src to dst with APFS copy-on-write clones, which is what
// makes per-worktree dependency trees affordable.
//
// Every file is still cloned rather than copied: metadata only, no bytes
// moved, blocks shared until one side writes. What changed from a single
// directory clone is only the granularity. Subtrees small enough to clear
// cloneChunkEntries are cloned in one call; larger ones are recreated as a
// directory and their children cloned individually, recursing until each call
// is short. Measured on a 336k-entry monorepo the whole tree still lands in
// well under a minute, against 6.4 minutes for cp -cR walking it file by
// file, without the minute-long freeze the single call produced.
//
// dst must not exist, and both paths must live on the same APFS volume.
func cloneTree(ctx context.Context, src, dst string) error {
	// Bootstrap calls this once per configured tree, so a start abandoned during
	// the first clone should not go on to issue the next forty.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cloneChunked(ctx, src, dst, cloneChunkEntries); err == nil {
		return nil
	} else if ctx.Err() != nil {
		return ctx.Err()
	}

	// clonefile refuses across volumes and on filesystems without clone support.
	// Falling back keeps those setups working at the old speed instead of
	// failing the session outright. A partial chunked result must go first: it
	// would read as a complete tree to every installer that looks at it.
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	//
	// taskpolicy gives this worker throttled disk I/O so the tmux captures and
	// git queries that keep the board interactive still win. It matters on this
	// path precisely because this path is the slow one.
	return exec.CommandContext(ctx, "/usr/sbin/taskpolicy", "-d", "throttle",
		"/bin/cp", "-cR", src, dst).Run()
}

// cloneChunked clones src to dst, never handing the kernel more than limit
// entries in one clonefile call.
//
// Cancellation is observed between calls. Whatever landed before it is left
// for the caller to remove, since a half-cloned dependency tree is worse than
// none.
func cloneChunked(ctx context.Context, src, dst string, limit int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() || countEntriesUpTo(src, limit) < limit {
		// Files, symlinks and small trees: one call, cloned as-is. NOFOLLOW keeps
		// a symlink a symlink rather than cloning whatever it points at.
		return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, info.Mode().Perm()); err != nil {
		return err
	}
	for _, e := range entries {
		if err := cloneChunked(ctx, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), limit); err != nil {
			return err
		}
	}
	return nil
}

// countEntriesUpTo reports how many entries live under dir, itself included,
// stopping as soon as the count reaches limit. It reads directory listings
// only, never stats, so deciding how to split a tree costs a fraction of
// cloning it.
func countEntriesUpTo(dir string, limit int) int {
	n := 1
	var walk func(string)
	walk = func(d string) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			n++
			if n >= limit {
				return
			}
			if e.IsDir() {
				walk(filepath.Join(d, e.Name()))
				if n >= limit {
					return
				}
			}
		}
	}
	walk(dir)
	return n
}
