package ui

import (
	"strings"
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

// The picker identifies what will run, not how dma observes it after launch.
// State acquisition is an implementation detail and does not help choose an
// agent, while the command does distinguish customized profiles with the same
// underlying agent.
func TestAgentPickerShowsCommandsWithoutStateAcquisitionDetails(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.AgentProfiles = []core.AgentProfile{
		{Name: "hooked", Command: "hooked --auto", Hooks: true},
		{Name: "probed", Command: "probed", Hooks: false},
	}
	cfg.DefaultProfile = "hooked"
	m := testModel(cfg)

	m.openDropdown(focusAgent)

	labels := strings.Join(m.dropdown.labels, "\n")
	for _, want := range []string{"hooked --auto", "probed"} {
		if !strings.Contains(labels, want) {
			t.Errorf("agent picker labels %q do not contain %q", labels, want)
		}
	}
	for _, detail := range []string{"state via hooks", "state via pane activity"} {
		if strings.Contains(labels, detail) {
			t.Errorf("agent picker exposes acquisition detail %q in %q", detail, labels)
		}
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
