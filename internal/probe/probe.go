// Package probe infers agent state for agents that cannot report it.
//
// Claude Code reports state through lifecycle hooks, which is exact. Codex and
// anything else launched in a tmux session has no such channel, so state is
// inferred from process liveness plus whether the pane is still changing.
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

// State is one probe result.
type State struct {
	SessionID string
	Agent     core.AgentState
	Detail    string
	Alive     bool
	// Content is the captured pane, reused as the preview so a probe and a
	// preview do not each pay for a capture.
	Content string
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
}

func New() *Prober { return &Prober{last: map[string]sample{}} }

// Probe captures the pane and classifies the session.
func (p *Prober) Probe(ctx context.Context, s *core.Session) State {
	if !tmuxx.HasSession(ctx, s.TmuxSession) {
		delete(p.last, s.ID)
		return State{SessionID: s.ID, Agent: core.AgentIdle, Detail: "session ended", Alive: false}
	}

	content, err := tmuxx.CapturePane(ctx, s.TmuxSession, 0)
	if err != nil {
		return State{SessionID: s.ID, Agent: s.AgentState, Alive: true}
	}

	now := time.Now()
	h := sha256.Sum256([]byte(content))
	prev, seen := p.last[s.ID]

	changedAt := now
	if seen && prev.hash == h {
		changedAt = prev.changed
	}

	st := State{SessionID: s.ID, Alive: true, Content: content}

	// An approval prompt outranks activity: the pane may still be animating a
	// spinner while it waits for an answer.
	if detail, ok := awaitingInput(content); ok {
		st.Agent, st.Detail = core.AgentNeedsYou, detail
	} else if now.Sub(changedAt) >= IdleAfter {
		// Quiet for a while. If it was working, it has finished a turn;
		// otherwise it is simply sitting there.
		if seen && prev.previous == core.AgentWorking {
			st.Agent = core.AgentDone
		} else if seen {
			st.Agent = prev.previous
		} else {
			st.Agent = core.AgentIdle
		}
		if st.Agent == core.AgentWorking {
			st.Agent = core.AgentDone
		}
	} else {
		st.Agent = core.AgentWorking
	}

	p.last[s.ID] = sample{hash: h, changed: changedAt, previous: st.Agent}
	return st
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
// where a live prompt would be. Matching the whole scrollback would fire on
// anything the agent merely printed earlier.
func awaitingInput(content string) (string, bool) {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	start := max(len(lines)-12, 0)
	for i := len(lines) - 1; i >= start; i-- {
		line := strings.TrimSpace(stripANSI(lines[i]))
		if line == "" {
			continue
		}
		for _, re := range promptPatterns {
			if re.MatchString(line) {
				return truncate(line, 60), true
			}
		}
	}
	return "", false
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
