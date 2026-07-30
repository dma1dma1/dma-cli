// Package notify raises desktop notifications. The point of the board is not
// to be babysat, so a session entering needs_you has to reach the user even
// when the TUI is not on screen.
package notify

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notify shows a desktop notification, best effort. Failures are silent: a
// missing notifier must never interrupt the board.
func Notify(title, body string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := command(ctx, title, body)
		if cmd == nil {
			return
		}
		_ = cmd.Run()
	}()
}

func command(ctx context.Context, title, body string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		// terminal-notifier is nicer when present: its notifications are
		// attributable and do not steal focus.
		if bin, err := exec.LookPath("terminal-notifier"); err == nil {
			return exec.CommandContext(ctx, bin, "-title", title, "-message", body, "-group", "dma")
		}
		script := "display notification " + quote(body) + " with title " + quote(title)
		return exec.CommandContext(ctx, "osascript", "-e", script)
	case "linux":
		if bin, err := exec.LookPath("notify-send"); err == nil {
			return exec.CommandContext(ctx, bin, title, body)
		}
	}
	return nil
}

// quote escapes a string for AppleScript.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
