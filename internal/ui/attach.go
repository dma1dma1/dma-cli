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

	applyAttachOptions(ctx, session)
}

// attachOptions are the session options attaching overrides, and restoring puts
// back. Keeping them in one list is what stops the two halves from drifting:
// every option set here is unset again by name.
var attachOptions = []struct{ name, value string }{
	{"status", "on"},
	{"status-style", "bg=colour214,fg=colour232,bold"},
	{"status-left-length", "80"},
	{"status-left", " ATTACHED · keys go to the agent · C-q to detach "},
	{"status-right", ""},
	// A colored pane border reinforces the mode change at a glance.
	{"pane-active-border-style", "fg=colour214"},
	// Scrollback is tmux's to give. Agents that draw inline rather than on the
	// alternate screen -- Codex is one -- push their transcript into the pane's
	// history, and with tmux's default "mouse off" the wheel reaches neither
	// tmux nor the agent: the outer terminal scrolls its own buffer, which holds
	// nothing but whatever was on screen before the attach. Turning mouse on for
	// the duration makes the wheel enter copy-mode, which is the scroll the user
	// is reaching for. Panes whose agent asks for mouse events still get them --
	// tmux's own wheel binding forwards to the application first.
	{"mouse", "on"},
}

// restoreAfterAttach undoes the visual changes so the session looks normal
// again if the user attaches to it outside this tool.
func restoreAfterAttach(session string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Back to a fixed size; the board re-applies the preview dimensions.
	_ = tmuxx.SetWindowSize(ctx, session, tmuxx.SizeManual)
	clearAttachOptions(ctx, session)
}

// applyAttachOptions sets the attached look on one session.
func applyAttachOptions(ctx context.Context, session string) {
	for _, opt := range attachOptions {
		runTmux(ctx, "set-option", "-t", session, opt.name, opt.value)
	}
}

// clearAttachOptions drops the session-local overrides, so each option inherits
// the user's own setting again rather than being forced back to a default.
func clearAttachOptions(ctx context.Context, session string) {
	for _, opt := range attachOptions {
		runTmux(ctx, "set-option", "-t", session, "-u", opt.name)
	}
}

// detachTmuxKey is the tmux spelling of detachKey.
const detachTmuxKey = "C-q"

func runTmux(ctx context.Context, args ...string) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	_ = cmd.Run()
}
