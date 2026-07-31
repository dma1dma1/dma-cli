// Package link hands a URL to the desktop: opening it in a browser, or putting
// it on the clipboard. Both are one-shot shell-outs to whatever the platform
// provides, for the same reason ghx shells out to gh -- the system tools
// already know the user's default browser and clipboard, and a Go library
// would only reimplement them worse.
package link

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Open shows a URL in the user's browser.
func Open(ctx context.Context, url string) error {
	cmd := openCmd(ctx, url)
	if cmd == nil {
		return fmt.Errorf("no browser opener on %s", runtime.GOOS)
	}
	return runQuiet(cmd)
}

// Copy puts text on the system clipboard.
func Copy(ctx context.Context, text string) error {
	cmd := copyCmd(ctx)
	if cmd == nil {
		// Worth naming the binaries: on a headless or SSH session this is the
		// whole diagnosis, and "clipboard failed" would not be.
		return fmt.Errorf("no clipboard tool found (install %s)", clipboardCandidates())
	}
	cmd.Stdin = strings.NewReader(text)
	return runQuiet(cmd)
}

func openCmd(ctx context.Context, url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", url)
	case "linux":
		if bin, err := exec.LookPath("xdg-open"); err == nil {
			return exec.CommandContext(ctx, bin, url)
		}
	case "windows":
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	}
	return nil
}

func copyCmd(ctx context.Context) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "pbcopy")
	case "windows":
		return exec.CommandContext(ctx, "clip")
	}
	// Wayland first when the session is one: wl-copy is the tool that works
	// there, and xclip against XWayland can succeed and still paste nothing.
	var names []string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		names = []string{"wl-copy", "xclip", "xsel"}
	} else {
		names = []string{"xclip", "xsel", "wl-copy"}
	}
	for _, n := range names {
		bin, err := exec.LookPath(n)
		if err != nil {
			continue
		}
		switch n {
		case "xclip":
			return exec.CommandContext(ctx, bin, "-selection", "clipboard")
		case "xsel":
			return exec.CommandContext(ctx, bin, "--clipboard", "--input")
		default:
			return exec.CommandContext(ctx, bin)
		}
	}
	return nil
}

func clipboardCandidates() string {
	switch runtime.GOOS {
	case "darwin", "windows":
		return "the system clipboard tool"
	}
	return "wl-clipboard, xclip or xsel"
}

// runQuiet reports the tool's own complaint rather than "exit status 1", which
// is all the status bar would otherwise get.
func runQuiet(cmd *exec.Cmd) error {
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %s", cmd.Path, firstLine(msg))
		}
		return fmt.Errorf("%s: %w", cmd.Path, err)
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
