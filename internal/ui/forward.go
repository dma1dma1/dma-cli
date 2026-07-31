package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
