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
	"sync"

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
	ErrTimeout
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
	case ErrTimeout:
		// GitHub's own timeout prose runs to three sentences, which is more than
		// a one-line status bar can carry and says nothing the user can act on.
		return "github timed out"
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
	case strings.Contains(s, "http 502"), strings.Contains(s, "http 503"),
		strings.Contains(s, "http 504"), strings.Contains(s, "context deadline exceeded"):
		return &Error{Kind: ErrTimeout, Remote: remote, Msg: firstLine(stderr)}
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
	out, _, err := runOutErr(ctx, dir, args...)
	return out, err
}

// runOutErr also hands back what gh wrote to stderr on success. gh reports some
// outcomes there rather than in its exit code -- a pull request that is already
// in the merge queue is a warning and a zero exit -- so a caller that has to
// tell those apart needs the text.
func runOutErr(ctx context.Context, dir string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), stderr.String(), classify("", stderr.String()+err.Error())
	}
	return stdout.String(), stderr.String(), nil
}

type checkEntry struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`     // CheckRun: QUEUED | IN_PROGRESS | COMPLETED
	Conclusion string `json:"conclusion"` // CheckRun: SUCCESS | FAILURE | SKIPPED | ...
	State      string `json:"state"`      // StatusContext: SUCCESS | PENDING | FAILURE | ERROR
}

type prJSON struct {
	Number         int          `json:"number"`
	URL            string       `json:"url"`
	HeadRefName    string       `json:"headRefName"`
	State          string       `json:"state"`
	IsDraft        bool         `json:"isDraft"`
	Mergeable      string       `json:"mergeable"`
	ReviewDecision string       `json:"reviewDecision"`
	Checks         []checkEntry `json:"statusCheckRollup"`
}

// PR is the normalized pull request state for one branch.
type PR struct {
	Number int
	// URL is the PR's web address as GitHub reports it. It is carried rather
	// than composed from the number, because composing it means assuming
	// github.com and an Enterprise host would get the wrong link.
	URL       string
	Branch    string
	State     core.PRState
	CI        core.CIState
	Review    core.ReviewState
	Mergeable core.Mergeable
}

const prFields = "number,url,headRefName,state,isDraft,mergeable,reviewDecision,statusCheckRollup"

// Poll is one repo's branch-scoped poll result.
type Poll struct {
	// Open holds the open pull request for each branch that has one.
	Open map[string]PR
	// Answered lists every branch whose query came back, whether or not it found
	// a PR. A branch absent from this set failed on this pass; the caller must
	// leave that session alone rather than read the silence as "the PR closed".
	Answered map[string]bool
}

// pollConcurrency bounds the in-flight gh processes for one repo. Each is a
// process plus an API round trip, and a board tracking dozens of branches
// should not fork dozens of them at once.
const pollConcurrency = 6

// PollBranches fetches the open pull request for each of a repo's tracked
// branches, one query per branch, run concurrently.
//
// The obvious implementation -- one `pr list` for the whole repo -- does not
// survive a busy monorepo. statusCheckRollup is computed per PR, so a page of
// 100 PRs carrying thirty checks each makes GitHub do thousands of check
// lookups for one request, and it answers HTTP 504 instead. The page cap is the
// worse half: a repo with more open PRs than the limit silently omits the ones
// past it, and a session whose PR fell off the page looks like it closed.
//
// Asking per branch makes both problems structural non-issues. The cost scales
// with the number of sessions on the board, which is small and under the user's
// control, rather than with the repo's open-PR count, which is neither.
//
// An error is returned only when no branch could be reached at all -- one
// flaky query must not discard the answers that did arrive.
func PollBranches(ctx context.Context, remote string, branches []string) (Poll, error) {
	if remote == "" {
		return Poll{}, &Error{Kind: ErrNoRepo, Remote: remote, Msg: "repo has no remote configured"}
	}
	p := Poll{Open: map[string]PR{}, Answered: map[string]bool{}}
	if len(branches) == 0 {
		return p, nil
	}

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	sem := make(chan struct{}, pollConcurrency)
	for _, branch := range branches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			prs, err := headPRs(ctx, remote, branch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			p.Answered[branch] = true
			for _, pr := range prs {
				// A branch can carry more than one open PR when the same head is
				// proposed against several bases. The newest is the live one.
				if prev, ok := p.Open[branch]; ok && prev.Number > pr.Number {
					continue
				}
				p.Open[branch] = pr
			}
		}()
	}
	wg.Wait()

	if len(p.Answered) == 0 {
		return Poll{}, firstErr
	}
	return p, nil
}

// headPRs lists the open PRs whose head is one branch. The limit is generous
// enough for the multi-base case and small enough to never be the slow query.
func headPRs(ctx context.Context, remote, branch string) ([]PR, error) {
	out, err := run(ctx, "", "pr", "list", "-R", remote, "--state", "open",
		"--head", branch, "--limit", "10", "--json", prFields)
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
		prs = append(prs, decodePR(r))
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
	return decodePR(r), nil
}

func decodePR(r prJSON) PR {
	return PR{
		Number:    r.Number,
		URL:       r.URL,
		Branch:    r.HeadRefName,
		State:     prState(r.State, r.IsDraft),
		CI:        rollupCI(r.Checks),
		Review:    reviewState(r.ReviewDecision),
		Mergeable: mergeable(r.Mergeable),
	}
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

// CreatePR opens a pull request for the branch checked out in wt, returning its
// number and web address.
func CreatePR(ctx context.Context, wt, remote, base, head, title, body string, draft bool) (int, string, error) {
	args := []string{"pr", "create", "-R", remote, "--base", base, "--head", head,
		"--title", title, "--body", body}
	if draft {
		args = append(args, "--draft")
	}
	out, err := run(ctx, wt, args...)
	if err != nil {
		return 0, "", err
	}
	n, url := parseCreated(out)
	return n, url, nil
}

// parseCreated pulls the number and the URL out of the line gh prints on
// success.
func parseCreated(out string) (int, string) {
	for _, f := range strings.Fields(out) {
		i := strings.LastIndex(f, "/pull/")
		if i < 0 {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(f[i+len("/pull/"):], "%d", &n); err == nil {
			return n, f
		}
	}
	return 0, ""
}

// MergeOutcome says what a merge request actually accomplished. A base branch
// behind a merge queue does not merge on demand, and the board must not claim
// otherwise.
type MergeOutcome int

const (
	// MergeCompleted means the pull request is merged and done.
	MergeCompleted MergeOutcome = iota
	// MergeQueued means the queue owns it now: it merges when the queue reaches
	// it, and can just as well be dropped back out.
	MergeQueued
	// MergeAlreadyQueued means it was in the queue before we asked, so nothing
	// happened. gh treats this as success, and so do we.
	MergeAlreadyQueued
)

// QueueState is one pull request's standing with its base branch's merge queue.
type QueueState struct {
	// Enabled reports that the base branch requires a merge queue, which makes
	// merging a matter of joining the queue rather than landing a commit.
	Enabled bool
	// InQueue reports that the pull request is already waiting in that queue.
	InQueue bool
}

// queueQuery asks for the two fields that decide how a merge must be performed.
// It is a GraphQL query because gh's --json field set exposes neither, though
// gh's own merge command reads both.
const queueQuery = `query($owner:String!,$name:String!,$number:Int!){` +
	`repository(owner:$owner,name:$name){` +
	`pullRequest(number:$number){isInMergeQueue isMergeQueueEnabled}}}`

// PRQueueState reports whether a pull request's base branch requires a merge
// queue, and whether the pull request is already in it.
func PRQueueState(ctx context.Context, remote string, number int) (QueueState, error) {
	owner, name, ok := splitRemote(remote)
	if !ok {
		return QueueState{}, &Error{Kind: ErrNoRepo, Remote: remote, Msg: "repo has no remote configured"}
	}
	out, err := run(ctx, "", "api", "graphql", "-f", "query="+queueQuery,
		"-f", "owner="+owner, "-f", "name="+name, "-F", "number="+fmt.Sprint(number))
	if err != nil {
		if e, ok := err.(*Error); ok {
			e.Remote = remote
			return QueueState{}, e
		}
		return QueueState{}, err
	}
	return parseQueueState(out, remote)
}

func parseQueueState(out, remote string) (QueueState, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					IsInMergeQueue      bool `json:"isInMergeQueue"`
					IsMergeQueueEnabled bool `json:"isMergeQueueEnabled"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return QueueState{}, &Error{Kind: ErrOther, Remote: remote, Msg: "parse gh output: " + err.Error()}
	}
	pr := resp.Data.Repository.PullRequest
	return QueueState{Enabled: pr.IsMergeQueueEnabled, InQueue: pr.IsInMergeQueue}, nil
}

// splitRemote takes the owner and name off a remote. A host-qualified remote
// still ends in that pair.
func splitRemote(remote string) (owner, name string, ok bool) {
	parts := strings.Split(strings.Trim(remote, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner, name = parts[len(parts)-2], parts[len(parts)-1]
	return owner, name, owner != "" && name != ""
}

// MergePR merges an open pull request, deleting the remote branch, or adds it
// to the merge queue when its base branch has one.
//
// The queued path is not the same request with a flag changed. The queue picks
// the strategy, so none is passed; and gh refuses --delete-branch outright,
// because deleting the branch would eject the pull request from the queue and
// close it. The outcome tells the caller which of the two happened, since a
// queued pull request has not merged and may yet fail to.
func MergePR(ctx context.Context, remote string, number int, method string) (MergeOutcome, error) {
	// A probe that cannot answer must not block a merge that would otherwise
	// work. Falling through costs nothing: the direct attempt recognizes a queue
	// from gh's refusal and retries the right way.
	qs, _ := PRQueueState(ctx, remote, number)
	if qs.InQueue {
		return MergeAlreadyQueued, nil
	}
	if qs.Enabled {
		return queueMerge(ctx, remote, number)
	}

	out, err := directMerge(ctx, remote, number, method)
	if err != nil && refusedForMergeQueue(err) {
		return queueMerge(ctx, remote, number)
	}
	return out, err
}

func directMerge(ctx context.Context, remote string, number int, method string) (MergeOutcome, error) {
	flag := "--squash"
	switch method {
	case "merge":
		flag = "--merge"
	case "rebase":
		flag = "--rebase"
	}
	_, stderr, err := runOutErr(ctx, "", "pr", "merge", "-R", remote, fmt.Sprint(number), flag, "--delete-branch")
	if err != nil {
		return MergeCompleted, err
	}
	return mergeOutcome(stderr, MergeCompleted), nil
}

// queueMerge adds a pull request to its base branch's merge queue. gh enables
// auto-merge when the required checks are still running and enqueues outright
// once they pass; both mean the same thing here -- the queue merges it later,
// without us.
func queueMerge(ctx context.Context, remote string, number int) (MergeOutcome, error) {
	_, stderr, err := runOutErr(ctx, "", "pr", "merge", "-R", remote, fmt.Sprint(number))
	if err != nil {
		return MergeCompleted, err
	}
	return mergeOutcome(stderr, MergeQueued), nil
}

// ClosePR closes an open pull request, leaving its remote branch alone.
//
// The branch stays on purpose. Closing happens as part of pruning a session,
// which has just deleted the local branch, so the pushed one is the last copy
// of the work -- and a closed pull request can be reopened for as long as its
// head branch exists.
//
// A pull request GitHub already considers closed or merged is not a failure.
// The board's view of PR state is up to a poll interval stale, and the caller
// only ever wants the pull request not-open.
func ClosePR(ctx context.Context, remote string, number int) error {
	if remote == "" {
		return &Error{Kind: ErrNoRepo, Msg: "repo has no remote, so its PR cannot be closed"}
	}
	_, stderr, err := runOutErr(ctx, "", "pr", "close", "-R", remote, fmt.Sprint(number))
	if err != nil && !alreadyNotOpen(stderr, err) {
		return err
	}
	return nil
}

// alreadyNotOpen recognizes gh refusing to close a pull request because it is
// already closed or merged. gh reports the merged case as a failure with an
// empty error of its own, so the reason is only in what it printed.
func alreadyNotOpen(stderr string, err error) bool {
	s := strings.ToLower(stderr)
	if e, ok := err.(*Error); ok {
		s += "\n" + strings.ToLower(e.Msg)
	}
	return strings.Contains(s, "already closed") || strings.Contains(s, "already merged")
}

// mergeOutcome reads what gh says it did, since gh reports a pull request that
// was already queued as a warning on a successful run.
func mergeOutcome(stderr string, dflt MergeOutcome) MergeOutcome {
	if strings.Contains(strings.ToLower(stderr), "already queued") {
		return MergeAlreadyQueued
	}
	return dflt
}

// refusedForMergeQueue recognizes gh declining a direct merge because the base
// branch is behind a queue. It backs up the queue query for hosts that cannot
// answer it -- an older GitHub Enterprise, most likely.
func refusedForMergeQueue(err error) bool {
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	return strings.Contains(strings.ToLower(e.Msg), "merge queue")
}
