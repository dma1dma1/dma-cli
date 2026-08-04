package ops

// End-to-end session creation against a real registered repo, opt-in via
// DMA_LIVE=1 so it never runs in CI or a normal `go test ./...`.
//
// This exists because the unit tests cannot see the failure it guards: bootstrap
// cost scales with the repo, and a monorepo's dependency trees took long enough
// to expire the deadline the tmux launch then ran on -- so every start failed,
// blaming tmux, and left an unregistered worktree behind. Nothing reproduces
// that except a real repo of real size.
//
// It removes only the worktree and tmux session it created itself.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

func TestLiveCreateSession(t *testing.T) {
	if os.Getenv("DMA_LIVE") != "1" {
		t.Skip("set DMA_LIVE=1 to run against the real ~/.dma config")
	}
	// Worth pointing at the largest repo you have registered: the bug this
	// guards only appears once bootstrap is big enough to matter.
	liveRepoID := os.Getenv("DMA_LIVE_REPO_ID")
	if liveRepoID == "" {
		liveRepoID = "devops-copilot"
	}

	// TestMain redirects DMA_HOME so no test can touch the real board. This one
	// has to read the real registered repos, so it points back deliberately and
	// only for its own duration. It reads config and never saves it.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DMA_HOME", filepath.Join(home, ".dma"))

	cfg, err := core.LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	repo, err := cfg.ResolveRepo(liveRepoID)
	if err != nil {
		t.Skipf("repo %q is not registered: %v", liveRepoID, err)
	}
	t.Logf("repo %s: %d trees to clone", repo.ID, len(repo.Bootstrap.Clone))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	start := time.Now()
	res, err := Create(ctx, cfg, CreateRequest{
		// An empty prompt launches the agent with no task, so it idles at its
		// own prompt instead of doing work we would then have to interrupt.
		Title:         "dma live validation of bootstrap clone speed",
		RepoID:        liveRepoID,
		InitialPrompt: "",
		HookURL:       "",
		Cols:          200,
		Rows:          50,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Create failed after %s: %v", elapsed.Round(time.Millisecond), err)
	}
	s := res.Session
	t.Logf("Create succeeded in %s", elapsed.Round(time.Millisecond))
	t.Logf("worktree: %s", s.WorktreePath)
	t.Logf("tmux:     %s", s.TmuxSession)
	for _, w := range res.Warnings {
		t.Logf("warning:  %s", w)
	}

	// Clean up whatever this test created, whatever the assertions below do.
	//
	// Through Teardown rather than a raw worktree remove, because teardown cost is
	// the other half of what only a real repo can show: unlinking the trees
	// bootstrap just cloned is the slowest thing the board does, and the timings
	// logged here are what say whether it is still on the critical path.
	t.Cleanup(func() {
		bg := context.Background()

		start := time.Now()
		if err := Teardown(bg, cfg, s, TeardownOptions{Force: true}); err != nil {
			t.Errorf("cleanup teardown: %v", err)
		}
		t.Logf("Teardown returned in %s", time.Since(start).Round(time.Millisecond))

		if _, err := os.Lstat(s.WorktreePath); !os.IsNotExist(err) {
			t.Errorf("cleanup left the worktree behind: %v", err)
		}
		if tmuxx.HasSession(bg, s.TmuxSession) {
			t.Errorf("cleanup left the tmux session behind")
		}

		// Teardown only moved the files; the board sweeps afterwards, and skipping
		// it here would leak a whole bootstrapped worktree per run.
		start = time.Now()
		if err := SweepTrash(bg, repo.WorktreeRoot); err != nil {
			t.Errorf("cleanup sweep: %v", err)
		}
		t.Logf("SweepTrash finished in %s", time.Since(start).Round(time.Millisecond))

		if entries, err := os.ReadDir(TrashDir(repo.WorktreeRoot)); err == nil && len(entries) != 0 {
			t.Errorf("the sweep left %d worktrees in the trash", len(entries))
		}
	})

	if !tmuxx.HasSession(ctx, s.TmuxSession) {
		t.Errorf("tmux session %q is not alive", s.TmuxSession)
	}

	// The point of bootstrap: every configured tree is fully present, not merely
	// attempted. A truncated clone is the specific failure worth catching here --
	// it reads as a complete install to anything that looks at it, so comparing
	// against the source is the only way to see it.
	for _, rel := range repo.Bootstrap.Clone {
		src := countEntries(filepath.Join(repo.Path, rel))
		dst := countEntries(filepath.Join(s.WorktreePath, rel))
		if dst != src {
			t.Errorf("%s: %d entries cloned, source has %d", rel, dst, src)
			continue
		}
		if rel == "node_modules" {
			t.Logf("%s: %d entries, matches source", rel, dst)
		}
	}

	// A dirty worktree here would mean bootstrap artifacts were not excluded.
	out, err := gitx.Run(ctx, s.WorktreePath, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if out != "" {
		t.Errorf("worktree reads as dirty after bootstrap:\n%s", out)
	}
}

// countEntries reports how many filesystem entries live under path, or -1 if it
// is absent -- which keeps a missing tree distinguishable from an empty one.
func countEntries(path string) int {
	if _, err := os.Lstat(path); err != nil {
		return -1
	}
	n := 0
	_ = filepath.WalkDir(path, func(_ string, _ os.DirEntry, err error) error {
		if err == nil {
			n++
		}
		return nil
	})
	return n
}
