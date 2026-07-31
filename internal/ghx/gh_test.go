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

func TestParseCreated(t *testing.T) {
	out := "https://github.com/owner/name/pull/1234\n"
	n, url := parseCreated(out)
	if n != 1234 {
		t.Errorf("number = %d, want 1234", n)
	}
	// The whole address is kept, host included: an Enterprise PR is not on
	// github.com, and a link composed from the number would go to the wrong one.
	if url != "https://github.com/owner/name/pull/1234" {
		t.Errorf("url = %q, want the address gh printed", url)
	}
	if n, url := parseCreated("no url here"); n != 0 || url != "" {
		t.Fatalf("parseCreated = %d, %q, want nothing", n, url)
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
