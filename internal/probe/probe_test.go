package probe

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
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

// Codex answering with a numbered list, captured from a live session. This is
// the frame that made a finished session claim it needed attention: the answer's
// own list is shaped exactly like a menu's options, down to the indentation.
const codexList = `
› Reply with a numbered list of three short tips about tmux. Do not run any commands.


• 1. Use Ctrl-b d to detach safely.
  2. Rename windows with Ctrl-b ,.
  3. Split panes using Ctrl-b % or Ctrl-b ".


› Improve documentation in @filename

  gpt-5.6-sol high · ~/.dma/worktrees/dma-cli/hello                Vim: Normal`

// A real Codex approval dialog. Note what the board has to go on: the interrupt
// hint is gone, the question is four lines above the options, and the marker on
// the selected row is the only thing separating this from codexList.
const codexApproval = `• Running curl -sI https://example.com


  Would you like to run the following command?

  Environment: local

  $ curl -sI https://example.com

› 1. Yes, proceed (y)
  2. Yes, and don't ask again for commands that start with ` + "`curl -sI https://example.com`" + ` (p)
  3. No, and tell Codex what to do differently (esc)

  Press enter to confirm or esc to cancel`

// The dialog Codex opens in a directory it has not been told to trust, which is
// the first thing a fresh session shows.
const codexTrust = `> You are in ~/.dma/worktrees/dma-cli/hello

  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt
  injection. Trusting the directory allows project-local config, hooks, and exec policies to load.

› 1. Yes, continue
  2. No, quit

  Press enter to continue`

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

// The flap this whole file exists to prevent. Opening a session hands its window
// to the real terminal and closing it pins the window back, and either one
// reflows every line on the pane. An agent that says when it is working is not
// saying it now, so a changed pane is a redraw -- not a turn nobody started.
func TestClassifyIgnoresARedrawOnAnIdlePane(t *testing.T) {
	for _, was := range []core.AgentState{core.AgentIdle, core.AgentDone} {
		prev := sample{previous: was, sawBusy: true}
		// quiet is 0: the pane changed between this frame and the last.
		if state, _, _ := classify(codexDone, 0, false, prev, true); state != was {
			t.Errorf("state = %q after a redraw, want %q left alone", state, was)
		}
	}
}

// Typing at an idle agent is the user working, not the agent. The pane changes
// under either, so the keystrokes dma forwarded are what tell them apart.
func TestClassifyTypingAtAnIdleAgentIsNotWork(t *testing.T) {
	prev := sample{previous: core.AgentDone}
	// quiet is 0: the character just landed in the composer.
	state, _, _ := classify("some agent output\n", 0, true, prev, true)
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

// Nothing is known about a pane the first time it is captured, because "is it
// still changing" needs two frames to answer. Guessing from a zero-length quiet
// window instead is what made every Codex card announce a turn at startup and
// finish it 25 seconds later.
func TestClassifyKeepsTheKnownStateOnFirstSight(t *testing.T) {
	for _, was := range []core.AgentState{core.AgentIdle, core.AgentDone, core.AgentWorking} {
		state, _, _ := classify(codexDone, 0, false, sample{previous: was}, false)
		if state != was {
			t.Errorf("state = %q on first sight, want %q kept until there is a baseline", state, was)
		}
	}
}

// A dialog answered outside dma -- while attached, or in another terminal -- has
// to release the badge, or "needs you" outlives the question that raised it.
func TestClassifyClearsNeedsYouOnceTheDialogGoes(t *testing.T) {
	prev := sample{previous: core.AgentNeedsYou, sawBusy: true}
	state, detail, _ := classify(codexDone, 0, false, prev, true)
	if state != core.AgentIdle {
		t.Errorf("state = %q, want idle: there is no dialog on the pane", state)
	}
	if detail != "" {
		t.Errorf("detail = %q, want it cleared with the dialog", detail)
	}
}

// An unambiguous request outranks the hint, which agents keep showing underneath
// their own prompts.
func TestClassifyPrefersNeedsYouOverTheHint(t *testing.T) {
	content := codexWorking + "\n  Do you want to proceed? [y/n]\n"
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
		"• Reviewing approval request (6s • esc to interrupt)",
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
		codexList,
		codexApproval,
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

// The dialogs Codex really draws, and what the badge should say about each.
func TestAwaitingInputRecognizesRealDialogs(t *testing.T) {
	cases := []struct {
		name, content, detail string
	}{
		{"approval", codexApproval, "Would you like to run the following command?"},
		{"trust", codexTrust, "› 1. Yes, continue"},
		{"y/n", "Overwrite main.go? [y/N]", "Overwrite main.go? [y/N]"},
		{"enter", "  Press enter to confirm or esc to go back", "Press enter to confirm or esc to go back"},
		{"waiting", "waiting for your approval", "waiting for your approval"},
		{"boxed", "│ Do you want to proceed?                    │", "Do you want to proceed?"},
	}
	for _, tc := range cases {
		detail, ok := awaitingInput(tc.content, false)
		if !ok {
			t.Errorf("%s: did not recognize a dialog", tc.name)
			continue
		}
		if detail != tc.detail {
			t.Errorf("%s: detail = %q, want %q", tc.name, detail, tc.detail)
		}
	}
}

// The reported bug: an answer that happens to be enumerated is not a question.
// Codex renders its own prose list "• 1. Use Ctrl-b d…" over "  2. Rename
// windows…", which is a menu's shape without a menu's marker.
func TestAwaitingInputIgnoresAnEnumeratedAnswer(t *testing.T) {
	cases := []struct{ name, content string }{
		{"codex answer", codexList},
		{"steps", "Here is the plan:\n1. Read the config\n2. Patch the handler\n3. Run the tests\n"},
		{"quoted", "> 1. first quoted point\n> 2. second quoted point\n"},
		{"prose allow", "  allow: GET, HEAD\n  age: 8536\n"},
		{"prose question", "I could approve the queued PR for you, want me to?\n"},
		{"ordinary output", "Reading files…\n  wrote internal/server/http.go\n  ran tests: 42 passed\nDone in 18s.\n"},
	}
	for _, tc := range cases {
		if detail, ok := awaitingInput(tc.content, false); ok {
			t.Errorf("%s: false positive, reported %q", tc.name, detail)
		}
	}
}

// A list arriving mid-turn is the agent writing, and the interrupt hint says so.
// Only an agent with nothing in flight is plausibly holding a menu open.
func TestAwaitingInputIgnoresAMenuShapeMidTurn(t *testing.T) {
	content := "› 1. Yes, proceed\n  2. No, stop\n• Working (4s • esc to interrupt)\n"
	if _, ok := awaitingInput(content, true); ok {
		t.Error("a numbered list read as a dialog while the agent was working")
	}
	if _, ok := awaitingInput(content, false); !ok {
		t.Error("the same options should count once the agent stops advertising a turn")
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
	if _, ok := awaitingInput(content, false); ok {
		t.Error("matched a prompt that had scrolled far up the pane")
	}
}

func TestAwaitingInputSeesThroughANSI(t *testing.T) {
	line := "\x1b[1;33mAllow this command?\x1b[0m"
	if _, ok := awaitingInput(line+"\n", false); !ok {
		t.Error("styling hid a prompt from the matcher")
	}
}

// Agents pad the pane below their composer and space their output out, so the
// window has to be a count of content rather than of rows -- a dozen rows off the
// bottom of a Codex pane is two lines of a dialog.
func TestTailSkipsBlankRows(t *testing.T) {
	content := "  Would you like to run this?\n" + strings.Repeat("\n", 20)
	if got := tail(content); len(got) != 1 || got[0] != "Would you like to run this?" {
		t.Fatalf("tail = %q, want the one line of content", got)
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[31mred\x1b[0m"); got != "red" {
		t.Fatalf("stripANSI = %q, want \"red\"", got)
	}
}

// cleanLine drops the decoration agents draw around their output, but never the
// marker that says which row of a menu is selected.
func TestCleanLineKeepsSelectionMarkers(t *testing.T) {
	cases := map[string]string{
		"  │ ❯ 1. Yes                    │": "❯ 1. Yes",
		"• 1. Use Ctrl-b d to detach":       "1. Use Ctrl-b d to detach",
		"› 1. Yes, proceed (y)":             "› 1. Yes, proceed (y)",
		"⏺ Ran two commands":                "Ran two commands",
	}
	for in, want := range cases {
		if got := cleanLine(in); got != want {
			t.Errorf("cleanLine(%q) = %q, want %q", in, got, want)
		}
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

// End to end against a real pane: the first probe of a session reports what the
// board already knows, whatever the pane looks like. Every card the board loads
// is on its first probe, so a guess here is a flap on every startup.
func TestProbeKeepsTheStoredStateOnFirstSight(t *testing.T) {
	ctx, name := livePane(t, 100, 30)

	for _, was := range []core.AgentState{core.AgentIdle, core.AgentWorking} {
		s := &core.Session{ID: "s1", TmuxSession: name, AgentState: was}
		got := New().Probe(ctx, s, time.Time{})
		if !got.Alive {
			t.Fatal("live session reported as gone")
		}
		if got.Agent != was {
			t.Errorf("first probe of a %q session = %q, want %q", was, got.Agent, was)
		}
	}
}

// The reported flap, run against a real tmux pane rather than a fixture: a
// session that has finished its turn must not walk back to working because
// somebody opened it. Attaching releases the window to the real terminal and
// detaching pins it back to the preview size, and every line on the pane reflows
// each way -- which is a wholesale change to the text the probe reads.
func TestProbeIgnoresAReflowAfterATurn(t *testing.T) {
	ctx, name := livePane(t, 100, 30)
	p := New()
	s := &core.Session{ID: "s1", TmuxSession: name, AgentState: core.AgentIdle}

	// Long enough to wrap differently at either window width, so a resize really
	// does rewrite the pane.
	const wrapping = `'a line long enough that it wraps at one window width and not at the other, which is the whole point of it'`
	show := func(last string) {
		if err := tmuxx.SendLiteral(ctx, name, "clear; printf '%s\\n' "+wrapping+" '"+last+"'"); err != nil {
			t.Fatalf("write to pane: %v", err)
		}
		time.Sleep(700 * time.Millisecond)
	}
	probe := func() core.AgentState {
		got := p.Probe(ctx, s, time.Time{})
		s.AgentState = got.Agent
		return got.Agent
	}

	show("• Working (8s • esc to interrupt)")
	if got := probe(); got != core.AgentWorking {
		t.Fatalf("mid-turn probe = %q, want working", got)
	}

	show("› Use /skills to list available skills")
	probe() // the frame the settle window is measured from
	time.Sleep(SettleAfter + 500*time.Millisecond)
	if got := probe(); got != core.AgentDone {
		t.Fatalf("probe after the hint went = %q, want done", got)
	}

	for _, size := range [][2]int{{180, 50}, {100, 30}} {
		if err := tmuxx.ResizeWindow(ctx, name, size[0], size[1]); err != nil {
			t.Fatalf("ResizeWindow: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
		if got := probe(); got != core.AgentDone {
			t.Errorf("probe after a resize to %dx%d = %q, want done left alone", size[0], size[1], got)
		}
	}
}

// livePane starts a real tmux session for a test to probe, and returns the
// context and session name to address it with.
func livePane(t *testing.T, cols, rows int) (context.Context, string) {
	t.Helper()
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	name := "dma-probe-test-" + strings.ToLower(t.Name())
	if err := tmuxx.NewSession(ctx, name, os.TempDir(), cols, rows); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), name) })
	return ctx, name
}
