// Package probe infers agent state for agents that cannot report it.
//
// Claude Code reports state through lifecycle hooks, which is exact. Codex and
// anything else launched in a tmux session has no such channel, so state is read
// off the pane instead, in this order of trust:
//
//  1. the interrupt hint, which an agent shows for exactly as long as it has a
//     turn to interrupt -- the closest thing to a statement of state on offer;
//  2. a dialog, which is a menu with a selection marker on it or a line naming
//     the key it wants pressed;
//  3. whether the pane is still changing, for agents that show neither.
//
// Quiescence is last for a reason. A pane changes for reasons that have nothing
// to do with the agent -- attaching hands the window to the real terminal and
// detaching pins it back, and either reflows every line on screen -- so once an
// agent has been seen to advertise its turns, its hint is believed over the
// pixels. An agent that never advertises one still gets the coarse treatment,
// with one exemption that applies either way: a frame dma provoked itself, by
// forwarding a key or a wheel event or by resizing the terminal, never starts the
// clock a turn is measured on.
//
// This is deliberately a fallback. Pane text is a rendering of the agent's UI,
// not a state machine, so nothing here matches on product wording, which changes
// between releases: only on punctuation, structure, and the idioms a terminal
// agent has to use to tell a user which key to press.
package probe

import (
	"context"
	"crypto/sha256"
	"regexp"
	"slices"
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
// every frame of a turn: Codex drops it while an approval dialog is up, and
// again while a tool result draws.
//
// A live turn was sampled to size this. The longest the pane stayed
// byte-identical mid-turn was 1.3s -- an agent that is working repaints at
// least once a second, if only to tick its own elapsed counter -- while the
// pane after the turn went still and stayed still. Three seconds clears the
// former with room to spare, which is what turns a stale "working" badge around
// in seconds rather than the better part of a minute.
const SettleAfter = 3 * time.Second

// ActionGrace is how long after something dma did to a terminal that terminal's
// redraw still counts as dma's doing rather than the agent's. Agents do not echo
// instantly -- a keystroke lands in a composer a moment later, and a resize
// reflows a moment after that -- so the action and the frame that shows it are
// never in the same instant.
//
// It is a grace on the sampling window rather than a window of its own: see
// caused, where the change is attributed.
const ActionGrace = 3 * time.Second

// promptPatterns match lines that only a dialog draws: a key the user is being
// asked to press, or a question posed as the whole line. They are about
// punctuation and structure rather than specific product wording, which changes
// between releases.
//
// Every one of them has to be a line an agent would not print while narrating
// its own work, because a false "needs you" is worse than a late one: it moves a
// card to the front of the board and raises a desktop notification for a session
// nobody has to look at. That rules out matching a phrase anywhere in a line --
// "allow: GET, HEAD" and "I'll ask before I approve anything, ok?" are both
// ordinary output -- so the question forms are anchored to the start of the line
// and have to end there too.
var promptPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\[[yY]/[nN]\]`),
	regexp.MustCompile(`\([yY]/[nN]\)`),
	regexp.MustCompile(`(?i)^(?:allow|approve)\b[^?]{0,80}\?$`),
	regexp.MustCompile(`(?i)^(?:do you want|would you like) to\b[^?]{0,80}\?$`),
	regexp.MustCompile(`(?i)press (?:enter|return) to (?:continue|confirm)`),
	regexp.MustCompile(`(?i)waiting for (?:your )?(?:input|approval|confirmation)`),
}

// selectMarker is the glyph an agent draws on the row its selection sits on:
// Codex uses "›", Claude Code "❯". It is the whole difference between a menu and
// a numbered list, so it is matched exactly rather than lumped in with the
// bullets and borders cleanLine strips.
const selectMarker = `[❯›▸▶→>]`

// choiceOption matches one row of a select dialog -- a number, a separator and
// some text -- and choiceMarker the one row of it that is selected.
var (
	choiceOption = regexp.MustCompile(`^(?:` + selectMarker + `\s*)?\d{1,2}[.)]\s+\S`)
	choiceMarker = regexp.MustCompile(`^` + selectMarker + `\s*\d{1,2}[.)]\s`)
)

// markedRow matches the row a selection marker sits on where the rows carry no
// numbers. See markerPrompt for what has to be true before it means anything.
var markedRow = regexp.MustCompile(`^` + selectMarker + `\s*\S`)

// navHints match the line a picker draws to say which keys move its selection.
//
// It is what makes an unnumbered menu recognizable at all. pi renders its rows as
// plain labels with an arrow on the selected one -- "→ Yes, and remember" -- so
// there are no numbers to key on, and the marker alone is far too common to trust:
// a quoted line opens with ">", and agents draw arrows into their own prose. The
// hint is drawn by a live picker and by nothing else, and only while one is open.
//
// Like everything else here it matches an idiom rather than product wording: a
// pair of arrows, and the word for what they do.
var navHints = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:↑↓|↑/↓|▲▼|up/down)\s*(?:to\s+)?(?:navigate|select|move|choose)\b`),
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

var piBusyStatus = regexp.MustCompile(`^[\x{2800}-\x{28ff}]\s+Working\.\.\.$`)

// tailLines is how many lines of content up the pane a live hint or dialog can
// be. Both sit just above the composer, but a dialog is several lines tall and
// carries its question above its options, so the window has to be deep enough to
// hold all of one.
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
	// probed is when this sample was taken, which bounds how old the next
	// change can be: a frame that differs changed somewhere between then and
	// now, and nothing narrower is knowable at four seconds a sample.
	probed time.Time
	// moved records that this pane has been seen to change under the agent at
	// least once, which is what makes "changed recently" mean anything. changed
	// starts at first sight, so without this a pane nobody has ever seen move is
	// a pane that changed seconds ago -- and the second probe of a freshly
	// opened board called every hookless card working on the strength of it.
	moved bool
	// sawBusy records that this agent has shown an interrupt hint at least
	// once, which is what makes the hint's absence meaningful later.
	sawBusy bool
}

func New() *Prober { return &Prober{last: map[string]sample{}} }

// Probe captures the pane and classifies the session.
//
// actedAt is when the board last did something to this session's terminal --
// forwarded a keystroke, a paste or a wheel event, or resized it -- or the zero
// time. It is what lets the pane's own text be attributed: dma caused that
// frame, so the agent did not.
func (p *Prober) Probe(ctx context.Context, s *core.Session, actedAt time.Time) State {
	alive, err := tmuxx.SessionExists(ctx, s.TmuxSession)
	if err != nil {
		// A shared probe budget can expire before this session gets its turn.
		// That says nothing about whether its terminal still exists.
		return State{SessionID: s.ID, Agent: s.AgentState, Alive: s.TmuxAlive}
	}
	if !alive {
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
	if !seen {
		// First sight of this session -- the board has just started, or the
		// session has just been created. There is no earlier frame to compare
		// against, so nothing can yet be said about whether the pane is
		// changing, and the state the board already holds is the only honest
		// answer. Inferring one from a zero-length quiet window instead is what
		// made every Codex card report "working" the moment dma opened, and
		// "done" 25 seconds later.
		prev = sample{previous: s.AgentState}
	}

	// A change is the agent's only when nothing dma did accounts for it. The hash
	// below moves on either way -- the frame is the frame -- but a provoked one
	// neither restarts the quiet clock nor counts as this pane having moved.
	//
	// Resetting the clock and then excusing the one frame around the keystroke,
	// which is what this did, excused nothing: the next sample found a pane that
	// had "changed" moments ago and called it work for the whole IdleAfter window.
	// That is what walked an idle card into active and back out again on every
	// scroll, keystroke and resize.
	moved := seen && prev.hash != h && !caused(actedAt, prev.probed)
	changedAt := prev.changed
	if moved || !seen {
		changedAt = now
	}
	moved = moved || prev.moved

	st := State{SessionID: s.ID, Alive: true, Content: content, Cursor: pane.Cursor}
	var sawBusy bool
	st.Agent, st.Detail, sawBusy = classify(s.AgentProfile, content, now.Sub(changedAt), moved, prev, seen)

	p.last[s.ID] = sample{
		hash: h, changed: changedAt, previous: st.Agent,
		probed: now, moved: moved, sawBusy: sawBusy,
	}
	return st
}

// caused reports whether a change first seen in this sample can be put down to
// something dma did rather than to the agent.
//
// The window is the gap since the previous sample, not a fixed span: the change
// happened somewhere in that gap, so an action anywhere in it is a candidate
// cause. A fixed window is what made this unreliable -- three seconds of
// forgiveness against a four second sampling interval means a keystroke sent
// just after one probe has expired by the next, and the frame it caused arrives
// unexplained.
//
// ActionGrace pads the far end, because the redraw an action causes lands after
// the action does and can therefore fall the other side of a sample boundary.
func caused(actedAt, probedAt time.Time) bool {
	return !actedAt.IsZero() && actedAt.After(probedAt.Add(-ActionGrace))
}

// classify decides a session's state from one captured frame, how long the agent
// has left the pane alone, whether it has ever been seen to touch it, and what
// the session looked like last time.
//
// quiet and moved both describe changes the agent is answerable for: attribution
// is settled in Probe, so nothing below has to know who typed.
func classify(agentProfile, content string, quiet time.Duration, moved bool, prev sample, seen bool) (core.AgentState, string, bool) {
	busy := isBusy(agentProfile, content)
	sawBusy := busy || prev.sawBusy

	// A dialog outranks activity: the pane may still be animating a spinner, and
	// still be showing an interrupt hint, while it waits on a keypress.
	if detail, ok := awaitingInput(content, busy); ok {
		return core.AgentNeedsYou, detail, sawBusy
	}
	if busy {
		return core.AgentWorking, "", sawBusy
	}
	// The dialog this session was blocked on has left the screen, so it is not
	// blocked any more -- whoever answered it, and whatever the pane does next.
	// Without this a "needs you" badge outlives the question that raised it,
	// because every later frame just reports the state before it.
	if prev.previous == core.AgentNeedsYou {
		return core.AgentIdle, "", sawBusy
	}

	if sawBusy {
		// This agent announces its own turns, so the hint -- not the pane
		// changing -- is what starts one. Plenty changes a pane while the agent
		// does nothing at all: attaching hands the window to the real terminal
		// and detaching pins it back, and either reflows every line on screen.
		// Reading a reflow as output is what walked an untouched card from idle
		// to working to done every time someone opened the session.
		if prev.previous != core.AgentWorking {
			return held(prev), "", sawBusy
		}
		// Ending a turn still waits for the pane to hold still, because the hint
		// is not on screen for every frame of one and a gap mid-turn must not
		// read as finished.
		if quiet < SettleAfter {
			return core.AgentWorking, "", sawBusy
		}
		return core.AgentDone, "", sawBusy
	}

	// Nothing has ever advertised a turn on this pane, so pane quiescence is all
	// there is to go on -- and quiescence needs a second frame to measure.
	if !seen {
		return held(prev), "", sawBusy
	}
	// "Changed recently" is only evidence of a turn once this pane has been seen
	// to change at all. The clock starts when the board starts watching, so a pane
	// nobody has ever seen move is one that changed seconds ago by that clock, and
	// reading that as output announced a turn on the second probe of every
	// hookless session -- before anyone had touched anything.
	if moved && quiet < IdleAfter {
		return core.AgentWorking, "", sawBusy
	}
	return settled(prev), "", sawBusy
}

// settled names the state of an agent that has stopped producing output: it
// finished a turn if it was working, and otherwise keeps whatever it had.
func settled(prev sample) core.AgentState {
	if prev.previous == core.AgentWorking {
		return core.AgentDone
	}
	return held(prev)
}

// held is the state a session keeps when a frame taught the probe nothing. It is
// deliberately not settled(): a turn cannot have finished on the strength of an
// observation that was never made.
func held(prev sample) core.AgentState {
	if prev.previous == "" {
		return core.AgentIdle
	}
	return prev.previous
}

// isBusy reports whether the agent is showing its interrupt hint, which it does
// only while a turn is in flight.
//
// A line that is also asking for a keypress does not count, however much it
// reads like a hint. Codex closes an approval dialog with "Press enter to
// confirm or esc to cancel": the escape it offers cancels the question, not a
// turn, and taking that for activity would leave a blocked session sitting in the
// active column.
func isBusy(agentProfile, content string) bool {
	for _, line := range tail(content) {
		if agentProfile == "pi" && piBusyStatus.MatchString(line) {
			return true
		}
		if matches(line, busyPatterns) && !matches(line, promptPatterns) {
			return true
		}
	}
	return false
}

// Forget drops remembered state for sessions that no longer exist.
func (p *Prober) Forget(keep map[string]bool) {
	for id := range p.last {
		if !keep[id] {
			delete(p.last, id)
		}
	}
}

// awaitingInput looks for a request for input near the end of the pane, where a
// live dialog would be, and names it for the badge.
//
// busy is whether the agent is advertising a turn in flight, which decides
// whether a menu counts. Mid-turn, numbered lines are the agent writing a list;
// only an agent with nothing in flight is plausibly holding a menu open. The
// wording patterns above need no such help -- nothing prints them by accident --
// so they still answer while a turn runs.
func awaitingInput(content string, busy bool) (string, bool) {
	lines := tail(content)
	// A menu is asked about first because it carries the better answer: the
	// question a dialog poses reads on a card, where the key it wants pressed
	// does not.
	if !busy {
		if q, ok := choicePrompt(lines); ok {
			return truncate(q, 60), true
		}
		if q, ok := markerPrompt(lines); ok {
			return truncate(q, 60), true
		}
	}
	if line := matchAny(lines, promptPatterns); line != "" {
		return truncate(line, 60), true
	}
	return "", false
}

// choicePrompt reports whether the tail holds a select-one dialog: numbered
// options on consecutive lines, exactly one of them carrying a selection marker.
//
// The marker is the whole signal, because agents render a list they wrote the
// same way they render options -- Codex answers with "• 1. Use Ctrl-b d to
// detach" above "  2. Rename windows with Ctrl-b ," -- and matching numbered
// lines alone put every session that replied with a list into "needs you".
// Insisting on exactly one marked row is what keeps a quoted or bulleted list,
// where every row carries the same prefix, from reading as a selection.
func choicePrompt(lines []string) (string, bool) {
	// The run nearest the bottom is the live one; a footer like "Press enter to
	// confirm" sits below the options, so the search starts under it.
	end := len(lines)
	for end > 0 && !choiceOption.MatchString(lines[end-1]) {
		end--
	}
	start := end
	for start > 0 && choiceOption.MatchString(lines[start-1]) {
		start--
	}
	if end-start < 2 {
		return "", false
	}

	marked := ""
	for _, line := range lines[start:end] {
		if !choiceMarker.MatchString(line) {
			continue
		}
		if marked != "" {
			return "", false
		}
		marked = line
	}
	if marked == "" {
		return "", false
	}
	return question(lines[:start], marked), true
}

// markerPrompt reports whether the tail holds a dialog whose rows are not
// numbered: exactly one row carrying a selection marker, alongside a line saying
// which keys move it.
//
// It is asked after choicePrompt because a numbered menu is the more specific
// shape of the same thing, and answering with the numbers when they are there
// keeps the looser rule off frames that never needed it.
//
// Insisting on exactly one marked row is the same discipline choicePrompt
// follows: a quoted or bulleted block where every row carries the same prefix is
// not a selection. What this does not reach is a picker so long that the marked
// row has scrolled past the window the tail looks at -- the question is then off
// screen too, and a dialog nobody can see the top of is not one this can name.
func markerPrompt(lines []string) (string, bool) {
	if matchAny(lines, navHints) == "" {
		return "", false
	}
	marked, at := "", -1
	for i, line := range lines {
		if !markedRow.MatchString(line) {
			continue
		}
		if marked != "" {
			return "", false
		}
		marked, at = line, i
	}
	if marked == "" {
		return "", false
	}
	return question(lines[:at], marked), true
}

// question names what a dialog is asking. The options themselves are the
// fallback: a menu's own rows say less than the line that posed it, and a real
// dialog puts that line just above them.
func question(above []string, marked string) string {
	for i := len(above) - 1; i >= 0 && i >= len(above)-4; i-- {
		if strings.HasSuffix(above[i], "?") {
			return above[i]
		}
	}
	return marked
}

// matchAny returns the last of lines matching any pattern, or "" for none.
func matchAny(lines []string, patterns []*regexp.Regexp) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if matches(lines[i], patterns) {
			return lines[i]
		}
	}
	return ""
}

func matches(line string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// tail returns the end of the pane, cleaned for matching. Only the tail is
// looked at: a hint or dialog is live where the agent is drawing now, and
// matching the whole screen would fire on anything it merely printed earlier.
//
// Blank lines are dropped rather than counted, which is what makes the window a
// fixed amount of content instead of a fixed number of rows. Agents space their
// output out -- Codex puts a blank line between every block, and pads the rest of
// the pane below the composer -- so a dozen rows off the bottom of the screen can
// hold as little as two lines of a dialog.
func tail(content string) []string {
	rows := strings.Split(strings.TrimRight(content, "\n"), "\n")
	lines := make([]string, 0, tailLines)
	for i := len(rows) - 1; i >= 0 && len(lines) < tailLines; i-- {
		if line := cleanLine(rows[i]); line != "" {
			lines = append(lines, line)
		}
	}
	slices.Reverse(lines)
	return lines
}

// cleanLine reduces one captured row to the text a pattern should see: no
// styling, no surrounding space, and none of the bullets and box borders agents
// draw around their own output.
//
// Selection markers survive on purpose. They are the one piece of decoration
// that carries meaning, and stripping them with the rest would leave a menu
// indistinguishable from a list.
func cleanLine(s string) string {
	s = strings.TrimSpace(stripANSI(s))
	s = strings.TrimLeft(s, "│┃▌|•∙·*✔✗⚠■◆●⏺ ")
	s = strings.TrimRight(s, "│┃▌| ")
	return strings.TrimSpace(s)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// truncate shortens a line for the badge, counting characters rather than bytes:
// the lines it is given are full of glyphs an agent drew, and cutting one in
// half would leave a replacement character in the notification.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
