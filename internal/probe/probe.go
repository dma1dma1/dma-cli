// Package probe infers agent state for agents that cannot report it.
//
// Claude Code reports state through lifecycle hooks, which is exact. Codex and
// anything else launched in a tmux session has no such channel, so state is
// inferred from process liveness, whether the pane is still changing, and
// whether the agent is showing its interrupt hint.
//
// This is deliberately a fallback. Pane text is a rendering of the agent's UI,
// not a state machine, so the heuristic is kept coarse: "is it still producing
// output" is durable, while matching on specific words is not.
package probe

import (
	"context"
	"crypto/sha256"
	"regexp"
	"strings"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// IdleAfter is how long a pane must stop changing before its agent counts as
// idle rather than working. Agents think for a while between writes, so this is
// generous enough not to flap on a slow model call.
const IdleAfter = 25 * time.Second

// SettleAfter is the same question asked of an agent that shows an interrupt
// hint while it works, where the hint's absence already argues the turn is
// over. The pane still has to hold still, because the hint is not on screen for
// every frame of a turn: Codex replaces it with the message it is streaming.
//
// A live turn was sampled to size this. The longest the pane stayed
// byte-identical mid-turn was 1.3s -- an agent that is working repaints at
// least once a second, if only to tick its own elapsed counter -- while the
// pane after the turn went still and stayed still. Three seconds clears the
// former with room to spare, which is what turns a stale "working" badge around
// in seconds rather than the better part of a minute.
const SettleAfter = 3 * time.Second

// TypingWindow is how long a forwarded keystroke accounts for what is on the
// pane. Inside it, a pane that changed is the user's own text appearing in the
// composer, which says nothing about the agent -- so it must not read as work.
//
// It has to outlast the gap between keystrokes rather than the keystroke
// itself, since a person typing a sentence pauses mid-word and the pane goes
// still each time they do.
const TypingWindow = 3 * time.Second

// promptPatterns match the shapes an approval or input request takes across
// agents. They are intentionally about punctuation and structure rather than
// specific product wording, which changes between releases.
var promptPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\[y/n\]`),
	regexp.MustCompile(`\[Y/n\]`),
	regexp.MustCompile(`\[y/N\]`),
	regexp.MustCompile(`\(y/n\)`),
	regexp.MustCompile(`(?i)\ballow\b.*\?`),
	regexp.MustCompile(`(?i)\bapprove\b.*\?`),
	regexp.MustCompile(`(?i)do you want to (proceed|continue|allow)`),
	regexp.MustCompile(`(?i)press enter to (continue|confirm)`),
	regexp.MustCompile(`(?i)waiting for (your )?(input|approval|confirmation)`),
	regexp.MustCompile(`(?i)^\s*❯?\s*\d\.\s`), // numbered choice list
}

// busyPatterns match the affordance an agent shows while a turn is in flight.
// A terminal agent that can be interrupted has to say which key does it, so the
// hint is present for exactly as long as there is something to interrupt --
// Codex renders "• Working (8s • esc to interrupt)" and drops the line the
// moment the turn ends.
//
// This matches the key-plus-verb idiom rather than the surrounding words, which
// differ per agent and per release. An agent that shows no such hint is not
// punished for it: absence only counts once a hint has been seen for that
// session, so anything else falls back to pane quiescence alone.
var busyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(esc|escape|⎋|ctrl[-+ ]?c|\^c)\b.{0,24}\bto (interrupt|cancel|stop)\b`),
	regexp.MustCompile(`(?i)\b(interrupt|cancel|stop)\b.{0,16}\bwith (esc|escape|ctrl[-+ ]?c)\b`),
}

// tailLines is how far up the pane a live hint or prompt can be. Both sit just
// above the composer; matching the whole screen would fire on anything the
// agent merely printed earlier.
const tailLines = 12

// State is one probe result.
type State struct {
	SessionID string
	Agent     core.AgentState
	Detail    string
	Alive     bool
	// Content is the captured pane, reused as the preview so a probe and a
	// preview do not each pay for a capture.
	Content string
	// Cursor belongs to that same frame, so the preview can draw a caret the
	// capture itself does not carry.
	Cursor tmuxx.Cursor
}

// Prober remembers the last pane fingerprint per session, which is what makes
// "still changing" answerable at all.
type Prober struct {
	last map[string]sample
}

type sample struct {
	hash     [32]byte
	changed  time.Time
	previous core.AgentState
	// sawBusy records that this agent has shown an interrupt hint at least
	// once, which is what makes the hint's absence meaningful later.
	sawBusy bool
}

func New() *Prober { return &Prober{last: map[string]sample{}} }

// Probe captures the pane and classifies the session.
//
// typedAt is when the board last forwarded a keystroke to this session, or the
// zero time. It is what lets the pane's own text be attributed: dma sent those
// characters, so the agent did not.
func (p *Prober) Probe(ctx context.Context, s *core.Session, typedAt time.Time) State {
	if !tmuxx.HasSession(ctx, s.TmuxSession) {
		delete(p.last, s.ID)
		return State{SessionID: s.ID, Agent: core.AgentIdle, Detail: "session ended", Alive: false}
	}

	pane, err := tmuxx.CapturePane(ctx, s.TmuxSession, 0)
	if err != nil {
		return State{SessionID: s.ID, Agent: s.AgentState, Alive: true}
	}
	content := pane.Content

	now := time.Now()
	h := sha256.Sum256([]byte(content))
	prev, seen := p.last[s.ID]

	changedAt := now
	if seen && prev.hash == h {
		changedAt = prev.changed
	}

	typing := !typedAt.IsZero() && now.Sub(typedAt) < TypingWindow

	st := State{SessionID: s.ID, Alive: true, Content: content, Cursor: pane.Cursor}
	var sawBusy bool
	st.Agent, st.Detail, sawBusy = classify(content, now.Sub(changedAt), typing, prev, seen)

	p.last[s.ID] = sample{hash: h, changed: changedAt, previous: st.Agent, sawBusy: sawBusy}
	return st
}

// classify decides a session's state from one captured frame, how long that
// frame has been unchanged, whether the user has just typed into it, and what
// the session looked like last time.
func classify(content string, quiet time.Duration, typing bool, prev sample, seen bool) (core.AgentState, string, bool) {
	busy := isBusy(content)
	sawBusy := busy || (seen && prev.sawBusy)

	// An approval prompt outranks activity: the pane may still be animating a
	// spinner, and still be showing an interrupt hint, while it waits.
	if detail, ok := awaitingInput(content); ok {
		return core.AgentNeedsYou, detail, sawBusy
	}
	if busy {
		return core.AgentWorking, "", sawBusy
	}

	// The user is typing into the composer and the agent is not offering to be
	// interrupted, so the pane is changing because of them. Composing a message
	// for an idle agent is not the agent working, and moving the card to active
	// while someone types at it gets the board exactly backwards.
	if typing {
		return settled(prev, seen), "", sawBusy
	}

	// How long the pane has to be still before it counts as finished. An agent
	// that advertises its turns is not advertising one now, so quiescence is
	// only being asked to rule out a gap mid-turn, and can be much shorter.
	quietFor := IdleAfter
	if sawBusy {
		quietFor = SettleAfter
	}
	if quiet < quietFor {
		return core.AgentWorking, "", sawBusy
	}
	return settled(prev, seen), "", sawBusy
}

// settled names the state of an agent that has stopped producing output: it
// finished a turn if it was working, and is otherwise just sitting there.
func settled(prev sample, seen bool) core.AgentState {
	if !seen {
		return core.AgentIdle
	}
	if prev.previous == core.AgentWorking {
		return core.AgentDone
	}
	return prev.previous
}

// isBusy reports whether the agent is showing its interrupt hint, which it does
// only while a turn is in flight.
func isBusy(content string) bool {
	return matchesTail(content, busyPatterns) != ""
}

// Forget drops remembered state for sessions that no longer exist.
func (p *Prober) Forget(keep map[string]bool) {
	for id := range p.last {
		if !keep[id] {
			delete(p.last, id)
		}
	}
}

// awaitingInput looks for an approval or input request near the end of the pane,
// where a live prompt would be.
func awaitingInput(content string) (string, bool) {
	line := matchesTail(content, promptPatterns)
	if line == "" {
		return "", false
	}
	return truncate(line, 60), true
}

// matchesTail returns the last line of the pane's tail matching any pattern, or
// "" for none. Only the tail is searched: a hint or prompt is live where the
// agent is drawing now, and matching the whole screen would fire on anything it
// merely printed earlier.
func matchesTail(content string, patterns []*regexp.Regexp) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	start := max(len(lines)-tailLines, 0)
	for i := len(lines) - 1; i >= start; i-- {
		line := strings.TrimSpace(stripANSI(lines[i]))
		if line == "" {
			continue
		}
		for _, re := range patterns {
			if re.MatchString(line) {
				return line
			}
		}
	}
	return ""
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
