package tmuxx

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestWindowSizeModes covers the sizing contract the board depends on: a
// detached session holds the preview size, and an attached one is released to
// follow the real terminal.
//
// Both commands take a target-window, where tmux rejects the "=" exact-match
// prefix on its own. That failure is invisible at runtime -- the callers ignore
// the error -- so the window silently stayed pinned to the preview size and an
// attached agent rendered into a corner of the terminal, the rest filled with
// tmux's padding dots.
func TestWindowSizeModes(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-tmuxx-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := NewSession(ctx, name, os.TempDir(), 100, 30); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })

	if got := windowSize(t, name); got != "100x30" {
		t.Fatalf("new session is %s, want 100x30", got)
	}
	if got := windowSizeOption(t, name); got != SizeManual {
		t.Fatalf("new session window-size is %q, want %q", got, SizeManual)
	}

	// Attaching: release the window so it grows to the attaching client.
	if err := SetWindowSize(ctx, name, SizeLatest); err != nil {
		t.Fatalf("SetWindowSize(latest): %v", err)
	}
	if got := windowSizeOption(t, name); got != SizeLatest {
		t.Fatalf("window-size is %q after SetWindowSize(latest), want %q", got, SizeLatest)
	}

	// Detaching: back to a size the preview panel controls.
	if err := SetWindowSize(ctx, name, SizeManual); err != nil {
		t.Fatalf("SetWindowSize(manual): %v", err)
	}
	if got := windowSizeOption(t, name); got != SizeManual {
		t.Fatalf("window-size is %q after SetWindowSize(manual), want %q", got, SizeManual)
	}
	if err := ResizeWindow(ctx, name, 120, 40); err != nil {
		t.Fatalf("ResizeWindow: %v", err)
	}
	if got := windowSize(t, name); got != "120x40" {
		t.Fatalf("after ResizeWindow the window is %s, want 120x40", got)
	}
}

func windowSize(t *testing.T, name string) string {
	t.Helper()
	// display-message takes a target-pane, which does not accept a bare "=name".
	return tmuxOut(t, "display-message", "-p", "-t", name,
		"#{window_width}x#{window_height}")
}

func windowSizeOption(t *testing.T, name string) string {
	t.Helper()
	out := tmuxOut(t, "show-options", "-t", name+":", "-w", "window-size")
	return strings.TrimPrefix(out, "window-size ")
}

func tmuxOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		t.Fatalf("tmux %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// TestSendTextIsLiteral covers what "-l" alone does not.
//
// tmux parses its own command line before -l applies, so a payload the user
// typed can be altered on the way through: text spelling a key name must not
// become that keypress, and a trailing semicolon must survive rather than be
// eaten as a command separator.
func TestSendTextIsLiteral(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tests := []struct {
		name string
		text string
	}{
		{"key names stay text", "Enter C-c Escape"},
		{"trailing semicolon", "trailing;"},
		{"only a semicolon", ";"},
		{"two trailing semicolons", "two;;"},
		{"semicolon mid string", "a;b"},
		{"semicolon as a word", "fix the parser ; add tests"},
		{"shell metacharacters", "$HOME `id` && echo | tee ~ *"},
		{"quotes and backslash", `he said "no" 'x' back\`},
		{"multibyte", "é 字 🙂"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name := "dma-sendtext-" + strings.ReplaceAll(t.Name(), "/", "-")
			// cat echoes what it is sent without a shell interpreting any of it,
			// which is what makes this a test of tmux rather than of zsh.
			if err := NewSession(ctx, name, os.TempDir(), 200, 10); err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			t.Cleanup(func() { _ = KillSession(context.Background(), name) })
			if err := SendLiteral(ctx, name, "exec cat"); err != nil {
				t.Fatalf("start cat: %v", err)
			}
			time.Sleep(500 * time.Millisecond)

			if err := SendText(ctx, name, tc.text); err != nil {
				t.Fatalf("SendText(%q): %v", tc.text, err)
			}
			if err := SendKey(ctx, name, "Enter"); err != nil {
				t.Fatalf("SendKey(Enter): %v", err)
			}
			time.Sleep(500 * time.Millisecond)

			pane, err := CapturePane(ctx, name, 0)
			if err != nil {
				t.Fatalf("CapturePane: %v", err)
			}
			out := pane.Content
			// cat echoes the line twice: the terminal's own echo, then cat's copy.
			if strings.Count(out, tc.text) < 2 {
				t.Errorf("SendText(%q) did not arrive verbatim; pane holds:\n%s", tc.text, out)
			}
		})
	}
}

// TestSendKeyNamesAreAccepted checks that every key name the UI can emit is one
// tmux actually understands. A rejected name is invisible in the panel -- the
// keystroke just vanishes -- so it is worth asserting outright.
func TestSendKeyNamesAreAccepted(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-sendkey-names"
	if err := NewSession(ctx, name, os.TempDir(), 100, 30); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })

	for _, key := range []string{
		"Enter", "Tab", "BSpace", "Escape", "Space",
		"Up", "Down", "Left", "Right",
		"IC", "DC", "PageUp", "PageDown", "Home", "End",
		"F1", "F12",
		"C-c", "C-a", "M-b", "BTab", "C-M-a",
	} {
		if err := SendKey(ctx, name, key); err != nil {
			t.Errorf("SendKey(%q): %v", key, err)
		}
	}
}

// TestSendKeyDeliversDistinctBytes asserts what each key name puts on the pane's
// input, not merely that tmux accepted the name.
//
// Acceptance is far too weak a check. "S-Tab" is accepted and sends a plain tab:
// the shift is dropped with no error at any layer, so an agent reading shift+tab
// (Claude Code cycles permission modes with it) sees Tab instead and the panel
// looks like it ignored the key. Only the bytes reveal that.
func TestSendKeyDeliversDistinctBytes(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tests := []struct {
		key  string
		want string // as rendered by cat -v
	}{
		{"Tab", "\t"},
		{"BTab", "^[[Z"}, // CSI Z, the back-tab the terminal defines
		{"Escape", "^["},
		{"Up", "^[[A"},
		{"S-Up", "^[[1;2A"}, // the prefix form is right for arrows
		{"Left", "^[[D"},
		{"S-Left", "^[[1;2D"},
		{"Home", "^[[1~"},
		{"F1", "^[OP"},
		// C-c is deliberately absent: it interrupts the cat reading the pane, so
		// there is no way to observe its bytes this way. It is covered by driving a
		// real agent instead.
	}

	name := "dma-sendkey-bytes"
	// cat -v renders control bytes visibly and involves no shell to reinterpret
	// them, which is what makes the pane contents a faithful record of the keys.
	if err := NewSession(ctx, name, os.TempDir(), 200, 30); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })
	if err := SendLiteral(ctx, name, "exec cat -v"); err != nil {
		t.Fatalf("start cat: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	for _, tc := range tests {
		if err := SendKey(ctx, name, tc.key); err != nil {
			t.Errorf("SendKey(%q): %v", tc.key, err)
			continue
		}
		// A label after each key keeps the lines apart in the capture.
		if err := SendText(ctx, name, " ="+tc.key); err != nil {
			t.Fatalf("SendText: %v", err)
		}
		if err := SendKey(ctx, name, "Enter"); err != nil {
			t.Fatalf("SendKey(Enter): %v", err)
		}
	}
	time.Sleep(700 * time.Millisecond)

	pane, err := CapturePane(ctx, name, 0)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	out := pane.Content
	for _, tc := range tests {
		if !strings.Contains(out, tc.want+" ="+tc.key) {
			t.Errorf("SendKey(%q) did not deliver %q; pane holds:\n%s", tc.key, tc.want, out)
		}
	}
	// The bug this test exists for: shift+tab must not arrive as a plain tab.
	if strings.Contains(out, "\t =BTab") {
		t.Error("BTab arrived as a plain tab — the shift was dropped")
	}
}

// TestCapturePaneReportsCursor covers what capture-pane alone cannot answer.
//
// The panel renders captured cells, and cells carry no cursor: an agent that
// draws its own composer leaves the caret to the real terminal cursor, so the
// preview showed a prompt with nothing marking where typing would land. This
// asserts the position comes back, and that it follows what was typed.
func TestCapturePaneReportsCursor(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-cursor-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := NewSession(ctx, name, os.TempDir(), 80, 24); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })
	// cat holds the line without a shell prompt redrawing under it, so the column
	// is exactly the number of characters sent.
	if err := SendLiteral(ctx, name, "exec cat"); err != nil {
		t.Fatalf("start cat: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	pane, err := CapturePane(ctx, name, 0)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !pane.Cursor.Visible {
		t.Error("cursor reported hidden while a program waits for input")
	}
	if pane.Cursor.X != 0 {
		t.Errorf("cursor starts at column %d, want 0", pane.Cursor.X)
	}
	startRow := pane.Cursor.Y

	const typed = "hello"
	if err := SendText(ctx, name, typed); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	pane, err = CapturePane(ctx, name, 0)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if pane.Cursor.X != len(typed) {
		t.Errorf("cursor is at column %d after typing %q, want %d", pane.Cursor.X, typed, len(typed))
	}
	if pane.Cursor.Y != startRow {
		t.Errorf("cursor moved to row %d, want it to stay on %d", pane.Cursor.Y, startRow)
	}
	// The whole point: the caret is nowhere in the captured cells, which is why it
	// has to be drawn from these coordinates.
	if strings.Contains(pane.Content, "\x1b[7m") {
		t.Error("capture already contains a reverse-video cell; the overlay would double up")
	}
}
