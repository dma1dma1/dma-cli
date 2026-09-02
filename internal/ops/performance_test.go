package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func TestSetupGateWaitIsCancellable(t *testing.T) {
	setupGate <- struct{}{}
	defer func() { <-setupGate }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got []CreateProgress
	release, err := acquireSetupGate(ctx, func(p CreateProgress) { got = append(got, p) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquireSetupGate error = %v, want context canceled", err)
	}
	if release != nil {
		t.Fatal("cancelled acquire returned a release")
	}
	if len(got) != 1 || got[0] != "waiting for another session to finish setting up" {
		t.Fatalf("progress = %q, want a single waiting report", got)
	}
}

func TestSetupGateReleaseIsIdempotent(t *testing.T) {
	release, err := acquireSetupGate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	// The gate must be free again exactly once: a second acquire succeeds
	// without blocking, and the buffered channel has not been drained twice.
	again, err := acquireSetupGate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestBootstrapTakesSetupGateWhenCalledDirectly(t *testing.T) {
	setupGate <- struct{}{}
	defer func() { <-setupGate }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Bootstrap(ctx, core.Repo{
		Bootstrap: core.Bootstrap{Clone: []string{"node_modules"}},
	}, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Bootstrap error = %v, want context canceled", err)
	}
}
