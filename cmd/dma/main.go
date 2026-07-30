// Command dma is a terminal kanban board for running and monitoring parallel
// AI coding agent sessions.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/ops"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
	"github.com/dma1dma1/dma-cli/internal/ui"
)

const usage = `dma — a kanban board for parallel coding agent sessions

usage:
  dma                       open the board (registers the repo you are in)
  dma repo add <path>       register a repository
  dma repo list             list registered repositories
  dma repo remove <id>      unregister a repository
  dma ls                    list sessions without opening the TUI
  dma hooks print           print the hook config the board installs
  dma doctor                check required external tools

Most of the time you need none of these: cd into a repo and run dma.
Repos can also be added from inside the board by pressing r.

state lives in $DMA_HOME (default ~/.dma)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dma: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runBoard()
	}
	switch args[0] {
	case "repo":
		return runRepo(args[1:])
	case "ls":
		return runList()
	case "hooks":
		return runHooks(args[1:])
	case "doctor":
		return runDoctor()
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	case "-v", "--version", "version":
		fmt.Println("dma dev")
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

// --- board ---

func runBoard() error {
	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	if !tmuxx.Available() {
		return fmt.Errorf("tmux is required but not on PATH")
	}

	// Adopt whatever repo we are standing in. Being in a repo is the whole
	// declaration of intent -- there is no reason to make someone register it
	// first, and its dependencies and env files are detectable from the
	// checkout itself.
	launchRepo, notice := adoptCwd(cfg)

	sessions, err := core.LoadSessions()
	if err != nil {
		return err
	}

	srv, err := hooks.Start(cfg.HookPort)
	if err != nil {
		// A port clash should not stop the board; fall back to an ephemeral
		// port and carry on with hook-driven state intact.
		srv, err = hooks.Start(0)
		if err != nil {
			return fmt.Errorf("start hook listener: %w", err)
		}
	}
	defer srv.Close()

	// bubblezone needs a global manager initialized before any Mark call.
	zone.NewGlobal()
	defer zone.Close()

	m := ui.New(ui.Options{
		Config:     cfg,
		Sessions:   sessions,
		HookEvents: srv.Events(),
		HookURL:    srv.URL(),
		LaunchRepo: launchRepo,
		Notice:     notice,
	})
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

// adoptCwd registers the repo containing the working directory, if there is
// one. It returns the repo id to default new sessions to, plus a one-line
// notice when something was registered, so adoption is never silent.
func adoptCwd(cfg *core.Config) (repoID, notice string) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if !gitx.IsRepo(ctx, cwd) {
		if len(cfg.Repos) == 0 {
			return "", "Not in a git repository — press r then a to add one."
		}
		return "", ""
	}
	repo, added, err := ops.Adopt(ctx, cfg, cwd)
	if err != nil {
		return "", "could not register this repo: " + err.Error()
	}
	if !added {
		return repo.ID, ""
	}
	return repo.ID, fmt.Sprintf("registered %s — %s", repo.ID, ops.SummarizeBootstrap(repo.Bootstrap))
}

// --- repo ---

func runRepo(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: dma repo <add|list|remove>")
	}
	switch args[0] {
	case "add":
		return runRepoAdd(args[1:])
	case "list":
		return runRepoList()
	case "remove", "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: dma repo remove <id>")
		}
		return runRepoRemove(args[1])
	}
	return fmt.Errorf("unknown repo command %q", args[0])
}

func runRepoAdd(args []string) error {
	fs := flag.NewFlagSet("repo add", flag.ContinueOnError)
	id := fs.String("id", "", "short handle (default: directory name)")
	remote := fs.String("remote", "", "owner/name (default: read from origin)")
	base := fs.String("base", "", "base branch (default: repo default branch)")
	wtRoot := fs.String("worktree-root", "", "where worktrees live (must differ per repo)")
	prefix := fs.String("branch-prefix", "", "branch name prefix, e.g. feat/")
	symlink := fs.String("symlink", "", "comma-separated paths to symlink into each worktree")
	copyPaths := fs.String("copy", "", "comma-separated paths to copy into each worktree")
	setDefault := fs.Bool("default", false, "make this the default repo")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: dma repo add [flags] <path>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if !gitx.IsRepo(ctx, path) {
		return fmt.Errorf("%s is not a git repository", path)
	}
	if top, err := gitx.TopLevel(ctx, path); err == nil && top != "" {
		path = top
	}

	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}

	r := core.Repo{Path: path}
	r.ID = *id
	if r.ID == "" {
		r.ID = core.Slug(filepath.Base(path))
	}
	if _, exists := cfg.Repo(r.ID); exists {
		return fmt.Errorf("a repo with id %q is already registered", r.ID)
	}

	// Reading the remote from origin is almost always right, so it is the
	// default rather than something to configure by hand.
	r.Remote = *remote
	if r.Remote == "" {
		if slug, err := gitx.RemoteSlug(ctx, path); err == nil {
			r.Remote = slug
		} else {
			fmt.Fprintf(os.Stderr, "warning: could not read origin remote; PR polling is disabled for %s\n", r.ID)
		}
	}

	r.BaseBranch = *base
	if r.BaseBranch == "" {
		r.BaseBranch = gitx.DefaultBranch(ctx, path)
	}

	// Worktree roots must differ per repo, for the same reason branch names
	// alone cannot identify a worktree.
	r.WorktreeRoot = *wtRoot
	if r.WorktreeRoot == "" {
		r.WorktreeRoot = filepath.Join(core.Dir(), "worktrees", r.ID)
	}
	r.WorktreeRoot, _ = filepath.Abs(r.WorktreeRoot)
	for _, other := range cfg.Repos {
		if other.WorktreeRoot == r.WorktreeRoot {
			return fmt.Errorf("worktree_root %s is already used by repo %q; each repo needs its own",
				r.WorktreeRoot, other.ID)
		}
	}

	r.BranchPrefix = *prefix
	// Explicit flags win; otherwise detect what a fresh worktree will need.
	if *symlink == "" && *copyPaths == "" {
		r.Bootstrap = ops.DetectBootstrap(ctx, path)
	} else {
		r.Bootstrap.Symlink = splitList(*symlink)
		r.Bootstrap.Copy = splitList(*copyPaths)
	}

	cfg.Repos = append(cfg.Repos, r)
	if *setDefault || cfg.DefaultRepo == "" {
		cfg.DefaultRepo = r.ID
	}
	if err := core.SaveConfig(cfg); err != nil {
		return err
	}

	fmt.Printf("registered %s\n", r.ID)
	fmt.Printf("  path          %s\n", r.Path)
	fmt.Printf("  remote        %s\n", orDash(r.Remote))
	fmt.Printf("  base branch   %s\n", r.BaseBranch)
	fmt.Printf("  worktrees     %s\n", r.WorktreeRoot)
	fmt.Printf("  bootstrap     %s\n", ops.SummarizeBootstrap(r.Bootstrap))
	return nil
}

func runRepoList() error {
	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Repos) == 0 {
		fmt.Println("no repositories registered")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREMOTE\tBASE\tPATH\tWORKTREES")
	for _, r := range cfg.Repos {
		id := r.ID
		if r.ID == cfg.DefaultRepo {
			id += " *"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", id, orDash(r.Remote), r.BaseBranch, r.Path, r.WorktreeRoot)
	}
	return w.Flush()
}

func runRepoRemove(id string) error {
	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	sessions, err := core.LoadSessions()
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.RepoID == id {
			return fmt.Errorf("repo %q still has session %q; prune it first", id, s.Title)
		}
	}
	out := cfg.Repos[:0]
	found := false
	for _, r := range cfg.Repos {
		if r.ID == id {
			found = true
			continue
		}
		out = append(out, r)
	}
	if !found {
		return fmt.Errorf("no repo with id %q", id)
	}
	cfg.Repos = out
	if cfg.DefaultRepo == id {
		cfg.DefaultRepo = ""
		if len(cfg.Repos) > 0 {
			cfg.DefaultRepo = cfg.Repos[0].ID
		}
	}
	if err := core.SaveConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", id)
	return nil
}

// --- ls ---

func runList() error {
	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	sessions, err := core.LoadSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	live, _ := tmuxx.ListSessions(ctx)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	header := "TITLE\tLIFECYCLE\tAGENT\tFOR\tBRANCH\tPR\tTMUX"
	if cfg.MultiRepo() {
		header = "TITLE\tREPO\tLIFECYCLE\tAGENT\tFOR\tBRANCH\tPR\tTMUX"
	}
	fmt.Fprintln(w, header)
	for _, s := range sessions {
		pr := "-"
		if s.HasPR() {
			pr = fmt.Sprintf("#%d %s", s.PRNumber, s.PRState)
		}
		tm := "gone"
		if live[s.TmuxSession] {
			tm = "up"
		}
		if cfg.MultiRepo() {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Title, s.RepoID, s.Lifecycle,
				s.AgentState, core.FormatDuration(s.TimeInState()), s.Branch, pr, tm)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Title, s.Lifecycle,
				s.AgentState, core.FormatDuration(s.TimeInState()), s.Branch, pr, tm)
		}
	}
	return w.Flush()
}

// --- hooks ---

func runHooks(args []string) error {
	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] != "print" {
		return fmt.Errorf("usage: dma hooks print")
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", cfg.HookPort, hooks.Path)
	out, err := hooks.SettingsJSON(url)
	if err != nil {
		return err
	}
	fmt.Println("// dma installs this into each worktree's .claude/settings.local.json")
	fmt.Println("// automatically. This is what it writes:")
	fmt.Println(out)
	return nil
}

// --- doctor ---

func runDoctor() error {
	type check struct {
		name, why string
		required  bool
	}
	checks := []check{
		{"tmux", "hosts agent sessions", true},
		{"git", "worktrees, branches, diffs", true},
		{"gh", "pull request state", true},
		{"delta", "nicer diff rendering (optional)", false},
	}
	ok := true
	for _, c := range checks {
		path, err := exec.LookPath(c.name)
		switch {
		case err == nil:
			fmt.Printf("  ✓ %-6s %s\n", c.name, path)
		case c.required:
			fmt.Printf("  ✗ %-6s missing — %s\n", c.name, c.why)
			ok = false
		default:
			fmt.Printf("  · %-6s not installed — %s\n", c.name, c.why)
		}
	}

	if _, err := exec.LookPath("gh"); err == nil {
		if !ghAuthed() {
			fmt.Printf("  ✗ gh     not authenticated — run: gh auth login\n")
			ok = false
		} else {
			fmt.Printf("  ✓ gh     authenticated\n")
		}
	}

	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("\n  config   %s\n", core.ConfigPath())
	fmt.Printf("  state    %s\n", core.StatePath())
	fmt.Printf("  repos    %d registered\n", len(cfg.Repos))
	fmt.Printf("  hooks    http://127.0.0.1:%d%s\n", cfg.HookPort, hooks.Path)

	if !ok {
		return fmt.Errorf("some required tools are missing")
	}
	return nil
}

func ghAuthed() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ghx.Available()
	cmd := exec.CommandContext(ctx, "gh", "auth", "status")
	return cmd.Run() == nil
}

// --- helpers ---

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
