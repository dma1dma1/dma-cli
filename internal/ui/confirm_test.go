package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// The footer is one line in every mode. A confirm that renders taller pushes the
// board off the top of the screen and the answer keys off the bottom, which
// leaves the user looking at a half-drawn frame with no visible way out.
func TestConfirmedPruneIsPersistedBeforeTeardown(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	s := sess("done", "", core.LifecycleMerged, core.AgentIdle, "r")
	m := testModel(nil, s)

	mm, _ := m.sessionAction("x")
	m = mm.(Model)
	mm, cmd := m.keyConfirm("y")
	m = mm.(Model)
	if cmd == nil {
		t.Fatal("confirm did not start teardown")
	}
	if !s.Pruning {
		t.Fatal("session was not marked as pruning before teardown")
	}
	stored, err := core.LoadSessions()
	if err != nil {
		t.Fatalf("load sessions: %v", err)
	}
	if len(stored) != 1 || !stored[0].Pruning {
		t.Fatalf("stored sessions = %+v, want one persisted prune", stored)
	}
	if got := len(m.establishedSessions()); got != 0 {
		t.Fatalf("pruning session remained eligible for background work: %d", got)
	}
}

func TestConfirmFooterIsOneLine(t *testing.T) {
	m := testModel(nil, sess("Hello!", "", core.LifecycleIdle, core.AgentNeedsYou, "r"))
	m.width, m.height = 120, 36
	m.layoutSizes()

	mm, _ := m.sessionAction("x")
	m = mm.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("x did not open a confirm: mode=%v", m.mode)
	}

	if n := strings.Count(m.statusBar(), "\n") + 1; n != 1 {
		t.Errorf("confirm footer is %d lines, want 1:\n%s", n, m.statusBar())
	}
	if lines := strings.Split(m.render(), "\n"); len(lines) > m.height {
		t.Errorf("confirm render is %d lines, want <= %d", len(lines), m.height)
	}
	if !strings.Contains(m.statusBar(), "Prune") {
		t.Errorf("confirm footer does not show the question:\n%s", m.statusBar())
	}
}
