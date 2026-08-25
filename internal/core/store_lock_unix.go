//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package core

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// withStateLock serializes read-modify-write operations across dma processes.
// Atomic rename prevents a torn file; this lock prevents two complete writes
// from racing and silently dropping whichever one landed first.
func withStateLock(fn func() error) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(StatePath()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open state lock: %w", err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:errcheck
	return fn()
}
