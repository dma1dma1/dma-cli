// Package ghx wraps the gh CLI for pull request state. gh already handles
// GitHub auth, so there is no reason to pull in a Go GitHub client and a
// separate token story.
package ghx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// Available reports whether gh is installed.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// ErrKind classifies failures so the UI can distinguish "you are logged out"
// from "you are on a plane" without either blocking the board.
type ErrKind int

const (
	ErrOther ErrKind = iota
	ErrOffline
	ErrUnauthenticated
	ErrNoRepo
)

type Error struct {
	Kind   ErrKind
	Remote string
	Msg    string
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrOffline:
		return "offline"
	case ErrUnauthenticated:
		return "gh not authenticated (run: gh auth login)"
	case ErrNoRepo:
		if e.Remote == "" {
			return e.Msg
		}
		return fmt.Sprintf("no such repo: %s", e.Remote)
	}
	return e.Msg
}

func classify(remote, stderr string) *Error {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "no such host"), strings.Contains(s, "dial tcp"),
		strings.Contains(s, "network is unreachable"), strings.Contains(s, "i/o timeout"),
		strings.Contains(s, "connection refused"):
		return &Error{Kind: ErrOffline, Remote: remote, Msg: firstLine(stderr)}
	case strings.Contains(s, "authentication"), strings.Contains(s, "gh auth login"),
		strings.Contains(s, "http 401"), strings.Contains(s, "bad credentials"):
		return &Error{Kind: ErrUnauthenticated, Remote: remote, Msg: firstLine(stderr)}
	case strings.Contains(s, "could not resolve to a repository"), strings.Contains(s, "http 404"):
		return &Error{Kind: ErrNoRepo, Remote: remote, Msg: firstLine(stderr)}
	}
	return &Error{Kind: ErrOther, Remote: remote, Msg: firstLine(stderr)}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "unknown gh error"
	}
	return s
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), classify("", stderr.String()+err.Error())
	}
	return stdout.String(), nil
}

type checkEntry struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`     // CheckRun: QUEUED | IN_PROGRESS | COMPLETED
	Conclusion string `json:"conclusion"` // CheckRun: SUCCESS | FAILURE | SKIPPED | ...
	State      string `json:"state"`      // StatusContext: SUCCESS | PENDING | FAILURE | ERROR
}

type prJSON struct {
	Number         int          `json:"number"`
	HeadRefName    string       `json:"headRefName"`
	State          string       `json:"state"`
	IsDraft        bool         `json:"isDraft"`
	Mergeable      string       `json:"mergeable"`
	ReviewDecision string       `json:"reviewDecision"`
	Checks         []checkEntry `json:"statusCheckRollup"`
}

// PR is the normalized pull request state for one branch.
type PR struct {
	Number    int
	Branch    string
	State     core.PRState
	CI        core.CIState
	Review    core.ReviewState
	Mergeable core.Mergeable
}

const prFields = "number,headRefName,state,isDraft,mergeable,reviewDecision,statusCheckRollup"

// ListPRs fetches open pull requests for one repo. It is called once per
// registered repo that has at least one live session -- never once per session,
// and never for repos with nothing running.
//
// Only open PRs are listed. Asking for --state all makes GitHub compute the
// check rollup across the repo's entire PR history, which times out with a 502
// on large repos; a session whose PR has left the open set is resolved
// individually with GetPR instead, and there are only ever a handful of those.
func ListPRs(ctx context.Context, remote string) ([]PR, error) {
	if remote == "" {
		return nil, &Error{Kind: ErrNoRepo, Remote: remote, Msg: "repo has no remote configured"}
	}
	out, err := run(ctx, "", "pr", "list", "-R", remote,
		"--state", "open", "--limit", "100", "--json", prFields)
	if err != nil {
		if e, ok := err.(*Error); ok {
			e.Remote = remote
			return nil, e
		}
		return nil, err
	}
	var raw []prJSON
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, &Error{Kind: ErrOther, Remote: remote, Msg: "parse gh output: " + err.Error()}
	}
	prs := make([]PR, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PR{
			Number:    r.Number,
			Branch:    r.HeadRefName,
			State:     prState(r.State, r.IsDraft),
			CI:        rollupCI(r.Checks),
			Review:    reviewState(r.ReviewDecision),
			Mergeable: mergeable(r.Mergeable),
		})
	}
	return prs, nil
}

// GetPR fetches one pull request by number.
//
// This resolves the terminal state of a PR that has dropped out of the open
// list -- merged or closed -- without paying for a full-history scan.
func GetPR(ctx context.Context, remote string, number int) (PR, error) {
	if remote == "" {
		return PR{}, &Error{Kind: ErrNoRepo, Msg: "repo has no remote configured"}
	}
	out, err := run(ctx, "", "pr", "view", fmt.Sprint(number), "-R", remote, "--json", prFields)
	if err != nil {
		if e, ok := err.(*Error); ok {
			e.Remote = remote
			return PR{}, e
		}
		return PR{}, err
	}
	var r prJSON
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return PR{}, &Error{Kind: ErrOther, Remote: remote, Msg: "parse gh output: " + err.Error()}
	}
	return PR{
		Number:    r.Number,
		Branch:    r.HeadRefName,
		State:     prState(r.State, r.IsDraft),
		CI:        rollupCI(r.Checks),
		Review:    reviewState(r.ReviewDecision),
		Mergeable: mergeable(r.Mergeable),
	}, nil
}

func prState(state string, draft bool) core.PRState {
	switch strings.ToUpper(state) {
	case "MERGED":
		return core.PRMerged
	case "CLOSED":
		return core.PRClosed
	case "OPEN":
		if draft {
			return core.PRDraft
		}
		return core.PROpen
	}
	return core.PRNone
}

// rollupCI reduces the per-check rollup to a single verdict. Any failure wins,
// then any still-running check, otherwise pass. Skipped and neutral checks are
// not failures.
func rollupCI(checks []checkEntry) core.CIState {
	if len(checks) == 0 {
		return core.CINone
	}
	pending := false
	for _, c := range checks {
		verdict := c.Conclusion
		if verdict == "" {
			verdict = c.State
		}
		switch strings.ToUpper(verdict) {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return core.CIFail
		case "SUCCESS", "SKIPPED", "NEUTRAL":
			// passing
		case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "EXPECTED", "":
			pending = true
		default:
			pending = true
		}
		// A CheckRun that has not completed is pending regardless of conclusion.
		if c.Typename == "CheckRun" && c.Status != "" && strings.ToUpper(c.Status) != "COMPLETED" {
			pending = true
		}
	}
	if pending {
		return core.CIPending
	}
	return core.CIPass
}

func reviewState(d string) core.ReviewState {
	switch strings.ToUpper(d) {
	case "APPROVED":
		return core.ReviewApproved
	case "CHANGES_REQUESTED":
		return core.ReviewChangesRequested
	}
	return core.ReviewNone
}

func mergeable(m string) core.Mergeable {
	switch strings.ToUpper(m) {
	case "MERGEABLE":
		return core.MergeClean
	case "CONFLICTING":
		return core.MergeConflicts
	}
	return core.MergeUnknown
}

// CreatePR opens a pull request for the branch checked out in wt.
func CreatePR(ctx context.Context, wt, remote, base, head, title, body string, draft bool) (int, error) {
	args := []string{"pr", "create", "-R", remote, "--base", base, "--head", head,
		"--title", title, "--body", body}
	if draft {
		args = append(args, "--draft")
	}
	out, err := run(ctx, wt, args...)
	if err != nil {
		return 0, err
	}
	return parsePRNumber(out), nil
}

// parsePRNumber pulls the number out of the PR URL gh prints on success.
func parsePRNumber(out string) int {
	for _, f := range strings.Fields(out) {
		if i := strings.LastIndex(f, "/pull/"); i >= 0 {
			var n int
			if _, err := fmt.Sscanf(f[i+len("/pull/"):], "%d", &n); err == nil {
				return n
			}
		}
	}
	return 0
}

// MergePR merges an open pull request, deleting the remote branch.
func MergePR(ctx context.Context, remote string, number int, method string) error {
	flag := "--squash"
	switch method {
	case "merge":
		flag = "--merge"
	case "rebase":
		flag = "--rebase"
	}
	_, err := run(ctx, "", "pr", "merge", "-R", remote, fmt.Sprint(number), flag, "--delete-branch")
	return err
}
