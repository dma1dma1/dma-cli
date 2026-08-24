package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestForwardedKeysEnterOneOrderedQueue(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	first, _ := m.handleKey(keyOf('a'))
	afterFirst := first.(Model)
	if !afterFirst.paneInputSending {
		t.Fatal("first key did not start the pane input queue")
	}
	if len(afterFirst.paneInputs) != 1 {
		t.Fatalf("queue has %d inputs after first key, want 1", len(afterFirst.paneInputs))
	}

	second, _ := afterFirst.handleKey(keyOf('b'))
	afterSecond := second.(Model)
	if len(afterSecond.paneInputs) != 2 {
		t.Fatalf("queue has %d inputs after second key, want 2", len(afterSecond.paneInputs))
	}
}

func TestPaneInputCompletionStartsNextQueuedInput(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	first, _ := m.handleKey(keyOf('a'))
	second, _ := first.(Model).handleKey(tea.KeyPressMsg{Code: 13})
	queued := second.(Model)

	next, cmd := queued.update(paneInputDoneMsg{})
	got := next.(Model)
	if !got.paneInputSending {
		t.Error("queue stopped while another input was waiting")
	}
	if len(got.paneInputs) != 1 {
		t.Fatalf("queue has %d inputs after completion, want 1", len(got.paneInputs))
	}
	if cmd == nil {
		t.Error("completion did not start the next input")
	}
}
