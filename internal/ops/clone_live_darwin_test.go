package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCloneTreeLive clones real dependency trees and reports how long each
// took. It runs only when DMA_CLONE_LIVE_SRC names a comma-separated list of
// source trees and DMA_CLONE_LIVE_DST an empty directory on the same volume;
// the clones are left in place for the caller to inspect and remove.
func TestCloneTreeLive(t *testing.T) {
	srcs := os.Getenv("DMA_CLONE_LIVE_SRC")
	dstRoot := os.Getenv("DMA_CLONE_LIVE_DST")
	if srcs == "" || dstRoot == "" {
		t.Skip("set DMA_CLONE_LIVE_SRC and DMA_CLONE_LIVE_DST to run")
	}
	var total time.Duration
	for _, src := range strings.Split(srcs, ",") {
		dst := filepath.Join(dstRoot, strings.ReplaceAll(strings.TrimPrefix(src, "/"), "/", "__"))
		start := time.Now()
		err := cloneTree(context.Background(), src, dst)
		took := time.Since(start)
		total += took
		if err != nil {
			t.Fatalf("clone %s: %v", src, err)
		}
		t.Logf("%8.2fs  %s", took.Seconds(), src)
	}
	t.Logf("total %.2fs across %d trees", total.Seconds(), len(strings.Split(srcs, ",")))
}
