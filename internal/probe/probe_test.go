package probe

import (
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// The two frames Codex actually draws, captured from a live session: the turn
// line carries the interrupt hint and vanishes when the turn ends, leaving the
// composer placeholder and the status bar behind.
const (
	codexWorking = `• Hello! What can I help you with?

› explain tmux panes

• Working (8s • esc to interrupt)

› Write tests for @filename

  gpt-5.6-sol high · ~/.dma/worktrees/dma-cli/hello                Vim: Normal`

	codexDone = `• Hello! What can I help you with?

› explain tmux panes

• Panes split one window into several terminals.

› Use /skills to list available skills

  gpt-5.6-sol high · ~/.dma/worktrees/dma-cli/hello                Vim: Normal`
)

func TestClassifyWorkingWhileInterruptHintShows(t *testing.T) {
	// Quiet long enough to be called idle on pane changes alone: the hint is
	// what keeps it working.
	state, _, sawBusy := classify(codexWorking, IdleAfter+time.Minute, false, sample{}, false)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working while the agent offers to be interrupted", state)
	}
	if !sawBusy {
		t.Error("did not remember that this agent shows an interrupt hint")
	}
}

// The point of the whole exercise: a finished Codex turn should not sit in the
// active column waiting out a 25s quiescence window.
func TestClassifyDoneAsSoonAsTheHintGoes(t *testing.T) {
	prev := sample{previous: core.AgentWorking, sawBusy: true}
	state, _, _ := classify(codexDone, SettleAfter, false, prev, true)
	if state != core.AgentDone {
		t.Errorf("state = %q, want done once the interrupt hint is gone", state)
	}
}

// A repaint between turns must not read as a finished turn.
func TestClassifyWaitsOutARepaintBeforeSettling(t *testing.T) {
	prev := sample{previous: core.AgentWorking, sawBusy: true}
	state, _, _ := classify(codexDone, SettleAfter/2, false, prev, true)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working until the pane holds still", state)
	}
}

// An agent that has never shown a hint gets the old, coarse treatment rather
// than being called done the moment it pauses.
func TestClassifyFallsBackToQuiescenceWithoutAHint(t *testing.T) {
	prev := sample{previous: core.AgentWorking}
	if state, _, _ := classify("some agent output\n", IdleAfter-time.Second, false, prev, true); state != core.AgentWorking {
		t.Errorf("state = %q, want working: nothing says this agent is finished", state)
	}
	if state, _, _ := classify("some agent output\n", IdleAfter, false, prev, true); state != core.AgentDone {
		t.Errorf("state = %q, want done after the pane has been quiet", state)
	}
}

// Typing at an idle agent is the user working, not the agent. The pane changes
// under either, so the keystrokes dma forwarded are what tell them apart.
func TestClassifyTypingAtAnIdleAgentIsNotWork(t *testing.T) {
	prev := sample{previous: core.AgentDone, sawBusy: true}
	// quiet is 0: the character just landed in the composer.
	state, _, _ := classify(codexDone, 0, true, prev, true)
	if state != core.AgentDone {
		t.Errorf("state = %q, want the session left alone while the user types at it", state)
	}
}

// The same keystrokes against an agent that is mid-turn must not talk the board
// out of active: the hint says the agent is busy, whoever else is typing.
func TestClassifyTypingDoesNotMaskAWorkingAgent(t *testing.T) {
	prev := sample{previous: core.AgentWorking, sawBusy: true}
	state, _, _ := classify(codexWorking, 0, true, prev, true)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working: the agent is still offering to be interrupted", state)
	}
}

// Typing suppression is about attribution, not about a quiet pane. Once the
// window lapses, a pane still changing is the agent's output again.
func TestClassifyResumesInferenceAfterTyping(t *testing.T) {
	prev := sample{previous: core.AgentDone, sawBusy: true}
	state, _, _ := classify(codexDone, 0, false, prev, true)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working: nothing accounts for the pane changing", state)
	}
}

// An approval request outranks the hint, which Codex keeps showing underneath
// its own prompts.
func TestClassifyPrefersNeedsYouOverTheHint(t *testing.T) {
	content := codexWorking + "\n  1. Yes, continue\n  2. No, quit\n"
	state, detail, _ := classify(content, 0, false, sample{}, false)
	if state != core.AgentNeedsYou {
		t.Fatalf("state = %q, want needs_you", state)
	}
	if detail == "" {
		t.Error("no detail recorded for a blocked session")
	}
}

func TestIsBusyRecognizesInterruptHints(t *testing.T) {
	cases := []string{
		"• Working (8s • esc to interrupt)",
		"• Starting MCP servers (5/6): resolve (2s • esc to interrupt)",
		"✻ Thinking… (12s · esc to interrupt · ctrl+t to show todos)",
		"press ctrl-c to cancel",
		"stop with Esc",
	}
	for _, line := range cases {
		if !isBusy("earlier output\n" + line + "\n") {
			t.Errorf("did not recognize a busy agent in %q", line)
		}
	}
}

func TestIsBusyIgnoresIdleChrome(t *testing.T) {
	cases := []string{
		codexDone,
		"› Use /skills to list available skills",
		"esc to edit previous message",
		"⏎ send   ⌃J newline   ⌃C quit",
		"  Interrupted the build to fix a flaky test.",
	}
	for _, content := range cases {
		if isBusy(content) {
			t.Errorf("false positive on idle pane %q", content)
		}
	}
}

// The hint sits just above the composer; one from a turn that has scrolled up
// is not a live one.
func TestIsBusyOnlyLooksAtTheTail(t *testing.T) {
	content := "• Working (3s • esc to interrupt)\n"
	for i := 0; i < 30; i++ {
		content += "later unrelated output line\n"
	}
	if isBusy(content) {
		t.Error("matched an interrupt hint that had scrolled far up the pane")
	}
}

func TestAwaitingInputRecognizesApprovalShapes(t *testing.T) {
	cases := []string{
		"Do you want to proceed? [y/n]",
		"Allow this command?",
		"Approve the edit to main.go?",
		"  1. Yes  2. No",
		"waiting for your approval",
		"Press enter to continue",
	}
	for _, line := range cases {
		if _, ok := awaitingInput("some earlier output\n" + line + "\n"); !ok {
			t.Errorf("did not recognize a prompt in %q", line)
		}
	}
}

func TestAwaitingInputIgnoresOrdinaryOutput(t *testing.T) {
	content := `Reading files…
  wrote internal/server/http.go
  ran tests: 42 passed
Done in 18s.`
	if detail, ok := awaitingInput(content); ok {
		t.Errorf("false positive on ordinary output: %q", detail)
	}
}

// A prompt printed long ago is not a live prompt. Only the tail of the pane is
// where a real request for input would sit.
func TestAwaitingInputOnlyLooksAtTheTail(t *testing.T) {
	var content string
	content += "Do you want to proceed? [y/n]\n"
	for i := 0; i < 30; i++ {
		content += "later unrelated output line\n"
	}
	if _, ok := awaitingInput(content); ok {
		t.Error("matched a prompt that had scrolled far up the pane")
	}
}

func TestAwaitingInputSeesThroughANSI(t *testing.T) {
	line := "\x1b[1;33mAllow this command?\x1b[0m"
	if _, ok := awaitingInput(line + "\n"); !ok {
		t.Error("styling hid a prompt from the matcher")
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[31mred\x1b[0m"); got != "red" {
		t.Fatalf("stripANSI = %q, want \"red\"", got)
	}
}

func TestForgetDropsUnknownSessions(t *testing.T) {
	p := New()
	p.last["gone"] = sample{}
	p.last["kept"] = sample{}
	p.Forget(map[string]bool{"kept": true})
	if _, ok := p.last["gone"]; ok {
		t.Error("state for a removed session was retained")
	}
	if _, ok := p.last["kept"]; !ok {
		t.Error("state for a live session was dropped")
	}
}
