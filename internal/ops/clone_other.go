//go:build !darwin

package ops

import "errors"

// errNoCloneSupport reports that this platform has no filesystem clone, leaving
// the caller to fall back to a plain recursive copy.
var errNoCloneSupport = errors.New("filesystem cloning is unavailable")

func cloneTree(src, dst string) error { return errNoCloneSupport }
