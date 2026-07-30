// Package ops orchestrates session lifecycle: create, observe, tear down.
package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
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
	Branch        string // optional explicit branch; derived from Title when empty
	InitialPrompt string
	// HookURL points at the running board's hook listener. Empty disables hook
	// installation, leaving the session on liveness-only state reporting.
	HookURL string
}

// CreateResult carries the new session plus any non-fatal bootstrap warnings.
type CreateResult struct {
	Session  *core.Session
	Warnings []string
}

// Create performs every step needed to get an agent running: worktree, branch,
// bootstrap, tmux session, agent launch, initial prompt, persistence. It is one
// operation on purpose -- any step that needed separate user action would get
// skipped under time pressure.
func Create(ctx context.Context, cfg *core.Config, existing []*core.Session, req CreateRequest) (*CreateResult, error) {
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

	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = repo.BranchPrefix + core.Slug(title)
	}
	branch = uniqueBranch(ctx, repo, existing, branch)

	profile := req.Profile
	if profile == "" {
		profile = cfg.DefaultProfile
	}
	prof, ok := cfg.Profile(profile)
	if !ok {
		return nil, fmt.Errorf("unknown agent profile %q", profile)
	}

	worktree := filepath.Join(repo.WorktreeRoot, sanitizePathSegment(branch))
	if _, err := os.Stat(worktree); err == nil {
		return nil, fmt.Errorf("worktree path already exists: %s", worktree)
	}

	if err := gitx.AddWorktree(ctx, repo.Path, worktree, branch, base); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	warnings := Bootstrap(ctx, repo, worktree)

	// Hooks are installed into the worktree before the agent starts, so the
	// very first SessionStart already reports to the board.
	if req.HookURL != "" {
		if err := hooks.InstallInWorktree(ctx, worktree, req.HookURL); err != nil {
			warnings = append(warnings, fmt.Sprintf("install hooks: %v", err))
		}
	}

	// tmux session names must be unique across repos, so two repos with the
	// same branch name cannot collide on one session.
	tmuxName := tmuxx.SafeName(repo.ID + "-" + branch)
	tmuxName = uniqueTmux(ctx, tmuxName)

	if err := tmuxx.NewSession(ctx, tmuxName, worktree); err != nil {
		// Roll the worktree back rather than leaving a half-created session.
		_ = gitx.RemoveWorktree(ctx, repo.Path, worktree, true)
		_ = gitx.DeleteBranch(ctx, repo.Path, branch, true)
		return nil, fmt.Errorf("start tmux session: %w", err)
	}

	if err := tmuxx.SendKeys(ctx, tmuxName, prof.Command); err != nil {
		return nil, fmt.Errorf("launch agent: %w", err)
	}

	if p := strings.TrimSpace(req.InitialPrompt); p != "" {
		// Give the agent a moment to come up before typing at it.
		go func(name, prompt string) {
			time.Sleep(2 * time.Second)
			_ = tmuxx.SendLiteral(context.Background(), name, prompt)
		}(tmuxName, p)
	}

	now := time.Now()
	s := &core.Session{
		ID:              core.NewID(),
		Title:           title,
		RepoID:          repo.ID,
		Group:           strings.TrimSpace(req.Group),
		WorktreePath:    worktree,
		Branch:          branch,
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

// uniqueBranch appends a numeric suffix until the (repo_id, branch) pair is
// free, both in git and among tracked sessions.
func uniqueBranch(ctx context.Context, repo core.Repo, existing []*core.Session, want string) string {
	taken := func(b string) bool {
		if core.FindByKey(existing, core.Key{RepoID: repo.ID, Branch: b}) != nil {
			return true
		}
		return gitx.BranchExists(ctx, repo.Path, b)
	}
	if !taken(want) {
		return want
	}
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", want, i)
		if !taken(cand) {
			return cand
		}
	}
	return fmt.Sprintf("%s-%s", want, core.NewID()[:4])
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

// sanitizePathSegment flattens a branch name into a single directory name, so
// "feat/auth" does not create a nested directory tree under the worktree root.
func sanitizePathSegment(branch string) string {
	return strings.ReplaceAll(branch, string(filepath.Separator), "-")
}

// TeardownOptions controls how aggressive a teardown is allowed to be.
type TeardownOptions struct {
	// Force removes the worktree and branch even when work would be lost. It is
	// only ever set after an explicit user confirmation.
	Force bool
}

// Teardown removes a session's tmux session, worktree, branch and record.
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

	// A branch delete that would lose commits fails without -D; that is a
	// warning, not a teardown failure, since the worktree is already gone.
	if err := gitx.DeleteBranch(ctx, repo.Path, s.Branch, opt.Force); err != nil && !opt.Force {
		return &BranchNotMergedError{Branch: s.Branch, Err: err}
	}
	return nil
}

// DirtyError reports that a worktree has uncommitted changes.
type DirtyError struct{ Path string }

func (e *DirtyError) Error() string {
	return fmt.Sprintf("worktree has uncommitted changes: %s", e.Path)
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

// Observation is the periodically refreshed, non-persisted view of a session.
type Observation struct {
	ID          string
	Alive       bool
	Dirty       bool
	DiffAdded   int
	DiffRemoved int
}

// Observe collects liveness and worktree facts for all sessions. Liveness comes
// from a single tmux list-sessions rather than one call per session.
func Observe(ctx context.Context, sessions []*core.Session) []Observation {
	live, _ := tmuxx.ListSessions(ctx)
	out := make([]Observation, 0, len(sessions))
	for _, s := range sessions {
		o := Observation{ID: s.ID, Alive: live[s.TmuxSession]}
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
