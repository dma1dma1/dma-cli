package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/clip"
)

// ctrl-u empties the whole composer, not just the row the cursor is on: a task
// arrives pasted and wrapped as often as it is typed.
func TestCtrlUClearsTheTaskInput(t *testing.T) {
	m := testModel(nil)
	m.focus = focusInput
	m.input.Focus()
	m.input.SetValue("first line\nsecond line\nthird line")
	m.layoutSizes()

	next, _ := m.handleKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = next.(Model)

	if got := m.input.Value(); got != "" {
		t.Fatalf("input = %q, want it cleared", got)
	}
	if got := m.inputRows(); got != 1 {
		t.Errorf("cleared input occupies %d rows, want 1", got)
	}
	if m.focus != focusInput {
		t.Errorf("focus = %v, want the input kept so typing can start again", m.focus)
	}
}

// The box holds one composed task, so clearing it takes the attachments with
// it rather than leaving an image behind to notice and remove separately.
func TestCtrlUClearsPendingImages(t *testing.T) {
	m := testModel(nil)
	m.focus = focusInput
	m.input.Focus()
	m.input.SetValue("inspect this")
	m.pendingImages = []clip.Image{{PNG: []byte("png"), Width: 640, Height: 480}}

	next, _ := m.keyInput(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}, "ctrl+u")
	m = next.(Model)

	if got := m.input.Value(); got != "" {
		t.Fatalf("input = %q, want it cleared", got)
	}
	if len(m.pendingImages) != 0 {
		t.Fatalf("pending images = %#v, want them cleared with the text", m.pendingImages)
	}
	if got := m.imageSummary(); got != "" {
		t.Errorf("input row still shows %q after clearing", got)
	}
}
