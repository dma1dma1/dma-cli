package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A forwarded keystroke must open the fast re-read window. Without it the only
// capture is the 1.2s preview tick, and Escape -- which an agent may sit on for
// 80ms before it redraws -- looks like it did nothing for over a second.
func TestForwardedKeyStartsEcho(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	next, cmd := m.handleKey(tea.KeyPressMsg{Code: 27}) // esc
	got := next.(Model)
	if !got.echoing {
		t.Error("esc did not start the echo ticker; the panel would hold a stale frame")
	}
	if !got.echoUntil.After(time.Now()) {
		t.Error("echo window is already closed")
	}
	if cmd == nil {
		t.Error("esc produced no command at all")
	}
}

// Typing extends the window rather than starting a second ticker per key: the
// captures are process spawns, and one chain is as fresh as five.
func TestEchoTickerIsNotStartedTwice(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	first := press(m, keyOf('x'))
	if !first.echoing {
		t.Fatal("first keystroke did not start the echo ticker")
	}
	was := first.echoUntil

	if cmd := (&first).startEcho(); cmd != nil {
		t.Error("a second keystroke started a second echo ticker")
	}
	if !first.echoUntil.After(was) {
		t.Error("a second keystroke did not extend the echo window")
	}
}

// The keystroke also has to be recorded, or the prober reads the character
// appearing in the composer as the agent producing output and moves a session
// nobody is running into the active column.
func TestForwardedKeyIsAttributedToTheUser(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	before := time.Now()
	next := press(m, keyOf('x'))
	if next.touchedAt["a"].Before(before) {
		t.Error("keystroke was not recorded against the session it was sent to")
	}
}

// The record is dropped with the session, so the map does not grow for the life
// of the process.
func TestProbeForgetsTouchesForDeadSessions(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.touchedAt["a"] = time.Now()
	m.touchedAt["gone"] = time.Now()

	probeCmd(m.prober, m.cfg, m.sessions, m.touchedAt, m.hookSeen)
	if _, ok := m.touchedAt["gone"]; ok {
		t.Error("kept a touch record for a session that no longer exists")
	}
	if _, ok := m.touchedAt["a"]; !ok {
		t.Error("dropped the touch record for a live session")
	}
}

// The window has to close, or the panel keeps scheduling captures forever.
func TestEchoStopsAfterWindow(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.echoing = true
	m.echoUntil = time.Now().Add(-time.Millisecond)

	next, cmd := m.Update(echoTickMsg(time.Now()))
	if next.(Model).echoing {
		t.Error("echo ticker kept running past its window")
	}
	if cmd != nil {
		t.Error("expired echo tick scheduled another one")
	}
}

// While the window is open the ticker owns the captures; the slow preview tick
// must not spawn a duplicate one in the same moment. Batching is the tell: a
// tick that captures returns the timer and the capture together, one that skips
// returns only the timer.
func TestPreviewTickSkipsCaptureWhileEchoing(t *testing.T) {
	m := testModel(nil, liveSess("a"))

	_, idle := m.Update(previewTickMsg(time.Now()))
	if !batches(idle) {
		t.Fatal("preview tick no longer captures when nothing else is")
	}

	m.echoing = true
	m.echoUntil = time.Now().Add(echoWindow)
	next, cmd := m.Update(previewTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("preview tick stopped rescheduling itself")
	}
	if batches(cmd) {
		t.Error("preview tick captured on top of the echo ticker")
	}
	if !next.(Model).echoing {
		t.Error("preview tick cancelled the echo window")
	}
}

// A tmux capture can take longer than the 40ms echo interval under load. The
// next tick must wait for it instead of adding another process to the queue.
func TestEchoCaptureDoesNotOverlap(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.echoing = true
	m.echoUntil = time.Now().Add(echoWindow)
	m.echoCaptureInFlight = true

	next, cmd := m.Update(echoTickMsg(time.Now()))
	got := next.(Model)
	if !got.echoCaptureInFlight {
		t.Fatal("an in-flight echo capture was forgotten")
	}
	if batches(cmd) {
		t.Error("echo tick started a second capture while the first was in flight")
	}

	next, _ = got.Update(previewMsg{echo: true})
	if next.(Model).echoCaptureInFlight {
		t.Error("completed echo capture did not release the next tick")
	}
}

// batches reports whether cmd is a tea.Batch, which resolves at once, as
// opposed to a lone timer, which does not resolve for as long as it is set for.
func batches(cmd tea.Cmd) bool {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		_, ok := msg.(tea.BatchMsg)
		return ok
	case <-time.After(250 * time.Millisecond):
		return false
	}
}
