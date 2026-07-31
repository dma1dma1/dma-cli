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

// The notifiers each platform can hand a notification to.
const (
	macNotifier   = "terminal-notifier"
	linuxNotifier = "notify-send"
)

// Requirement is a notifier dma wants on this platform, and how to get it.
type Requirement struct {
	Tool    string
	Why     string
	Install string // empty when there is no single command that installs it
	// Required is true when the platform's fallback is bad enough to call a
	// missing notifier an incomplete setup rather than a missing extra.
	Required bool
}

// Hint is the one line to show someone who does not have this notifier.
func (r Requirement) Hint() string {
	if r.Install == "" {
		return "Desktop notifications need " + r.Tool
	}
	return "Desktop notifications need " + r.Tool + " — " + r.Install
}

// Requirements lists the notifiers this platform uses.
//
// macOS marks terminal-notifier required even though Notify still works without
// it. The osascript fallback below is attributed to whichever app hosts the
// AppleScript, so notifications arrive wearing Script Editor's paper-scroll icon
// and clicking one launches Script Editor instead of returning to the board --
// and it takes focus, which is exactly what a board you are not supposed to
// babysit must not do. There is no AppleScript parameter that fixes either
// thing; the icon belongs to the posting app.
//
// Linux keeps notify-send optional because it has no fallback to be bad: no
// notify-send simply means no notifications, and that has been the documented
// behavior since before this check existed.
func Requirements() []Requirement {
	switch runtime.GOOS {
	case "darwin":
		return []Requirement{{
			Tool:     macNotifier,
			Why:      "desktop notifications you can attribute and click back to",
			Install:  "brew install " + macNotifier,
			Required: true,
		}}
	case "linux":
		return []Requirement{{
			Tool: linuxNotifier,
			Why:  "desktop notifications",
		}}
	}
	return nil
}

// MissingRequirement returns the required notifier this machine does not have,
// so callers can say so once rather than each deciding what a platform needs.
func MissingRequirement() (Requirement, bool) {
	for _, r := range Requirements() {
		if !r.Required {
			continue
		}
		if _, err := exec.LookPath(r.Tool); err != nil {
			return r, true
		}
	}
	return Requirement{}, false
}

func command(ctx context.Context, title, body string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		// terminal-notifier is nicer when present: its notifications are
		// attributable and do not steal focus. It is what dma doctor asks for;
		// the fallback stays because a notification the user was told about is
		// still better than silence.
		if bin, err := exec.LookPath(macNotifier); err == nil {
			return exec.CommandContext(ctx, bin, "-title", title, "-message", body, "-group", "dma")
		}
		script := "display notification " + quote(body) + " with title " + quote(title)
		return exec.CommandContext(ctx, "osascript", "-e", script)
	case "linux":
		if bin, err := exec.LookPath(linuxNotifier); err == nil {
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
