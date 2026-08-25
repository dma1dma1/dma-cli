package ops

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

func imagePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{G: 0xff, A: 0xff})
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

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

	// A shared cache and a per-worktree env file: two of the three cases the
	// bootstrap step exists for. The third, a cloned tree, is below.
	if err := os.MkdirAll(filepath.Join(repoPath, ".pnpm-store", "v3"), 0o755); err != nil {
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
			Symlink: []string{".pnpm-store"},
			Copy:    []string{".env"},
		},
	}
	if warns, err := Bootstrap(context.Background(), repo, wt); err != nil || len(warns) != 0 {
		t.Fatalf("bootstrap: err=%v warnings=%v", err, warns)
	}

	info, err := os.Lstat(filepath.Join(wt, ".pnpm-store"))
	if err != nil {
		t.Fatalf(".pnpm-store not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error(".pnpm-store should be a symlink so the cache is shared")
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

// A cloned tree has to be the worktree's own. Sharing one is what makes pnpm
// offer to delete and reinstall the tree every other worktree is reading, and
// uv rewrite a venv's editable installs to point at whichever worktree synced
// last -- so the test that matters is that a write on one side stays there.
func TestBootstrapClonesTreePerWorktree(t *testing.T) {
	repoPath := newTestRepo(t, "clone")

	tree := filepath.Join(repoPath, "node_modules", "pkg")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "index.js"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	repo := core.Repo{
		Path:      repoPath,
		Bootstrap: core.Bootstrap{Clone: []string{"node_modules"}},
	}
	if warns, err := Bootstrap(context.Background(), repo, wt); err != nil || len(warns) != 0 {
		t.Fatalf("bootstrap: err=%v warnings=%v", err, warns)
	}

	cloned := filepath.Join(wt, "node_modules")
	info, err := os.Lstat(cloned)
	if err != nil {
		t.Fatalf("node_modules not created: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("node_modules was shared rather than cloned")
	}

	// The content has to arrive, or the worktree gains nothing over reinstalling.
	if data, err := os.ReadFile(filepath.Join(cloned, "pkg", "index.js")); err != nil {
		t.Fatalf("cloned tree is missing its contents: %v", err)
	} else if string(data) != "main\n" {
		t.Errorf("cloned content = %q", data)
	}

	// And it has to be independent in both directions.
	if err := os.WriteFile(filepath.Join(cloned, "pkg", "index.js"), []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(tree, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "main\n" {
		t.Errorf("writing in the worktree changed the main checkout: %q", data)
	}
}

// Canceling a session start must stop its clone rather than falling through to
// the recursive-copy fallback, which could keep saturating the disk after the
// caller has already given up.
func TestBootstrapCanceledCloneDoesNotCopy(t *testing.T) {
	repoPath := newTestRepo(t, "canceled-clone")
	tree := filepath.Join(repoPath, "node_modules", "pkg")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "index.js"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wt := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	warns, err := Bootstrap(ctx, core.Repo{
		Path:      repoPath,
		Bootstrap: core.Bootstrap{Clone: []string{"node_modules"}},
	}, wt)
	// Cancellation is an error rather than a warning: the caller has to roll the
	// worktree back instead of launching an agent into a truncated tree.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none: cancellation is reported as an error", warns)
	}
	if _, err := os.Lstat(filepath.Join(wt, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("canceled clone left a destination behind: %v", err)
	}
}

// A failed start is usually a start whose context has just expired, so the
// cleanup has to run on a context of its own. Sharing the caller's meant the
// rollback git command could not launch at all, and the half-built worktree
// stayed on disk and in git's registry with nothing on the board pointing at it.
func TestAbortRemovesWorktreeWithDeadContext(t *testing.T) {
	repoPath := newTestRepo(t, "abort")
	repo := core.Repo{Path: repoPath}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := gitx.AddDetachedWorktree(context.Background(), repoPath, wt, "main"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cause := errors.New("start tmux session: boom")
	if err := abort(ctx, repo, wt, "", cause); !errors.Is(err, cause) {
		t.Fatalf("abort returned %v, want the original cause unchanged", err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree survived the rollback: %v", err)
	}
	out, err := gitx.Run(context.Background(), repoPath, "worktree", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, wt) {
		t.Errorf("worktree still registered with git:\n%s", out)
	}
}

// A rollback that cannot finish leaves state nobody records, so the error has to
// name the leftover rather than reporting only the original failure.
//
// The repo has no worktree root, so there is nowhere to rename the directory to
// and cleanup falls back to asking git to remove it -- which it will not, since
// the directory is not a worktree of this repo.
func TestAbortReportsWorktreeItCouldNotRemove(t *testing.T) {
	repoPath := newTestRepo(t, "abort-fail")
	repo := core.Repo{Path: repoPath}
	stray := filepath.Join(t.TempDir(), "not-a-worktree")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}

	cause := errors.New("start tmux session: boom")
	err := abort(context.Background(), repo, stray, "", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("abort dropped the original cause: %v", err)
	}
	if !strings.Contains(err.Error(), stray) {
		t.Errorf("error does not name the leftover worktree: %v", err)
	}
}

func TestBootstrapWarnsButDoesNotFailOnMissingPaths(t *testing.T) {
	repoPath := newTestRepo(t, "boot2")
	wt := t.TempDir()
	repo := core.Repo{
		Path: repoPath,
		Bootstrap: core.Bootstrap{
			Symlink: []string{"nope"},
			Clone:   []string{"nope-either"},
			Copy:    []string{"also-nope"},
		},
	}
	warns, err := Bootstrap(context.Background(), repo, wt)
	if err != nil {
		t.Fatalf("missing paths should warn, not fail: %v", err)
	}
	if len(warns) != 3 {
		t.Fatalf("got %d warnings, want 3: %v", len(warns), warns)
	}
}

func TestStageImagesWritesIgnoredPNGFiles(t *testing.T) {
	repoPath := newTestRepo(t, "images")
	first := imagePNG(t, 2, 3)
	second := imagePNG(t, 4, 5)

	paths, err := stageImages(context.Background(), repoPath, []ImageAttachment{
		{PNG: first},
		{PNG: second},
	})
	if err != nil {
		t.Fatalf("stageImages: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want two", paths)
	}
	for i, path := range paths {
		if !strings.HasPrefix(path, filepath.Join(repoPath, ".dma", "attachments-")) {
			t.Errorf("path %q is not in the session attachment directory", path)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read image %d: %v", i+1, readErr)
			continue
		}
		want := [][]byte{first, second}[i]
		if !bytes.Equal(data, want) {
			t.Errorf("image %d data changed while staging", i+1)
		}
	}
	if dirty, err := gitx.IsDirty(context.Background(), repoPath); err != nil || dirty {
		t.Errorf("staged attachments made the worktree dirty: dirty=%v err=%v", dirty, err)
	}
}

func TestStageImagesRejectsInvalidPNG(t *testing.T) {
	repoPath := newTestRepo(t, "bad-image")
	if _, err := stageImages(context.Background(), repoPath,
		[]ImageAttachment{{PNG: []byte("not png")}}); err == nil {
		t.Fatal("invalid image was staged")
	}
	matches, err := filepath.Glob(filepath.Join(repoPath, ".dma", "attachments-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("failed staging left attachment directories behind: %v", matches)
	}
}

// Bootstrap paths come from a config file; they must not be able to reach
// outside the repo.
func TestBootstrapRejectsEscapingPaths(t *testing.T) {
	repoPath := newTestRepo(t, "boot3")
	wt := t.TempDir()
	repo := core.Repo{
		Path: repoPath,
		Bootstrap: core.Bootstrap{
			Symlink: []string{"../../etc/passwd"},
			Clone:   []string{"../../etc/ssh"},
			Copy:    []string{"/etc/hosts"},
		},
	}
	warns, err := Bootstrap(context.Background(), repo, wt)
	if err != nil {
		t.Fatalf("escaping paths should warn, not fail: %v", err)
	}
	if len(warns) != 3 {
		t.Fatalf("escaping paths were not rejected: %v", warns)
	}
	if _, err := os.Lstat(filepath.Join(wt, "passwd")); err == nil {
		t.Error("an escaping symlink was created")
	}
	if _, err := os.Lstat(filepath.Join(wt, "ssh")); err == nil {
		t.Error("an escaping clone was created")
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
			WorktreeRoot: wtRoot,
			Bootstrap:    core.Bootstrap{Copy: []string{".env"}},
		}},
		DefaultRepo:    "testrepo",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Create(ctx, cfg, CreateRequest{Title: "Auth Refresh", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() {
		_ = tmuxx.KillSession(context.Background(), s.TmuxSession)
	})

	// No branch is created: naming the work is the agent's job.
	if s.Branch != "" {
		t.Errorf("branch = %q, want none", s.Branch)
	}
	if b := gitx.CurrentBranch(ctx, s.WorktreePath); b != "" {
		t.Errorf("worktree is on branch %q, want a detached HEAD", b)
	}
	if s.RepoID != "testrepo" {
		t.Errorf("repo_id = %q", s.RepoID)
	}
	if s.BaseBranch != "main" {
		t.Errorf("base = %q", s.BaseBranch)
	}

	// The worktree is named from the title, one flat directory under the root.
	if filepath.Dir(s.WorktreePath) != wtRoot {
		t.Errorf("worktree %q is not directly under %q", s.WorktreePath, wtRoot)
	}
	if got := filepath.Base(s.WorktreePath); got != "auth-refresh" {
		t.Errorf("worktree directory = %q, want auth-refresh", got)
	}
	if _, err := os.Stat(filepath.Join(s.WorktreePath, "README.md")); err != nil {
		t.Errorf("worktree not populated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.WorktreePath, ".env")); err != nil {
		t.Errorf("bootstrap copy did not run: %v", err)
	}

	// The tmux session name is namespaced by repo so two repos running the same
	// task cannot collide.
	if s.TmuxSession != "testrepo-auth-refresh" {
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
}

// Teardown's dirty check covers uncommitted work. Work the agent committed
// before naming a branch is just as easy to lose and far less visible: nothing
// but the worktree's HEAD refers to it.
func TestTeardownRefusesCommitsOnNoBranch(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "unnamed")
	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "unnamed", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: filepath.Join(t.TempDir(), "wt"),
		}},
		DefaultRepo:    "unnamed",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Create(ctx, cfg, CreateRequest{Title: "unnamed work", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}) })

	// A clean, detached worktree with nothing committed tears down freely.
	if err := Teardown(ctx, cfg, s, TeardownOptions{}); err != nil {
		t.Fatalf("teardown of an untouched worktree: %v", err)
	}

	res, err = Create(ctx, cfg, CreateRequest{Title: "unnamed work", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create again: %v", err)
	}
	s = res.Session
	if err := os.WriteFile(filepath.Join(s.WorktreePath, "work.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gitx.CommitAll(ctx, s.WorktreePath, "work"); err != nil {
		t.Fatal(err)
	}

	err = Teardown(ctx, cfg, s, TeardownOptions{})
	if _, ok := err.(*UnnamedCommitsError); !ok {
		t.Fatalf("teardown returned %v, want UnnamedCommitsError", err)
	}
	if _, statErr := os.Stat(s.WorktreePath); statErr != nil {
		t.Error("refused teardown still removed the worktree")
	}
	if err := Teardown(ctx, cfg, s, TeardownOptions{Force: true}); err != nil {
		t.Fatalf("forced teardown: %v", err)
	}
}

// A repo driven mainly through this tool never has anyone standing in it
// running `git pull`, so its local main is whatever the last direct visit left
// behind. Sessions have to start from the remote tip regardless.
func TestCreateStartsFromTheFetchedRemoteTip(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	repoPath := newTestRepo(t, "stale")
	bare := filepath.Join(t.TempDir(), "origin.git")
	if _, err := gitx.Run(ctx, filepath.Dir(bare), "init", "--bare", "-q", "-b", "main", bare); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repoPath, "remote", "add", "origin", bare); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repoPath, "push", "-q", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	// Someone else lands a commit. This repo's main, and its origin/main, are
	// now both behind.
	other := filepath.Join(t.TempDir(), "other")
	if _, err := gitx.Run(ctx, filepath.Dir(other), "clone", "-q", "-b", "main", bare, other); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "user.email", "other@example.com"},
		{"config", "user.name", "other"},
	} {
		if _, err := gitx.Run(ctx, other, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(other, "landed.txt"), []byte("upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gitx.CommitAll(ctx, other, "land upstream work"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, other, "push", "-q", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "stale", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: filepath.Join(t.TempDir(), "wt"),
		}},
		DefaultRepo:    "stale",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}
	res, err := Create(ctx, cfg, CreateRequest{Title: "fresh start", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}) })

	if _, err := os.Stat(filepath.Join(s.WorktreePath, "landed.txt")); err != nil {
		t.Errorf("worktree is missing the upstream commit: %v", err)
	}
	// And the stale local ref must not make that upstream commit look like the
	// session's own work.
	if added, _, _ := gitx.DiffStat(ctx, s.WorktreePath, s.BaseBranch); added != 0 {
		t.Errorf("diff stat = +%d on an untouched worktree, want 0", added)
	}
}

// Two sessions can carry the same title, and with no branch to tell them apart
// the worktree directory is the only thing that can.
func TestUniqueWorktreeDirSuffixesOnCollision(t *testing.T) {
	root := t.TempDir()
	first := uniqueWorktreeDir(root, "fix-login")
	if got := filepath.Base(first); got != "fix-login" {
		t.Fatalf("first worktree = %q, want fix-login", got)
	}
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	second := uniqueWorktreeDir(root, "fix-login")
	if got := filepath.Base(second); got != "fix-login-2" {
		t.Errorf("second worktree = %q, want fix-login-2", got)
	}
}

// Two repos running the same task is the multi-repo case the design warns
// about; the worktrees and tmux sessions must stay distinct.
func TestCreateAcrossReposSharingATaskName(t *testing.T) {
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
		res, err := Create(ctx, cfg, CreateRequest{
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
	if filepath.Base(api.WorktreePath) != filepath.Base(web.WorktreePath) {
		t.Fatalf("test premise broken: worktree names differ (%q vs %q)",
			api.WorktreePath, web.WorktreePath)
	}
	if api.WorktreePath == web.WorktreePath {
		t.Error("both repos got the same worktree path")
	}
	if api.TmuxSession == web.TmuxSession {
		t.Errorf("both repos got the same tmux session name %q", api.TmuxSession)
	}

	// Once each agent names its branch -- and two agents given the same task
	// will land on the same name -- lookups keyed on the pair must still
	// resolve to the right session.
	api.Branch, web.Branch = "auth", "auth"
	if got := core.FindByKey(sessions, core.Key{RepoID: "web", Branch: "auth"}); got != web {
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
	if err := tmuxx.NewSession(ctx, name, os.TempDir(), 120, 30); err != nil {
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
	// And keep anything that reaches the state directory away from the real one:
	// a test that writes there replaces the developer's own board.
	home, err := os.MkdirTemp("", "dma-ops-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("DMA_HOME", home)
	if _, err := exec.LookPath("git"); err != nil {
		os.RemoveAll(home)
		os.Exit(0)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// Shipping is the agent's own work now -- s only asks for it -- so what matters
// is that the board reads that work back: the branch the agent names, the
// commits it makes, and the files it leaves behind. The commit and push here
// stand in for the agent's, against a local bare remote.
func TestBoardTracksAgentGitWork(t *testing.T) {
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
	res, err := Create(ctx, cfg, CreateRequest{Title: "add thing", Profile: "noop"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}) })

	// Nothing committed yet, so there is nothing a PR could be opened for.
	if gitx.HasCommits(ctx, s.WorktreePath, s.BaseBranch) {
		t.Error("a fresh worktree reported commits ahead of base")
	}

	// Stand in for the agent naming its own branch, and check the board picks
	// the name up -- nothing else tells it what to push.
	if _, err := gitx.Run(ctx, s.WorktreePath, "switch", "-q", "-c", "agent-picked-this"); err != nil {
		t.Fatalf("create branch in worktree: %v", err)
	}
	obs := Observe(ctx, []*core.Session{s})
	if len(obs) != 1 || obs[0].Branch != "agent-picked-this" {
		t.Fatalf("Observe reported branch %+v, want agent-picked-this", obs)
	}
	s.Branch = obs[0].Branch

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

	// A second commit with no new work must not create an empty one.
	if err := gitx.CommitAll(ctx, s.WorktreePath, "again"); err != nil {
		t.Fatalf("CommitAll on a clean tree: %v", err)
	}
	n, _ := gitx.Run(ctx, s.WorktreePath, "rev-list", "--count", s.BaseBranch+"..HEAD")
	if n != "1" {
		t.Errorf("commit count = %s, want 1 — an empty commit was created", n)
	}
}

// A detached tmux session defaults to 80x24 regardless of the real terminal, so
// an agent launched without an explicit size renders its UI into a narrow strip
// no matter how wide the board's panel is.
func TestCreateSizesTheAgentTerminal(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "sized")
	cfg := &core.Config{
		Repos:          []core.Repo{{ID: "sized", Path: repoPath, BaseBranch: "main", WorktreeRoot: filepath.Join(t.TempDir(), "wt")}},
		DefaultRepo:    "sized",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Create(ctx, cfg, CreateRequest{
		Title: "wide", Profile: "noop", Cols: 164, Rows: 13,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = Teardown(context.Background(), cfg, s, TeardownOptions{Force: true}) })

	if got := paneSize(t, s.TmuxSession); got != "164x13" {
		t.Fatalf("agent terminal is %s, want 164x13 (tmux would default to 80x24)", got)
	}

	// The panel changes size when the window resizes or the preview expands, and
	// the agent has to follow it.
	if err := tmuxx.ResizeWindow(ctx, s.TmuxSession, 100, 30); err != nil {
		t.Fatalf("ResizeWindow: %v", err)
	}
	if got := paneSize(t, s.TmuxSession); got != "100x30" {
		t.Fatalf("after resize the terminal is %s, want 100x30", got)
	}
}

func paneSize(t *testing.T, session string) string {
	t.Helper()
	// display-message takes a target-pane, where the "=" exact-match prefix used
	// elsewhere does not parse.
	out, err := exec.Command("tmux", "display-message", "-p", "-t", session,
		"#{window_width}x#{window_height}").Output()
	if err != nil {
		t.Fatalf("display-message: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// A session is started from a description of the work, which is a paragraph
// more often than it is a name. The directory has to be named before any
// summary of that paragraph exists, so it is named from the opening of it --
// cut at a word, since "the-login-test-flakes-on-ci-about-1-in" is what a raw
// forty-character truncation looks like.
func TestCreateNamesTheWorktreeFromTheOpeningOfTheTask(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "opening")
	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "testrepo", Path: repoPath, BaseBranch: "main", WorktreeRoot: wtRoot,
		}},
		DefaultRepo:    "testrepo",
		AgentProfiles:  []core.AgentProfile{{Name: "noop", Command: "true"}},
		DefaultProfile: "noop",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task := "the login test flakes on CI about 1 in 5 runs, look at why and fix it"
	res, err := Create(ctx, cfg, CreateRequest{Title: task})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), s.TmuxSession) })

	// The record keeps the whole task: the board renames the card once it has a
	// summary, and it can only do that from the text the session started with.
	if s.Title != task {
		t.Errorf("title = %q, want the task as typed", s.Title)
	}
	dir := filepath.Base(s.WorktreePath)
	if dir != "the-login-test-flakes-on-ci-about-1" {
		t.Errorf("worktree directory = %q", dir)
	}
	if filepath.Dir(s.WorktreePath) != wtRoot {
		t.Errorf("worktree %q is not directly under %q", s.WorktreePath, wtRoot)
	}
}

// The agent has to receive the prompt as one argument. Typing the prompt into a
// running agent's UI instead loses characters -- codex reads its composer
// through a vim keymap, turning the first letters into cursor motions -- so the
// prompt travels on the launch line, and this test stands in for the agent to
// check what actually arrives there.
func TestCreateHandsTheAgentThePromptAsOneArgument(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "prompt")

	// A stand-in agent that records its argv, one argument per line.
	out := filepath.Join(t.TempDir(), "argv")
	agent := filepath.Join(t.TempDir(), "agent.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + out + "\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "testrepo", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		}},
		DefaultRepo:    "testrepo",
		AgentProfiles:  []core.AgentProfile{{Name: "recorder", Command: agent + " --flag"}},
		DefaultProfile: "recorder",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A multiline prompt carrying a leading dash and everything a shell would
	// otherwise act on. The line breaks must stay inside the one prompt argument.
	prompt := "- don't stop; run $(whoami)\n- then list *.go"
	res, err := Create(ctx, cfg, CreateRequest{Title: "Shell Safety", InitialPrompt: prompt})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	var got string
	for range 100 {
		if b, err := os.ReadFile(out); err == nil {
			got = string(b)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	want := "--flag\n" + prompt + "\n"
	if got != want {
		t.Errorf("agent argv:\n%q\nwant:\n%q", got, want)
	}
}

func TestCreatePassesStagedImagesToTheAgent(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "image-prompt")
	out := filepath.Join(t.TempDir(), "argv")
	agent := filepath.Join(t.TempDir(), "agent.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + out + "\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "testrepo", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		}},
		DefaultRepo: "testrepo",
		AgentProfiles: []core.AgentProfile{{
			Name: "recorder", Command: agent + " --flag", ImageArgument: "--image {path}",
		}},
		DefaultProfile: "recorder",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Create(ctx, cfg, CreateRequest{
		Title:         "See Screenshot",
		InitialPrompt: "inspect it",
		InitialImages: []ImageAttachment{{PNG: imagePNG(t, 7, 9)}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	var args []string
	for range 100 {
		if b, readErr := os.ReadFile(out); readErr == nil {
			args = strings.Split(strings.TrimSpace(string(b)), "\n")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(args) != 4 {
		t.Fatalf("agent args = %q, want flag, image flag, path, prompt", args)
	}
	if args[0] != "--flag" || args[1] != "--image" || args[3] != "inspect it" {
		t.Errorf("agent args = %q", args)
	}
	if !strings.HasPrefix(args[2], filepath.Join(res.Session.WorktreePath, ".dma", "attachments-")) {
		t.Errorf("image path %q is outside the session worktree", args[2])
	}
	if _, err := os.Stat(args[2]); err != nil {
		t.Errorf("staged image is unavailable to the agent: %v", err)
	}
	if dirty, err := gitx.IsDirty(ctx, res.Session.WorktreePath); err != nil || dirty {
		t.Errorf("created image session starts dirty: dirty=%v err=%v", dirty, err)
	}
}
