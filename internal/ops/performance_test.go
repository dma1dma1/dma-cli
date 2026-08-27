package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func TestObserveConcurrencyStaysInteractive(t *testing.T) {
	if got := observeConcurrency(); got != 4 {
		t.Fatalf("observeConcurrency = %d, want 4", got)
	}
}

func TestBootstrapCloneGateWaitIsCancellable(t *testing.T) {
	bootstrapGate <- struct{}{}
	defer func() { <-bootstrapGate }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := bootstrapWithProgress(ctx, core.Repo{
		Bootstrap: core.Bootstrap{Clone: []string{"node_modules"}},
	}, t.TempDir(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("bootstrapWithProgress error = %v, want context canceled", err)
	}
}
