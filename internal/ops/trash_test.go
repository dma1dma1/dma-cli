package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// trashEntries lists what is waiting in a repo's trash.
func trashEntries(t *testing.T, cfg *core.Config) []string {
	t.Helper()
	entries, err := os.ReadDir(TrashDir(cfg.Repos[0].WorktreeRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read trash: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// The whole point of the trash: teardown returns without waiting for the files.
// A worktree carrying cloned dependency trees is hundreds of thousands of
// entries, and unlinking them is half a minute the board spent frozen.
func TestTeardownMovesTheWorktreeAsideRatherThanDeletingIt(t *testing.T) {
	cfg, s := teardownFixture(t, "aside")
	stubClosePR(t, nil)
	s.PRState = core.PRMerged

	if err := os.WriteFile(filepath.Join(s.WorktreePath, "deps.txt"), []byte("bulky\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(context.Background(), s.WorktreePath, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(context.Background(), s.WorktreePath, "commit", "-q", "-m", "deps"); err != nil {
		t.Fatal(err)
	}

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Error("the worktree is still at its old path")
	}

	names := trashEntries(t, cfg)
	if len(names) != 1 {
		t.Fatalf("trash holds %v, want the one discarded worktree", names)
	}
	if !strings.HasPrefix(names[0], filepath.Base(s.WorktreePath)+"-") {
		t.Errorf("trashed as %q, want the worktree name with a unique suffix", names[0])
	}
	// The files are the evidence the rename happened instead of a delete.
	moved := filepath.Join(TrashDir(cfg.Repos[0].WorktreeRoot), names[0], "deps.txt")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the worktree's contents did not come along: %v", err)
	}
}

// Renaming leaves git holding a registry entry for a path that no longer exists.
// Teardown has to prune it, or git still believes the branch is checked out
// somewhere and refuses to delete it.
func TestTeardownStillClearsTheRegistryAndTheBranch(t *testing.T) {
	cfg, s := teardownFixture(t, "registry")
	stubClosePR(t, nil)
	s.PRState = core.PRMerged
	ctx := context.Background()

	if err := Teardown(ctx, cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}

	list, err := gitx.Run(ctx, cfg.Repos[0].Path, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	if strings.Contains(list, s.WorktreePath) {
		t.Errorf("git still registers the discarded worktree:\n%s", list)
	}
	if _, err := gitx.Run(ctx, cfg.Repos[0].Path, "rev-parse", "--verify", "-q", "refs/heads/feature"); err == nil {
		t.Error("the branch survived teardown")
	}
}

func TestTeardownCanResumeAfterResourcesAreAlreadyGone(t *testing.T) {
	cfg, s := teardownFixture(t, "resume")
	stubClosePR(t, nil)
	s.PRState = core.PRMerged
	ctx := context.Background()

	if err := Teardown(ctx, cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("first teardown: %v", err)
	}
	if err := Teardown(ctx, cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("resumed teardown: %v", err)
	}
}

// Every prune adds to the trash, so the sweep is what keeps it from being a leak.
func TestSweepTrashUnlinksWhatTeardownLeft(t *testing.T) {
	cfg, s := teardownFixture(t, "sweep")
	stubClosePR(t, nil)
	s.PRState = core.PRMerged

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if names := trashEntries(t, cfg); len(names) != 1 {
		t.Fatalf("trash holds %v before the sweep, want one worktree", names)
	}

	if err := SweepTrash(context.Background(), cfg.Repos[0].WorktreeRoot); err != nil {
		t.Fatalf("SweepTrash: %v", err)
	}
	if names := trashEntries(t, cfg); len(names) != 0 {
		t.Errorf("the sweep left %v behind", names)
	}
	// The directory itself stays: a teardown renaming into the trash while a
	// sweep runs would otherwise land in a directory being removed underneath it.
	if _, err := os.Stat(TrashDir(cfg.Repos[0].WorktreeRoot)); err != nil {
		t.Errorf("the sweep removed the trash directory itself: %v", err)
	}
}

// A sweep with nothing to do is the common case -- it runs on every start and
// after every prune, and most of those find an empty or absent trash.
func TestSweepTrashWithNothingToDoIsQuiet(t *testing.T) {
	root := t.TempDir()
	if err := SweepTrash(context.Background(), filepath.Join(root, "never-used")); err != nil {
		t.Errorf("sweeping an absent trash failed: %v", err)
	}
	if err := SweepTrash(context.Background(), ""); err != nil {
		t.Errorf("sweeping an unconfigured root failed: %v", err)
	}
}

// The rename needs a trash directory on the worktree's own filesystem. Where
// there is none to be had, teardown still has to work -- slowly is fine, not at
// all is not.
func TestTeardownFallsBackToDeletingInPlace(t *testing.T) {
	cfg, s := teardownFixture(t, "fallback")
	stubClosePR(t, nil)
	s.PRState = core.PRMerged
	// An unconfigured root is the reachable stand-in for a rename that cannot
	// happen; a cross-filesystem worktree is the real one.
	cfg.Repos[0].WorktreeRoot = ""

	if err := Teardown(context.Background(), cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Error("the worktree is still on disk")
	}
}

// Two sessions started from one title get the same directory name, so the trash
// cannot key on it: the second rename would land inside the first tree instead of
// beside it, and the sweep would report a name that no longer means anything.
func TestTrashNamesDoNotCollide(t *testing.T) {
	repoPath := newTestRepo(t, "collide")
	repo := core.Repo{ID: "collide", Path: repoPath, WorktreeRoot: filepath.Join(t.TempDir(), "wt")}

	for i := 0; i < 2; i++ {
		wt := filepath.Join(repo.WorktreeRoot, "same-title")
		if err := os.MkdirAll(wt, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := trashWorktree(repo, wt); err != nil {
			t.Fatalf("trashWorktree: %v", err)
		}
	}

	entries, err := os.ReadDir(TrashDir(repo.WorktreeRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("trash holds %v, want both discarded worktrees", names)
	}
}
