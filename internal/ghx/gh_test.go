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

func TestParsePRNumber(t *testing.T) {
	out := "https://github.com/owner/name/pull/1234\n"
	if got := parsePRNumber(out); got != 1234 {
		t.Fatalf("parsePRNumber = %d, want 1234", got)
	}
	if got := parsePRNumber("no url here"); got != 0 {
		t.Fatalf("parsePRNumber = %d, want 0", got)
	}
}
