package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dma1dma1/dma-cli/internal/convo"
	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/summarize"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// AttachRequest describes an existing agent conversation to bring onto the
// board. Everything but the agent and the id is inferred from the conversation
// itself.
type AttachRequest struct {
	// Profile is the agent that holds the conversation.
	Profile string
	// SessionID is that agent's id for it.
	SessionID string
	// RepoID overrides the repo inferred from where the conversation was held.
	// It is required when that directory is not in a git repository at all.
	RepoID string
	// Group is the project to file the session under.
	Group string
	// Title overrides the name taken from the conversation's opening prompt.
	Title string
	// Clean skips carrying the conversation's work in progress into the new
	// worktree, starting it from the base branch instead. See Attach.
	Clean bool
	// Existing is the board's current sessions, so a conversation already on it
	// is refused rather than given a second worktree.
	Existing []*core.Session
	// HookURL, Cols and Rows are as they are on CreateRequest.
	HookURL    string
	Cols, Rows int
}

// AttachResult is the new session plus what had to be said about making it.
type AttachResult struct {
	Session      *core.Session
	Conversation convo.Conversation
	// Carried reports the work in progress moved into the new worktree.
	Carried  gitx.Carried
	Warnings []string
}

// Attach gives an agent conversation that was started outside dma a worktree
// and a card, and opens it there.
//
// The conversation is not restarted: the agent comes up knowing everything it
// knew a moment ago, either resumed on the conversation itself or on a copy of it,
// whichever its profile offers -- see openConversation. What changes is where it
// is standing. That relocation is the whole operation, and it is why the default
// is to bring the work in progress along: a conversation whose last few turns
// were spent editing files, resumed in a worktree cut clean from a commit, would
// be an agent confidently describing changes that are not in front of it.
//
// So unless Clean is set, the worktree is cut from the commit the conversation's
// directory is sitting on rather than from the fetched base tip, and that
// directory's uncommitted changes are replayed into it. Cutting from the same
// commit is what makes the replay a plain apply with nothing to merge.
//
// The directory the conversation came from is only ever read. It keeps its
// files, its branch and its own copy of the work; nothing here moves out of it,
// so an attach that turns out to be the wrong idea is undone by pruning the new
// session. Note the flip side: the work now exists twice, and edits made in one
// place do not reach the other.
func Attach(ctx context.Context, cfg *core.Config, req AttachRequest) (*AttachResult, error) {
	prof, ok := cfg.Profile(req.Profile)
	if !ok {
		return nil, fmt.Errorf("unknown agent profile %q", req.Profile)
	}
	// Checked before anything is looked up, because it is a fact about the
	// configuration rather than about this conversation, and the answer is the
	// same for every id the user might try next.
	if strings.TrimSpace(prof.ForkCommand) == "" && strings.TrimSpace(prof.ResumeIDCommand) == "" {
		return nil, fmt.Errorf("agent profile %q has no resume_id_command or fork_command, so dma cannot open one of its conversations in a worktree of its own", req.Profile)
	}

	conversation, err := convo.Find(req.Profile, req.SessionID)
	if err != nil {
		return nil, err
	}
	for _, s := range req.Existing {
		// Either the board is holding that conversation, or it is holding a copy
		// made from it. Both mean the same work already has a card.
		if s.AgentSessionID == conversation.ID || s.ForkedFrom == conversation.ID {
			return nil, &AlreadyAttachedError{Title: s.Title, Worktree: s.WorktreePath}
		}
	}
	open := openConversation(prof, conversation.ID)
	if open.Line == "" {
		return nil, fmt.Errorf("agent profile %q cannot open conversation %s", req.Profile, conversation.ID)
	}

	var warnings []string
	repo, repoWarnings, err := attachRepo(ctx, cfg, req.RepoID, conversation.Cwd)
	warnings = append(warnings, repoWarnings...)
	if err != nil {
		return nil, err
	}

	// The source is only a source if it is still there and still in this repo.
	// A conversation held months ago in a worktree since pruned is perfectly
	// attachable; there is just nothing to carry out of it.
	source := carrySource(ctx, repo, conversation.Cwd, req.Clean)

	base := repo.BaseBranch
	if base == "" {
		base = gitx.DefaultBranch(ctx, repo.Path)
	}

	start, source, startWarnings := attachStartPoint(ctx, repo, base, source)
	warnings = append(warnings, startWarnings...)

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(conversation.Title)
	}
	if title == "" {
		// A conversation with no readable opening prompt still needs a name on
		// the card, and the id is the one thing it definitely has.
		title = req.Profile + " session " + shortID(conversation.ID)
	}

	worktree := uniqueWorktreeDir(repo.WorktreeRoot, core.Slug(summarize.Shorten(title)))
	if err := gitx.AddDetachedWorktree(ctx, repo.Path, worktree, start); err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

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

	// After the bootstrap, so a carried file cannot be overwritten by the
	// dependency materialization that follows it.
	var carried gitx.Carried
	if source != "" {
		// A carry that half-lands still reports what it moved, and the counts are
		// kept either way so the caller can say which files did arrive.
		moved, carryErr := gitx.Carry(ctx, source, worktree)
		carried = moved
		if carryErr != nil {
			// Not fatal. The session is worth having with its files missing --
			// the alternative is no session and a conversation the user still
			// has to move by hand -- but it is worth saying loudly, because the
			// agent is about to come back up believing the files are there.
			warnings = append(warnings, fmt.Sprintf("could not carry work in progress from %s: %v", source, carryErr))
		}
	}

	if req.HookURL != "" {
		if err := hooks.InstallInWorktree(ctx, worktree, req.HookURL); err != nil {
			warnings = append(warnings, fmt.Sprintf("install hooks: %v", err))
		}
	}

	tmuxName := uniqueTmux(ctx, tmuxx.SafeName(repo.ID+"-"+filepath.Base(worktree)))
	if err := tmuxx.NewSession(ctx, tmuxName, worktree, req.Cols, req.Rows); err != nil {
		return nil, abort(ctx, repo, worktree, "", fmt.Errorf("start tmux session: %w", err))
	}
	// No fallback launch line behind this one, unlike a restart: an attach that
	// cannot find the conversation must not quietly become a fresh agent under
	// a card named after the session the user asked for.
	if err := tmuxx.SendLiteral(ctx, tmuxName, open.Line); err != nil {
		return nil, abort(ctx, repo, worktree, tmuxName, fmt.Errorf("resume agent: %w", err))
	}

	now := time.Now()
	s := &core.Session{
		ID:             core.NewID(),
		Title:          title,
		RepoID:         repo.ID,
		Group:          strings.TrimSpace(req.Group),
		WorktreePath:   worktree,
		BaseBranch:     base,
		TmuxSession:    tmuxName,
		AgentProfile:   req.Profile,
		AgentSessionID: open.SessionID,
		ForkedFrom:     open.ForkedFrom,
		CreatedAt:      now,
		// Idle, not working: a resumed conversation opens on its history and
		// waits, because nothing was sent with it. The first thing the agent
		// does will move the card itself.
		Lifecycle:       core.LifecycleIdle,
		AgentState:      core.AgentIdle,
		AgentStateSince: now,
		PRState:         core.PRNone,
		PRCI:            core.CINone,
		PRReview:        core.ReviewNone,
		PRMergeable:     core.MergeUnknown,
		TmuxAlive:       true,
	}
	// For an agent that reports through hooks, the conversation id is the id the
	// hooks arrive under, so filling it in now means the very first event
	// correlates by id instead of falling back to matching on the directory.
	if prof.Hooks && open.SessionID != "" {
		s.ClaudeSessionID = open.SessionID
	}

	return &AttachResult{
		Session:      s,
		Conversation: conversation,
		Carried:      carried,
		Warnings:     warnings,
	}, nil
}

// opened is how one conversation is being brought up in the worktree cut for it.
type opened struct {
	// Line is the shell line that does it.
	Line string
	// SessionID is the conversation the session holds afterwards. It is empty
	// only for a fork whose agent could not be told what to call the copy, which
	// then restarts by directory like a session dma started itself.
	SessionID string
	// ForkedFrom is the conversation copied, empty when the original was reopened
	// where it stands.
	ForkedFrom string
}

// openConversation decides how to put an existing conversation in front of an
// agent standing in a new worktree.
//
// A fork wins wherever a profile offers one, and the session then holds the copy
// rather than the original: an agent that carries its own idea of where it lives
// would otherwise go on working in the directory the conversation came from, with
// this card's worktree left empty and two cards' edits landing in one tree. See
// AgentProfile.ForkCommand.
//
// The id for the copy is minted here rather than read back afterwards, because
// the only other way to learn it is to guess which file in the agent's store
// appeared just now.
func openConversation(prof core.AgentProfile, sourceID string) opened {
	if strings.TrimSpace(prof.ForkCommand) != "" {
		minted := core.NewID()
		if line := prof.ForkLine(sourceID, minted); line != "" {
			if !prof.ForkMintsID() {
				minted = ""
			}
			return opened{Line: line, SessionID: minted, ForkedFrom: sourceID}
		}
	}
	return opened{Line: prof.ResumeIDLine(sourceID), SessionID: sourceID}
}

// AlreadyAttachedError reports a conversation that is already on the board.
// Attaching it twice would produce two worktrees for one agent, which can only
// end with one of them being silently abandoned.
type AlreadyAttachedError struct {
	Title    string
	Worktree string
}

func (e *AlreadyAttachedError) Error() string {
	return fmt.Sprintf("that conversation is already attached as %q (%s)", e.Title, e.Worktree)
}

// attachRepo decides which repo the new worktree belongs to.
//
// Where the conversation was held answers this on its own in the normal case,
// and an unregistered repo is registered rather than refused -- the same bargain
// the board makes with the directory it is launched from. An explicit RepoID
// wins, and is the only answer available when the conversation was not held in
// a repository at all.
func attachRepo(ctx context.Context, cfg *core.Config, repoID, cwd string) (core.Repo, []string, error) {
	if repoID != "" {
		repo, err := cfg.ResolveRepo(repoID)
		if err != nil {
			return core.Repo{}, nil, err
		}
		if !gitx.IsRepo(ctx, repo.Path) {
			return core.Repo{}, nil, fmt.Errorf("repo %q: %s is not a git repository", repo.ID, repo.Path)
		}
		return repo, nil, nil
	}

	if cwd == "" {
		return core.Repo{}, nil, fmt.Errorf("that conversation does not record where it was held; name a repo with -repo")
	}
	if _, err := os.Stat(cwd); err != nil {
		return core.Repo{}, nil, fmt.Errorf("that conversation was held in %s, which is no longer there; name a repo with -repo", cwd)
	}
	if !gitx.IsRepo(ctx, cwd) {
		return core.Repo{}, nil, fmt.Errorf("that conversation was held in %s, which is not a git repository; name a repo with -repo", cwd)
	}

	repo, added, err := Adopt(ctx, cfg, cwd)
	if err != nil {
		return core.Repo{}, nil, fmt.Errorf("register the repo it was held in: %w", err)
	}
	if added {
		return repo, []string{fmt.Sprintf("registered %s — %s", repo.ID, SummarizeBootstrap(repo.Bootstrap))}, nil
	}
	return repo, nil, nil
}

// carrySource is the directory to carry work in progress out of, or "" when
// there is nothing to carry from.
func carrySource(ctx context.Context, repo core.Repo, cwd string, clean bool) string {
	if clean || cwd == "" {
		return ""
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return ""
	}
	if !gitx.IsRepo(ctx, cwd) {
		return ""
	}
	// It has to be this repo's working tree. A conversation held in some other
	// checkout would produce a patch against commits this repo has never heard
	// of, and the apply would fail after the worktree had already been made.
	if !sameRepo(ctx, repo.Path, cwd) {
		return ""
	}
	if dirty, err := gitx.IsDirty(ctx, cwd); err != nil || !dirty {
		return ""
	}
	return cwd
}

// sameRepo reports whether a working tree belongs to a repo. Both sides are
// resolved to the shared git directory rather than compared as paths, so a
// linked worktree is recognized as part of the repo it was cut from.
func sameRepo(ctx context.Context, repoPath, wt string) bool {
	common := func(dir string) string {
		out, err := gitx.Run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return ""
		}
		resolved, err := filepath.EvalSymlinks(strings.TrimSpace(out))
		if err != nil {
			return strings.TrimSpace(out)
		}
		return resolved
	}
	a, b := common(repoPath), common(wt)
	return a != "" && a == b
}

// attachStartPoint is the commit the new worktree is cut from, along with the
// source it is safe to carry from once that is decided.
//
// With a source to carry from it is that source's HEAD, so the patch taken from
// it applies cleanly and the session starts from the same history the
// conversation has been talking about. Without one it is the fetched base tip,
// which is where every session dma starts itself begins.
//
// The two answers travel together because they have to agree. Cutting from the
// base tip and then replaying a patch built against some other commit is the one
// combination that produces conflicts, so a source whose HEAD cannot be read is
// dropped here rather than left for the apply to fail on.
func attachStartPoint(ctx context.Context, repo core.Repo, base, source string) (start, carryFrom string, warnings []string) {
	if source != "" {
		if head, err := gitx.Head(ctx, source); err == nil && head != "" {
			return head, source, nil
		}
		// An unborn HEAD is the realistic way to get here: a repo whose first
		// commit has not been made has nothing to cut a worktree from, so this
		// falls through to the base tip with the carry given up on.
		warnings = append(warnings,
			fmt.Sprintf("%s has no commit to start from — starting from %s, with its work left where it is", source, base))
	}
	if err := gitx.Fetch(ctx, repo.Path, base); err != nil {
		warnings = append(warnings, fmt.Sprintf("fetch origin %s: %v — starting from the last fetched tip", base, err))
	}
	return gitx.StartPoint(ctx, repo.Path, base), "", warnings
}

// shortID is the leading fragment of a conversation id, for a card title with
// nothing better to use. Both agents mint UUIDs, whose first group is plenty to
// tell two sessions apart by eye.
func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
