// Package ops orchestrates session lifecycle: create, observe, tear down.
package ops

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
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

	warnings = append(warnings, Bootstrap(ctx, repo, worktree)...)
	if err := authorizeMatchingDirenv(ctx, repo.Path, worktree); err != nil {
		warnings = append(warnings, fmt.Sprintf("authorize direnv: %v", err))
	}

	imagePaths, err := stageImages(ctx, worktree, req.InitialImages)
	if err != nil {
		_ = gitx.RemoveWorktree(ctx, repo.Path, worktree, true)
		return nil, fmt.Errorf("stage initial images: %w", err)
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
		// Roll the worktree back rather than leaving a half-created session.
		_ = gitx.RemoveWorktree(ctx, repo.Path, worktree, true)
		return nil, fmt.Errorf("start tmux session: %w", err)
	}

	// The prompt is part of the launch line, so the agent starts already working
	// on it -- nothing is typed into its UI afterwards. SendLiteral, not SendKeys:
	// the line now carries user text, and only the literal path keeps tmux from
	// reading a trailing semicolon as its own command separator.
	if err := tmuxx.SendLiteral(ctx, tmuxName, prof.LaunchCommand(req.InitialPrompt, imagePaths...)); err != nil {
		return nil, fmt.Errorf("launch agent: %w", err)
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

	if _, err := os.Stat(s.WorktreePath); err == nil {
		if err := gitx.RemoveWorktree(ctx, repo.Path, s.WorktreePath, opt.Force); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	if err := gitx.PruneWorktrees(ctx, repo.Path); err != nil {
		return fmt.Errorf("prune worktrees: %w", err)
	}

	// A session only has a branch once its agent made one, and the worktree is
	// already gone by here, so there is nothing left to clean up without one.
	if s.Branch == "" {
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

// Observe collects liveness and worktree facts for all sessions. Liveness comes
// from a single tmux list-sessions rather than one call per session.
func Observe(ctx context.Context, sessions []*core.Session) []Observation {
	live, _ := tmuxx.ListSessions(ctx)
	out := make([]Observation, 0, len(sessions))
	for _, s := range sessions {
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
		out = append(out, o)
	}
	return out
}
