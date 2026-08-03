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

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestDetectFindsIgnoredDepsAndEnv(t *testing.T) {
	repo := newTestRepo(t, "detect")
	ctx := context.Background()

	mustMkdir(t, filepath.Join(repo, "node_modules", "pkg"))
	mustMkdir(t, filepath.Join(repo, ".venv", "lib"))
	mustWrite(t, filepath.Join(repo, ".env"), "S=1\n")
	mustWrite(t, filepath.Join(repo, ".gitignore"), "node_modules\n.venv\n.env\n")

	b := DetectBootstrap(ctx, repo)

	// Both trees record the path they were built for, so each worktree needs its
	// own -- sharing one is what makes pnpm offer to reinstall from scratch and
	// uv rewrite the venv out from under the other worktrees.
	if !has(b.Clone, "node_modules") || !has(b.Clone, ".venv") {
		t.Errorf("dependency trees not detected for cloning: %v", b.Clone)
	}
	if has(b.Symlink, "node_modules") || has(b.Symlink, ".venv") {
		t.Errorf("path-keyed trees must never be shared: %v", b.Symlink)
	}
	if !has(b.Copy, ".env") {
		t.Errorf(".env not detected for copying: %v", b.Copy)
	}
	// A dependency tree must never be byte-copied, and a per-session env file
	// must never be shared or cloned.
	if has(b.Copy, "node_modules") || has(b.Symlink, ".env") || has(b.Clone, ".env") {
		t.Errorf("bootstrap classification inverted: %+v", b)
	}
}

// A cache whose contents do not name their own location is still worth pointing
// every worktree at, and must not be needlessly duplicated.
func TestDetectSharesPathIndependentCaches(t *testing.T) {
	repo := newTestRepo(t, "caches")
	ctx := context.Background()

	mustMkdir(t, filepath.Join(repo, ".pnpm-store", "v3"))
	mustMkdir(t, filepath.Join(repo, "vendor", "x"))
	mustWrite(t, filepath.Join(repo, ".gitignore"), ".pnpm-store\nvendor\n")

	b := DetectBootstrap(ctx, repo)

	for _, want := range []string{".pnpm-store", "vendor"} {
		if !has(b.Symlink, want) {
			t.Errorf("%s not shared; got symlink=%v clone=%v", want, b.Symlink, b.Clone)
		}
		if has(b.Clone, want) {
			t.Errorf("%s was cloned rather than shared", want)
		}
	}
}

// A repo's toolchain cache is left alone entirely. Handing a worktree a
// populated one tells the repo's activation hook there is nothing to install,
// and the step it skips is the one that repoints a cloned venv at this
// worktree's sources. See core.IsPathKeyed.
func TestDetectIgnoresToolchainCaches(t *testing.T) {
	repo := newTestRepo(t, "flox")
	ctx := context.Background()

	mustMkdir(t, filepath.Join(repo, ".flox", "cache", "gobin"))
	mustWrite(t, filepath.Join(repo, ".gitignore"), ".flox/cache\n")

	b := DetectBootstrap(ctx, repo)

	cache := filepath.Join(".flox", "cache")
	if has(b.Clone, cache) || has(b.Symlink, cache) || has(b.Copy, cache) {
		t.Errorf(".flox/cache should not be bootstrapped at all: %+v", b)
	}
}

// Anything git tracks arrives with the worktree already; linking over it would
// shadow the checkout with the main copy.
func TestDetectSkipsTrackedPaths(t *testing.T) {
	repo := newTestRepo(t, "tracked")
	ctx := context.Background()

	// "vendor" committed to the repo, as Go projects sometimes do.
	mustMkdir(t, filepath.Join(repo, "vendor", "x"))
	mustWrite(t, filepath.Join(repo, "vendor", "x", "f.go"), "package x\n")
	if _, err := gitx.Run(ctx, repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repo, "commit", "-qm", "vendor"); err != nil {
		t.Fatal(err)
	}

	b := DetectBootstrap(ctx, repo)
	if has(b.Symlink, "vendor") {
		t.Errorf("tracked vendor/ was selected for symlinking: %v", b.Symlink)
	}
}

func TestDetectFindsWorkspacePackages(t *testing.T) {
	repo := newTestRepo(t, "mono")
	ctx := context.Background()

	mustMkdir(t, filepath.Join(repo, "node_modules"))
	mustMkdir(t, filepath.Join(repo, "packages", "llm", "node_modules"))
	mustMkdir(t, filepath.Join(repo, "packages", "db", "node_modules"))
	mustMkdir(t, filepath.Join(repo, "apps", "web", "node_modules"))
	mustWrite(t, filepath.Join(repo, ".gitignore"), "node_modules\n")

	b := DetectBootstrap(ctx, repo)

	for _, want := range []string{
		"node_modules",
		filepath.Join("packages", "llm", "node_modules"),
		filepath.Join("packages", "db", "node_modules"),
		filepath.Join("apps", "web", "node_modules"),
	} {
		if !has(b.Clone, want) {
			t.Errorf("%s not detected; got %v", want, b.Clone)
		}
	}
}

func TestDetectEmptyRepoProducesNothing(t *testing.T) {
	repo := newTestRepo(t, "bare")
	b := DetectBootstrap(context.Background(), repo)
	if len(b.Symlink) != 0 || len(b.Clone) != 0 || len(b.Copy) != 0 {
		t.Fatalf("expected nothing to bootstrap, got %+v", b)
	}
	if got := SummarizeBootstrap(b); got != "nothing to share" {
		t.Errorf("summary = %q", got)
	}
}

func TestAdoptRegistersOnceAndIsIdempotent(t *testing.T) {
	repo := newTestRepo(t, "adopt")
	ctx := context.Background()
	mustMkdir(t, filepath.Join(repo, "node_modules"))
	mustWrite(t, filepath.Join(repo, ".gitignore"), "node_modules\n")

	t.Setenv("DMA_HOME", t.TempDir())
	cfg := core.DefaultConfig()

	r1, added, err := Adopt(ctx, cfg, repo)
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !added {
		t.Fatal("first adoption did not report the repo as added")
	}
	if r1.ID != "adopt" {
		t.Errorf("id = %q, want adopt (from the directory name)", r1.ID)
	}
	if r1.BaseBranch != "main" {
		t.Errorf("base branch = %q", r1.BaseBranch)
	}
	if !has(r1.Bootstrap.Clone, "node_modules") {
		t.Errorf("bootstrap not detected during adoption: %+v", r1.Bootstrap)
	}
	if cfg.DefaultRepo != "adopt" {
		t.Errorf("default repo = %q", cfg.DefaultRepo)
	}

	// Launching from the same repo again must not register a duplicate.
	r2, added, err := Adopt(ctx, cfg, repo)
	if err != nil {
		t.Fatalf("second Adopt: %v", err)
	}
	if added {
		t.Error("second adoption registered a duplicate")
	}
	if r2.ID != r1.ID || len(cfg.Repos) != 1 {
		t.Errorf("config now holds %d repos", len(cfg.Repos))
	}

	// A subdirectory resolves to the same repo, not a new one.
	sub := filepath.Join(repo, "sub", "dir")
	mustMkdir(t, sub)
	r3, added, err := Adopt(ctx, cfg, sub)
	if err != nil || added || r3.ID != r1.ID {
		t.Errorf("adopting from a subdirectory gave (%v, added=%v, err=%v)", r3.ID, added, err)
	}
}

// Launching from inside a session's own worktree must resolve to the repo that
// owns it, not register the worktree as a separate repo.
func TestAdoptFromWorktreeResolvesToParentRepo(t *testing.T) {
	repo := newTestRepo(t, "parent")
	ctx := context.Background()
	t.Setenv("DMA_HOME", t.TempDir())

	cfg := core.DefaultConfig()
	parent, _, err := Adopt(ctx, cfg, repo)
	if err != nil {
		t.Fatalf("Adopt parent: %v", err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if err := gitx.AddDetachedWorktree(ctx, repo, wt, "main"); err != nil {
		t.Fatalf("AddDetachedWorktree: %v", err)
	}

	got, added, err := Adopt(ctx, cfg, wt)
	if err != nil {
		t.Fatalf("Adopt from worktree: %v", err)
	}
	if added {
		t.Error("a worktree was registered as a repo of its own")
	}
	if got.ID != parent.ID {
		t.Errorf("worktree resolved to %q, want %q", got.ID, parent.ID)
	}
	if len(cfg.Repos) != 1 {
		t.Errorf("config holds %d repos, want 1", len(cfg.Repos))
	}
}

func TestAdoptGivesDistinctIDsToSameNamedRepos(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DMA_HOME", t.TempDir())
	cfg := core.DefaultConfig()

	a := newTestRepo(t, "svc")
	b := newTestRepo(t, "svc") // different temp dir, same basename

	r1, _, err := Adopt(ctx, cfg, a)
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := Adopt(ctx, cfg, b)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID == r2.ID {
		t.Fatalf("both repos got id %q", r1.ID)
	}
	// Distinct ids matter because they namespace worktree roots and tmux names.
	if r1.WorktreeRoot == r2.WorktreeRoot {
		t.Errorf("both repos share worktree root %s", r1.WorktreeRoot)
	}
}

func TestAdoptRejectsNonRepo(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	if _, _, err := Adopt(context.Background(), core.DefaultConfig(), t.TempDir()); err == nil {
		t.Fatal("expected an error outside a git repository")
	}
}

func TestSummarizeBootstrapCaps(t *testing.T) {
	b := core.Bootstrap{
		Symlink: []string{"a", "b", "c", "d", "e"},
		Copy:    []string{".env"},
	}
	got := SummarizeBootstrap(b)
	if !strings.Contains(got, "+2 more") || !strings.Contains(got, ".env") {
		t.Fatalf("summary = %q", got)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A repo registered before it had an origin -- a local project pushed to
// GitHub later -- kept an empty remote forever, and the PR poll skips repos
// with an empty remote. Every session in that repo stayed in the agent-owned
// columns with no PR state and nothing saying why.
func TestRefreshRemotesPicksUpAnOriginAddedLater(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	ctx := context.Background()
	repo := newTestRepo(t, "late-origin")

	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "late-origin", Path: repo}}

	// Nothing to find yet: the repo has no origin, and PR polling stays off.
	if learned := RefreshRemotes(ctx, cfg); len(learned) != 0 {
		t.Fatalf("learned %v from a repo with no origin", learned)
	}

	if _, err := gitx.Run(ctx, repo, "remote", "add", "origin",
		"git@github.com:owner/name.git"); err != nil {
		t.Fatal(err)
	}
	if learned := RefreshRemotes(ctx, cfg); len(learned) != 1 || learned[0] != "late-origin" {
		t.Fatalf("learned = %v, want [late-origin]", learned)
	}
	if got := cfg.Repos[0].Remote; got != "owner/name" {
		t.Errorf("remote = %q, want owner/name", got)
	}

	// And it is written down, so the next launch does not repeat the lookup.
	saved, err := core.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := saved.Repo("late-origin"); !ok || r.Remote != "owner/name" {
		t.Errorf("saved remote = %q, want owner/name", r.Remote)
	}
}

// A remote someone set by hand -- a fork, or a second remote deliberately
// chosen -- must not be overwritten by whatever origin happens to say.
func TestRefreshRemotesLeavesAConfiguredRemoteAlone(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	ctx := context.Background()
	repo := newTestRepo(t, "configured")
	if _, err := gitx.Run(ctx, repo, "remote", "add", "origin",
		"git@github.com:owner/name.git"); err != nil {
		t.Fatal(err)
	}

	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "configured", Path: repo, Remote: "someone-else/fork"}}

	if learned := RefreshRemotes(ctx, cfg); len(learned) != 0 {
		t.Errorf("touched a repo that already had a remote: %v", learned)
	}
	if got := cfg.Repos[0].Remote; got != "someone-else/fork" {
		t.Errorf("remote = %q, want the configured one", got)
	}
}
