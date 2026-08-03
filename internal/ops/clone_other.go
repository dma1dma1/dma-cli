//go:build !darwin

package ops

import (
	"context"
	"errors"
)

// errNoCloneSupport reports that this platform has no filesystem clone, leaving
// the caller to fall back to a plain recursive copy.
var errNoCloneSupport = errors.New("filesystem cloning is unavailable")

func cloneTree(context.Context, string, string) error { return errNoCloneSupport }
