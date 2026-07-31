package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// The point of the feature: enter starts the agent, so writing a task over more
// than one line needs a key that means newline instead. All three spellings do
// it, because only some terminals report shift+enter as its own keypress.
func TestNewlineKeysBreakTheLineInsteadOfStartingAnAgent(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyEnter, Mod: tea.ModShift},
		{Code: tea.KeyEnter, Mod: tea.ModAlt},
		{Code: 'j', Mod: tea.ModCtrl},
	} {
		m := focusedInput(90, 40)
		m = typeTask(m, "first")
		m = press(m, key)
		m = typeTask(m, "second")

		if got, want := m.input.Value(), "first\nsecond"; got != want {
			t.Errorf("%s: input holds %q, want %q", key, got, want)
		}
		if m.focus != focusInput {
			t.Errorf("%s: left the input, so it started an agent instead of a line", key)
		}
		if m.input.Height() < 2 {
			t.Errorf("%s: field stayed %d row tall over two lines of task", key, m.input.Height())
		}
	}
}

// A task written over several lines is one session: the first line titles the
// card, and the whole of it is what the agent is started on.
func TestEnterStillStartsTheAgentOnAMultiLineTask(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}}
	m := testModel(cfg, sess("a", "", core.LifecycleActive, core.AgentWorking, "api"))
	m.layoutSizes()
	m.focus = focusInput
	m.input.Focus()

	m = typeTask(m, "port the store")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	m = typeTask(m, "and the board with it")

	req, err := m.newSessionRequest(strings.TrimSpace(m.input.Value()))
	if err != nil {
		t.Fatalf("newSessionRequest: %v", err)
	}
	if got, want := req.Title, "port the store"; got != want {
		t.Errorf("title is %q, want the first line %q", got, want)
	}
	if got, want := req.InitialPrompt, "port the store\nand the board with it"; got != want {
		t.Errorf("prompt is %q, want the whole task %q", got, want)
	}

	// Enter is still enter: it hands the board back and clears the field.
	next := press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.focus != focusBoard {
		t.Error("enter did not start an agent on the multi-line task")
	}
	if got := next.input.Value(); got != "" {
		t.Errorf("field kept %q after the agent was started", got)
	}
}
