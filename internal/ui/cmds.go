package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/clip"
	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/link"
	"github.com/dma1dma1/dma-cli/internal/ops"
	"github.com/dma1dma1/dma-cli/internal/probe"
	"github.com/dma1dma1/dma-cli/internal/render"
	"github.com/dma1dma1/dma-cli/internal/summarize"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// --- messages ---

type tickMsg time.Time
type pollTickMsg time.Time
type previewTickMsg time.Time
type probeTickMsg time.Time

// previewMsg carries recent terminal output for the panel, with the cursor
// position belonging to that same frame.
type previewMsg struct {
	id              string
	content         string
	cursor          tmuxx.Cursor
	mouseSGR        bool
	requestedScroll int
	actualScroll    int
}

// probeMsg carries inferred state for agents that cannot report their own.
type probeMsg struct{ states []probe.State }

type hookMsg hooks.Event

type observeMsg struct{ obs []ops.Observation }

// prSyncMsg carries one repo's poll result. Repos are polled and reported
// independently so one unreachable remote cannot stall the others.
type prSyncMsg struct {
	repoID string
	poll   ghx.Poll
	err    error
}

// prDetailMsg carries the resolved state of a single PR that has left the open
// list, which is how merged and closed are detected.
type prDetailMsg struct {
	sessionID string
	pr        ghx.PR
	err       error
}

// adoptedMsg reports a repo registered from inside the TUI.
type adoptedMsg struct {
	repo  core.Repo
	added bool
	err   error
}

type createdMsg struct {
	res *ops.CreateResult
	// task is the whole text the session was started on, which the session
	// record does not keep: its title is only the first line of it. Naming the
	// card wants all of it -- the line that matters is as often in the stack
	// trace below the first one as in the first one.
	task string
	err  error
}

// titledMsg carries a summary of the task back to the card that is currently
// showing the task itself.
type titledMsg struct {
	id    string
	title string
}

// linkAction is what to do with a PR's web address once it is in hand.
type linkAction int

const (
	linkOpen linkAction = iota
	linkCopy
)

// prLinkMsg carries a PR address that had to be fetched before it could be
// opened or copied, so the model can cache it on the session and then act.
type prLinkMsg struct {
	id     string
	url    string
	action linkAction
	err    error
}

type mergedMsg struct {
	id string
	// outcome distinguishes a merge that landed from one that only joined a
	// merge queue, which is still open work.
	outcome ghx.MergeOutcome
	err     error
}

// prQueueMsg carries a refreshed merge-queue standing for one session's PR.
type prQueueMsg struct {
	sessionID string
	inQueue   bool
	err       error
}

type teardownMsg struct {
	id string
	// bulk marks a teardown that came from X rather than x, which changes how a
	// failure is answered: one question was already asked for the whole batch.
	bulk bool
	err  error
}

type killedMsg struct {
	id  string
	err error
}

// restartMsg reports one session's terminal having been rebuilt and its agent
// put back in it.
type restartMsg struct {
	id string
	// tmuxSession is the terminal the session has now, normally the one it always
	// had. See ops.RestartResult.
	tmuxSession string
	// resumed is whether the agent came back on its own conversation or as a fresh
	// one. The board cannot see the difference, so the notice line says it.
	resumed bool
	// bulk marks a restart that came from C rather than c, so a failure among many
	// names the session it belongs to.
	bulk     bool
	warnings []string
	err      error
}

type diffMsg struct {
	id      string
	content string
	// key identifies which file, mode and layout this render is of, so a diff
	// that arrives after the cursor has moved on is filed and not drawn.
	key string
	err error
}

// changedFilesMsg carries the file list the tree is built from.
type changedFilesMsg struct {
	id    string
	mode  gitx.DiffMode
	files []gitx.ChangedFile
	err   error
}

type captureMsg struct {
	id      string
	content string
}

// noticeMsg is something that went wrong, on its way to the notice line. There
// is no neutral form: an action that worked says so by changing the board.
type noticeMsg struct{ text string }

type attachDoneMsg struct{ err error }

type clipboardMsg struct {
	content clip.Content
	err     error
}

// --- commands ---

func tickCmd() tea.Cmd {
	// One second is enough to keep "needs you 8m" honest without burning CPU.
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func pollTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return pollTickMsg(t) })
}

// previewInterval keeps the panel feeling live without spawning a capture more
// often than a person can read.
const previewInterval = 1200 * time.Millisecond

func previewTickCmd() tea.Cmd {
	return tea.Tick(previewInterval, func(t time.Time) tea.Msg { return previewTickMsg(t) })
}

// probeInterval is slower: it exists to notice an agent going quiet, and
// probe.IdleAfter is measured in tens of seconds.
const probeInterval = 4 * time.Second

func probeTickCmd() tea.Cmd {
	return tea.Tick(probeInterval, func(t time.Time) tea.Msg { return probeTickMsg(t) })
}

// resizeSessionsCmd points every agent's terminal at the current preview size.
//
// Only issued when the size actually changes: a resize makes an agent redraw
// and reflow, so doing it on a timer would make the UI twitch.
func resizeSessionsCmd(sessions []*core.Session, cols, rows int) tea.Cmd {
	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.TmuxSession)
	}
	if len(names) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, n := range names {
			_ = tmuxx.ResizeWindow(ctx, n, cols, rows)
		}
		return nil
	}
}

// previewCmd captures the selected session's pane for display. Nothing is
// inferred from this text; state comes from hooks or the prober.
func previewCmd(s *core.Session) tea.Cmd {
	return previewCmdAt(s, 0)
}

// previewCmdAt captures a history viewport without putting the real tmux pane
// into copy mode. requestedScroll travels with the result so a slow capture
// from an older wheel position cannot overwrite a newer one.
func previewCmdAt(s *core.Session, requestedScroll int) tea.Cmd {
	if s == nil {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pane, actual, _ := tmuxx.CapturePaneAt(ctx, sess.TmuxSession, requestedScroll)
		return previewMsg{
			id: sess.ID, content: pane.Content, cursor: pane.Cursor, mouseSGR: pane.MouseSGR,
			requestedScroll: requestedScroll, actualScroll: actual,
		}
	}
}

// sendWheelCmd forwards a wheel event to a full-screen application that asked
// the terminal for SGR mouse input. Capability is checked again at send time:
// dialogs can change terminal modes between the preview capture and the event.
func sendWheelCmd(s *core.Session, up bool, x, y int) tea.Cmd {
	if s == nil {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := tmuxx.SendMouseWheel(ctx, sess.TmuxSession, up, x, y)
		if err != nil {
			return noticeMsg{text: fmt.Sprintf("scroll %s: %v", sess.Title, err)}
		}
		return nil
	}
}

// echoInterval and echoWindow define the burst of captures that follows a
// forwarded keystroke, so the panel does not sit on a stale frame until the
// next preview tick.
//
// A single capture timed off the send does not work, because agents do not
// repaint on a predictable schedule. Claude Code answers a printable character
// in 20-30ms, but a bare Escape first has to clear its own escape-sequence
// timeout, so nothing changes on screen for 65-80ms -- and a capture that fires
// before the redraw returns the screen as it was, leaving Escape looking dead
// for the rest of the 1.2s preview interval. Capturing repeatedly across a
// window wide enough for the slow case catches whichever one this was, and
// catches multi-stage redraws too.
const (
	echoInterval = 40 * time.Millisecond
	echoWindow   = 700 * time.Millisecond
)

type echoTickMsg time.Time

func echoTickCmd() tea.Cmd {
	return tea.Tick(echoInterval, func(t time.Time) tea.Msg { return echoTickMsg(t) })
}

// sendKeyCmd forwards one keystroke to a session's terminal. The capture that
// shows its effect is driven by the echo ticker, not from here.
func sendKeyCmd(s *core.Session, fk forwardedKey) tea.Cmd {
	if s == nil {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var err error
		if fk.literal {
			err = tmuxx.SendText(ctx, sess.TmuxSession, fk.arg)
		} else {
			err = tmuxx.SendKey(ctx, sess.TmuxSession, fk.arg)
		}
		if err != nil {
			return noticeMsg{text: fmt.Sprintf("send to %s: %v", sess.Title, err)}
		}
		return nil
	}
}

func sendPasteCmd(s *core.Session, text string) tea.Cmd {
	if s == nil || text == "" {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tmuxx.SendPaste(ctx, sess.TmuxSession, text); err != nil {
			return noticeMsg{text: fmt.Sprintf("paste to %s: %v", sess.Title, err)}
		}
		return nil
	}
}

func readClipboardCmd() tea.Cmd {
	return func() tea.Msg {
		content, err := clip.Read()
		return clipboardMsg{content: content, err: err}
	}
}

// probeCmd infers state for sessions whose agent has no hook channel, and for
// the few hook-backed ones whose recorded state nothing has confirmed -- see
// stranded. A hook-capable agent is otherwise skipped: its own reports are
// exact, and a heuristic could only contradict them.
//
// touchedAt and hookSeen are read and pruned here rather than inside the
// command, because the maps belong to the model and the command runs on another
// goroutine.
func probeCmd(p *probe.Prober, cfg *core.Config, sessions []*core.Session, touchedAt map[string]time.Time, hookSeen map[string]bool) tea.Cmd {
	var targets []*core.Session
	keep := map[string]bool{}
	touched := map[string]time.Time{}
	for _, s := range sessions {
		keep[s.ID] = true
		if prof, ok := cfg.Profile(s.AgentProfile); ok && prof.Hooks && !stranded(s, hookSeen[s.ID]) {
			continue
		}
		copied := *s
		targets = append(targets, &copied)
		touched[s.ID] = touchedAt[s.ID]
	}
	p.Forget(keep)
	for id := range touchedAt {
		if !keep[id] {
			delete(touchedAt, id)
		}
	}
	for id := range hookSeen {
		if !keep[id] {
			delete(hookSeen, id)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		states := make([]probe.State, 0, len(targets))
		for _, s := range targets {
			states = append(states, p.Probe(ctx, s, touched[s.ID]))
		}
		return probeMsg{states: states}
	}
}

// stranded reports whether a hook-backed session is sitting in a state that no
// hook has confirmed and no later hook can correct, which is the one case where
// reading the pane beats trusting the record.
//
// Hooks are exact but they are not durable. A hook is an HTTP post to the board,
// so an event raised while the board is not listening is refused and the
// transition it carried is lost outright -- and restarting the board takes long
// enough for a turn to end inside the gap. What survives on disk is the last
// event that did land, which the next launch reloads and then believes forever,
// because a hook-backed session is never probed.
//
// Only working strands that way, and it is the state the agent leaves behind
// when its Stop is the event that went missing: the turn is over, so nothing is
// coming to say so, and the card advertises work that finished before the board
// was even running. Every other state is self-healing by comparison. A card
// wrongly reading idle or done belongs to an agent that is running, and its next
// tool call reports within seconds. A card reading needs_you belongs to one
// blocked on a question, which is what needs_you means -- and the moment the
// question is answered the agent resumes and hooks again.
//
// reported is whether a hook for this session has reached the board since it
// started, which is what "confirmed" means here: state read off disk is a claim
// about a board that is no longer running.
func stranded(s *core.Session, reported bool) bool {
	return !reported && s.AgentState == core.AgentWorking
}

// waitForHook blocks on the hook channel and re-arms itself after each event,
// which is how a background source feeds a Bubble Tea update loop.
func waitForHook(ch <-chan hooks.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return hookMsg(ev)
	}
}

func observeCmd(sessions []*core.Session) tea.Cmd {
	snapshot := append([]*core.Session(nil), sessions...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return observeMsg{obs: ops.Observe(ctx, snapshot)}
	}
}

// adoptedSessionsMsg carries sessions that appeared in the state file while the
// board was running.
type adoptedSessionsMsg struct{ sessions []*core.Session }

// adoptExternalCmd looks for sessions something else added to the state file.
//
// `dma attach` is that something else: it starts a session from a shell, and
// the board it is meant to appear on is usually already open. The board holds
// its sessions in memory and writes the whole list every time it saves, so
// without this the attached session would not merely be invisible -- the next
// save would erase its record, leaving a worktree and a running agent that
// nothing on disk points at.
//
// Only additions are taken. Sessions the board already knows are left exactly
// as they are, because the board's copy is the newer one: it holds this poll's
// liveness, diff and PR state, none of which is written to disk. Nothing here
// removes a session either -- an absence on disk is far more likely to be a
// stale read than a prune, and dropping a live card over one would be the
// worse mistake.
func adoptExternalCmd(known []*core.Session) tea.Cmd {
	ids := make(map[string]bool, len(known))
	for _, s := range known {
		ids[s.ID] = true
	}
	return func() tea.Msg {
		stored, err := core.LoadSessions()
		if err != nil {
			// A state file being written as it is read is the expected failure
			// here, and the next poll gets a clean look at it.
			return adoptedSessionsMsg{}
		}
		var fresh []*core.Session
		for _, s := range stored {
			if !ids[s.ID] {
				fresh = append(fresh, s)
			}
		}
		return adoptedSessionsMsg{sessions: fresh}
	}
}

// pollPRsCmd polls the branches the board is tracking, grouped by repo. A repo
// with nothing running is skipped rather than polled for completeness, and only
// the branches on screen are asked about -- the board has no use for the rest
// of a repo's open PRs, and on a busy repo asking for them is what breaks the
// query outright. See ghx.PollBranches.
func pollPRsCmd(cfg *core.Config, sessions []*core.Session) tea.Cmd {
	branches := trackedBranches(sessions)
	var cmds []tea.Cmd
	for _, repo := range cfg.Repos {
		// Repos with no remote at all are skipped too -- polling those could only
		// ever produce the same error every interval.
		if len(branches[repo.ID]) == 0 || repo.Remote == "" {
			continue
		}
		r, bs := repo, branches[repo.ID]
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			poll, err := ghx.PollBranches(ctx, r.Remote, bs)
			return prSyncMsg{repoID: r.ID, poll: poll, err: err}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// trackedBranches groups the branches worth polling by repo. Merged sessions
// are done and a session with no branch has nothing to look up.
func trackedBranches(sessions []*core.Session) map[string][]string {
	out := map[string][]string{}
	seen := map[core.Key]bool{}
	for _, s := range sessions {
		if s.Lifecycle == core.LifecycleMerged || s.Branch == "" {
			continue
		}
		// Two sessions can share a repo and branch after a split; one query
		// answers for both.
		if seen[s.Key()] {
			continue
		}
		seen[s.Key()] = true
		out[s.RepoID] = append(out[s.RepoID], s.Branch)
	}
	return out
}

// prDetailCmd resolves one tracked PR that is no longer open.
func prDetailCmd(remote, sessionID string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pr, err := ghx.GetPR(ctx, remote, number)
		return prDetailMsg{sessionID: sessionID, pr: pr, err: err}
	}
}

// createCmd starts a session. Its budget is an outer backstop only: the phases
// inside ops.Create carry their own, because bootstrapping a large repo's
// dependency trees can legitimately outlast any single deadline worth putting on
// the rest of the start. Two minutes here used to expire mid-clone on a
// monorepo, and every start in that repo then failed reporting tmux -- the step
// after the one that actually ran out of time.
func createCmd(cfg *core.Config, req ops.CreateRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		res, err := ops.Create(ctx, cfg, req)
		return createdMsg{res: res, task: req.InitialPrompt, err: err}
	}
}

// titleCmd names a session from the task it was started with.
//
// It runs after the card is already on the board rather than before, because
// the answer costs the better part of ten seconds and none of the rest of a
// session start depends on it. The card carries the opening of the task until
// this lands, which is worse than a summary and much better than nothing on
// screen while a model thinks.
func titleCmd(s *core.Session, task string) tea.Cmd {
	id := s.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*summarize.Timeout)
		defer cancel()
		return titledMsg{id: id, title: summarize.Title(ctx, task)}
	}
}

// shipRequest is what s sends to the agent.
//
// The agent ships its own work rather than dma doing the git for it: it is the
// one that knows what it changed, so the commit message and the PR are its to
// write, and a repo with its own conventions gets them followed. The permission
// line and the closing sentence are both there because the value of the key is
// not having to come back -- an agent that stops to ask whether it may push has
// handed the card straight back to you.
const shipRequest = "Commit, push, and open a PR. You have full permission to do so. Do not come back to me until the PR is open."

// shepherdRequest is what S sends to the agent. It begins with the same grant as
// s, then keeps the agent responsible for the PR until CI and review converge.
// Merging remains a separate, explicit board action.
const shepherdRequest = "Commit, push, and open a PR. You have full permission to do so. Then shepherd the PR: use the available PR shepherd skill, monitor CI and review threads, fix valid failures and feedback, commit and push each fix, and continue until CI passes and all review threads are resolved. Do not merge. Do not come back to me until the PR is ready to merge or you are genuinely blocked."

// askShipCmd asks a session's agent to ship its work, with the requested stopping
// point supplied by the board key that called it.
//
// A paste followed by a separate Enter, rather than a typed line: the paste
// lands as one insertion the agent's input reads in a single go, and the Enter
// is then unambiguously the submit.
func askShipCmd(s *core.Session, request string) tea.Cmd {
	if s == nil {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tmuxx.SendPaste(ctx, sess.TmuxSession, request); err != nil {
			return noticeMsg{text: fmt.Sprintf("ask %s to ship: %v", sess.Title, err)}
		}
		if err := tmuxx.SendKey(ctx, sess.TmuxSession, "Enter"); err != nil {
			return noticeMsg{text: fmt.Sprintf("ask %s to ship: %v", sess.Title, err)}
		}
		return nil
	}
}

func mergeCmd(cfg *core.Config, s *core.Session) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		repo, ok := cfg.Repo(sess.RepoID)
		if !ok {
			return mergedMsg{id: sess.ID, err: fmt.Errorf("unknown repo %q", sess.RepoID)}
		}
		if sess.PRNumber == 0 {
			return mergedMsg{id: sess.ID, err: fmt.Errorf("no PR to merge")}
		}
		outcome, err := ghx.MergePR(ctx, repo.Remote, sess.PRNumber, ghx.MergeOptions{
			Method: "squash",
			Auto:   sess.PRCI == core.CIPending,
		})
		return mergedMsg{id: sess.ID, outcome: outcome, err: err}
	}
}

// prQueueCmd re-checks a PR the board is showing as queued. A merge queue can
// drop a PR back out -- its checks fail against the queue's merge candidate --
// and the open-PR poll says nothing about that, since the PR was open all
// along.
func prQueueCmd(remote, sessionID string, number int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		qs, err := ghx.PRQueueState(ctx, remote, number)
		return prQueueMsg{sessionID: sessionID, inQueue: qs.InQueue, err: err}
	}
}

// linkCmd opens a PR in the browser or puts its address on the clipboard.
//
// Neither announces itself: the browser coming to the front is the feedback for
// one, and the clipboard holding the link is the feedback for the other. Only
// the failures reach the notice line.
func linkCmd(url string, action linkAction) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if action == linkCopy {
			if err := link.Copy(ctx, url); err != nil {
				return noticeMsg{text: "copy link: " + err.Error()}
			}
			return nil
		}
		if err := link.Open(ctx, url); err != nil {
			return noticeMsg{text: "open link: " + err.Error()}
		}
		return nil
	}
}

// copyTextCmd puts an application-owned terminal selection on the clipboard.
// Success is already visible as the held highlight; only a failure needs a
// notice line.
func copyTextCmd(content string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := link.Copy(ctx, content); err != nil {
			return noticeMsg{text: "copy selection: " + err.Error()}
		}
		return nil
	}
}

// prLinkCmd resolves the address of a PR whose link the session does not know.
//
// Sessions record the URL as it arrives from the poll, so this only runs for a
// PR that predates that -- most visibly one in the merged column, which is no
// longer polled at all and would otherwise never learn its own link.
func prLinkCmd(remote, sessionID string, number int, action linkAction) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pr, err := ghx.GetPR(ctx, remote, number)
		if err == nil && pr.URL == "" {
			err = fmt.Errorf("github returned no link for #%d", number)
		}
		return prLinkMsg{id: sessionID, url: pr.URL, action: action, err: err}
	}
}

func teardownCmd(cfg *core.Config, s *core.Session, opt ops.TeardownOptions) tea.Cmd {
	return teardownOne(cfg, s, opt, false)
}

// teardownAllCmd prunes several sessions, one after another rather than at
// once: teardown runs git against the shared repo, and concurrent worktree
// removals and prunes on one repo race for the same lock.
func teardownAllCmd(cfg *core.Config, sessions []*core.Session) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(sessions))
	for _, s := range sessions {
		cmds = append(cmds, teardownOne(cfg, s, ops.TeardownOptions{}, true))
	}
	return tea.Sequence(cmds...)
}

// sweepTimeout bounds one sweep of the trash. A worktree carrying cloned
// dependency trees takes half a minute to unlink and the trash can hold a whole
// merged column of them, so this is sized for several rather than one.
const sweepTimeout = 10 * time.Minute

// sweepTrashCmd unlinks the worktrees teardown moved aside.
//
// This is the slow half of a prune with nothing waiting on it: the rename that
// freed the board already happened, and the card is gone. So it reports nothing,
// not even a failure -- a sweep that fails, or that a quit cuts short, leaves the
// tree in the trash, and the next prune or the next start collects it.
func sweepTrashCmd(cfg *core.Config) tea.Cmd {
	roots := make([]string, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		roots = append(roots, r.WorktreeRoot)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
		defer cancel()
		for _, root := range roots {
			_ = ops.SweepTrash(ctx, root)
		}
		return nil
	}
}

func teardownOne(cfg *core.Config, s *core.Session, opt ops.TeardownOptions, bulk bool) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := ops.Teardown(ctx, cfg, &sess, opt)
		return teardownMsg{id: sess.ID, bulk: bulk, err: err}
	}
}

func killCmd(s *core.Session) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return killedMsg{id: sess.ID, err: ops.Kill(ctx, &sess)}
	}
}

func restartCmd(cfg *core.Config, s *core.Session, hookURL string, cols, rows int) tea.Cmd {
	return restartOne(cfg, s, hookURL, cols, rows, false)
}

// restartAllCmd brings back every session whose terminal is gone, one after
// another rather than at once.
//
// The reason is the same shape as teardownAllCmd's. Installing hooks writes the
// repo's exclude file to keep the settings out of git status, and every worktree
// of a repo shares one of those -- so restarting a repo's sessions concurrently
// would have several restarts reading and rewriting the same file, and the last
// writer would drop the others' lines.
func restartAllCmd(cfg *core.Config, sessions []*core.Session, hookURL string, cols, rows int) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(sessions))
	for _, s := range sessions {
		cmds = append(cmds, restartOne(cfg, s, hookURL, cols, rows, true))
	}
	return tea.Sequence(cmds...)
}

func restartOne(cfg *core.Config, s *core.Session, hookURL string, cols, rows int, bulk bool) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		res, err := ops.Restart(ctx, cfg, &sess, ops.RestartRequest{
			HookURL: hookURL,
			Cols:    cols,
			Rows:    rows,
		})
		msg := restartMsg{id: sess.ID, bulk: bulk, err: err}
		if res != nil {
			msg.tmuxSession, msg.resumed, msg.warnings = res.TmuxSession, res.Resumed, res.Warnings
		}
		return msg
	}
}

// changedFilesCmd lists the paths a session's diff touches. It is cheap enough
// to run on every open and mode switch: two plumbing commands and a read of the
// untracked files, with no patch text rendered at all.
func changedFilesCmd(s *core.Session, mode gitx.DiffMode) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		files, err := gitx.ChangedFiles(ctx, sess.WorktreePath, sess.BaseBranch, mode)
		return changedFilesMsg{id: sess.ID, mode: mode, files: files, err: err}
	}
}

// diffCmd renders one target's diff by shelling out to git (and delta when
// present) rather than parsing and re-rendering it here.
//
// Rendering one file at a time is what the file tree buys: the whole diff of a
// session an agent has been working in for an hour is a lot of text to colorize
// for a pane showing forty lines of it.
func diffCmd(s *core.Session, mode gitx.DiffMode, target gitx.DiffTarget, opts gitx.DiffOpts, key string) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := gitx.Diff(ctx, sess.WorktreePath, sess.BaseBranch, mode, target, opts)
		return diffMsg{id: sess.ID, content: out, key: key, err: err}
	}
}

// captureCmd reads recent pane content for display only. Agent state comes from
// hooks; this output is never parsed for it.
func captureCmd(s *core.Session) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pane, _ := tmuxx.CapturePane(ctx, sess.TmuxSession, 400)
		return captureMsg{id: sess.ID, content: pane.Content}
	}
}

// attachCmd hands the terminal to tmux. Bubble Tea releases input handling for
// the duration and restores it when the process returns.
func attachCmd(s *core.Session) tea.Cmd {
	name := s.TmuxSession
	prepareAttach(name)
	return tea.ExecProcess(tmuxx.AttachCmd(name), func(err error) tea.Msg {
		restoreAfterAttach(name)
		return attachDoneMsg{err: err}
	})
}

// contextWithTimeout is the standard budget for a repo-registration call.
func contextWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// expandPath resolves ~ so a pasted path works as typed.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// errText is errStatus for a failure that is not an error value in hand -- a
// step that failed inside an operation that otherwise succeeded, or a message
// already formatted with the context the bare error lacks.
func errText(text string) tea.Cmd {
	return func() tea.Msg { return noticeMsg{text: text} }
}

func errStatus(err error) tea.Cmd {
	return func() tea.Msg { return noticeMsg{text: err.Error()} }
}

// fileMsg carries a rendered file for the pane, keyed the same way a diff is so
// a render that finished after the cursor moved on is filed and not drawn.
type fileMsg struct {
	id      string
	key     string
	path    string
	content string
	err     error
}

// worktreeFilesMsg carries the path list the fuzzy finder ranks.
type worktreeFilesMsg struct {
	id    string
	paths []string
	err   error
}

// grepMsg carries one search's hits. gen says which query asked, so a slow
// answer cannot land on top of a newer one.
type grepMsg struct {
	id   string
	gen  int
	hits []gitx.Hit
	err  error
}

// grepDebounceMsg fires once typing has paused. Each keystroke starts one and
// bumps the generation, so only the last of a burst reaches the search.
type grepDebounceMsg struct{ gen int }

// grepDebounce is how long typing has to stop before the worktree is searched.
// Short enough to feel immediate, long enough that a word typed at speed costs
// one process rather than one per letter.
const grepDebounce = 150 * time.Millisecond

func grepDebounceCmd(gen int) tea.Cmd {
	return tea.Tick(grepDebounce, func(time.Time) tea.Msg { return grepDebounceMsg{gen: gen} })
}

// fileCmd renders one file of the worktree for the pane.
func fileCmd(s *core.Session, path string, width int, key string) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		src, err := gitx.ReadFile(sess.WorktreePath, path)
		if err != nil {
			return fileMsg{id: sess.ID, key: key, path: path, err: err}
		}
		return fileMsg{id: sess.ID, key: key, path: path,
			content: render.File(path, src, 0).Text()}
	}
}

// worktreeFilesCmd lists every path the finder can offer.
func worktreeFilesCmd(s *core.Session) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		paths, err := gitx.ListFiles(ctx, sess.WorktreePath)
		return worktreeFilesMsg{id: sess.ID, paths: paths, err: err}
	}
}

// grepTimeout is the cap on a single search. A query of one letter against a
// large repo can take long enough to be worth abandoning, and cancelling the
// context kills the process rather than leaving it writing into a buffer nobody
// will read.
const grepTimeout = 10 * time.Second

// searchCancel is the board's handle on the search in flight, so starting a new
// one can stop the old.
//
// The picker's generation counter already keeps a stale answer off the screen,
// but it does nothing about the process behind it, which keeps reading the
// worktree either way. A query typed with pauses in it starts one search per
// pause, and on a large repo the abandoned ones were still running -- several
// searches of the whole tree at once, all but the last of them for a query the
// user had already typed past.
//
// It is a pointer held by the model rather than a value in it, because Bubble
// Tea copies the model on every update and a cancel stored by value would stop
// a copy nobody is waiting on. The mutex guards against the search goroutine
// finishing, and clearing its own entry, while the update loop is installing
// the next one.
// Searches are identified by the picker's generation, which is already what
// tells one query from the next.
type searchCancel struct {
	mu   sync.Mutex
	gen  int
	stop context.CancelFunc
}

// set installs the search now running. The caller must have taken the previous
// one first.
func (c *searchCancel) set(gen int, stop context.CancelFunc) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen, c.stop = gen, stop
}

// take stops whatever is running and forgets it. It is safe to call when
// nothing is.
func (c *searchCancel) take() {
	if c == nil {
		return
	}
	c.mu.Lock()
	stop := c.stop
	c.stop = nil
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// clear forgets a search that finished on its own, leaving a newer one that has
// already replaced it alone.
func (c *searchCancel) clear(gen int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen == gen {
		c.stop = nil
	}
}

// grepCmd searches the worktree.
//
// The context is made by the caller on the update loop rather than here, so
// that stopping the previous search and registering this one happen in a fixed
// order. Created inside the goroutine, a slow-starting search could install its
// cancel after the keystroke meant to abandon it had already looked.
func grepCmd(ctx context.Context, stop context.CancelFunc, holder *searchCancel, s *core.Session, query string, gen int) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		defer stop()
		defer holder.clear(gen)
		hits, err := gitx.Grep(ctx, sess.WorktreePath, query, pickLimit)
		return grepMsg{id: sess.ID, gen: gen, hits: hits, err: err}
	}
}
