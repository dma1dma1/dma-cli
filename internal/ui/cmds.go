package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/ops"
	"github.com/dma1dma1/dma-cli/internal/probe"
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
	id      string
	content string
	cursor  tmuxx.Cursor
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
	err error
}

type shippedMsg struct {
	id string
	// branch is the name the agent gave its work, read at ship time -- the
	// session record may still be showing none.
	branch string
	number int
	err    error
}

type mergedMsg struct {
	id  string
	err error
}

type teardownMsg struct {
	id  string
	err error
}

type killedMsg struct {
	id  string
	err error
}

type diffMsg struct {
	id      string
	content string
	err     error
}

type captureMsg struct {
	id      string
	content string
}

type statusMsg struct {
	text  string
	isErr bool
}

type attachDoneMsg struct{ err error }

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
	if s == nil {
		return nil
	}
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		pane, _ := tmuxx.CapturePane(ctx, sess.TmuxSession, 0)
		return previewMsg{id: sess.ID, content: pane.Content, cursor: pane.Cursor}
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
			return statusMsg{text: fmt.Sprintf("send to %s: %v", sess.Title, err), isErr: true}
		}
		return nil
	}
}

// probeCmd infers state for sessions whose agent has no hook channel. Sessions
// running a hook-capable agent are skipped: their own reports are exact, and a
// heuristic could only contradict them.
//
// typedAt is read and pruned here rather than inside the command, because the
// map belongs to the model and the command runs on another goroutine.
func probeCmd(p *probe.Prober, cfg *core.Config, sessions []*core.Session, typedAt map[string]time.Time) tea.Cmd {
	var targets []*core.Session
	keep := map[string]bool{}
	typed := map[string]time.Time{}
	for _, s := range sessions {
		keep[s.ID] = true
		if prof, ok := cfg.Profile(s.AgentProfile); ok && prof.Hooks {
			continue
		}
		copied := *s
		targets = append(targets, &copied)
		typed[s.ID] = typedAt[s.ID]
	}
	p.Forget(keep)
	for id := range typedAt {
		if !keep[id] {
			delete(typedAt, id)
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
			states = append(states, p.Probe(ctx, s, typed[s.ID]))
		}
		return probeMsg{states: states}
	}
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

func createCmd(cfg *core.Config, req ops.CreateRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := ops.Create(ctx, cfg, req)
		return createdMsg{res: res, err: err}
	}
}

// shipCmd commits, pushes and opens a PR in one action.
func shipCmd(cfg *core.Config, s *core.Session, title string) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		repo, ok := cfg.Repo(sess.RepoID)
		if !ok {
			return shippedMsg{id: sess.ID, err: fmt.Errorf("unknown repo %q", sess.RepoID)}
		}
		// Read the branch from the worktree rather than the session record: the
		// agent names it, and a poll may not have picked it up yet. Naming it is
		// also the agent's job, so a still-detached worktree stops here instead
		// of getting a name invented for it.
		branch := gitx.CurrentBranch(ctx, sess.WorktreePath)
		if branch == "" {
			return shippedMsg{id: sess.ID, err: fmt.Errorf("no branch yet: the agent has not created one in %s", sess.WorktreePath)}
		}
		if err := gitx.CommitAll(ctx, sess.WorktreePath, title); err != nil {
			return shippedMsg{id: sess.ID, err: err}
		}
		if !gitx.HasCommits(ctx, sess.WorktreePath, sess.BaseBranch) {
			return shippedMsg{id: sess.ID, err: fmt.Errorf("nothing to push: no commits on %s", branch)}
		}
		if err := gitx.Push(ctx, sess.WorktreePath, branch); err != nil {
			return shippedMsg{id: sess.ID, err: err}
		}
		remote := repo.Remote
		if remote == "" {
			if r, err := gitx.RemoteSlug(ctx, repo.Path); err == nil {
				remote = r
			}
		}
		n, err := ghx.CreatePR(ctx, sess.WorktreePath, remote, sess.BaseBranch, branch,
			title, "Opened from dma.", false)
		return shippedMsg{id: sess.ID, branch: branch, number: n, err: err}
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
		err := ghx.MergePR(ctx, repo.Remote, sess.PRNumber, "squash")
		return mergedMsg{id: sess.ID, err: err}
	}
}

func teardownCmd(cfg *core.Config, s *core.Session, force bool) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := ops.Teardown(ctx, cfg, &sess, ops.TeardownOptions{Force: force})
		return teardownMsg{id: sess.ID, err: err}
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

// diffCmd renders the diff by shelling out to git (and delta when present)
// rather than parsing and re-rendering it here.
func diffCmd(s *core.Session, mode gitx.DiffMode) tea.Cmd {
	sess := *s
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := gitx.Diff(ctx, sess.WorktreePath, sess.BaseBranch, mode)
		return diffMsg{id: sess.ID, content: out, err: err}
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

// status announces work the board cannot show on its own -- something in
// flight, or something that went wrong. It is deliberately not used to confirm
// an action whose result is already visible on a card: the message takes the
// footer away from the shortcut bar for ten seconds, and the shortcuts are what
// the user is looking at.
func status(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}

// errText is errStatus for a failure that is not an error value in hand -- a
// step that failed inside an operation that otherwise succeeded, or a message
// already formatted with the context the bare error lacks.
func errText(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text, isErr: true} }
}

func errStatus(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: err.Error(), isErr: true} }
}
