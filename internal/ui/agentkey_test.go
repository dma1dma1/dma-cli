package ui

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// The agent chip used to be reachable only by clicking it or tab-walking the
// focus ring, which is three keys away from the board for a choice made right
// before every new task. A is the direct route, mirroring p for projects.
func TestAOpensTheAgentPicker(t *testing.T) {
	m := press(testModel(nil, sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")), keyOf('A'))

	if !m.dropdown.open || m.dropdown.area != focusAgent {
		t.Fatalf("A did not open the agent picker: %+v", m.dropdown)
	}
	if m.focus != focusAgent {
		t.Errorf("focus = %v, want the agent chip", m.focus)
	}
	// Opening on the current choice means enter is a no-op rather than a silent
	// switch to whichever profile happens to be first.
	if got := m.dropdown.options[m.dropdown.cursor]; got != m.agentChoice {
		t.Errorf("picker opened on %q, want the active agent %q", got, m.agentChoice)
	}
}

// A is a board binding, so the running agent keeps it while the panel is focused.
func TestAIsNotStolenFromAFocusedAgent(t *testing.T) {
	m := testModel(nil, liveSess("a"))
	m.focus = focusPreview

	if next := press(m, keyOf('A')); next.dropdown.open {
		t.Error("A opened the picker while the agent had the keyboard")
	}
}
