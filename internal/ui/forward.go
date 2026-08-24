package ui

import (
	"context"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// This file translates a Bubble Tea keypress into what tmux send-keys wants, so
// the session panel can drive the agent in place instead of only after an
// attach.
//
// Two representations of the same keystroke are possible and the choice matters.
// Printable characters go through send-keys -l, which takes them literally: a
// user typing the word "Enter" into an agent's prompt must not press Return, and
// a tilde or semicolon must not be read as a tmux key name. Everything else goes
// as a tmux key name, because there is no other way to spell Escape or C-c.

// forwardedKey is a keystroke resolved into a single send-keys call.
type forwardedKey struct {
	// arg is either literal text or a tmux key name, per literal.
	arg     string
	literal bool
}

// tmuxKeyNames maps the Bubble Tea spelling of a special key to tmux's.
//
// Only keys both sides agree exist are listed. Anything absent is dropped
// rather than guessed at: a wrong key name makes tmux either error or send
// something the user did not type, and silently typing the wrong thing into a
// coding agent is worse than dropping the keystroke.
var tmuxKeyNames = map[string]string{
	"enter":     "Enter",
	"tab":       "Tab",
	"backspace": "BSpace",
	"esc":       "Escape",
	"space":     "Space",
	"up":        "Up",
	"down":      "Down",
	"left":      "Left",
	"right":     "Right",
	"insert":    "IC",
	"delete":    "DC",
	"pgup":      "PageUp",
	"pgdown":    "PageDown",
	"home":      "Home",
	"end":       "End",
	"f1":        "F1",
	"f2":        "F2",
	"f3":        "F3",
	"f4":        "F4",
	"f5":        "F5",
	"f6":        "F6",
	"f7":        "F7",
	"f8":        "F8",
	"f9":        "F9",
	"f10":       "F10",
	"f11":       "F11",
	"f12":       "F12",
}

// tmuxMods maps Bubble Tea's modifier prefixes to tmux's. Bubble Tea emits them
// in a documented order (ctrl, alt, shift, meta, ...), so a prefix scan is
// enough and no sorting is needed.
var tmuxMods = map[string]string{
	"ctrl":  "C-",
	"alt":   "M-",
	"shift": "S-",
}

// tmuxCombos are modified keystrokes tmux names outright rather than as a
// prefix plus a base key. They are checked before the prefix scan, because the
// composed spelling is not merely uglier -- it is wrong.
//
// shift+tab is the case that matters: tmux accepts "S-Tab" without complaint and
// sends a plain tab, so the agent receives Tab and the shift is lost with no
// error anywhere. "BTab" is the name that sends the CSI Z the terminal actually
// defines as back-tab, which is how Claude Code reads shift+tab.
var tmuxCombos = map[string]string{
	"shift+tab": "BTab",
}

// tmuxKey resolves a keypress into one send-keys argument.
//
// ok is false when the keystroke has no faithful tmux spelling. Callers drop
// those; see tmuxKeyNames.
func tmuxKey(k tea.Key) (fk forwardedKey, ok bool) {
	name := k.Keystroke()

	// Printable text with no ctrl/alt is the common case and the one that must
	// stay literal. Shift is not excluded: it is already baked into the text, and
	// "S-A" would be a different keystroke than the "A" the user typed.
	if k.Text != "" && !k.Mod.Contains(tea.ModCtrl) && !k.Mod.Contains(tea.ModAlt) {
		return forwardedKey{arg: k.Text, literal: true}, true
	}

	if combo, isCombo := tmuxCombos[name]; isCombo {
		return forwardedKey{arg: combo}, true
	}

	var prefix strings.Builder
	base := name
	for {
		plus := strings.Index(base, "+")
		// A trailing "+" is the plus key itself, not a modifier separator.
		if plus <= 0 {
			break
		}
		mod, ok := tmuxMods[base[:plus]]
		if !ok {
			// meta, hyper and super have no send-keys spelling worth guessing.
			return forwardedKey{}, false
		}
		prefix.WriteString(mod)
		base = base[plus+1:]
	}

	if named, isNamed := tmuxKeyNames[base]; isNamed {
		return forwardedKey{arg: prefix.String() + named}, true
	}
	// A single character with a modifier: ctrl+c, alt+b. tmux spells these as the
	// prefix plus the character, and only single characters are safe here -- a
	// multi-rune base is a key name neither side agreed on.
	if prefix.Len() > 0 && len([]rune(base)) == 1 {
		return forwardedKey{arg: prefix.String() + base}, true
	}
	return forwardedKey{}, false
}

// --- modal composers ---

// composerNormalMode matches the mode a modal composer reports in its own status
// line. Codex draws "Vim: Normal" there when its composer is in vim's normal
// mode, and "Vim: Insert" when a typed letter is text.
//
// The marker is the only thing that has to be recognized here, and it answers
// two questions at once: whether this agent's composer is modal at all, and
// which mode it is in. An agent that draws no marker is not modal, so nothing is
// sent to it -- and neither is anything sent while a dialog is open, since the
// composer is not on screen for a Codex approval prompt and the marker goes with
// it. Keystrokes then reach the dialog, which is what answering one needs.
// It is anchored to the end of the status line, where the mode is drawn. That
// costs nothing and rules out an agent that merely wrote the words: a stray "A"
// in the middle of somebody's prompt is a worse failure than this quietly
// stopping if Codex ever appends another segment after the mode.
var composerNormalMode = regexp.MustCompile(`(?i)\bvim:\s*normal$`)

// paneStyling matches one terminal escape sequence. The pane is captured with
// its styling so the panel can render it, and Codex colors the mode marker, so
// the styling has to come off before the marker can be read.
var paneStyling = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// insertKey puts a modal composer into insert mode with the cursor after
// everything already typed there -- vim's "append at end of line". Plain "i"
// would insert wherever normal mode happens to have left the cursor, which for
// somebody about to type a message is the wrong end of their own draft.
const insertKey = "A"

// insertModeCmd puts a modal composer into insert mode before the panel starts
// forwarding keystrokes to it.
//
// Codex can be configured with a vim composer (`tui.vim_mode_default`), and it
// returns to normal mode after every message it submits. The panel forwards what
// the user types one key at a time, so in normal mode a sentence is executed
// rather than typed: "Run the tests" enters replace mode, undoes an edit,
// deletes to end of line and substitutes a character, and what arrives in the
// composer is neither the message nor nothing.
//
// This runs on the transition into panel focus -- not per keystroke -- because it
// is the one moment the keyboard changes hands, and because a mid-word capture
// that read the mode wrongly would type a stray "A" into the user's own
// sentence. For the same reason the pane is captured fresh here rather than read
// from the panel's last frame, which can be a second old.
func insertModeCmd(s *core.Session) tea.Cmd {
	if s == nil || !s.TmuxAlive {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		prepareComposer(ctx, sess.TmuxSession)
		return nil
	}
}

func prepareComposer(ctx context.Context, tmuxSession string) bool {
	pane, err := tmuxx.CapturePane(ctx, tmuxSession, 0)
	if err != nil || !inNormalMode(pane.Content) {
		return false
	}
	// Best effort, and silent: the panel still works if this does not land,
	// and an error about a mode the user never asked about would be noise.
	return tmuxx.SendText(ctx, tmuxSession, insertKey) == nil
}

// inNormalMode reports whether the pane shows a modal composer sitting in normal
// mode, where what the user types next would be read as commands.
//
// Only the last line of content is read, because that is the status line: an
// agent draws its composer and status under everything it has said, so the mode
// is on the bottom row whatever else is on screen -- and a marker anywhere else
// is not a marker.
func inNormalMode(content string) bool {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(paneStyling.ReplaceAllString(lines[i], ""))
		if line == "" {
			continue
		}
		return composerNormalMode.MatchString(line)
	}
	return false
}
