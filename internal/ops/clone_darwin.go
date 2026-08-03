package ops

import (
	"context"
	"os/exec"
)

// cloneTree copies src to dst with APFS copy-on-write clones, which is what
// makes per-worktree dependency trees affordable.
//
// A large node_modules can contain hundreds of thousands of entries. Running
// that metadata work in dma's process at the default disk policy can starve the
// tmux captures and git queries that keep the board interactive. taskpolicy
// gives the clone worker throttled disk I/O, so foreground operations win while
// the dependency tree continues to materialize in the background.
//
// cp -c keeps the clone copy-on-write: files share their original blocks until
// one side writes to them. dst must not exist.
func cloneTree(ctx context.Context, src, dst string) error {
	return exec.CommandContext(ctx, "/usr/sbin/taskpolicy", "-d", "throttle",
		"/bin/cp", "-cR", src, dst).Run()
}
