//go:build !darwin

package ops

import (
	"context"
	"os"
)

func removeTree(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
