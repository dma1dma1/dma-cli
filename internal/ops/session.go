// Package ops orchestrates session lifecycle: create, observe, restart, tear
// down.
package ops

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/summarize"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// CreateRequest describes a session to be created. Everything except Title has
// a sensible inferred default, so the common path stays "type a title, enter".
type CreateRequest struct {
	Title         string
	RepoID        string
	Group         string
	Profile       string
	BaseBranch    string
	InitialPrompt string
	InitialImages []ImageAttachment
	// HookURL points at the running board's hook listener. Empty disables hook
	// installation, leaving the session on liveness-only state reporting.
	HookURL string
	// Cols and Rows size the agent's terminal. They should match the area the
	// board will render it into: a detached tmux session otherwise defaults to
	// 80x24 and the agent draws its UI into a narrow strip.
	Cols, Rows int
}

// ImageAttachment is PNG data captured before a session worktree exists.
type ImageAttachment struct {
	PNG []byte
}

const (
	// bootstrapTimeout bounds dependency materialization. A large monorepo --
	// 336k entries across 42 trees -- measures around 26 seconds via directory
	// clonefile, so this is not sized for the normal path. It is sized for
	// cloneTree's fallback: a volume without clone support drops to a throttled
	// recursive copy, which measured 6.4 minutes on that same repo. This has to
	// cover the slow path or the fallback would fail every start it rescued.
	//
	// It exists to catch a genuinely stuck clone, which is why it sits far above
	// both figures rather than near either.
	bootstrapTimeout = 15 * time.Minute
	// rollbackTimeout bounds cleanup after a failed start. Discarding a worktree
	// is a rename now, so the normal path needs milliseconds of this; the budget
	// is still here for the case where the rename is unavailable and cleanup
	// falls back to deleting a worktree full of cloned dependency trees in place.
	rollbackTimeout = 5 * time.Minute
)

// abort undoes a half-created session and returns cause unchanged, so callers
// read as "fail with this error, and leave nothing behind".
//
// Cleanup deliberately runs on a context detached from the caller's. The usual
// reason to abort is that the caller's context has just died, and a dead context
// cannot run the git command that removes the worktree -- so the cleanup failed
// at exactly the moment it was needed. That is what left orphaned worktrees on
// disk and in git's registry, with nothing in the board's state pointing at them
// and no record anywhere that they existed.
func abort(ctx context.Context, repo core.Repo, worktree, tmuxName string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()

	if tmuxName != "" {
		_ = tmuxx.KillSession(ctx, tmuxName)
	}
	if err := discardWorktree(ctx, repo, worktree, true); err != nil {
		// A worktree that could not be removed is state the user has to know
		// about: it will never appear on the board, and nothing else reports it.
		return fmt.Errorf("%w (left behind worktree %s: %v)", cause, worktree, err)
	}
	return cause
}

// CreateResult carries the new session plus any non-fatal bootstrap warnings.
type CreateResult struct {
	Session  *core.Session
	Warnings []string
}

// Create performs every step needed to get an agent running: worktree,
// bootstrap, tmux session, agent launch, initial prompt, persistence. It is one
// operation on purpose -- any step that needed separate user action would get
// skipped under time pressure.
//
// The worktree starts detached at the freshly fetched tip of the base branch
// and carries no branch of its own. See gitx.AddDetachedWorktree for why the
// branch is left to the agent.
func Create(ctx context.Context, cfg *core.Config, req CreateRequest) (*CreateResult, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, fmt.Errorf("a title is required")
	}

	repo, err := cfg.ResolveRepo(req.RepoID)
	if err != nil {
		return nil, err
	}
	if !gitx.IsRepo(ctx, repo.Path) {
		return nil, fmt.Errorf("repo %q: %s is not a git repository", repo.ID, repo.Path)
	}

	base := req.BaseBranch
	if base == "" {
		base = repo.BaseBranch
	}
	if base == "" {
		base = gitx.DefaultBranch(ctx, repo.Path)
	}

	profile := req.Profile
	if profile == "" {
		profile = cfg.DefaultProfile
	}
	prof, ok := cfg.Profile(profile)
	if !ok {
		return nil, fmt.Errorf("unknown agent profile %q", profile)
	}

	// Every session starts from the remote tip. Fetching is what makes that
	// true; without it the start point is a local ref nobody has updated since
	// the last time someone worked in the repo directly.
	var warnings []string
	if err := gitx.Fetch(ctx, repo.Path, base); err != nil {
		warnings = append(warnings, fmt.Sprintf("fetch origin %s: %v — starting from the last fetched tip", base, err))
	}
	start := gitx.StartPoint(ctx, repo.Path, base)

	// The title here is the task as it was typed, which is usually a paragraph
	// -- its summary does not exist yet and arrives after the session does. So
	// the directory is named from the opening of that paragraph, cut at a word
	// rather than wherever forty characters happens to land.
	worktree := uniqueWorktreeDir(repo.WorktreeRoot, core.Slug(summarize.Shorten(title)))
	if err := gitx.AddDetachedWorktree(ctx, repo.Path, worktree, start); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	// Bootstrap gets a budget of its own. Materializing a large monorepo's
	// dependency trees is minutes of work -- a few hundred thousand files
	// through a deliberately throttled clone -- and sharing one deadline with
	// the rest of the start meant a slow bootstrap left nothing but an expired
	// context for the tmux session that follows it. The agent then launched into
	// a worktree whose dependencies were still arriving, so the wait is spent
	// here rather than deferred.
	bootCtx, cancelBoot := context.WithTimeout(ctx, bootstrapTimeout)
	bootWarnings, err := Bootstrap(bootCtx, repo, worktree)
	cancelBoot()
	warnings = append(warnings, bootWarnings...)
	if err != nil {
		return nil, abort(ctx, repo, worktree, "", fmt.Errorf("bootstrap worktree: %w", err))
	}

	if err := authorizeMatchingDirenv(ctx, repo.Path, worktree); err != nil {
		warnings = append(warnings, fmt.Sprintf("authorize direnv: %v", err))
	}

	imagePaths, err := stageImages(ctx, worktree, req.InitialImages)
	if err != nil {
		return nil, abort(ctx, repo, worktree, "", fmt.Errorf("stage initial images: %w", err))
	}

	// Hooks are installed into the worktree before the agent starts, so the
	// very first SessionStart already reports to the board.
	if req.HookURL != "" {
		if err := hooks.InstallInWorktree(ctx, worktree, req.HookURL); err != nil {
			warnings = append(warnings, fmt.Sprintf("install hooks: %v", err))
		}
	}

	// tmux session names must be unique across repos, so two repos running the
	// same task cannot collide on one session.
	tmuxName := tmuxx.SafeName(repo.ID + "-" + filepath.Base(worktree))
	tmuxName = uniqueTmux(ctx, tmuxName)

	if err := tmuxx.NewSession(ctx, tmuxName, worktree, req.Cols, req.Rows); err != nil {
		return nil, abort(ctx, repo, worktree, "", fmt.Errorf("start tmux session: %w", err))
	}

	// The prompt is part of the launch line, so the agent starts already working
	// on it -- nothing is typed into its UI afterwards. SendLiteral, not SendKeys:
	// the line now carries user text, and only the literal path keeps tmux from
	// reading a trailing semicolon as its own command separator.
	if err := tmuxx.SendLiteral(ctx, tmuxName, prof.LaunchCommand(req.InitialPrompt, imagePaths...)); err != nil {
		return nil, abort(ctx, repo, worktree, tmuxName, fmt.Errorf("launch agent: %w", err))
	}

	now := time.Now()
	s := &core.Session{
		ID:              core.NewID(),
		Title:           title,
		RepoID:          repo.ID,
		Group:           strings.TrimSpace(req.Group),
		WorktreePath:    worktree,
		BaseBranch:      base,
		TmuxSession:     tmuxName,
		AgentProfile:    profile,
		CreatedAt:       now,
		Lifecycle:       core.LifecycleActive,
		AgentState:      core.AgentWorking,
		AgentStateSince: now,
		PRState:         core.PRNone,
		PRCI:            core.CINone,
		PRReview:        core.ReviewNone,
		PRMergeable:     core.MergeUnknown,
		TmuxAlive:       true,
	}
	return &CreateResult{Session: s, Warnings: warnings}, nil
}

const attachmentExclude = ".dma/attachments-*/"

// stageImages puts initial images inside the worktree so every agent can read
// them, while excluding the session-owned directory from git status and normal
// commits. The directory leaves with the worktree during teardown.
func stageImages(ctx context.Context, worktree string, images []ImageAttachment) (paths []string, err error) {
	if len(images) == 0 {
		return nil, nil
	}
	root := filepath.Join(worktree, ".dma")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "attachments-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	if err = gitx.AddLocalExclude(ctx, worktree, attachmentExclude); err != nil {
		return nil, fmt.Errorf("exclude attachment directory: %w", err)
	}
	for i, image := range images {
		if len(image.PNG) == 0 {
			return nil, fmt.Errorf("image %d is empty", i+1)
		}
		if _, decodeErr := png.DecodeConfig(bytes.NewReader(image.PNG)); decodeErr != nil {
			return nil, fmt.Errorf("image %d is not valid PNG: %w", i+1, decodeErr)
		}
		path := filepath.Join(dir, fmt.Sprintf("image-%d.png", i+1))
		if writeErr := os.WriteFile(path, image.PNG, 0o600); writeErr != nil {
			return nil, writeErr
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// uniqueWorktreeDir picks a free directory under root for a session's slug.
//
// With no branch name to identify a session, the directory carries that job
// alone -- and titles repeat, since "fix flaky login test" is a thing worth
// doing twice. A collision suffixes rather than fails: refusing to start the
// second session would be a strange answer to a name clash.
func uniqueWorktreeDir(root, slug string) string {
	free := func(path string) bool {
		_, err := os.Stat(path)
		return os.IsNotExist(err)
	}
	if path := filepath.Join(root, slug); free(path) {
		return path
	}
	for i := 2; i < 100; i++ {
		if path := filepath.Join(root, fmt.Sprintf("%s-%d", slug, i)); free(path) {
			return path
		}
	}
	return filepath.Join(root, slug+"-"+core.NewID()[:4])
}

func uniqueTmux(ctx context.Context, want string) string {
	if !tmuxx.HasSession(ctx, want) {
		return want
	}
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", want, i)
		if !tmuxx.HasSession(ctx, cand) {
			return cand
		}
	}
	return fmt.Sprintf("%s-%s", want, core.NewID()[:4])
}

// TeardownOptions controls how aggressive a teardown is allowed to be.
type TeardownOptions struct {
	// Force removes the worktree and branch even when work would be lost. It is
	// only ever set after an explicit user confirmation.
	Force bool
	// KeepPR leaves an open pull request open. It is only ever set after the
	// close has been tried, failed, and the user chose to prune regardless.
	KeepPR bool
}

// closePR is the pull request half of teardown, indirected so tests can drive
// the ordering and the failure path without a GitHub round trip.
var closePR = ghx.ClosePR

// Teardown removes a session's tmux session, worktree, branch and record, and
// closes its pull request first if that PR is still open.
//
// It refuses to destroy uncommitted work unless Force is set.
func Teardown(ctx context.Context, cfg *core.Config, s *core.Session, opt TeardownOptions) error {
	repo, ok := cfg.Repo(s.RepoID)
	if !ok {
		return fmt.Errorf("session references unknown repo %q", s.RepoID)
	}

	if !opt.Force {
		dirty, err := gitx.IsDirty(ctx, s.WorktreePath)
		if err == nil && dirty {
			return &DirtyError{Path: s.WorktreePath}
		}
		// Commits made before the agent named a branch are reachable only from
		// this worktree's HEAD. Removing it takes the last reference to them,
		// which is a quieter way to lose work than an unmerged branch.
		if s.Branch == "" && gitx.HasCommits(ctx, s.WorktreePath, s.BaseBranch) {
			return &UnnamedCommitsError{Path: s.WorktreePath}
		}
	}

	// Pruning a session with an open pull request means abandoning that work, so
	// the pull request has to go with it: the worktree it came from and the
	// branch it can be updated from are about to stop existing, and a PR nobody
	// can push to sits in the review queue forever.
	//
	// It goes before any local removal, and a close that cannot happen -- gh
	// logged out, or no network -- stops the teardown rather than being reported
	// past it. Otherwise the failure notice arrives just as the card carrying
	// the PR number leaves the board. Nothing here has been destroyed yet, so
	// the retry is a keystroke.
	//
	// The checks above run first for a reason: their recovery is to confirm and
	// retry with Force, and that retry has to still have the pull request to
	// close.
	if !opt.KeepPR && s.HasOpenPR() {
		if err := closePR(ctx, repo.Remote, s.PRNumber); err != nil {
			return &PRCloseError{Number: s.PRNumber, Err: err}
		}
	}

	if tmuxx.HasSession(ctx, s.TmuxSession) {
		if err := tmuxx.KillSession(ctx, s.TmuxSession); err != nil {
			return fmt.Errorf("kill tmux session: %w", err)
		}
	}

	// The worktree's files are moved aside rather than unlinked here, so the
	// board is free as soon as the rename lands. See internal/ops/trash.go for
	// what that is worth and who does the deleting.
	if err := discardWorktree(ctx, repo, s.WorktreePath, opt.Force); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}

	// A session only has a branch once its agent made one, and the worktree is
	// already gone by here, so there is nothing left to clean up without one.
	// A missing branch also means teardown already got this far before its board
	// quit, so retrying the persisted prune is complete rather than an error.
	if s.Branch == "" || !gitx.BranchExists(ctx, repo.Path, s.Branch) {
		return nil
	}
	// A branch delete that would lose commits fails without -D; that is a
	// warning, not a teardown failure, since the worktree is already gone.
	if err := gitx.DeleteBranch(ctx, repo.Path, s.Branch, opt.Force); err != nil && !opt.Force {
		return &BranchNotMergedError{Branch: s.Branch, Err: err}
	}
	return nil
}

// PRCloseError reports that a session's open pull request could not be closed.
// Nothing was torn down: the session is still whole, and still on the board.
type PRCloseError struct {
	Number int
	Err    error
}

func (e *PRCloseError) Error() string {
	return fmt.Sprintf("close PR #%d: %v", e.Number, e.Err)
}

func (e *PRCloseError) Unwrap() error { return e.Err }

// DirtyError reports that a worktree has uncommitted changes.
type DirtyError struct{ Path string }

func (e *DirtyError) Error() string {
	return fmt.Sprintf("worktree has uncommitted changes: %s", e.Path)
}

// UnnamedCommitsError reports commits sitting on a worktree's detached HEAD,
// which no branch would keep alive after teardown.
type UnnamedCommitsError struct{ Path string }

func (e *UnnamedCommitsError) Error() string {
	return fmt.Sprintf("worktree has commits on no branch: %s", e.Path)
}

// BranchNotMergedError reports that a branch still holds unmerged commits.
type BranchNotMergedError struct {
	Branch string
	Err    error
}

func (e *BranchNotMergedError) Error() string {
	return fmt.Sprintf("branch %s is not fully merged", e.Branch)
}

// Kill stops the agent's tmux session but leaves the worktree and branch in
// place, so the work can be resumed or reviewed.
func Kill(ctx context.Context, s *core.Session) error {
	if !tmuxx.HasSession(ctx, s.TmuxSession) {
		return nil
	}
	return tmuxx.KillSession(ctx, s.TmuxSession)
}

// RestartRequest carries what a restart needs from the running board, which is
// the same pair of things a create needs: where to report hooks, and how big the
// terminal it is about to draw into is.
type RestartRequest struct {
	// HookURL points at this board's hook listener. It is reinstalled on every
	// restart rather than trusted from the worktree, because the address is not
	// stable: a board that found its configured port taken listens on an ephemeral
	// one instead, so the settings written when the session was created can point
	// at a port nothing is on.
	HookURL string
	// Cols and Rows size the rebuilt terminal, for the reason given on
	// CreateRequest: a detached tmux session otherwise defaults to 80x24.
	Cols, Rows int
}

// RestartResult reports what came back up.
type RestartResult struct {
	// TmuxSession is the session's terminal now. It is normally the name the
	// session already had, and differs only in the rare case that name could not
	// be reclaimed.
	TmuxSession string
	// Resumed is whether the agent was started on the conversation it had here, or
	// as a fresh one that knows nothing about the task. The difference is invisible
	// on the board, so the caller has to say it.
	Resumed  bool
	Warnings []string
}

// Restart rebuilds a session's terminal and puts its agent back in it, resuming
// the conversation it was having in this worktree.
//
// This is what a session needs after the machine it was running on restarted:
// tmux takes every agent with it, and what comes back is a board of cards
// describing worktrees with no process in them. Everything durable survived --
// the worktree, its branch, its commits, and the agent's own transcript -- so the
// session does not need creating again, only running again.
//
// The worktree is left exactly as it is. It is not re-fetched, re-based or
// re-bootstrapped: it holds work in progress, and a restart that moved the ground
// under it would be a different and much more destructive operation than the one
// the user asked for. Dependencies materialized when the session was created are
// still there, since they live in the worktree too.
//
// A live tmux session is killed first, which makes this the way to restart a
// wedged agent as well as a dead one. Callers confirm that with the user; nothing
// here can tell an agent mid-thought from one that has been stuck for an hour.
func Restart(ctx context.Context, cfg *core.Config, s *core.Session, req RestartRequest) (*RestartResult, error) {
	prof, ok := cfg.Profile(s.AgentProfile)
	if !ok {
		return nil, fmt.Errorf("unknown agent profile %q", s.AgentProfile)
	}

	// The worktree is the session: an agent has nowhere to be resumed without it,
	// and starting tmux at a missing directory would produce a live session whose
	// pane is an error message -- which reads as running. A worktree can go missing
	// for undramatic reasons, most often a prune whose state write did not land, so
	// this is a distinct error the board can offer the right key for.
	info, err := os.Stat(s.WorktreePath)
	if err != nil || !info.IsDir() {
		return nil, &WorktreeMissingError{Path: s.WorktreePath}
	}

	if tmuxx.HasSession(ctx, s.TmuxSession) {
		if err := tmuxx.KillSession(ctx, s.TmuxSession); err != nil {
			return nil, fmt.Errorf("stop the running agent: %w", err)
		}
	}
	// The old name is reclaimed rather than replaced, so a session keeps the
	// terminal name it has been referred to by since it was created. uniqueTmux is
	// the fallback for the name that outlived the kill -- a session tmux is still
	// tearing down, or one this board does not own.
	name := uniqueTmux(ctx, s.TmuxSession)

	var warnings []string
	if req.HookURL != "" {
		if err := hooks.InstallInWorktree(ctx, s.WorktreePath, req.HookURL); err != nil {
			warnings = append(warnings, fmt.Sprintf("install hooks: %v", err))
		}
	}

	if err := tmuxx.NewSession(ctx, name, s.WorktreePath, req.Cols, req.Rows); err != nil {
		return nil, fmt.Errorf("start tmux session: %w", err)
	}
	// SendLiteral for the same reason Create uses it: the line carries whatever the
	// profile's command is, and only the literal path keeps tmux from reading part
	// of it as a command of its own.
	if err := tmuxx.SendLiteral(ctx, name, prof.RestartCommandFor(s.AgentSessionID)); err != nil {
		// The terminal is ours and was made a moment ago, so taking it back leaves
		// the session exactly as it was found -- not running, and restartable again.
		// The worktree is never touched here: unlike a failed create, there is
		// nothing new on disk to undo, and it holds the user's work.
		//
		// On a context of its own, for the reason abort explains: the usual way to
		// reach this line is the caller's context expiring, and a dead context cannot
		// run the tmux command that undoes the damage.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		_ = tmuxx.KillSession(cleanup, name)
		cancel()
		return nil, fmt.Errorf("launch agent: %w", err)
	}

	return &RestartResult{
		TmuxSession: name,
		// An attached session resumes by id, which is a form of resume the
		// profile can have without having the by-directory one.
		Resumed:  prof.ResumeIDLine(s.AgentSessionID) != "" || strings.TrimSpace(prof.ResumeCommand) != "",
		Warnings: warnings,
	}, nil
}

// WorktreeMissingError reports that a session's worktree is no longer on disk.
// There is nothing left to run an agent in, so the card describes work that
// cannot be picked up again.
type WorktreeMissingError struct{ Path string }

func (e *WorktreeMissingError) Error() string {
	return fmt.Sprintf("worktree is gone: %s", e.Path)
}

// Observation is the periodically refreshed view of a session.
type Observation struct {
	ID          string
	Alive       bool
	Dirty       bool
	DiffAdded   int
	DiffRemoved int
	// Branch is the branch the worktree is on right now, empty while its HEAD
	// is still detached. Since dma never creates one, this is the only way the
	// board learns the name the agent chose.
	Branch string
}

// observeConcurrency bounds how many worktrees are inspected at once. Each one
// is a handful of git processes, so this is a limit on processes in flight
// rather than on goroutines: a board of fifty sessions should not answer a poll
// tick by forking two hundred and fifty gits at the same moment.
func observeConcurrency() int {
	if n := runtime.NumCPU(); n > 4 {
		return n
	}
	return 4
}

// Observe collects liveness and worktree facts for all sessions. Liveness comes
// from a single tmux list-sessions rather than one call per session.
//
// The worktrees are inspected concurrently. Each one costs several git
// processes -- a branch read, a status, and a diff stat -- and on a large repo
// that is most of half a second, so done one after another a poll grew directly
// with the size of the board: ten sessions against a monorepo measured 2.9s of
// git per tick, and fanning them out brought it to 1.5s. It is off the update
// loop either way, so this was never a freeze; what it was is a burst of some
// fifty processes that got longer every time a session was added.
//
// Running them together is safe because none of it writes anything shared. Each
// worktree has its own index, so the refresh a status does lands in its own
// file; everything else reads refs and objects, which git is happy to serve
// concurrently. The writing operations on a repo -- worktree add, remove, prune
// -- do contend, which is why teardown is still sequenced; see teardownAllCmd.
func Observe(ctx context.Context, sessions []*core.Session) []Observation {
	live, _ := tmuxx.ListSessions(ctx)

	// Indexed rather than appended, so the results stay in the order the
	// sessions arrived in whatever order they finish.
	out := make([]Observation, len(sessions))
	sem := make(chan struct{}, observeConcurrency())
	var wg sync.WaitGroup
	for i, s := range sessions {
		wg.Add(1)
		go func(i int, s *core.Session) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			o := Observation{
				ID:     s.ID,
				Alive:  live[s.TmuxSession],
				Branch: gitx.CurrentBranch(ctx, s.WorktreePath),
			}
			if dirty, err := gitx.IsDirty(ctx, s.WorktreePath); err == nil {
				o.Dirty = dirty
			}
			if a, r, err := gitx.DiffStat(ctx, s.WorktreePath, s.BaseBranch); err == nil {
				o.DiffAdded, o.DiffRemoved = a, r
			}
			out[i] = o
		}(i, s)
	}
	wg.Wait()
	return out
}
