package ops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// depDirs are build and dependency trees worth bootstrapping into a worktree.
// They are large and expensive to rebuild, so a fresh worktree is given them
// rather than left to reinstall.
//
// How each one arrives is decided at classification time, not here:
// core.IsPathKeyed separates the trees that record the path they were built for
// -- which get a private clone -- from the caches that do not, which are shared.
var depDirs = []string{
	"node_modules",  // npm, yarn, pnpm
	".venv", "venv", // python
	".tox",             // python test envs
	"target",           // rust, maven
	".gradle", "build", // gradle
	"vendor",         // go, php, ruby
	".bundle",        // ruby
	"_build", "deps", // elixir
	"Pods",        // cocoapods
	".yarn/cache", // yarn berry
	".pnpm-store", // pnpm
	".terraform",  // terraform providers
}

// perWorktreeFiles are small config files each worktree needs its own copy of,
// because a session may want to point at a different database or port.
var perWorktreeFiles = []string{
	".env", ".env.local", ".env.development.local", ".env.test.local",
	".envrc.local", "local.settings.json",
}

// workspaceGlobs cover monorepo layouts, where dependency trees live under each
// package as well as at the root. Globs are used rather than a recursive walk so
// detection stays fast on a large repo.
var workspaceGlobs = []string{
	"packages/*", "apps/*", "services/*", "libs/*", "crates/*",
	"modules/*", "projects/*", "examples/*",
}

// maxDetected bounds the generated lists, so a pathological repo cannot produce
// a config file thousands of entries long.
const maxDetected = 250

// DetectBootstrap works out which paths a fresh worktree needs, so that
// registering a repo takes no flags.
//
// A path qualifies only if it exists and git is ignoring it. Anything git
// tracks already arrives with the worktree, and linking over it would shadow
// the checkout with the main copy.
func DetectBootstrap(ctx context.Context, repoPath string) core.Bootstrap {
	var candidates []string

	for _, d := range depDirs {
		candidates = append(candidates, d)
	}
	for _, f := range perWorktreeFiles {
		candidates = append(candidates, f)
	}

	// Monorepos keep a dependency tree per package as well as at the root.
	for _, glob := range workspaceGlobs {
		matches, err := filepath.Glob(filepath.Join(repoPath, glob))
		if err != nil {
			continue
		}
		for _, m := range matches {
			rel, err := filepath.Rel(repoPath, m)
			if err != nil {
				continue
			}
			for _, d := range []string{"node_modules", ".venv", "target", "vendor"} {
				candidates = append(candidates, filepath.Join(rel, d))
			}
		}
	}

	// Keep only what is actually on disk before asking git about it.
	var present []string
	for _, c := range candidates {
		if _, err := os.Lstat(filepath.Join(repoPath, c)); err == nil {
			present = append(present, c)
		}
	}
	if len(present) == 0 {
		return core.Bootstrap{}
	}

	ignored := ignoredSet(ctx, repoPath, present)

	var b core.Bootstrap
	isFile := map[string]bool{}
	for _, f := range perWorktreeFiles {
		isFile[f] = true
	}

	for _, p := range present {
		if !ignored[p] {
			// Tracked by git: the worktree gets its own copy already.
			continue
		}
		if len(b.Symlink)+len(b.Clone)+len(b.Copy) >= maxDetected {
			break
		}
		switch {
		case isFile[p]:
			b.Copy = append(b.Copy, p)
		case core.IsPathKeyed(p):
			b.Clone = append(b.Clone, p)
		default:
			b.Symlink = append(b.Symlink, p)
		}
	}

	sort.Strings(b.Symlink)
	sort.Strings(b.Clone)
	sort.Strings(b.Copy)
	return b
}

// ignoredSet asks git which of the given paths it is ignoring, in one call.
func ignoredSet(ctx context.Context, repoPath string, paths []string) map[string]bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "check-ignore", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	// check-ignore exits 1 when nothing matches, which is not an error here.
	_ = cmd.Run()

	set := map[string]bool{}
	for _, l := range strings.Split(out.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			set[l] = true
		}
	}
	return set
}

// Adopt registers the repo containing path, if it is not already known.
//
// This is what makes `cd ~/project && dma` work with no setup: the repo, its
// remote, its default branch and its bootstrap paths are all derived from the
// checkout itself.
func Adopt(ctx context.Context, cfg *core.Config, path string) (repo core.Repo, added bool, err error) {
	if !gitx.IsRepo(ctx, path) {
		return core.Repo{}, false, fmt.Errorf("%s is not a git repository", path)
	}
	top, err := gitx.TopLevel(ctx, path)
	if err != nil {
		return core.Repo{}, false, err
	}
	top = strings.TrimSpace(top)
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}

	// A worktree this tool created belongs to an already-registered repo;
	// adopting it as a repo of its own would nest worktrees inside worktrees.
	if common, err := gitx.Run(ctx, top, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		mainDir := filepath.Dir(strings.TrimSpace(common))
		if mainDir != top {
			if r, ok := repoByPath(cfg, mainDir); ok {
				return r, false, nil
			}
			top = mainDir
		}
	}

	if r, ok := repoByPath(cfg, top); ok {
		return r, false, nil
	}

	r := core.Repo{
		Path:       top,
		ID:         uniqueRepoID(cfg, core.Slug(filepath.Base(top))),
		BaseBranch: gitx.DefaultBranch(ctx, top),
		Bootstrap:  DetectBootstrap(ctx, top),
	}
	if slug, err := gitx.RemoteSlug(ctx, top); err == nil {
		r.Remote = slug
	}
	r.WorktreeRoot = filepath.Join(core.Dir(), "worktrees", r.ID)

	cfg.Repos = append(cfg.Repos, r)
	if cfg.DefaultRepo == "" {
		cfg.DefaultRepo = r.ID
	}
	if err := core.SaveConfig(cfg); err != nil {
		return r, true, err
	}
	return r, true, nil
}

// RefreshRemotes fills in the remote of any registered repo that has none,
// reporting the ids it learned one for.
//
// The remote is read once, when a repo is registered, and a repo registered
// before it had an origin -- a local project pushed to GitHub later -- keeps an
// empty one. Nothing revisits that, and an empty remote is skipped outright by
// the PR poll: every session in that repo sits in the agent-owned columns with
// no PR state, no badge and no error saying why. Re-reading origin at startup
// is one `git remote get-url` per repo still missing one, and it turns the
// board's silence back into the state it is supposed to be showing.
func RefreshRemotes(ctx context.Context, cfg *core.Config) (learned []string) {
	for i := range cfg.Repos {
		if cfg.Repos[i].Remote != "" {
			continue
		}
		slug, err := gitx.RemoteSlug(ctx, cfg.Repos[i].Path)
		if err != nil || slug == "" {
			// Still local-only, or the checkout is gone. Both are ordinary, and
			// both are answered by leaving PR polling off for that repo.
			continue
		}
		cfg.Repos[i].Remote = slug
		learned = append(learned, cfg.Repos[i].ID)
	}
	if len(learned) > 0 {
		// A failed write costs the next launch the same lookup, which is cheap;
		// the value is already live in the config this run is using.
		_ = core.SaveConfig(cfg)
	}
	return learned
}

func repoByPath(cfg *core.Config, path string) (core.Repo, bool) {
	for _, r := range cfg.Repos {
		if sameDir(r.Path, path) {
			return r, true
		}
	}
	return core.Repo{}, false
}

func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ra, erra := filepath.EvalSymlinks(a)
	rb, errb := filepath.EvalSymlinks(b)
	return erra == nil && errb == nil && ra == rb
}

func uniqueRepoID(cfg *core.Config, want string) string {
	taken := func(id string) bool {
		_, ok := cfg.Repo(id)
		return ok
	}
	if !taken(want) {
		return want
	}
	for i := 2; i < 100; i++ {
		cand := fmt.Sprintf("%s-%d", want, i)
		if !taken(cand) {
			return cand
		}
	}
	return want + "-" + core.NewID()[:4]
}

// SummarizeBootstrap renders what detection found, so registration is not a
// black box.
func SummarizeBootstrap(b core.Bootstrap) string {
	if len(b.Symlink)+len(b.Clone)+len(b.Copy) == 0 {
		return "nothing to share"
	}
	var parts []string
	if len(b.Symlink) > 0 {
		parts = append(parts, "shares "+joinCapped(b.Symlink, 3))
	}
	if len(b.Clone) > 0 {
		parts = append(parts, "clones "+joinCapped(b.Clone, 3))
	}
	if len(b.Copy) > 0 {
		parts = append(parts, "copies "+joinCapped(b.Copy, 3))
	}
	return strings.Join(parts, ", ")
}

func joinCapped(items []string, cap int) string {
	if len(items) <= cap {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(items[:cap], ", "), len(items)-cap)
}
