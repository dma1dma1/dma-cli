//go:build android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// tryTrashLock prevents two dma processes from sweeping the same worktree root
// concurrently. It is intentionally nonblocking: the process holding the lock
// already owns the cleanup, so another board has no reason to wait behind it.
func tryTrashLock(worktreeRoot string) (func(), bool, error) {
	f, err := os.OpenFile(filepath.Join(worktreeRoot, ".trash.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open trash lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return func() {}, false, nil
		}
		return nil, false, fmt.Errorf("lock trash: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
