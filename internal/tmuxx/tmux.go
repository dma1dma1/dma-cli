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

// NewSession starts a detached session rooted at dir.
func NewSession(ctx context.Context, name, dir string) error {
	_, err := run(ctx, "new-session", "-d", "-s", name, "-c", dir)
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

// CapturePane returns the last n lines of visible pane content. This is how the
// detail view shows recent agent output without owning a PTY. It is display
// only -- agent state comes from hooks, never from scraping this.
func CapturePane(ctx context.Context, name string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	return run(ctx, "capture-pane", "-p", "-e", "-t", name, "-S", fmt.Sprintf("-%d", lines))
}

// AttachCmd builds the command that hands the terminal to tmux. When the TUI is
// itself inside tmux, switch-client is correct; attach-session would nest.
func AttachCmd(name string) *exec.Cmd {
	if InsideTmux() {
		return exec.Command("tmux", "switch-client", "-t", "="+name)
	}
	return exec.Command("tmux", "attach-session", "-t", "="+name)
}
