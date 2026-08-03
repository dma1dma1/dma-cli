package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
)

func TestPreviewDragSelectsAndKeepsItsFrame(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "alpha bravo\ncharlie delta"
	m.layoutSizes()
	rendered(t, m, zonePreview)
	z := zone.Get(zonePreview)

	next, _ := m.handleMouse(tea.MouseClickMsg{
		X: z.StartX, Y: z.StartY, Button: tea.MouseLeft,
	})
	m = next.(Model)
	if m.focus == focusPreview {
		t.Fatal("mouse-down focused the agent before the gesture was known to be a click")
	}

	next, _ = m.handleMouse(tea.MouseMotionMsg{
		X: z.StartX + 4, Y: z.StartY, Button: tea.MouseLeft,
	})
	m = next.(Model)
	next, cmd := m.handleMouse(tea.MouseReleaseMsg{
		X: z.StartX + 4, Y: z.StartY, Button: tea.MouseLeft,
	})
	m = next.(Model)
	if !m.previewSelection.active {
		t.Fatal("drag left no active preview selection")
	}
	if got := m.previewSelection.text(); got != "alpha" {
		t.Errorf("selected text = %q, want alpha", got)
	}
	if cmd == nil {
		t.Error("releasing a selection scheduled no clipboard copy")
	}
	if _, repeated := m.handleMouse(tea.MouseReleaseMsg{
		X: z.StartX + 4, Y: z.StartY, Button: tea.MouseLeft,
	}); repeated != nil {
		t.Error("a second release copied the same selection again")
	}

	// A live capture may land immediately after release. The selection owns the
	// frame it began on, so neither its text nor its highlight moves underneath
	// the user.
	next, _ = m.Update(previewMsg{id: "a", content: "new live frame"})
	m = next.(Model)
	body := strings.Join(m.previewBody(len(m.previewSelection.lines), m.inputWidth()), "\n")
	if plain := ansi.Strip(body); !strings.Contains(plain, "alpha bravo") || strings.Contains(plain, "new live frame") {
		t.Errorf("live refresh replaced selected frame:\n%s", plain)
	}
	if !strings.Contains(body, "\x1b[7m") {
		t.Errorf("selected frame lost its reverse-video highlight: %q", body)
	}
}

func TestPreviewClickStillFocusesAgentOnRelease(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.preview = "agent output"
	m.layoutSizes()
	rendered(t, m, zonePreview)
	z := zone.Get(zonePreview)
	click := tea.MouseClickMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseLeft}

	next, _ := m.handleMouse(click)
	m = next.(Model)
	if m.focus == focusPreview {
		t.Fatal("preview focused on press instead of waiting for release")
	}
	next, _ = m.handleMouse(tea.MouseReleaseMsg(click))
	m = next.(Model)
	if m.focus != focusPreview {
		t.Error("an ordinary preview click no longer focuses the agent")
	}
	if m.previewSelection.held() {
		t.Error("ordinary click left selection state behind")
	}
}

func TestPreviewSelectionExtractsStyledWideTextInEitherDirection(t *testing.T) {
	lines := []string{"\x1b[31mzero αβ\x1b[0m    ", "second line    "}
	want := "αβ\nsecond"
	for _, s := range []previewSelection{
		{anchor: previewPoint{0, 5}, head: previewPoint{1, 5}, active: true, lines: lines},
		{anchor: previewPoint{1, 5}, head: previewPoint{0, 5}, active: true, lines: lines},
	} {
		if got := s.text(); got != want {
			t.Errorf("selected text = %q, want %q", got, want)
		}
	}
}

func TestKeyboardInputClearsHeldPreviewSelection(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.previewSelection = previewSelection{
		anchor: previewPoint{0, 0}, head: previewPoint{0, 2},
		active: true, lines: []string{"abc"},
	}

	next, _ := m.Update(keyOf('j'))
	if next.(Model).previewSelection.held() {
		t.Error("keyboard input left the old mouse selection highlighted")
	}
}

func TestOnlyARealResizeClearsHeldPreviewSelection(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.previewSelection = previewSelection{
		anchor: previewPoint{0, 0}, head: previewPoint{0, 2},
		active: true, lines: []string{"abc"},
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m = next.(Model)
	if !m.previewSelection.held() {
		t.Fatal("the poller's same-size window check erased the preview selection")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: m.width + 1, Height: m.height})
	if next.(Model).previewSelection.held() {
		t.Error("a real resize kept a selection whose coordinates are now stale")
	}
}
