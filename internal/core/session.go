package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Lifecycle determines which board column a card sits in. It changes only on
// user action or on durable git/PR events -- never on agent activity.
type Lifecycle string

const (
	LifecycleActive Lifecycle = "active"
	LifecycleReview Lifecycle = "review"
	LifecyclePROpen Lifecycle = "pr_open"
	LifecycleMerged Lifecycle = "merged"
)

// Columns is the fixed, ordered set of board columns. There are exactly four;
// additional axes become swimlanes, not columns.
var Columns = []Lifecycle{LifecycleActive, LifecycleReview, LifecyclePROpen, LifecycleMerged}

func (l Lifecycle) Title() string {
	switch l {
	case LifecycleActive:
		return "active"
	case LifecycleReview:
		return "review"
	case LifecyclePROpen:
		return "pr open"
	case LifecycleMerged:
		return "merged"
	}
	return string(l)
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

// AgentState determines the card's badge. It changes every few seconds and must
// never influence Lifecycle.
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

	PRNumber    int         `json:"pr_number"`
	PRState     PRState     `json:"pr_state"`
	PRCI        CIState     `json:"pr_ci"`
	PRReview    ReviewState `json:"pr_review"`
	PRMergeable Mergeable   `json:"pr_mergeable"`
	PRSyncedAt  time.Time   `json:"pr_synced_at"`

	// Runtime-only fields, recomputed at startup and on poll.
	TmuxAlive     bool `json:"-"`
	DiffAdded     int  `json:"-"`
	DiffRemoved   int  `json:"-"`
	WorktreeDirty bool `json:"-"`

	// ClaudeSessionID is learned from hook payloads and used as a secondary
	// correlation key once seen.
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
}

func (s *Session) Key() Key { return Key{RepoID: s.RepoID, Branch: s.Branch} }

// TimeInState is how long the session has held its current agent state.
func (s *Session) TimeInState() time.Duration {
	if s.AgentStateSince.IsZero() {
		return 0
	}
	return time.Since(s.AgentStateSince)
}

// SetAgentState updates the badge and its clock. It deliberately cannot touch
// Lifecycle: agent activity must never move a card between columns, or the
// selection would jump out from under the cursor mid-keystroke.
func (s *Session) SetAgentState(st AgentState, detail string) (changed bool) {
	if s.AgentState == st && s.AgentStateDetail == detail {
		return false
	}
	if s.AgentState != st {
		s.AgentStateSince = time.Now()
	}
	s.AgentState = st
	s.AgentStateDetail = detail
	return true
}

// HasPR reports whether a pull request is known for this session.
func (s *Session) HasPR() bool {
	return s.PRNumber > 0 && s.PRState != PRNone
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
