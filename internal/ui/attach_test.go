package ui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// TestAttachEnablesMouseForTheSession covers the scroll fix: an agent that draws
// inline puts its transcript in the pane's history, and with tmux's default
// "mouse off" nothing an attached user does with the wheel reaches it.
//
// The option has to be session-local in both directions. Setting it globally
// would change every tmux session on the machine, and restoring it to "off"
// afterwards would silently undo the setting of a user who runs with mouse on.
func TestAttachEnablesMouseForTheSession(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-attach-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := tmuxx.NewSession(ctx, name, os.TempDir(), 100, 30); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), name) })

	globalBefore := tmuxOption(t, "-g", "mouse")

	applyAttachOptions(ctx, name)
	if got := tmuxOption(t, "-t", name, "mouse"); got != "mouse on" {
		t.Errorf("attached session mouse = %q, want %q", got, "mouse on")
	}
	if got := tmuxOption(t, "-g", "mouse"); got != globalBefore {
		t.Errorf("global mouse = %q after attaching, want it untouched at %q", got, globalBefore)
	}

	clearAttachOptions(ctx, name)
	if got := tmuxOption(t, "-t", name, "mouse"); got != "" {
		t.Errorf("detached session still overrides mouse (%q); it should inherit the user's setting", got)
	}
}

// tmuxOption reads one option, returning "" when the target does not set it
// itself -- which is how an inherited value shows up.
func tmuxOption(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", append([]string{"show-options"}, args...)...).Output()
	if err != nil {
		t.Fatalf("tmux show-options %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}
