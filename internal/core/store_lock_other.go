//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package core

import "os"

// dma requires tmux and is only functional on Unix. Keep the state package
// buildable elsewhere; there is no supported multi-process runtime there.
func withStateLock(fn func() error) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	return fn()
}
