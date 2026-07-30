// Package tmuxx wraps the tmux CLI. Agent processes live in tmux sessions so
// they survive the TUI exiting, and so the TUI never has to own a PTY or
// implement a terminal emulator.
package tmuxx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("tmux %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Available reports whether tmux is installed.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// InsideTmux reports whether this process is itself running inside tmux, which
// decides between switch-client and attach-session.
func InsideTmux() bool { return os.Getenv("TMUX") != "" }

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// SafeName sanitizes a tmux session name. tmux treats "." and ":" as window and
// pane separators, so they cannot appear in a session name.
func SafeName(s string) string {
	s = unsafeName.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "dma"
	}
	return s
}

// HasSession reports whether the named session exists -- the liveness check.
func HasSession(ctx context.Context, name string) bool {
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", "="+name)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run() == nil
}

// ListSessions returns the names of all live tmux sessions, so liveness for the
// whole board costs one process spawn rather than one per session.
func ListSessions(ctx context.Context) (map[string]bool, error) {
	out, err := run(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		// No server running is the normal empty case, not a failure.
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(err.Error(), "No such file or directory") {
			return map[string]bool{}, nil
		}
		return map[string]bool{}, err
	}
	live := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			live[l] = true
		}
	}
	return live, nil
}

// NewSession starts a detached session rooted at dir, sized to cols x rows.
//
// The size matters: a detached tmux session defaults to 80x24 regardless of the
// terminal, so an agent launched without one renders its whole UI into 80
// columns and the preview shows a narrow strip inside a wide panel.
func NewSession(ctx context.Context, name, dir string, cols, rows int) error {
	args := []string{"new-session", "-d", "-s", name, "-c", dir}
	if cols > 0 && rows > 0 {
		args = append(args, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	}
	if _, err := run(ctx, args...); err != nil {
		return err
	}
	if cols > 0 && rows > 0 {
		// new-session -x/-y sets the initial size; pinning it manual keeps tmux
		// from resizing the window out from under the preview.
		_ = ResizeWindow(ctx, name, cols, rows)
	}
	return nil
}

// Window size modes.
const (
	// SizeManual holds the window at whatever size we set, which is what the
	// preview needs while nothing is attached.
	SizeManual = "manual"
	// SizeLatest lets the window follow the most recent client, which is what
	// an attached user needs so the agent fills their terminal.
	SizeLatest = "latest"
)

// ResizeWindow pins a detached session's window to an explicit size. tmux sets
// window-size to manual as a side effect.
func ResizeWindow(ctx context.Context, name string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	_, err := run(ctx, "resize-window", "-t", "="+name,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	return err
}

// SetWindowSize switches a session between following clients and holding a
// fixed size.
func SetWindowSize(ctx context.Context, name, mode string) error {
	_, err := run(ctx, "set-option", "-t", "="+name, "window-size", mode)
	return err
}

// SendKeys types a line into the session and presses Enter.
func SendKeys(ctx context.Context, name, text string) error {
	_, err := run(ctx, "send-keys", "-t", name, text, "Enter")
	return err
}

// SendLiteral types text without interpreting it as key names, then Enter.
// Used for user-supplied prompts, which may contain anything.
func SendLiteral(ctx context.Context, name, text string) error {
	if _, err := run(ctx, "send-keys", "-t", name, "-l", text); err != nil {
		return err
	}
	_, err := run(ctx, "send-keys", "-t", name, "Enter")
	return err
}

// KillSession terminates the session and everything in it.
func KillSession(ctx context.Context, name string) error {
	_, err := run(ctx, "kill-session", "-t", "="+name)
	return err
}

// CapturePane returns pane content, which is how the board shows agent output
// without owning a PTY. It is display only -- agent state comes from hooks, or
// from probing, never from parsing this.
//
// history <= 0 captures just the visible screen, which is what you want for a
// full-screen agent UI: those draw on the alternate screen, so their scrollback
// holds whatever was on the normal screen beforehand and splicing the two
// together renders stale fragments over the live view.
func CapturePane(ctx context.Context, name string, history int) (string, error) {
	args := []string{"capture-pane", "-p", "-e", "-t", name}
	if history > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", history))
	}
	return run(ctx, args...)
}

// AttachCmd builds the command that hands the terminal to tmux. When the TUI is
// itself inside tmux, switch-client is correct; attach-session would nest.
func AttachCmd(name string) *exec.Cmd {
	if InsideTmux() {
		return exec.Command("tmux", "switch-client", "-t", "="+name)
	}
	return exec.Command("tmux", "attach-session", "-t", "="+name)
}
