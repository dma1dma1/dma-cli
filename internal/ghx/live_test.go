package ghx

import (
	"context"
	"os"
	"testing"
)

// TestListPRsLive exercises the real gh path. It is skipped unless DMA_LIVE=1,
// so the normal suite stays offline and deterministic.
func TestListPRsLive(t *testing.T) {
	if os.Getenv("DMA_LIVE") != "1" {
		t.Skip("set DMA_LIVE=1 to run live gh tests")
	}
	prs, err := ListPRs(context.Background(), "cli/cli")
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(prs) == 0 {
		t.Fatal("expected at least one PR")
	}
	for _, p := range prs {
		if p.Number <= 0 || p.Branch == "" {
			t.Errorf("malformed PR: %+v", p)
		}
		switch p.State {
		case "open", "draft", "merged", "closed":
		default:
			t.Errorf("unexpected state %q on #%d", p.State, p.Number)
		}
	}
	t.Logf("decoded %d PRs; first: %+v", len(prs), prs[0])
}

// A repo that does not exist must be classified, not surfaced as a raw error.
func TestListPRsUnknownRepoLive(t *testing.T) {
	if os.Getenv("DMA_LIVE") != "1" {
		t.Skip("set DMA_LIVE=1 to run live gh tests")
	}
	_, err := ListPRs(context.Background(), "this-org-does-not-exist-xyz/nope")
	if err == nil {
		t.Fatal("expected an error for a nonexistent repo")
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error was not classified: %T %v", err, err)
	}
	t.Logf("classified as kind=%d: %v", e.Kind, e)
}
