package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Lifecycle determines which board column a card sits in.
//
// The first two columns are owned by the agent: a session is active while its
// agent is working and idle otherwise. The last two are owned by durable git
// facts, and agent activity can never move a card out of them -- whether a
// process happens to be mid-tool-call says nothing about whether its PR is
// merged.
type Lifecycle string

const (
	LifecycleIdle   Lifecycle = "idle"
	LifecycleActive Lifecycle = "active"
	LifecyclePROpen Lifecycle = "pr_open"
	LifecycleMerged Lifecycle = "merged"
)

// Columns is the fixed, ordered set of board columns. There are exactly four;
// additional axes become filters, not columns.
var Columns = []Lifecycle{LifecycleIdle, LifecycleActive, LifecyclePROpen, LifecycleMerged}

func (l Lifecycle) Title() string {
	switch l {
	case LifecycleIdle:
		return "idle"
	case LifecycleActive:
		return "active"
	case LifecyclePROpen:
		return "pr open"
	case LifecycleMerged:
		return "merged"
	}
	return string(l)
}

// Subtitle explains a column, since "idle" alone does not say whether it means
// broken, finished or waiting on you.
func (l Lifecycle) Subtitle() string {
	switch l {
	case LifecycleIdle:
		return "waiting on you"
	case LifecycleActive:
		return "agent working"
	case LifecyclePROpen:
		return "pushed"
	case LifecycleMerged:
		return "done"
	}
	return ""
}

// PRDriven reports whether a column is owned by pull request state rather than
// by the agent.
func (l Lifecycle) PRDriven() bool {
	return l == LifecyclePROpen || l == LifecycleMerged
}

// ColumnIndex returns the position of l in Columns, or 0 if unknown.
func (l Lifecycle) ColumnIndex() int {
	for i, c := range Columns {
		if c == l {
			return i
		}
	}
	return 0
}

// AgentState determines the card's badge, and which of the first two columns a
// session sits in.
type AgentState string

const (
	AgentIdle     AgentState = "idle"
	AgentWorking  AgentState = "working"
	AgentNeedsYou AgentState = "needs_you"
	AgentDone     AgentState = "done"
)

func (a AgentState) Badge() string {
	switch a {
	case AgentWorking:
		return "● working"
	case AgentNeedsYou:
		return "◆ needs you"
	case AgentDone:
		return "✓ done"
	default:
		return "○ idle"
	}
}

// SortRank orders agent states within a column: needs_you first.
func (a AgentState) SortRank() int {
	switch a {
	case AgentNeedsYou:
		return 0
	case AgentDone:
		return 1
	case AgentWorking:
		return 2
	default:
		return 3
	}
}

type PRState string

const (
	PRNone   PRState = "none"
	PRDraft  PRState = "draft"
	PROpen   PRState = "open"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed"
)

type CIState string

const (
	CINone    CIState = "none"
	CIPending CIState = "pending"
	CIPass    CIState = "pass"
	CIFail    CIState = "fail"
)

type ReviewState string

const (
	ReviewNone             ReviewState = "none"
	ReviewApproved         ReviewState = "approved"
	ReviewChangesRequested ReviewState = "changes_requested"
)

type Mergeable string

const (
	MergeUnknown   Mergeable = "unknown"
	MergeClean     Mergeable = "clean"
	MergeConflicts Mergeable = "conflicts"
)

// Key uniquely identifies a worktree across all registered repos. Branch names
// collide between repos, so every PR match, lookup and dedup check keys on the
// pair -- never on branch alone.
type Key struct {
	RepoID string
	Branch string
}

func (k Key) String() string { return k.RepoID + "\x00" + k.Branch }

// Session is one agent working in one worktree of one repo.
type Session struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	RepoID       string    `json:"repo_id"`
	Group        string    `json:"group"`
	WorktreePath string    `json:"worktree_path"`
	Branch       string    `json:"branch"`
	BaseBranch   string    `json:"base_branch"`
	TmuxSession  string    `json:"tmux_session"`
	AgentProfile string    `json:"agent_profile"`
	CreatedAt    time.Time `json:"created_at"`

	Lifecycle        Lifecycle  `json:"lifecycle"`
	AgentState       AgentState `json:"agent_state"`
	AgentStateSince  time.Time  `json:"agent_state_since"`
	AgentStateDetail string     `json:"agent_state_detail"`

	PRNumber int `json:"pr_number"`
	// PRURL is the PR's web address, kept so opening or copying the link is a
	// local lookup rather than a round trip to GitHub every time.
	PRURL       string      `json:"pr_url,omitempty"`
	PRState     PRState     `json:"pr_state"`
	PRCI        CIState     `json:"pr_ci"`
	PRReview    ReviewState `json:"pr_review"`
	PRMergeable Mergeable   `json:"pr_mergeable"`
	// PRQueued reports that the PR is waiting in its base branch's merge queue.
	// It is not a PR state: GitHub still calls a queued PR open, and the queue
	// can drop it back out again.
	PRQueued bool `json:"pr_queued,omitempty"`
	// PRAutoMerge reports that GitHub will merge the PR once its outstanding
	// requirements pass. Like a queued PR, it remains open in the meantime.
	PRAutoMerge bool `json:"pr_auto_merge,omitempty"`
	// PRMergeableNotified records that the user has already been told this PR is
	// ready to merge. It is persisted rather than kept in memory so that
	// relaunching the board does not re-announce every PR that was already ready
	// when it closed.
	PRMergeableNotified bool      `json:"pr_mergeable_notified,omitempty"`
	PRSyncedAt          time.Time `json:"pr_synced_at"`

	// Runtime-only fields, recomputed at startup and on poll.
	// Starting marks the temporary card shown while a new worktree is being
	// fetched and bootstrapped. It never reaches disk: a board restart cannot
	// resume a Create operation that died with the previous process.
	Starting       bool   `json:"-"`
	StartingDetail string `json:"-"`
	// Pruning makes teardown durable across a board quit. The operation removes
	// external resources before its completion message can remove the card, so
	// the next launch retries any teardown whose message was never applied.
	Pruning       bool `json:"pruning,omitempty"`
	TmuxAlive     bool `json:"-"`
	DiffAdded     int  `json:"-"`
	DiffRemoved   int  `json:"-"`
	WorktreeDirty bool `json:"-"`

	// ClaudeSessionID is learned from hook payloads and used as a secondary
	// correlation key once seen.
	ClaudeSessionID string `json:"claude_session_id,omitempty"`

	// AgentSessionID is the agent's own id for the conversation this session is
	// holding, set when the session was attached to an existing one rather than
	// started fresh.
	//
	// It is what a restart resumes from. An attached session's transcript lives
	// where the conversation began, not in the worktree dma made for it, so the
	// by-directory resume every other session restarts with finds nothing here;
	// see AgentProfile.ResumeIDCommand. Empty on a session dma started itself,
	// which restarts by directory as before.
	AgentSessionID string `json:"agent_session_id,omitempty"`

	// ForkedFrom is the conversation this session's own conversation was copied
	// from, set when attaching had to copy rather than reopen -- see
	// AgentProfile.ForkCommand.
	//
	// Nothing resumes from it. It is kept so attaching the same conversation
	// twice can still be refused: the session is holding a copy under a different
	// id, so the id it was made from is the only thing left that says the two
	// cards would be the same piece of work.
	ForkedFrom string `json:"forked_from,omitempty"`
}

// Attached reports whether this session is continuing a conversation that began
// outside dma.
func (s *Session) Attached() bool { return s.AgentSessionID != "" }

func (s *Session) Key() Key { return Key{RepoID: s.RepoID, Branch: s.Branch} }

// TimeInState is how long the session has held its current agent state.
func (s *Session) TimeInState() time.Duration {
	if s.AgentStateSince.IsZero() {
		return 0
	}
	return time.Since(s.AgentStateSince)
}

// SetAgentState updates the badge and its clock, and moves the card between the
// idle and active columns to match.
//
// It will not move a session out of a PR-driven column: those record git facts,
// and a card that fell back to "idle" because its agent exited would be lying
// about a PR that is still open.
func (s *Session) SetAgentState(st AgentState, detail string) (changed bool) {
	if s.AgentState == st && s.AgentStateDetail == detail {
		return false
	}
	if s.AgentState != st {
		s.AgentStateSince = time.Now()
	}
	s.AgentState = st
	s.AgentStateDetail = detail
	s.syncColumn()
	return true
}

// syncColumn keeps the agent-owned columns in step with the badge. Pull request
// facts, rather than a stale or manually moved column, decide when git owns it.
func (s *Session) syncColumn() {
	if s.HasPR() {
		return
	}
	if s.AgentState == AgentWorking {
		s.Lifecycle = LifecycleActive
		return
	}
	s.Lifecycle = LifecycleIdle
}

// HasPR reports whether a pull request is known for this session.
func (s *Session) HasPR() bool {
	return s.PRNumber > 0 && s.PRState != PRNone
}

// HasOpenPR reports whether the session's pull request is still open on GitHub,
// which is what makes it something teardown has to close. A draft counts: it is
// open as far as GitHub and everyone reviewing the repo is concerned.
func (s *Session) HasOpenPR() bool {
	return s.HasPR() && (s.PRState == PROpen || s.PRState == PRDraft)
}

// PRReadyToMerge reports whether an open pull request has nothing left standing
// between it and a merge: no conflicts, no failing or unfinished checks, and no
// reviewer asking for changes.
//
// Only PROpen qualifies. A draft is not offered for merging however green it is,
// and merged and closed are not pending anything.
func (s *Session) PRReadyToMerge() bool {
	if s.PRNumber <= 0 || s.PRState != PROpen {
		return false
	}
	// A queued or auto-merging PR is on its way in without the user, so it is not
	// something to be handed back to them.
	if s.PRQueued || s.PRAutoMerge {
		return false
	}
	// MergeUnknown is GitHub still computing the merge commit rather than an
	// answer, and guessing clean from it would fire on PRs that turn out to
	// conflict.
	if s.PRMergeable != MergeClean {
		return false
	}
	switch s.PRCI {
	case CIPass, CINone:
		// A repo with no checks at all has nothing to wait for.
	default:
		return false
	}
	return s.PRReview != ReviewChangesRequested
}

// ClaimMergeableNotice reports whether this is the moment to tell the user a PR
// is ready to merge, and records having done so.
//
// The notification belongs to the transition, not the state: a PR sits ready for
// as long as it takes the user to merge it, and every poll in that window would
// otherwise raise another one. The claim is released when the PR stops being
// ready, so a PR that goes red and comes back green is announced again -- the
// second pass is news too.
func (s *Session) ClaimMergeableNotice() bool {
	if !s.PRReadyToMerge() {
		s.PRMergeableNotified = false
		return false
	}
	if s.PRMergeableNotified {
		return false
	}
	s.PRMergeableNotified = true
	return true
}

// NewID returns a short random identifier.
func NewID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("s%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slug converts a free-text title into a branch-safe fragment.
func Slug(s string) string {
	s = slugStrip.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "session-" + NewID()[:4]
	}
	return s
}

// FormatDuration renders a duration compactly for the "needs you 8m" badge
// suffix. The elapsed time is the actionable half of the signal.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
