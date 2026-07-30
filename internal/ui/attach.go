package ui

import (
	"context"
	"os/exec"
	"time"

	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// prepareAttach makes level 3 unmistakable and installs the single reserved
// detach key.
//
// Once attached, tmux owns the whole screen, so the "you are in the agent now"
// signal has to live in tmux itself: a loud status line on that session, and a
// root-table binding for the detach key. Everything else -- Escape included --
// reaches the agent.
func prepareAttach(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// ctrl-q is bound in the root key table (-n), so it needs no prefix and
	// never reaches the child process.
	if tmuxx.InsideTmux() {
		// Inside tmux, detaching would drop the outer client entirely; switching
		// back to the previous session is the equivalent move.
		runTmux(ctx, "bind-key", "-n", detachTmuxKey, "switch-client", "-l")
	} else {
		runTmux(ctx, "bind-key", "-n", detachTmuxKey, "detach-client")
	}

	// While attached the window must follow the real terminal, not the small
	// size the preview pins it to.
	_ = tmuxx.SetWindowSize(ctx, session, tmuxx.SizeLatest)

	runTmux(ctx, "set-option", "-t", session, "status", "on")
	runTmux(ctx, "set-option", "-t", session, "status-style", "bg=colour214,fg=colour232,bold")
	runTmux(ctx, "set-option", "-t", session, "status-left-length", "80")
	runTmux(ctx, "set-option", "-t", session, "status-left",
		" ATTACHED · keys go to the agent · C-q to detach ")
	runTmux(ctx, "set-option", "-t", session, "status-right", "")
	// A colored pane border reinforces the mode change at a glance.
	runTmux(ctx, "set-option", "-t", session, "pane-active-border-style", "fg=colour214")
}

// restoreAfterAttach undoes the visual changes so the session looks normal
// again if the user attaches to it outside this tool.
func restoreAfterAttach(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Back to a fixed size; the board re-applies the preview dimensions.
	_ = tmuxx.SetWindowSize(ctx, session, tmuxx.SizeManual)
	for _, opt := range []string{
		"status-style", "status-left", "status-left-length",
		"status-right", "pane-active-border-style", "status",
	} {
		runTmux(ctx, "set-option", "-t", session, "-u", opt)
	}
}

// detachTmuxKey is the tmux spelling of detachKey.
const detachTmuxKey = "C-q"

func runTmux(ctx context.Context, args ...string) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	_ = cmd.Run()
}
