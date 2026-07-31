package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// stubClosePR swaps the GitHub call out for the length of one test and records
// what teardown asked it to close.
func stubClosePR(t *testing.T, err error) *[][2]any {
	t.Helper()
	var calls [][2]any
	prev := closePR
	closePR = func(_ context.Context, remote string, number int) error {
		calls = append(calls, [2]any{remote, number})
		return err
	}
	t.Cleanup(func() { closePR = prev })
	return &calls
}

// teardownFixture builds a repo with one worktree on a named branch, which is
// the state a session with a pull request is actually in.
func teardownFixture(t *testing.T, name string) (*core.Config, *core.Session) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	cfg := &core.Config{Repos: []core.Repo{{
		ID: name, Path: repoPath, BaseBranch: "main", Remote: "acme/" + name,
		WorktreeRoot: filepath.Join(t.TempDir(), "wt"),
	}}}

	ctx := context.Background()
	wt := filepath.Join(cfg.Repos[0].WorktreeRoot, "work")
	if _, err := gitx.Run(ctx, repoPath, "worktree", "add", "-q", "-b", "feature", wt, "main"); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	return cfg, &core.Session{
		ID: "s1", Title: "shipping", RepoID: name, WorktreePath: wt,
		Branch: "feature", BaseBranch: "main", TmuxSession: "absent-" + name,
		Lifecycle: core.LifecyclePROpen, PRNumber: 7, PRState: core.PROpen,
	}
}

// A pruned session's worktree and branch are about to stop existing, so its
// pull request cannot be left sitting open in someone's review queue.
func TestTeardownClosesAnOpenPR(t *testing.T) {
	cfg, s := teardownFixture(t, "closes")
	calls := stubClosePR(t, nil)

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0] != [2]any{"acme/closes", 7} {
		t.Fatalf("PR close calls = %v, want one for acme/closes#7", *calls)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Error("worktree still present after teardown")
	}
}

// Everything teardown does after the close is local and irreversible, and the
// notice would arrive as the card carrying the PR number left the board. So a
// close that cannot happen stops the whole thing instead.
func TestTeardownStopsWhenThePRWillNotClose(t *testing.T) {
	cfg, s := teardownFixture(t, "offline")
	stubClosePR(t, errors.New("offline"))

	err := Teardown(context.Background(), cfg, s, TeardownOptions{})
	var prErr *PRCloseError
	if !errors.As(err, &prErr) {
		t.Fatalf("Teardown returned %v, want PRCloseError", err)
	}
	if prErr.Number != 7 {
		t.Errorf("PRCloseError.Number = %d, want 7", prErr.Number)
	}
	if _, statErr := os.Stat(s.WorktreePath); statErr != nil {
		t.Error("a refused teardown still removed the worktree")
	}
	if _, gitErr := gitx.Run(context.Background(), cfg.Repos[0].Path,
		"rev-parse", "--verify", "-q", "refs/heads/feature"); gitErr != nil {
		t.Error("a refused teardown still deleted the branch")
	}
}

// The user has been asked and answered: prune it, leave the pull request.
func TestTeardownKeepPRSkipsTheClose(t *testing.T) {
	cfg, s := teardownFixture(t, "keep")
	calls := stubClosePR(t, errors.New("must not be called"))

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{KeepPR: true}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("KeepPR still closed the PR: %v", *calls)
	}
}

// The merged column is what X prunes, and every card in it has a pull request
// that is already gone. Asking GitHub to close them would fail on every one.
func TestTeardownLeavesAClosedPRAlone(t *testing.T) {
	cfg, s := teardownFixture(t, "merged")
	s.PRState = core.PRMerged
	calls := stubClosePR(t, errors.New("must not be called"))

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("a merged PR was closed again: %v", *calls)
	}
}

// The dirty and unnamed-commit refusals recover by confirming and retrying with
// Force. That retry has to still find a pull request to close, so the local
// checks come first.
func TestTeardownChecksForLostWorkBeforeClosingThePR(t *testing.T) {
	cfg, s := teardownFixture(t, "dirty")
	calls := stubClosePR(t, nil)

	if err := os.WriteFile(filepath.Join(s.WorktreePath, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Teardown(context.Background(), cfg, s, TeardownOptions{})
	if _, ok := err.(*DirtyError); !ok {
		t.Fatalf("Teardown returned %v, want DirtyError", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("the PR was closed for a teardown that then refused: %v", *calls)
	}

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}); err != nil {
		t.Fatalf("forced Teardown: %v", err)
	}
	if len(*calls) != 1 {
		t.Errorf("the forced retry did not close the PR: %v", *calls)
	}
}
