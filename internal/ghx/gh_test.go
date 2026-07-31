package ghx

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func TestRollupCI(t *testing.T) {
	cases := []struct {
		name   string
		checks []checkEntry
		want   core.CIState
	}{
		{"no checks", nil, core.CINone},
		{"all green", []checkEntry{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
		}, core.CIPass},
		{"skipped and neutral still pass", []checkEntry{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SKIPPED"},
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "NEUTRAL"},
		}, core.CIPass},
		{"one failure beats many passes", []checkEntry{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
		}, core.CIFail},
		{"in progress is pending", []checkEntry{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
			{Typename: "CheckRun", Status: "IN_PROGRESS"},
		}, core.CIPending},
		{"failure wins over pending", []checkEntry{
			{Typename: "CheckRun", Status: "IN_PROGRESS"},
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
		}, core.CIFail},
		{"status contexts use state", []checkEntry{
			{Typename: "StatusContext", State: "SUCCESS"},
			{Typename: "StatusContext", State: "PENDING"},
		}, core.CIPending},
		{"cancelled counts as failure", []checkEntry{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "CANCELLED"},
		}, core.CIFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rollupCI(c.checks); got != c.want {
				t.Fatalf("rollupCI = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPRState(t *testing.T) {
	cases := []struct {
		state string
		draft bool
		want  core.PRState
	}{
		{"OPEN", false, core.PROpen},
		{"OPEN", true, core.PRDraft},
		{"MERGED", false, core.PRMerged},
		{"CLOSED", false, core.PRClosed},
	}
	for _, c := range cases {
		if got := prState(c.state, c.draft); got != c.want {
			t.Errorf("prState(%q, %v) = %q, want %q", c.state, c.draft, got, c.want)
		}
	}
}

func TestMergeableAndReview(t *testing.T) {
	if mergeable("CONFLICTING") != core.MergeConflicts {
		t.Error("CONFLICTING did not map to conflicts")
	}
	if mergeable("UNKNOWN") != core.MergeUnknown {
		t.Error("UNKNOWN did not map to unknown")
	}
	if reviewState("CHANGES_REQUESTED") != core.ReviewChangesRequested {
		t.Error("CHANGES_REQUESTED did not map through")
	}
	if reviewState("REVIEW_REQUIRED") != core.ReviewNone {
		t.Error("REVIEW_REQUIRED should read as no decision yet")
	}
}

// GitHub answers 502/504 when a query is too expensive to finish. That is not
// the same as being offline or logged out, and its three-sentence apology is
// not what a one-line status bar should carry.
func TestClassifyRecognizesGatewayTimeouts(t *testing.T) {
	for _, stderr := range []string{
		"HTTP 504: We couldn't respond to your request in time. Sorry about that. (https://api.github.com/graphql)",
		"HTTP 502: Something went wrong (https://api.github.com/graphql)",
		"context deadline exceeded",
	} {
		e := classify("owner/repo", stderr)
		if e.Kind != ErrTimeout {
			t.Errorf("classify(%q) kind = %d, want ErrTimeout", stderr, e.Kind)
		}
		if got := e.Error(); got != "github timed out" {
			t.Errorf("message = %q, want a short one", got)
		}
	}
}

// The narrower classifications must still win over the new one.
func TestClassifyStillSeparatesOfflineAndAuth(t *testing.T) {
	cases := map[string]ErrKind{
		"dial tcp: lookup api.github.com: no such host": ErrOffline,
		"HTTP 401: Bad credentials":                     ErrUnauthenticated,
		"HTTP 404: Not Found":                           ErrNoRepo,
		"something else entirely":                       ErrOther,
	}
	for stderr, want := range cases {
		if got := classify("owner/repo", stderr); got.Kind != want {
			t.Errorf("classify(%q) kind = %d, want %d", stderr, got.Kind, want)
		}
	}
}

// The board's view of a PR is up to a poll interval old, so a teardown can ask
// GitHub to close one that has since closed or merged. gh reports the merged
// case as a failure whose error carries no reason of its own -- only what it
// printed says why -- and the caller wanted the PR not-open either way.
func TestAlreadyNotOpenReadsWhatGhPrinted(t *testing.T) {
	cases := map[string]bool{
		"! Pull request acme/api#7 (Fix login) is already closed":                             true,
		"X Pull request acme/api#7 (Fix login) can't be closed because it was already merged": true,
		"HTTP 404: Not Found": false,
		"":                    false,
	}
	for stderr, want := range cases {
		if got := alreadyNotOpen(stderr, &Error{Kind: ErrOther, Msg: firstLine(stderr)}); got != want {
			t.Errorf("alreadyNotOpen(%q) = %v, want %v", stderr, got, want)
		}
	}
	// gh prints nothing to stderr for some failures and puts the reason in the
	// error instead; both places have to be read.
	if !alreadyNotOpen("", &Error{Kind: ErrOther, Msg: "pull request is already closed"}) {
		t.Error("the reason was missed when it arrived in the error rather than stderr")
	}
}
