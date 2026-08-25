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
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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

func capturePaneContent(ctx context.Context, args ...string) (string, error) {
	content, err := run(ctx, args...)
	if err != nil {
		return "", err
	}
	return isolateSGRRows(content), nil
}

// capturePaneArgs preserves trailing cells. Pi paints tool and status rows by
// writing background-colored spaces to the pane edge; without -N tmux removes
// those cells and the preview shrinks each fill to the text it contains.
func capturePaneArgs(name string) []string {
	return []string{"capture-pane", "-p", "-e", "-N", "-t", name}
}

// isolateSGRRows turns tmux's stateful ANSI stream into independently styled
// rows. A style selected at the end of one row still begins the next row, but
// cannot color padding or borders that the board appends to the current row.
func isolateSGRRows(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}

	parser := ansi.GetParser()
	defer ansi.PutParser(parser)

	var out strings.Builder
	out.Grow(len(s))
	var style uv.Style
	var state byte
	for len(s) > 0 {
		seq, _, n, newState := ansi.DecodeSequence(s, state, parser)
		if ansi.HasCsiPrefix(seq) && parser.Command() == 'm' {
			uv.ReadStyle(parser.Params(), &style)
		}
		if seq == "\n" {
			if !style.IsZero() {
				out.WriteString(ansi.ResetStyle)
			}
			out.WriteByte('\n')
			if !style.IsZero() {
				out.WriteString(style.String())
			}
		} else {
			out.WriteString(seq)
		}
		state = newState
		s = s[n:]
	}
	if !style.IsZero() {
		out.WriteString(ansi.ResetStyle)
	}
	return out.String()
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

// direnvState names the variables direnv writes into a shell to record which
// .envrc it has loaded and what that load changed.
//
// They have to be cleared out of a session dma creates, because tmux builds
// every new session's environment from the server's, and the server's was
// whatever the shell that first started tmux happened to have -- typically a
// checkout with an .envrc of its own. The pane then starts believing that
// .envrc is loaded, and direnv's shell hook, running at the first prompt in a
// directory the .envrc does not cover, unloads it: it replaces PATH wholesale
// with the snapshot DIRENV_DIFF holds from before that load.
//
// That snapshot predates the pane's own shell startup, so the unload also
// discards every PATH entry the user's rc files just added -- and an agent
// installed under one of them, as pi is under ~/.local/bin, is then not on PATH
// at all. The launch line dma types into the pane is a command not found, and
// what the board shows is a live terminal sitting at a shell prompt, which it
// reads as an agent that started.
//
// Emptying them is what "nothing is loaded here" looks like to direnv, so there
// is nothing to unload and the pane keeps the PATH its own startup built. It
// does not disable direnv: a worktree carrying its own .envrc still loads it at
// that first prompt, which is the whole point of authorizeMatchingDirenv.
var direnvState = []string{"DIRENV_DIFF", "DIRENV_DIR", "DIRENV_FILE", "DIRENV_WATCHES"}

// NewSession starts a detached session rooted at dir, sized to cols x rows.
//
// The size matters: a detached tmux session defaults to 80x24 regardless of the
// terminal, so an agent launched without one renders its whole UI into 80
// columns and the preview shows a narrow strip inside a wide panel.
func NewSession(ctx context.Context, name, dir string, cols, rows int) error {
	base := []string{"new-session", "-d", "-s", name, "-c", dir}
	if cols > 0 && rows > 0 {
		base = append(base, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	}

	args := base
	for _, key := range direnvState {
		args = append(args, "-e", key+"=")
	}
	if _, err := run(ctx, args...); err != nil {
		// -e is not in every tmux dma might be run against, and a session that
		// cannot be created at all is a worse outcome than one that inherits a
		// stale environment: without it the board has no terminal to put an
		// agent in, which is every start failing rather than the subset of them
		// direnvState describes. So the flags come off and the session is made
		// the way it was before they were added, with the first error kept if
		// that fails too -- it is the one that says what actually went wrong.
		if _, plain := run(ctx, base...); plain != nil {
			return err
		}
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

// windowTarget addresses a session's current window.
//
// The "=" prefix, which stops a session name from matching by prefix, is only
// accepted where tmux expects a session. Commands that take a target-window --
// resize-window, and set-option for a window option like window-size -- reject
// a bare "=name" with "no such window". The trailing ":" makes it a window
// target, so the exact-match prefix survives.
func windowTarget(name string) string { return "=" + name + ":" }

// ResizeWindow pins a detached session's window to an explicit size. tmux sets
// window-size to manual as a side effect.
func ResizeWindow(ctx context.Context, name string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	_, err := run(ctx, "resize-window", "-t", windowTarget(name),
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
	return err
}

// SetWindowSize switches a session between following clients and holding a
// fixed size. window-size is a window option, so it needs a window target.
func SetWindowSize(ctx context.Context, name, mode string) error {
	_, err := run(ctx, "set-option", "-t", windowTarget(name), "window-size", mode)
	return err
}

// SendKeys types a line into the session and presses Enter.
func SendKeys(ctx context.Context, name, text string) error {
	_, err := run(ctx, "send-keys", "-t", name, text, "Enter")
	return err
}

// SendKey sends one tmux key name -- "Enter", "C-c", "Up" -- and nothing else.
//
// Unlike SendKeys this appends no Enter: it forwards a single keystroke as the
// user typed it, which is what driving an agent from the panel needs.
func SendKey(ctx context.Context, name, key string) error {
	_, err := run(ctx, "send-keys", "-t", name, key)
	return err
}

// SendText types text literally, with no trailing Enter. Used for the printable
// characters of a forwarded keystroke, where "-l" is what keeps text that
// happens to spell a tmux key name ("Enter", "C-c") from being read as one.
func SendText(ctx context.Context, name, text string) error {
	return sendLiteral(ctx, name, text)
}

// SendPaste inserts a complete terminal paste into the pane. tmux wraps it in
// bracketed-paste markers when the application requested that mode, preserving
// multiline text as one paste instead of turning embedded newlines into Enter
// keypresses.
func SendPaste(ctx context.Context, name, text string) error {
	if text == "" {
		return nil
	}
	buffer := fmt.Sprintf("dma-paste-%s-%d", SafeName(name), time.Now().UnixNano())
	cmd := exec.CommandContext(ctx, "tmux", "load-buffer", "-b", buffer, "-")
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("tmux load-buffer: %s", msg)
	}
	if _, err := run(ctx, "paste-buffer", "-d", "-p", "-r", "-S",
		"-b", buffer, "-t", name); err != nil {
		_, _ = run(ctx, "delete-buffer", "-b", buffer)
		return err
	}
	return nil
}

// sendLiteral sends text with no interpretation and no trailing Enter.
//
// It exists because "-l" is not quite literal enough. tmux parses its own
// command line before -l applies, and a semicolon ending an argument is a
// command separator there: exactly one trailing ";" is swallowed, so "trailing;"
// arrives as "trailing" and ";" arrives as nothing at all. Leading and
// mid-string semicolons are safe, which is why this only special-cases the tail.
//
// The tail goes through -H, which takes hex bytes and so bypasses the parser
// entirely. Only the semicolons take that path: hex-encoding everything would
// triple the size of every prompt for one edge case.
func sendLiteral(ctx context.Context, name, text string) error {
	body := strings.TrimRight(text, ";")
	semis := len(text) - len(body)

	if body != "" {
		if _, err := run(ctx, "send-keys", "-t", name, "-l", body); err != nil {
			return err
		}
	}
	if semis == 0 {
		return nil
	}
	args := []string{"send-keys", "-t", name, "-H"}
	for range semis {
		args = append(args, "3b") // ';'
	}
	_, err := run(ctx, args...)
	return err
}

// SendLiteral types text without interpreting it as key names, then Enter.
// Used for user-supplied prompts, which may contain anything -- including a
// trailing semicolon, which is why it goes through sendLiteral.
func SendLiteral(ctx context.Context, name, text string) error {
	if err := sendLiteral(ctx, name, text); err != nil {
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

// Cursor is where a pane's text cursor sits, in cells from the top-left of the
// visible screen. Visible is the terminal's own show-cursor flag: agents hide
// the cursor while they are drawing, or while they are not taking input.
type Cursor struct {
	X, Y    int
	Visible bool
}

// Pane is one snapshot of a pane: what is on screen, and where the cursor is.
// Content preserves captured ANSI styling, but each row resets before its end
// and restates any carried style at the start of the next row.
//
// The two travel together because they are only meaningful together -- a cursor
// position describes a cell of a particular frame, and pairing it with a later
// capture would draw a caret in the wrong place.
type Pane struct {
	Content  string
	Cursor   Cursor
	MouseSGR bool
}

// CapturePane returns pane content, which is how the board shows agent output
// without owning a PTY. It is display only -- agent state comes from hooks, or
// from probing, never from parsing this.
//
// history <= 0 captures just the visible screen, which is what you want for a
// full-screen agent UI: those draw on the alternate screen, so their scrollback
// holds whatever was on the normal screen beforehand and splicing the two
// together renders stale fragments over the live view.
func CapturePane(ctx context.Context, name string, history int) (Pane, error) {
	args := capturePaneArgs(name)
	if history > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", history))
	}
	content, err := capturePaneContent(ctx, args...)
	if err != nil {
		return Pane{}, err
	}
	// Best effort: a pane whose cursor cannot be read still renders, just
	// without a caret, which beats failing the whole capture over decoration.
	cur, mouseSGR, _ := paneState(ctx, name)
	return Pane{Content: content, Cursor: cur, MouseSGR: mouseSGR}, nil
}

// SendMouseWheel forwards one SGR mouse-wheel event when the application in
// the pane has requested that protocol. The bool says whether anything was
// sent, so a caller can distinguish an application-owned wheel from a pane
// that did not request mouse input.
//
// x and y are zero-based pane coordinates. SGR mouse positions are one-based,
// and clamping them to the pane keeps a slightly stale board layout from
// producing a position outside an agent that has just resized.
func SendMouseWheel(ctx context.Context, name string, up bool, x, y int) (bool, error) {
	out, err := run(ctx, "list-panes", "-t", windowTarget(name),
		"-F", "#{mouse_sgr_flag} #{pane_width} #{pane_height}")
	if err != nil {
		return false, err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	var mouseSGR, width, height int
	if _, err := fmt.Sscanf(line, "%d %d %d", &mouseSGR, &width, &height); err != nil {
		return false, fmt.Errorf("tmux pane mouse state %q: %w", line, err)
	}
	if mouseSGR == 0 {
		return false, nil
	}

	x = min(max(x+1, 1), max(width, 1))
	y = min(max(y+1, 1), max(height, 1))
	button := 65 // wheel down in the SGR protocol
	if up {
		button = 64
	}
	return true, sendLiteral(ctx, name, fmt.Sprintf("\x1b[<%d;%d;%dM", button, x, y))
}

// CapturePaneAt returns the pane viewport scroll lines above its live position.
// The actual offset is returned as well, since tmux clamps a request at the
// oldest line in its history.
//
// This is deliberately independent of tmux copy mode. The board is only a
// captured view of the pane, not a tmux client, and entering copy mode here
// would leave the real session there too -- the next key forwarded from the
// panel would no longer have the same behavior as typing into the live agent.
func CapturePaneAt(ctx context.Context, name string, scroll int) (Pane, int, error) {
	if scroll <= 0 {
		pane, err := CapturePane(ctx, name, 0)
		return pane, 0, err
	}

	out, err := run(ctx, "list-panes", "-t", windowTarget(name),
		"-F", "#{pane_height} #{history_size}")
	if err != nil {
		return Pane{}, 0, err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	var height, history int
	if _, err := fmt.Sscanf(line, "%d %d", &height, &history); err != nil {
		return Pane{}, 0, fmt.Errorf("tmux pane history %q: %w", line, err)
	}

	scroll = min(scroll, history)
	if scroll == 0 {
		pane, err := CapturePane(ctx, name, 0)
		return pane, 0, err
	}
	// capture-pane numbers the live screen from 0 and its history backwards
	// from -1. Moving the whole height-row viewport up by scroll therefore
	// shifts both ends of the capture range by that amount.
	args := append(capturePaneArgs(name),
		"-S", strconv.Itoa(-scroll), "-E", strconv.Itoa(height-1-scroll))
	content, err := capturePaneContent(ctx, args...)
	if err != nil {
		return Pane{}, 0, err
	}
	// A history view has no meaningful application cursor: tmux's cursor still
	// describes the live screen below it.
	return Pane{Content: content}, scroll, nil
}

// PaneCursor reports the cursor tmux is holding for a pane.
//
// It is a separate query because capture-pane returns cells and nothing else:
// the cursor is terminal state, not screen content, so a captured frame of a
// full-screen agent has no caret in it anywhere.
//
// The numbers come from list-panes rather than display-message because only
// list-panes accepts the "=" exact-match target; display-message answers a
// "=name" target with empty format fields instead of an error.
func PaneCursor(ctx context.Context, name string) (Cursor, error) {
	cur, _, err := paneState(ctx, name)
	return cur, err
}

// paneState reads terminal state that belongs to the same pane frame. Keeping
// it in one query avoids adding a tmux process to every preview capture when
// the panel needs both the cursor and the application's mouse mode.
func paneState(ctx context.Context, name string) (Cursor, bool, error) {
	out, err := run(ctx, "list-panes", "-t", windowTarget(name),
		"-F", "#{cursor_x} #{cursor_y} #{cursor_flag} #{mouse_sgr_flag}")
	if err != nil {
		return Cursor{}, false, err
	}
	// One line per pane; sessions the board starts have exactly one, and the
	// first is the one capture-pane read.
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	var x, y, flag int
	if _, err := fmt.Sscanf(line, "%d %d %d", &x, &y, &flag); err != nil {
		return Cursor{}, false, fmt.Errorf("tmux pane state %q: %w", line, err)
	}
	fields := strings.Fields(line)
	mouseSGR := len(fields) >= 4 && fields[3] == "1"
	return Cursor{X: x, Y: y, Visible: flag == 1}, mouseSGR, nil
}

// AttachCmd builds the command that hands the terminal to tmux. When the TUI is
// itself inside tmux, switch-client is correct; attach-session would nest.
func AttachCmd(name string) *exec.Cmd {
	if InsideTmux() {
		return exec.Command("tmux", "switch-client", "-t", "="+name)
	}
	return exec.Command("tmux", "attach-session", "-t", "="+name)
}
