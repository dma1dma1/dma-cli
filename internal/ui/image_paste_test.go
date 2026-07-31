package ui

import (
	"bytes"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/clip"
	"github.com/dma1dma1/dma-cli/internal/core"
)

func TestClipboardImageJoinsNewSessionRequest(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}}
	m := testModel(cfg)
	m.focus = focusInput

	png := []byte("image bytes")
	next, _ := m.handleClipboard(clipboardMsg{content: clip.Content{
		Image: &clip.Image{PNG: png, Width: 640, Height: 480},
	}})
	m = next.(Model)

	if got := m.imageSummary(); got != "[image 640×480] " {
		t.Fatalf("image summary = %q", got)
	}
	req, err := m.newSessionRequest("inspect this")
	if err != nil {
		t.Fatalf("newSessionRequest: %v", err)
	}
	if len(req.InitialImages) != 1 || !bytes.Equal(req.InitialImages[0].PNG, png) {
		t.Fatalf("initial images = %#v, want the pasted image", req.InitialImages)
	}

	// The operation owns its request bytes; clearing or replacing the composer
	// cannot mutate an already-started session.
	m.pendingImages[0].PNG[0] = 'X'
	if bytes.Equal(req.InitialImages[0].PNG, m.pendingImages[0].PNG) {
		t.Fatal("session request shares image storage with the composer")
	}
}

func TestClipboardTextStillPastesIntoNewSessionInput(t *testing.T) {
	m := testModel(nil)
	m.focus = focusInput
	m.input.Focus()

	next, _ := m.handleClipboard(clipboardMsg{content: clip.Content{Text: "line one\nline two"}})
	m = next.(Model)

	// The composer wraps onto as many rows as it needs, so pasted text keeps the
	// line breaks it came with rather than being flattened into one row. The
	// session is still named after a single line of it -- see firstLine.
	if got := m.input.Value(); got != "line one\nline two" {
		t.Fatalf("input = %q, want the pasted text with its line breaks kept", got)
	}
	if got := m.inputRows(); got != 2 {
		t.Fatalf("a two-line paste occupies %d rows, want 2", got)
	}
}

func TestBackspaceRemovesPendingImageAtStartOfInput(t *testing.T) {
	m := testModel(nil)
	m.focus = focusInput
	m.pendingImages = []clip.Image{
		{PNG: []byte("one"), Width: 10, Height: 20},
		{PNG: []byte("two"), Width: 30, Height: 40},
	}

	next, _ := m.keyInput(tea.KeyPressMsg{Code: 127}, "backspace")
	m = next.(Model)

	if len(m.pendingImages) != 1 || string(m.pendingImages[0].PNG) != "one" {
		t.Fatalf("pending images = %#v, want the last image removed", m.pendingImages)
	}
}

func TestTerminalPasteIsForwardedWithoutAttachedMode(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.TmuxAlive = true
	m := testModel(nil, s)
	m.selectedID = s.ID

	for _, focus := range []focusArea{focusBoard, focusPreview} {
		m.focus = focus
		next, cmd := m.handlePaste(tea.PasteMsg{Content: "first\nsecond;"})
		m = next.(Model)

		if cmd == nil {
			t.Fatalf("focus %v: terminal paste produced no forwarding command", focus)
		}
		if m.typedAt[s.ID].IsZero() {
			t.Fatalf("focus %v: terminal paste was not recorded as user input", focus)
		}
	}
}

func TestCtrlVFromBoardTargetsSelectedAgent(t *testing.T) {
	s := sess("a", "", core.LifecycleActive, core.AgentWorking, "r")
	s.TmuxAlive = true
	m := testModel(nil, s)
	m.selectedID = s.ID
	m.focus = focusBoard

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	m = next.(Model)

	if cmd == nil {
		t.Fatal("ctrl+v from the board produced no forwarding command")
	}
	if m.focus != focusBoard {
		t.Fatalf("ctrl+v changed focus to %v, want board focus preserved", m.focus)
	}
	if m.typedAt[s.ID].IsZero() {
		t.Fatal("ctrl+v was not recorded as user input")
	}
}
