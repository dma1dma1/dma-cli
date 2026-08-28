package probe

import (
	"context"
	"encoding/json"
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

// The frames pi draws. Its dialogs carry no numbers -- the rows are plain labels
// with an arrow on the selected one -- so the only thing separating one from an
// agent that started a line with an arrow is the line saying which keys move the
// selection.
const piTrustPrompt = `────────────────────────────────────────
Trust project folder?
/Users/someone/.dma/worktrees/proj/rate-limiter

This allows pi to load .pi settings and resources, install missing project packages, and execute project extensions.

→ Yes, and remember for this folder
  Yes, for this session only
  No

↑↓ navigate  enter select  escape cancel
────────────────────────────────────────`

// The same shape with no question above it, which is what /trust looks like. The
// marked row is then the best the badge can do.
const piTrustPanel = `────────────────────────────────────────
Project trust
/Users/someone/.dma/worktrees/proj/rate-limiter

Saved decision: none
Current session: untrusted

→ Yes, and remember for this folder
  No

↑↓ navigate  enter save  escape cancel
────────────────────────────────────────`

// Captured from /dagents when dma incorrectly reported that pi needed input.
const piAgentsOverlay = `── dstack tasks ───────────────────────────────────────────────
›   [-] dstack · investigation · 1/1 done · 7m13s
    [+] worker-1 · done · 2m10s

↑/↓ select · Enter/→ inspect · h hide history · Esc/q close`

const piWorking = `⏺ Read src/limiter.go (84 lines)

Working... (escape to interrupt)`

// pi's own startup header names the interrupt key without the verb, which is the
// difference between advertising a turn and listing a keybinding.
const piIdleHeader = `pi v0.84.2
escape interrupt · ctrl+c/ctrl+d clear/exit · / commands · ! bash · ctrl+t more
Press ctrl+t to show full startup help and loaded resources.`

func TestClassifyWorkingWhileInterruptHintShows(t *testing.T) {
	// Quiet long enough to be called idle on pane changes alone: the hint is
	// what keeps it working.
	state, _, sawBusy := classify("", codexWorking, IdleAfter+time.Minute, true, sample{}, false)
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
	state, _, _ := classify("", codexDone, SettleAfter, true, prev, true)
	if state != core.AgentDone {
		t.Errorf("state = %q, want done once the interrupt hint is gone", state)
	}
}

// A repaint between turns must not read as a finished turn.
func TestClassifyWaitsOutARepaintBeforeSettling(t *testing.T) {
	prev := sample{previous: core.AgentWorking, sawBusy: true}
	state, _, _ := classify("", codexDone, SettleAfter/2, true, prev, true)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working until the pane holds still", state)
	}
}

// The frame a stranded Claude Code session actually leaves behind, captured from
// the live pane that prompted this: a turn that finished, a Stop hook that could
// not reach a board mid-restart, and a card that went on claiming the agent was
// busy. The composer holds text the user typed and never sent, which is what
// makes this worth a fixture -- the pane of a finished agent is not empty, and
// the last thing on it is a line beginning with a selection marker.
const claudeStranded = `⏺ PR is open: https://github.com/dma1dma1/dma-cli/pull/39

⏺ Ran 1 stop hook
  ⎿  Stop hook error: connect ECONNREFUSED 127.0.0.1:8787

✻ Brewed for 2m 29s

────────────────────────────────────────────────────────────
❯ watch the PR until CI passes and reviews are resolved
────────────────────────────────────────────────────────────
  -- INSERT -- ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents`

// The pane a hook-backed session strands on has to resolve, or reconciling it is
// pointless: the board would hand the card to the probe and get "working" back
// forever. Nothing on this frame advertises a turn and nothing asks for a key, so
// the still pane is the whole answer and the turn reads as over.
func TestClassifySettlesAStrandedClaudePane(t *testing.T) {
	// Second sight of a pane that has not moved since the first: no change to
	// attribute, and nothing this agent has ever been seen to advertise.
	prev := sample{previous: core.AgentWorking}
	state, detail, _ := classify("", claudeStranded, 0, false, prev, true)
	if state != core.AgentDone {
		t.Errorf("state = %q (%q), want done: the turn ended before the board restarted", state, detail)
	}
}

// The same frame must not be read as a question. A composer with unsent text in
// it opens with the marker a menu puts on its selected row, and calling that a
// dialog would move a finished session to the front of the board and raise a
// desktop notification for it.
func TestAStrandedClaudePaneIsNotADialog(t *testing.T) {
	if line, ok := awaitingInput(claudeStranded, false); ok {
		t.Errorf("read %q as a request for input; it is a composer holding unsent text", line)
	}
	if isBusy("", claudeStranded) {
		t.Error("read a finished pane as a turn in flight")
	}
}

// An agent that has never shown a hint gets the old, coarse treatment rather
// than being called done the moment it pauses.
func TestClassifyFallsBackToQuiescenceWithoutAHint(t *testing.T) {
	prev := sample{previous: core.AgentWorking}
	if state, _, _ := classify("", "some agent output\n", IdleAfter-time.Second, true, prev, true); state != core.AgentWorking {
		t.Errorf("state = %q, want working: nothing says this agent is finished", state)
	}
	if state, _, _ := classify("", "some agent output\n", IdleAfter, true, prev, true); state != core.AgentDone {
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
		if state, _, _ := classify("", codexDone, 0, true, prev, true); state != was {
			t.Errorf("state = %q after a redraw, want %q left alone", state, was)
		}
	}
}

// Scrolling, typing or resizing changes a pane for a reason that is dma's, not
// the agent's. What has to be attributed is the whole gap since the last sample,
// because the change happened somewhere inside it: at four seconds a sample, an
// action is routinely older than a fixed few seconds by the time its frame is
// read.
func TestCausedAttributesWhatTheBoardDid(t *testing.T) {
	now := time.Now()
	probed := now.Add(-probeGap)
	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"never touched", time.Time{}, false},
		{"just now", now, true},
		{"mid-gap, older than any fixed window", probed.Add(probeGap / 2), true},
		{"just before the last sample, redraw still in flight", probed.Add(-ActionGrace / 2), true},
		{"long before the last sample", probed.Add(-time.Minute), false},
	}
	for _, tc := range cases {
		if got := caused(tc.at, probed); got != tc.want {
			t.Errorf("%s: caused = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// probeGap stands in for the board's sampling interval, which is deliberately
// longer than ActionGrace: the mismatch between the two is what the fixed window
// got wrong.
const probeGap = 4 * time.Second

// An agent that is mid-turn keeps its badge whoever else is touching the pane:
// the hint says it is busy, and nothing about a keystroke contradicts that.
func TestClassifyKeepsAWorkingAgentThroughALocalChange(t *testing.T) {
	prev := sample{previous: core.AgentWorking, sawBusy: true}
	// quiet is inherited, so it is long: the last change was the user's, not a
	// turn's. The hint is what has to keep the badge.
	state, _, _ := classify("", codexWorking, IdleAfter+time.Minute, true, prev, true)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working: the agent is still offering to be interrupted", state)
	}
}

// A pane nobody has ever seen move cannot be mid-turn, however young the clock
// is. The clock starts when the board starts watching, so the sample right after
// startup is always inside IdleAfter -- and reading that as output is a turn
// announced for a session that has done nothing at all.
func TestClassifyNeedsAChangeBeforeCallingItWork(t *testing.T) {
	for _, was := range []core.AgentState{core.AgentIdle, core.AgentDone} {
		prev := sample{previous: was}
		if state, _, _ := classify("", "some agent output\n", 0, false, prev, true); state != was {
			t.Errorf("state = %q on a pane that has never moved, want %q kept", state, was)
		}
	}
}

// Nothing is known about a pane the first time it is captured, because "is it
// still changing" needs two frames to answer. Guessing from a zero-length quiet
// window instead is what made every Codex card announce a turn at startup and
// finish it 25 seconds later.
func TestClassifyKeepsTheKnownStateOnFirstSight(t *testing.T) {
	for _, was := range []core.AgentState{core.AgentIdle, core.AgentDone, core.AgentWorking} {
		state, _, _ := classify("", codexDone, 0, true, sample{previous: was}, false)
		if state != was {
			t.Errorf("state = %q on first sight, want %q kept until there is a baseline", state, was)
		}
	}
}

// A dialog answered outside dma -- while attached, or in another terminal -- has
// to release the badge, or "needs you" outlives the question that raised it.
func TestClassifyClearsNeedsYouOnceTheDialogGoes(t *testing.T) {
	prev := sample{previous: core.AgentNeedsYou, sawBusy: true}
	state, detail, _ := classify("", codexDone, 0, true, prev, true)
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
	state, detail, _ := classify("", content, 0, true, sample{}, false)
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
		if !isBusy("", "earlier output\n"+line+"\n") {
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
		if isBusy("", content) {
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
	if isBusy("", content) {
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

// A dialog whose rows are not numbered is still a dialog. Without this a pi
// session sitting on its trust prompt reads as idle: nothing is changing on the
// pane, which is exactly what a blocked agent looks like.
func TestAwaitingInputRecognizesAnUnnumberedDialog(t *testing.T) {
	cases := []struct{ name, content, detail string }{
		{"trust prompt", piTrustPrompt, "Trust project folder?"},
		{"no question above it", piTrustPanel, "→ Yes, and remember for this folder"},
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

// A marker on its own is not a menu, and neither is a hint on its own. Both have
// to be there, and exactly one row can be marked -- a quoted block where every
// row opens the same way is not a selection.
func TestAwaitingInputNeedsMoreThanAMarker(t *testing.T) {
	cases := []struct{ name, content string }{
		{"arrow in prose", "The flow is:\n→ parse\n→ validate\n→ write\nDone in 18s.\n"},
		{"one arrow, no hint", "  → picked the shorter branch\n"},
		{"hint, no marker", "Here is how the picker works:\n↑↓ navigate between the rows\n"},
		{"quoted block", "> quoted line one\n> quoted line two\n↑↓ navigate  enter select\n"},
		{"navigational overlay", piAgentsOverlay},
		{"pi at rest", piIdleHeader},
	}
	for _, tc := range cases {
		if detail, ok := awaitingInput(tc.content, false); ok {
			t.Errorf("%s: false positive, reported %q", tc.name, detail)
		}
	}
}

// pi advertises its turns the way Codex does, so it gets the same treatment: the
// hint means working, and its absence plus a still pane means the turn is over.
func TestClassifyReadsAPiTurn(t *testing.T) {
	state, _, sawBusy := classify("", piWorking, IdleAfter+time.Minute, true, sample{}, false)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working", state)
	}
	if !sawBusy {
		t.Error("did not remember that pi shows an interrupt hint")
	}
	prev := sample{previous: core.AgentWorking, sawBusy: true}
	if state, _, _ := classify("", piIdleHeader, SettleAfter, true, prev, true); state != core.AgentDone {
		t.Errorf("state = %q, want done once the hint is gone", state)
	}
}

func TestClassifyReadsCurrentPiWorkingStatus(t *testing.T) {
	state, _, sawBusy := classify("pi", "  ⠦ Working...\n", IdleAfter+time.Minute, false,
		sample{previous: core.AgentDone}, true)
	if state != core.AgentWorking {
		t.Errorf("state = %q, want working", state)
	}
	if !sawBusy {
		t.Error("did not remember pi's working status")
	}
}

func TestClassifyDoesNotGiveOtherAgentsPiStatus(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
	}{
		{name: "other profile", profile: "custom"},
		{name: "pi prose", profile: "pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, _, sawBusy := classify(tc.profile, "  Working...\n", IdleAfter+time.Minute, false,
				sample{previous: core.AgentDone}, true)
			if state != core.AgentDone || sawBusy {
				t.Errorf("state = %q, sawBusy = %t, want done without a busy hint", state, sawBusy)
			}
		})
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

func TestProbePreservesLivenessWhenTmuxCheckTimesOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &core.Session{
		ID: "still-live", TmuxSession: "dma-timeout", TmuxAlive: true,
		AgentState: core.AgentWorking,
	}
	got := New().Probe(ctx, s, time.Time{})
	if !got.Alive || got.Agent != core.AgentWorking {
		t.Errorf("probe on tmux error = %+v, want prior live/working state", got)
	}
}

func TestProbeAttributionIgnoresBoardActionUntilRetired(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	p := &Prober{
		last: map[string]sample{},
		now:  func() time.Time { return now },
	}
	s := &core.Session{ID: "s1", AgentState: core.AgentIdle}
	ctx := context.Background()

	p.probePane(ctx, s, time.Time{}, tmuxx.Pane{Content: "idle\n"})

	now = now.Add(probeGap)
	acted := now.Add(-100 * time.Millisecond)
	if got := p.probePane(ctx, s, acted, tmuxx.Pane{Content: "the composer, echoed\n"}); got.Agent != core.AgentIdle {
		t.Fatalf("probe right after board action = %q, want idle", got.Agent)
	}

	now = now.Add(ActionGrace + time.Second)
	p.probePane(ctx, s, acted, tmuxx.Pane{Content: "the composer, echoed\n"})

	now = now.Add(probeGap)
	if got := p.probePane(ctx, s, acted, tmuxx.Pane{Content: "the agent, writing\n"}); got.Agent != core.AgentWorking {
		t.Fatalf("probe after unaccounted change = %q, want working", got.Agent)
	}
}

// A cwd does not identify the root Pi process because dstack workers may share
// it. Process ancestry does, and terminal snapshots must override stale pane text.
func TestDstackStatusIntegration(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	started := now.Add(-10 * time.Minute)
	processes := &ProcessTable{Procs: map[int]ProcInfo{
		100: {PID: 100, PPID: 1, StartTime: started.Add(-time.Minute)},
		200: {PID: 200, PPID: 100, StartTime: started},
		300: {PID: 300, PPID: 200, StartTime: started.Add(time.Second)},
	}}
	root := statusFixture("root-agent-01a0465d", 200, started, now.Add(-time.Second), "working")
	worker := statusFixture("worker-agent-01a0465e", 300, started.Add(time.Second), now.Add(-time.Second), "waiting_on_input")
	writeStatusFixture(t, dir, root)
	writeStatusFixture(t, dir, worker)

	p := &Prober{
		last: map[string]sample{}, statusDir: dir, procTable: processes,
		now: func() time.Time { return now },
	}
	s := &core.Session{ID: "dma-session", AgentProfile: "pi", AgentState: core.AgentIdle}
	pane := tmuxx.Pane{PanePID: 100, Content: "dmode · bg 1 running\n"}
	st := p.probePane(context.Background(), s, time.Time{}, pane)
	if st.AgentSessionID != root.SessionID || st.Agent != core.AgentWorking {
		t.Fatalf("nearest writer probe = %+v, want root session working", st)
	}

	root.Heartbeat.UpdatedAt = now.Add(-30 * time.Second).Format(time.RFC3339Nano)
	root.Rollup = "idle"
	writeStatusFixture(t, dir, root)
	s.AgentSessionID = root.SessionID
	if st = p.probePane(context.Background(), s, time.Time{}, pane); st.Agent != core.AgentWorking {
		t.Fatalf("stale live writer state = %q, want working", st.Agent)
	}

	p.procTable = &ProcessTable{Procs: map[int]ProcInfo{
		100: processes.Procs[100],
		200: {PID: 200, PPID: 100, StartTime: now},
	}}
	if st = p.probePane(context.Background(), s, time.Time{}, pane); st.Agent != core.AgentNeedsYou || st.Detail != "agent crashed" {
		t.Fatalf("crashed writer probe = %+v, want needs you", st)
	}

	p.procTable.Procs[300] = ProcInfo{PID: 300, PPID: 100, StartTime: started.Add(time.Second)}
	if st = p.probePane(context.Background(), s, time.Time{}, pane); st.AgentSessionID != worker.SessionID {
		t.Fatalf("replacement writer session = %q, want %q", st.AgentSessionID, worker.SessionID)
	}

	root.Shutdown = &struct {
		Clean bool   `json:"clean"`
		At    string `json:"at,omitempty"`
	}{Clean: true, At: now.Format(time.RFC3339Nano)}
	root.Rollup = "working"
	writeStatusFixture(t, dir, root)
	s.AgentSessionID = root.SessionID
	p.procTable = processes
	if st = p.probePane(context.Background(), s, time.Time{}, pane); st.Agent != core.AgentIdle {
		t.Fatalf("shutdown writer state = %q, want idle", st.Agent)
	}
}

func statusFixture(id string, pid int, started, heartbeat time.Time, rollup string) *StatusSnapshot {
	s := &StatusSnapshot{SchemaVersion: DstackSchemaVersion, SessionID: id, Rollup: rollup}
	s.Process.PID = pid
	s.Process.StartedAt = started.Format(time.RFC3339Nano)
	s.Process.Cwd = "/repo/worktree"
	s.Heartbeat.UpdatedAt = heartbeat.Format(time.RFC3339Nano)
	s.Heartbeat.IntervalMs = 5000
	s.Root.State = "working"
	return s
}

func writeStatusFixture(t *testing.T, dir string, snapshot *StatusSnapshot) {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DstackStatusPath(dir, snapshot.SessionID), data, 0600); err != nil {
		t.Fatal(err)
	}
}
