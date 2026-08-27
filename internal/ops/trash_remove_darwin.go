//go:build darwin

package ops

import (
	"context"
	"os/exec"
)

// removeTree puts the unavoidable recursive unlink in its own background-I/O
// process. The old os.RemoveAll ran at normal priority inside dma, so hundreds
// of thousands of APFS metadata updates competed directly with terminal
// captures and made the whole desktop hitch. CommandContext also makes a sweep
// cancellable in the middle of one large tree instead of only between trees.
func removeTree(ctx context.Context, path string) error {
	err := exec.CommandContext(ctx, "/usr/sbin/taskpolicy", "-b", "-d", "throttle",
		"/bin/rm", "-rf", path).Run()
	if err == nil || ctx.Err() != nil {
		return err
	}
	// Sandboxes and some managed Macs deny taskpolicy's priority change. The
	// cleanup still has to happen there; falling back preserves the old behavior
	// instead of leaking every discarded worktree.
	return exec.CommandContext(ctx, "/bin/rm", "-rf", path).Run()
}
