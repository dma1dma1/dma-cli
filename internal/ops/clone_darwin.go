package ops

import (
	"context"
	"os/exec"

	"golang.org/x/sys/unix"
)

// cloneTree copies src to dst with APFS copy-on-write clones, which is what
// makes per-worktree dependency trees affordable.
//
// One clonefile on the directory clones the entire subtree inside the
// filesystem: a single syscall, metadata only, no bytes moved. The obvious
// alternative -- cp -cR -- produces identical copy-on-write results but walks
// the tree in userspace and clones file by file, which for one node_modules is
// a quarter of a million syscalls. 83% of those files are under 4KB, so the
// per-file overhead is not part of the cost, it is all of it. Measured on a
// 336k-entry monorepo: 26 seconds for a full bootstrap here, against 6.4
// minutes through cp.
//
// dst must not exist, and both paths must live on the same APFS volume.
func cloneTree(ctx context.Context, src, dst string) error {
	// Bootstrap calls this once per configured tree, so a start abandoned during
	// the first clone should not go on to issue the next forty. clonefile itself
	// is one uninterruptible syscall; this is the only place cancellation can be
	// observed.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Clonefile(src, dst, 0); err == nil {
		return nil
	}

	// clonefile refuses across volumes and on filesystems without clone support.
	// Falling back keeps those setups working at the old speed instead of
	// failing the session outright; clonePath clears any partial dst first.
	//
	// taskpolicy gives this worker throttled disk I/O so the tmux captures and
	// git queries that keep the board interactive still win. It matters on this
	// path precisely because this path is the slow one.
	return exec.CommandContext(ctx, "/usr/sbin/taskpolicy", "-d", "throttle",
		"/bin/cp", "-cR", src, dst).Run()
}
