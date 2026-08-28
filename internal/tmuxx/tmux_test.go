package tmuxx

import (
	"context"
	"errors"
	"image/color"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionExistsPreservesContextFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SessionExists(ctx, "dma-context-cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionExists error = %v, want context canceled", err)
	}
}

func TestBatchArgsUsesOneTmuxCommandStream(t *testing.T) {
	got := batchArgs(
		[]string{"display-message", "-p", "state"},
		nil,
		[]string{"capture-pane", "-p"},
	)
	want := []string{"display-message", "-p", "state", ";", "capture-pane", "-p"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("batchArgs = %q, want %q", got, want)
	}
}

func TestIsolateSGRRows(t *testing.T) {
	input := "A\x1b[1;2;3;4:3;38;5;120;48;2;40;50;40m\nB\x1b[22;24;49m\nC"
	output := isolateSGRRows(input)
	if got, want := ansi.Strip(output), ansi.Strip(input); got != want {
		t.Errorf("visible text = %q, want %q", got, want)
	}
	if got := isolateSGRRows(output); got != output {
		t.Error("normalization is not idempotent")
	}

	whole := uv.NewScreenBuffer(3, 3)
	uv.NewStyledString(output).Draw(whole, whole.Bounds())
	rows := strings.Split(output, "\n")
	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3", len(rows))
	}
	for y, row := range rows {
		standalone := uv.NewScreenBuffer(2, 1)
		uv.NewStyledString(row+"X").Draw(standalone, standalone.Bounds())
		if got, want := standalone.CellAt(0, 0).Style, whole.CellAt(0, y).Style; !got.Equal(&want) {
			t.Errorf("row %d standalone style = %#v, whole-stream style = %#v", y, got, want)
		}
		if style := standalone.CellAt(1, 0).Style; !style.IsZero() {
			t.Errorf("row %d appended marker style = %#v, want default", y, style)
		}
	}

	wantBackground := color.RGBA{R: 40, G: 50, B: 40, A: 255}
	if bg := whole.CellAt(0, 1).Style.Bg; !sameColor(bg, wantBackground) {
		t.Errorf("row 1 B background = %v, want %v", bg, wantBackground)
	}
}

func TestIsolateSGRRowsLeavesPlainTextUnchanged(t *testing.T) {
	const input = "plain\ntext"
	if got := isolateSGRRows(input); got != input {
		t.Errorf("isolateSGRRows(%q) = %q", input, got)
	}
}

// Pi fills tool and status rows by painting spaces through the pane's last
// column. The preview needs those cells for the same row width and background
// as the attached session, then a reset before dma adds its panel border.
func TestCapturePanePreservesStyledTrailingCells(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-styled-row-" + strings.ReplaceAll(t.Name(), "/", "-")
	// Give tmux a deterministic pane command instead of typing into a login
	// shell whose rc files may still be running under load. This test is about
	// captured terminal cells, not interactive-shell startup latency.
	const command = `printf '\033[48;2;40;50;40m%-20s\033[0m\n' row; sleep 30`
	if _, err := run(ctx, "new-session", "-d", "-s", name, "-x", "20", "-y", "5", command); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })

	var pane Pane
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(ansi.Strip(pane.Content), "row") && time.Now().Before(deadline) {
		var err error
		pane, err = CapturePane(ctx, name, 0)
		if err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		if !strings.Contains(ansi.Strip(pane.Content), "row") {
			time.Sleep(50 * time.Millisecond)
		}
	}
	var row string
	for _, line := range strings.Split(pane.Content, "\n") {
		plain := ansi.Strip(line)
		if strings.TrimRight(plain, " ") == "row" && ansi.StringWidth(line) == 20 {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("captured pane lost the 20-cell painted row:\n%q", ansi.Strip(pane.Content))
	}

	screen := uv.NewScreenBuffer(21, 1)
	uv.NewStyledString(row+"X").Draw(screen, screen.Bounds())
	wantBackground := color.RGBA{R: 40, G: 50, B: 40, A: 255}
	if bg := screen.CellAt(19, 0).Style.Bg; !sameColor(bg, wantBackground) {
		t.Errorf("last pane cell background = %v, want %v", bg, wantBackground)
	}
	if style := screen.CellAt(20, 0).Style; !style.IsZero() {
		t.Errorf("cell after pane row inherited style %#v", style)
	}
}

func sameColor(got, want color.Color) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	gotR, gotG, gotB, gotA := got.RGBA()
	wantR, wantG, wantB, wantA := want.RGBA()
	return gotR == wantR && gotG == wantG && gotB == wantB && gotA == wantA
}

// newTestShellSession creates an interactive pane without loading the user's
// shell rc files. Most integration tests below exercise tmux byte transport,
// not shell startup; using /bin/sh keeps those concerns independent.
func newTestShellSession(ctx context.Context, name string, cols, rows int) error {
	_, err := run(ctx, "new-session", "-d", "-s", name, "-c", os.TempDir(),
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), "/bin/sh")
	if err == nil {
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

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

func TestResizeWindowsContinuesPastADeadSession(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-resize-batch-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := NewSession(ctx, name, os.TempDir(), 80, 20); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })

	_ = ResizeWindows(ctx, []string{"dma-definitely-not-live", name}, 91, 27)
	if got := windowSize(t, name); got != "91x27" {
		t.Fatalf("live session after dead target is %s, want 91x27", got)
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

	name := "dma-sendtext-" + strings.ReplaceAll(t.Name(), "/", "-")
	// cat records each payload without interpreting its shell syntax.
	if err := newTestShellSession(ctx, name, 200, 40); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })
	if err := SendLiteral(ctx, name, "exec cat"); err != nil {
		t.Fatalf("start cat: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	for _, tc := range tests {
		if err := SendText(ctx, name, tc.text); err != nil {
			t.Fatalf("SendText(%q): %v", tc.text, err)
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
	// cat echoes the line twice: the terminal's own echo, then cat's copy.
	lines := strings.Split(out, "\n")
	for _, tc := range tests {
		count := 0
		for _, line := range lines {
			if strings.TrimRight(line, " ") == tc.text {
				count++
			}
		}
		if count < 2 {
			t.Errorf("SendText(%q) did not arrive verbatim; pane holds:\n%s", tc.text, out)
		}
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
	if err := newTestShellSession(ctx, name, 200, 30); err != nil {
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

// A full-screen TUI owns its scrollback and asks the terminal for mouse input.
// The focused board panel is not a tmux client, so it recreates the SGR event
// from the Bubble Tea mouse message and sends those bytes directly.
func TestSendMouseWheelDeliversSGREvent(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-send-wheel"
	if err := newTestShellSession(ctx, name, 80, 20); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })
	// Enable basic tracking plus SGR coordinates, as Claude Code does, then
	// render received control bytes so the capture can assert on them.
	if err := SendLiteral(ctx, name, "printf '\\033[?1000h\\033[?1006h'; exec cat -v"); err != nil {
		t.Fatalf("start mouse-aware reader: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	sent, err := SendMouseWheel(ctx, name, true, 5, 7)
	if err != nil {
		t.Fatalf("SendMouseWheel: %v", err)
	}
	if !sent {
		t.Fatal("SendMouseWheel did not recognize the pane's SGR mouse mode")
	}
	sent, err = SendMouseWheel(ctx, name, false, -3, 99)
	if err != nil {
		t.Fatalf("SendMouseWheel(down): %v", err)
	}
	if !sent {
		t.Fatal("SendMouseWheel(down) did not recognize the pane's SGR mouse mode")
	}
	if err := SendKey(ctx, name, "Enter"); err != nil {
		t.Fatalf("finish input line: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	pane, err := CapturePane(ctx, name, 0)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(pane.Content, "^[[<64;6;8M") {
		t.Errorf("wheel-up event did not arrive with one-based coordinates:\n%s", pane.Content)
	}
	if !strings.Contains(pane.Content, "^[[<65;1;20M") {
		t.Errorf("wheel-down event was not clamped to the pane:\n%s", pane.Content)
	}
}

func TestSendPastePreservesMultilineBracketedText(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-send-paste"
	if err := newTestShellSession(ctx, name, 200, 30); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })
	// Request bracketed paste explicitly before replacing the shell with cat.
	if err := SendLiteral(ctx, name, `printf '\033[?2004h'; exec cat -v`); err != nil {
		t.Fatalf("start bracket-aware cat: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := SendPaste(ctx, name, "first line\nsecond line;"); err != nil {
		t.Fatalf("SendPaste: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	pane, err := CapturePane(ctx, name, 0)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	for _, want := range []string{"^[[200~first line", "second line;^[[201~"} {
		if !strings.Contains(pane.Content, want) {
			t.Errorf("paste missing %q; pane holds:\n%s", want, pane.Content)
		}
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
	if err := newTestShellSession(ctx, name, 80, 24); err != nil {
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

// A panel is not a tmux client, so its scrollback is captured by range rather
// than by entering copy mode. This pins the range arithmetic against a real
// pane and verifies requests stop at the oldest retained line.
func TestCapturePaneAtScrollsHistory(t *testing.T) {
	if !Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "dma-scroll-" + strings.ReplaceAll(t.Name(), "/", "-")
	if err := newTestShellSession(ctx, name, 80, 8); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = KillSession(context.Background(), name) })
	if err := SendLiteral(ctx, name, "seq 1 30; sleep 20"); err != nil {
		t.Fatalf("fill pane history: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	pane, actual, err := CapturePaneAt(ctx, name, 3)
	if err != nil {
		t.Fatalf("CapturePaneAt: %v", err)
	}
	if actual != 3 {
		t.Fatalf("actual scroll = %d, want 3", actual)
	}
	for _, want := range []string{"21", "22", "28"} {
		if !strings.Contains(pane.Content, want) {
			t.Errorf("scrolled capture missing %q:\n%s", want, pane.Content)
		}
	}
	if strings.Contains(pane.Content, "30") {
		t.Errorf("scrolled capture still contains live bottom:\n%s", pane.Content)
	}
	if pane.Cursor.Visible {
		t.Error("history capture exposed the live pane cursor")
	}

	_, actual, err = CapturePaneAt(ctx, name, 999)
	if err != nil {
		t.Fatalf("CapturePaneAt(oldest): %v", err)
	}
	if actual >= 999 {
		t.Errorf("scroll request was not clamped: actual=%d", actual)
	}
}

// TestNewSessionClearsInheritedDirenvState covers the launch failure described
// on direnvState: a session that starts believing another checkout's .envrc is
// loaded has its PATH replaced at the first prompt, and the agent dma types is
// then not on it.
//
// It asserts the override on the session rather than what the pane's shell ends
// up with, and the reason is worth recording: direnv unsets these variables as
// part of unloading. A pane asked what it inherited answers "nothing" either
// way -- the state is gone by the first prompt, having already taken PATH with
// it -- so reading them there tests nothing at all, which an earlier version of
// this test did. What can be checked is the thing that decides the outcome:
// whether the session carries an empty value, which is what direnv reads as
// nothing to unload.
//
// tmux reports a variable it has no override for as an error, so the two cases
// are distinguishable and the assertion cannot pass by inheriting.
func TestNewSessionClearsInheritedDirenvState(t *testing.T) {
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

	if len(direnvState) == 0 {
		t.Fatal("direnvState is empty, so nothing is being cleared")
	}
	for _, key := range direnvState {
		out, err := exec.Command("tmux", "show-environment", "-t", name, key).CombinedOutput()
		got := strings.TrimSpace(string(out))
		if err != nil {
			t.Errorf("session does not clear %s, so it inherits whatever the "+
				"server holds: tmux show-environment says %q", key, got)
			continue
		}
		if want := key + "="; got != want {
			t.Errorf("session has %s as %q, want %q", key, got, want)
		}
	}
}
