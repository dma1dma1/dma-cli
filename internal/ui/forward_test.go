package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// printable characters must go out literally, because send-keys would otherwise
// read some of them as key names.
func TestTmuxKeyPrintableStaysLiteral(t *testing.T) {
	for _, text := range []string{"a", "A", "7", " ", ";", "~", "-", "é", "字"} {
		k := tea.Key{Code: []rune(text)[0], Text: text}
		fk, ok := tmuxKey(k)
		if !ok {
			t.Fatalf("tmuxKey(%q): not ok, want literal", text)
		}
		if !fk.literal {
			t.Errorf("tmuxKey(%q): literal=false, want true (send-keys would interpret it)", text)
		}
		if fk.arg != text {
			t.Errorf("tmuxKey(%q): arg=%q, want %q", text, fk.arg, text)
		}
	}
}

// The word "Enter" typed into an agent's prompt must not press Return. This is
// the case that makes the literal path mandatory rather than a nicety.
func TestTmuxKeyTextSpellingAKeyNameStaysLiteral(t *testing.T) {
	for _, text := range []string{"E", "n", "t", "e", "r"} {
		fk, ok := tmuxKey(tea.Key{Code: []rune(text)[0], Text: text})
		if !ok || !fk.literal {
			t.Fatalf("tmuxKey(%q): got %+v ok=%v, want literal", text, fk, ok)
		}
	}
}

func TestTmuxKeySpecialKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
		want string
	}{
		{"enter", tea.Key{Code: uv.KeyEnter}, "Enter"},
		{"esc", tea.Key{Code: uv.KeyEscape}, "Escape"},
		{"tab", tea.Key{Code: uv.KeyTab}, "Tab"},
		{"backspace", tea.Key{Code: uv.KeyBackspace}, "BSpace"},
		{"up", tea.Key{Code: uv.KeyUp}, "Up"},
		{"down", tea.Key{Code: uv.KeyDown}, "Down"},
		{"delete", tea.Key{Code: uv.KeyDelete}, "DC"},
		{"pgup", tea.Key{Code: uv.KeyPgUp}, "PageUp"},
		{"home", tea.Key{Code: uv.KeyHome}, "Home"},
		{"f1", tea.Key{Code: uv.KeyF1}, "F1"},
		// Space arrives as printable text in practice, but with an empty Text it
		// must still resolve rather than be dropped.
		{"space", tea.Key{Code: uv.KeySpace}, "Space"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fk, ok := tmuxKey(tc.key)
			if !ok {
				t.Fatalf("tmuxKey(%s): not ok", tc.name)
			}
			if fk.literal {
				t.Errorf("tmuxKey(%s): literal=true, want a key name", tc.name)
			}
			if fk.arg != tc.want {
				t.Errorf("tmuxKey(%s): arg=%q, want %q", tc.name, fk.arg, tc.want)
			}
		})
	}
}

// Modified keys are the whole reason the panel is usable: ctrl+c interrupts an
// agent and esc is how you stop one mid-turn.
func TestTmuxKeyModifiers(t *testing.T) {
	tests := []struct {
		name string
		key  tea.Key
		want string
	}{
		{"ctrl+c", tea.Key{Code: 'c', Mod: tea.ModCtrl}, "C-c"},
		{"ctrl+r", tea.Key{Code: 'r', Mod: tea.ModCtrl}, "C-r"},
		{"ctrl+v", tea.Key{Code: 'v', Mod: tea.ModCtrl}, "C-v"},
		{"alt+b", tea.Key{Code: 'b', Mod: tea.ModAlt}, "M-b"},
		// Not "S-Tab": tmux accepts that and sends a plain tab, losing the shift
		// silently. See tmuxCombos.
		{"shift+tab", tea.Key{Code: uv.KeyTab, Mod: tea.ModShift}, "BTab"},
		// Shift on other named keys does use the prefix form.
		{"shift+up", tea.Key{Code: uv.KeyUp, Mod: tea.ModShift}, "S-Up"},
		{"ctrl+alt+a", tea.Key{Code: 'a', Mod: tea.ModCtrl | tea.ModAlt}, "C-M-a"},
		// A ctrl-modified key carrying Text must not take the literal path: the
		// text is the bare character and would drop the modifier silently.
		{"ctrl+a with text", tea.Key{Code: 'a', Text: "a", Mod: tea.ModCtrl}, "C-a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fk, ok := tmuxKey(tc.key)
			if !ok {
				t.Fatalf("tmuxKey(%s): not ok", tc.name)
			}
			if fk.literal {
				t.Errorf("tmuxKey(%s): literal=true, want a key name", tc.name)
			}
			if fk.arg != tc.want {
				t.Errorf("tmuxKey(%s): arg=%q, want %q", tc.name, fk.arg, tc.want)
			}
		})
	}
}

// Shift on a printable character is already baked into the text, so it must not
// also become an S- prefix -- that would be a different keystroke.
func TestTmuxKeyShiftedPrintableIsLiteral(t *testing.T) {
	fk, ok := tmuxKey(tea.Key{Code: 'a', ShiftedCode: 'A', Text: "A", Mod: tea.ModShift})
	if !ok {
		t.Fatal("tmuxKey(shift+a): not ok")
	}
	if !fk.literal || fk.arg != "A" {
		t.Errorf("tmuxKey(shift+a): got %+v, want literal %q", fk, "A")
	}
}

// A key with no faithful tmux spelling is dropped rather than approximated:
// typing the wrong thing into a coding agent is worse than typing nothing.
func TestTmuxKeyUnmappableIsDropped(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.Key
	}{
		{"meta", tea.Key{Code: 'a', Mod: tea.ModMeta}},
		{"hyper", tea.Key{Code: 'a', Mod: tea.ModHyper}},
		{"super", tea.Key{Code: 'a', Mod: tea.ModSuper}},
		{"unnamed special", tea.Key{Code: uv.KeyLeftShift}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if fk, ok := tmuxKey(tc.key); ok {
				t.Errorf("tmuxKey(%s): ok with %+v, want dropped", tc.name, fk)
			}
		})
	}
}
