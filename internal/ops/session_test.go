package ops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// newTestRepo builds a real git repo with one commit, since the whole point of
// these paths is their interaction with git.
func newTestRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		if _, err := gitx.Run(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, dir, "commit", "-q", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBootstrapSymlinksAndCopies(t *testing.T) {
	repoPath := newTestRepo(t, "boot")

	// A shared dependency tree and a per-worktree env file: the two cases the
	// bootstrap step exists for.
	if err := os.MkdirAll(filepath.Join(repoPath, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, ".env"), []byte("SECRET=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	repo := core.Repo{
		Path: repoPath,
		Bootstrap: core.Bootstrap{
			Symlink: []string{"node_modules"},
			Copy:    []string{".env"},
		},
	}
	if warns := Bootstrap(context.Background(), repo, wt); len(warns) != 0 {
		t.Fatalf("unexpected bootstrap warnings: %v", warns)
	}

	info, err := os.Lstat(filepath.Join(wt, "node_modules"))
	if err != nil {
		t.Fatalf("node_modules not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("node_modules should be a symlink so the dependency tree is shared")
	}

	data, err := os.ReadFile(filepath.Join(wt, ".env"))
	if err != nil {
		t.Fatalf(".env not copied: %v", err)
	}
	if string(data) != "SECRET=1\n" {
		t.Errorf(".env content = %q", data)
	}
	// A copy, not a link: editing one worktree's env must not touch the others.
	envInfo, _ := os.Lstat(filepath.Join(wt, ".env"))
	if envInfo.Mode()&os.ModeSymlink != 0 {
		t.Error(".env should be copied, not symlinked")
	}
}

func TestBootstrapWarnsButDoesNotFailOnMissingPaths(t *testing.T) {
	repoPath := newTestRepo(t, "boot2")
	wt := t.TempDir()
	repo := core.Repo{
		Path:      repoPath,
		Bootstrap: core.Bootstrap{Symlink: []string{"nope"}, Copy: []string{"also-nope"}},
	}
	warns := Bootstrap(context.Background(), repo, wt)
	if len(warns) != 2 {
		t.Fatalf("got %d warnings, want 2", len(warns))
	}
}

// Bootstrap paths come from a config file; they must not be able to reach
// outside the repo.
func TestBootstrapRejectsEscapingPaths(t *testing.T) {
	repoPath := newTestRepo(t, "boot3")
	wt := t.TempDir()
	repo := core.Repo{
		Path:      repoPath,
		Bootstrap: core.Bootstrap{Symlink: []string{"../../etc/passwd"}, Copy: []string{"/etc/hosts"}},
	}
	warns := Bootstrap(context.Background(), repo, wt)
	if len(warns) != 2 {
		t.Fatalf("escaping paths were not rejected: %v", warns)
	}
	if _, err := os.Lstat(filepath.Join(wt, "passwd")); err == nil {
		t.Error("an escaping symlink was created")
	}
}

func TestCreateAndTeardown(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "create")
	if err := os.WriteFile(filepath.Join(repoPath, ".env"), []byte("X=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "testrepo", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: wtRoot, BranchPrefix: "feat/",
			Bootstrap: core.Bootstrap{Copy: []string{".env"}},
		}},
		DefaultRepo:    "testrepo",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Create(ctx, cfg, nil, CreateRequest{Title: "Auth Refresh", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() {
		_ = tmuxx.KillSession(context.Background(), s.TmuxSession)
	})

	if s.Branch != "feat/auth-refresh" {
		t.Errorf("branch = %q, want feat/auth-refresh (prefix + slug)", s.Branch)
	}
	if s.RepoID != "testrepo" {
		t.Errorf("repo_id = %q", s.RepoID)
	}
	if s.BaseBranch != "main" {
		t.Errorf("base = %q", s.BaseBranch)
	}

	// The branch has a slash; the worktree must be one flat directory.
	if filepath.Dir(s.WorktreePath) != wtRoot {
		t.Errorf("worktree %q is not directly under %q", s.WorktreePath, wtRoot)
	}
	if _, err := os.Stat(filepath.Join(s.WorktreePath, "README.md")); err != nil {
		t.Errorf("worktree not populated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.WorktreePath, ".env")); err != nil {
		t.Errorf("bootstrap copy did not run: %v", err)
	}

	// The tmux session name is namespaced by repo so two repos sharing a branch
	// name cannot collide.
	if s.TmuxSession != "testrepo-feat-auth-refresh" {
		t.Errorf("tmux session = %q", s.TmuxSession)
	}
	if !tmuxx.HasSession(ctx, s.TmuxSession) {
		t.Fatal("tmux session was not started")
	}

	// A dirty worktree must not be torn down silently.
	if err := os.WriteFile(filepath.Join(s.WorktreePath, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Teardown(ctx, cfg, s, TeardownOptions{})
	if _, ok := err.(*DirtyError); !ok {
		t.Fatalf("teardown of a dirty worktree returned %v, want DirtyError", err)
	}
	if _, statErr := os.Stat(s.WorktreePath); statErr != nil {
		t.Fatal("refused teardown still removed the worktree")
	}

	// With explicit confirmation it goes through.
	if err := Teardown(ctx, cfg, s, TeardownOptions{Force: true}); err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
	if _, err := os.Stat(s.WorktreePath); !os.IsNotExist(err) {
		t.Error("worktree still present after teardown")
	}
	if tmuxx.HasSession(ctx, s.TmuxSession) {
		t.Error("tmux session still running after teardown")
	}
	if gitx.BranchExists(ctx, repoPath, s.Branch) {
		t.Error("branch still present after teardown")
	}
}

// Two repos with the same branch name is the multi-repo case the design warns
// about; the worktrees and tmux sessions must stay distinct.
func TestCreateAcrossReposSharingABranchName(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	apiPath := newTestRepo(t, "api")
	webPath := newTestRepo(t, "web")
	root := t.TempDir()

	cfg := &core.Config{
		Repos: []core.Repo{
			{ID: "api", Path: apiPath, BaseBranch: "main", WorktreeRoot: filepath.Join(root, "api")},
			{ID: "web", Path: webPath, BaseBranch: "main", WorktreeRoot: filepath.Join(root, "web")},
		},
		DefaultRepo:    "api",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var sessions []*core.Session
	for _, repoID := range []string{"api", "web"} {
		res, err := Create(ctx, cfg, sessions, CreateRequest{
			Title: "auth", RepoID: repoID, Profile: "noop",
		})
		if err != nil {
			t.Fatalf("create in %s: %v", repoID, err)
		}
		sessions = append(sessions, res.Session)
		s := res.Session
		t.Cleanup(func() {
			_ = Teardown(context.Background(), cfg, s, TeardownOptions{Force: true})
		})
	}

	api, web := sessions[0], sessions[1]
	if api.Branch != web.Branch {
		t.Fatalf("test premise broken: branches differ (%q vs %q)", api.Branch, web.Branch)
	}
	if api.WorktreePath == web.WorktreePath {
		t.Error("both repos got the same worktree path")
	}
	if api.TmuxSession == web.TmuxSession {
		t.Errorf("both repos got the same tmux session name %q", api.TmuxSession)
	}

	// Lookups keyed on the pair must resolve to the right session.
	if got := core.FindByKey(sessions, core.Key{RepoID: "web", Branch: web.Branch}); got != web {
		t.Error("(repo_id, branch) lookup returned the wrong session")
	}
}

func TestObserveReportsLiveness(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	name := "dma-test-observe"
	ctx := context.Background()
	_ = tmuxx.KillSession(ctx, name)
	if err := tmuxx.NewSession(ctx, name, os.TempDir()); err != nil {
		t.Skipf("cannot start tmux session: %v", err)
	}
	defer tmuxx.KillSession(ctx, name)

	sessions := []*core.Session{
		{ID: "live", TmuxSession: name, WorktreePath: os.TempDir()},
		{ID: "dead", TmuxSession: "dma-test-does-not-exist", WorktreePath: os.TempDir()},
	}
	obs := Observe(ctx, sessions)
	byID := map[string]bool{}
	for _, o := range obs {
		byID[o.ID] = o.Alive
	}
	if !byID["live"] {
		t.Error("running session reported as dead")
	}
	if byID["dead"] {
		t.Error("nonexistent session reported as alive")
	}
}

func TestMain(m *testing.M) {
	// Keep git from picking up the developer's own hooks or templates.
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if _, err := exec.LookPath("git"); err != nil {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestShipGitHalf covers the git side of the ship action -- commit, detect
// commits, push -- against a local bare remote. Opening the PR itself is gh's
// job and is covered separately.
func TestShipGitHalf(t *testing.T) {
	repoPath := newTestRepo(t, "ship")
	ctx := context.Background()

	bare := filepath.Join(t.TempDir(), "origin.git")
	if _, err := gitx.Run(ctx, filepath.Dir(bare), "init", "--bare", "-q", bare); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repoPath, "remote", "add", "origin", bare); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repoPath, "push", "-q", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	// The remote slug is derived from the origin URL rather than configured.
	if _, err := gitx.RemoteSlug(ctx, repoPath); err != nil {
		t.Errorf("RemoteSlug on a path remote: %v", err)
	}

	wtRoot := filepath.Join(t.TempDir(), "wt")
	cfg := &core.Config{
		Repos:          []core.Repo{{ID: "ship", Path: repoPath, BaseBranch: "main", WorktreeRoot: wtRoot}},
		DefaultRepo:    "ship",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}
	res, err := Create(ctx, cfg, nil, CreateRequest{Title: "add thing", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}) })

	// Nothing committed yet, so there is nothing to open a PR for.
	if gitx.HasCommits(ctx, s.WorktreePath, s.BaseBranch) {
		t.Error("a fresh branch reported commits ahead of base")
	}

	if err := os.WriteFile(filepath.Join(s.WorktreePath, "thing.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The new file is untracked, so it must still show up in the diff stat.
	added, _, _ := gitx.DiffStat(ctx, s.WorktreePath, s.BaseBranch)
	if added == 0 {
		t.Error("diff stat ignored an untracked file")
	}

	if err := gitx.CommitAll(ctx, s.WorktreePath, "add thing"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if !gitx.HasCommits(ctx, s.WorktreePath, s.BaseBranch) {
		t.Fatal("commit was not detected ahead of base")
	}
	if dirty, _ := gitx.IsDirty(ctx, s.WorktreePath); dirty {
		t.Error("worktree still dirty after committing everything")
	}

	if err := gitx.Push(ctx, s.WorktreePath, s.Branch); err != nil {
		t.Fatalf("Push: %v", err)
	}
	out, err := gitx.Run(ctx, bare, "branch", "--list", s.Branch)
	if err != nil || out == "" {
		t.Fatalf("branch %s not present on the remote (out=%q err=%v)", s.Branch, out, err)
	}

	// A second ship with no new work must not create an empty commit.
	if err := gitx.CommitAll(ctx, s.WorktreePath, "again"); err != nil {
		t.Fatalf("CommitAll on a clean tree: %v", err)
	}
	n, _ := gitx.Run(ctx, s.WorktreePath, "rev-list", "--count", s.BaseBranch+"..HEAD")
	if n != "1" {
		t.Errorf("commit count = %s, want 1 — an empty commit was created", n)
	}
}
