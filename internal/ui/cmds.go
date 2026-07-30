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

// previewMsg carries recent terminal output for the panel.
type previewMsg struct {
	id      string
	content string
}

// probeMsg carries inferred state for agents that cannot report their own.
type probeMsg struct{ states []probe.State }

type hookMsg hooks.Event

type observeMsg struct{ obs []ops.Observation }

// prSyncMsg carries one repo's poll result. Repos are polled and reported
// independently so one unreachable remote cannot stall the others.
type prSyncMsg struct {
	repoID string
	prs    []ghx.PR
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
	id     string
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
		out, _ := tmuxx.CapturePane(ctx, sess.TmuxSession, 300)
		return previewMsg{id: sess.ID, content: out}
	}
}

// probeCmd infers state for sessions whose agent has no hook channel. Sessions
// running a hook-capable agent are skipped: their own reports are exact, and a
// heuristic could only contradict them.
func probeCmd(p *probe.Prober, cfg *core.Config, sessions []*core.Session) tea.Cmd {
	var targets []*core.Session
	keep := map[string]bool{}
	for _, s := range sessions {
		keep[s.ID] = true
		if prof, ok := cfg.Profile(s.AgentProfile); ok && prof.Hooks {
			continue
		}
		copied := *s
		targets = append(targets, &copied)
	}
	p.Forget(keep)
	if len(targets) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		states := make([]probe.State, 0, len(targets))
		for _, s := range targets {
			states = append(states, p.Probe(ctx, s))
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

// pollPRsCmd polls each repo that has at least one unfinished session. Repos
// with nothing running are skipped rather than polled for completeness.
func pollPRsCmd(cfg *core.Config, sessions []*core.Session) tea.Cmd {
	active := map[string]bool{}
	for _, s := range sessions {
		if s.Lifecycle != core.LifecycleMerged {
			active[s.RepoID] = true
		}
	}
	var cmds []tea.Cmd
	for _, repo := range cfg.Repos {
		// Skip repos with nothing running, and repos with no remote at all --
		// polling those could only ever produce the same error every interval.
		if !active[repo.ID] || repo.Remote == "" {
			continue
		}
		r := repo
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			prs, err := ghx.ListPRs(ctx, r.Remote)
			return prSyncMsg{repoID: r.ID, prs: prs, err: err}
		})
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
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

func createCmd(cfg *core.Config, sessions []*core.Session, req ops.CreateRequest) tea.Cmd {
	snapshot := append([]*core.Session(nil), sessions...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res, err := ops.Create(ctx, cfg, snapshot, req)
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
		if err := gitx.CommitAll(ctx, sess.WorktreePath, title); err != nil {
			return shippedMsg{id: sess.ID, err: err}
		}
		if !gitx.HasCommits(ctx, sess.WorktreePath, sess.BaseBranch) {
			return shippedMsg{id: sess.ID, err: fmt.Errorf("nothing to push: no commits on %s", sess.Branch)}
		}
		if err := gitx.Push(ctx, sess.WorktreePath, sess.Branch); err != nil {
			return shippedMsg{id: sess.ID, err: err}
		}
		remote := repo.Remote
		if remote == "" {
			if r, err := gitx.RemoteSlug(ctx, repo.Path); err == nil {
				remote = r
			}
		}
		n, err := ghx.CreatePR(ctx, sess.WorktreePath, remote, sess.BaseBranch, sess.Branch,
			title, "Opened from dma.", false)
		return shippedMsg{id: sess.ID, number: n, err: err}
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
		out, _ := tmuxx.CapturePane(ctx, sess.TmuxSession, 400)
		return captureMsg{id: sess.ID, content: out}
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

func status(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: text} }
}

func errStatus(err error) tea.Cmd {
	return func() tea.Msg { return statusMsg{text: err.Error(), isErr: true} }
}
