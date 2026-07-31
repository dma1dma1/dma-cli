package ghx

import (
	"context"
	"os"
	"testing"
)

// TestPollBranchesLive exercises the real gh path. It is skipped unless
// DMA_LIVE=1, so the normal suite stays offline and deterministic.
func TestPollBranchesLive(t *testing.T) {
	if os.Getenv("DMA_LIVE") != "1" {
		t.Skip("set DMA_LIVE=1 to run live gh tests")
	}
	// trunk has no open PR; the other name is nonsense. Both must come back
	// answered-and-empty rather than as errors -- that is the distinction the
	// board relies on to tell "no PR yet" from "the query failed".
	branches := []string{"trunk", "dma-live-test-branch-that-does-not-exist"}
	poll, err := PollBranches(context.Background(), "cli/cli", branches)
	if err != nil {
		t.Fatalf("PollBranches: %v", err)
	}
	for _, b := range branches {
		if !poll.Answered[b] {
			t.Errorf("branch %q was not answered for", b)
		}
		if pr, ok := poll.Open[b]; ok {
			t.Errorf("branch %q unexpectedly has an open PR: %+v", b, pr)
		}
	}
	t.Logf("answered %d branches, %d open PRs", len(poll.Answered), len(poll.Open))
}

// A branch that does have an open PR must decode into a well-formed record.
func TestPollBranchesDecodesAnOpenPRLive(t *testing.T) {
	if os.Getenv("DMA_LIVE") != "1" {
		t.Skip("set DMA_LIVE=1 to run live gh tests")
	}
	head := os.Getenv("DMA_LIVE_BRANCH")
	remote := os.Getenv("DMA_LIVE_REPO")
	if head == "" || remote == "" {
		t.Skip("set DMA_LIVE_REPO and DMA_LIVE_BRANCH to a branch with an open PR")
	}
	poll, err := PollBranches(context.Background(), remote, []string{head})
	if err != nil {
		t.Fatalf("PollBranches: %v", err)
	}
	pr, ok := poll.Open[head]
	if !ok {
		t.Fatalf("no open PR found for %s on %s", head, remote)
	}
	if pr.Number <= 0 || pr.Branch != head {
		t.Errorf("malformed PR: %+v", pr)
	}
	switch pr.State {
	case "open", "draft", "merged", "closed":
	default:
		t.Errorf("unexpected state %q on #%d", pr.State, pr.Number)
	}
	t.Logf("decoded %+v", pr)
}

// A repo that does not exist must be classified, not surfaced as a raw error.
// Every branch query fails, so the failure has to reach the caller.
func TestPollBranchesUnknownRepoLive(t *testing.T) {
	if os.Getenv("DMA_LIVE") != "1" {
		t.Skip("set DMA_LIVE=1 to run live gh tests")
	}
	_, err := PollBranches(context.Background(), "this-org-does-not-exist-xyz/nope", []string{"main"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent repo")
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error was not classified: %T %v", err, err)
	}
	t.Logf("classified as kind=%d: %v", e.Kind, e)
}
