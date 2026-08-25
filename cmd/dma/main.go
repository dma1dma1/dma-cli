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
	"runtime/debug"
	"strings"
	"text/tabwriter"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/dma1dma1/dma-cli/internal/convo"
	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/hooks"
	"github.com/dma1dma1/dma-cli/internal/notify"
	"github.com/dma1dma1/dma-cli/internal/ops"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
	"github.com/dma1dma1/dma-cli/internal/ui"
)

const usage = `dma — a kanban board for parallel coding agent sessions

usage:
  dma                       open the board (registers the repo you are in)
  dma attach <agent> [id]   put an agent conversation you already have onto the
                            board, in a worktree of its own; without an id,
                            lists that agent's recent conversations
  dma repo add <path>       register a repository
  dma repo list             list registered repositories
  dma repo remove <id>      unregister a repository
  dma ls                    list sessions without opening the TUI
  dma hooks print           print the hook config the board installs
  dma doctor                check required external tools
  dma version               print the commit this binary was built from

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
	case "attach":
		return runAttach(args[1:])
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
		fmt.Println(version())
		return nil
	}
	return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
}

// version names the commit this binary was built from. The module carries no
// semver tags, so upgrading means re-running go install and getting whatever
// is on main -- which leaves the commit as the only way to answer "did my
// upgrade take". go install and a go build inside a checkout both stamp it
// into the module's pseudo-version, and the VCS settings cover what is left:
// builds Go could not date, such as one made inside a linked git worktree,
// where it reports no version at all.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dma unknown"
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		v = "devel"
	}
	var revision string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	// Go marks a build made over edited files by suffixing the version rather
	// than by the setting alone, so fold the two spellings together before
	// saying it once in words.
	if strings.HasSuffix(v, "+dirty") {
		v, dirty = strings.TrimSuffix(v, "+dirty"), true
	}
	// A pseudo-version already ends in the commit, so naming it again would
	// only be noise.
	if len(revision) >= 12 && !strings.Contains(v, revision[:12]) {
		v += " (" + revision[:12] + ")"
	}
	if dirty {
		v += " with uncommitted changes"
	}
	return "dma " + v
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
	if notice == "" {
		notice = notifierNotice(cfg)
	}
	refreshRemotes(cfg)

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

// notifierNotice is the one line telling the user that notifications need a
// helper this machine does not have, and marks it said.
//
// It yields to an adoption notice rather than being crammed alongside it: the
// notice line is one row, and a hint that loses its install command to
// truncation is worse than a hint that waits for the next launch. Not claiming
// the flag is what makes waiting work.
func notifierNotice(cfg *core.Config) string {
	req, missing := notify.MissingRequirement()
	if !missing || cfg.NotifierHintShown {
		return ""
	}
	cfg.NotifierHintShown = true
	// A failed write only costs the user the same hint again next launch.
	_ = core.SaveConfig(cfg)
	return req.Hint() + " (see dma doctor)"
}

// refreshRemotes gives every repo another chance to have an origin. It is
// silent: the PR badges that were missing simply appear on the next poll, which
// says it better than a line of prose about configuration.
func refreshRemotes(cfg *core.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ops.RefreshRemotes(ctx, cfg)
}

// --- attach ---

// attachUsage is spelled out rather than left to flag's own dump, because the
// argument that matters is positional and the flags are all overrides for
// something attach works out on its own.
const attachUsage = `usage: dma attach <agent> [session-id] [flags]

Puts a conversation you are already having with an agent onto the board: dma
cuts a worktree for it, carries over whatever work is uncommitted where the
conversation has been running, and reopens the same conversation there.

  dma attach claude                     list recent claude conversations
  dma attach claude <session-id>        attach one of them
  dma attach codex <session-id>         the same, for codex
  dma attach pi <session-id>            the same, for pi

flags:
  -repo <id>       repo to cut the worktree in (default: the repo the
                   conversation was running in, registering it if it is new)
  -project <name>  file the session under a project
  -title <text>    name the card (default: the conversation's opening prompt)
  -clean           start from the base branch instead of carrying over the
                   uncommitted work where the conversation has been running

The directory the conversation came from is never modified.`

// attachCols and attachRows size the agent's terminal until a board is opened
// and lays it out for real. A detached tmux session is 80x24 otherwise, which is
// narrow enough that an agent draws its whole UI into a strip; this is a
// comfortable full-screen shape to be resumed into in the meantime.
const (
	attachCols = 120
	attachRows = 40
)

func runAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, attachUsage) }
	repoID := fs.String("repo", "", "repo to cut the worktree in")
	project := fs.String("project", "", "project to file the session under")
	title := fs.String("title", "", "name for the card")
	clean := fs.Bool("clean", false, "start from the base branch, carrying nothing over")
	positional, err := parseAnywhere(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return fmt.Errorf("%s", attachUsage)
	}
	agent := positional[0]

	// Listing needs neither tmux nor a repo, so the requirement is checked on
	// the path that actually starts something.
	if len(positional) < 2 {
		return runAttachList(agent)
	}
	if !tmuxx.Available() {
		return fmt.Errorf("tmux is required but not on PATH")
	}

	cfg, err := core.LoadConfig()
	if err != nil {
		return err
	}
	sessions, err := core.LoadSessions()
	if err != nil {
		return err
	}

	// Generous, and for the same reason Create's budget is: the worktree this
	// makes gets the repo's bootstrap run into it, which on a large repo is
	// minutes of copying dependency trees.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	res, err := ops.Attach(ctx, cfg, ops.AttachRequest{
		Profile:   agent,
		SessionID: positional[1],
		RepoID:    *repoID,
		Group:     *project,
		Title:     *title,
		Clean:     *clean,
		Existing:  sessions,
		HookURL:   hookURL(cfg),
		Cols:      attachCols,
		Rows:      attachRows,
	})
	if err != nil {
		return err
	}

	// The upsert is locked with board saves, so a board that starts or prunes a
	// session while this bootstrap is running cannot have its result overwritten.
	// A board already running picks this up on its next poll.
	s := res.Session
	if err := core.UpsertSessions([]*core.Session{s}); err != nil {
		// The agent is already up in a worktree of its own by this point, so a
		// failed write is a session that exists and is not on the board rather
		// than a session that did not happen. Naming both halves is what makes
		// it recoverable -- otherwise the only trace is a tmux session the user
		// has no reason to look for.
		return fmt.Errorf("save state: %w\n"+
			"the agent is running in %s (tmux session %s), but is not on the board",
			err, s.WorktreePath, s.TmuxSession)
	}

	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	fmt.Printf("attached %q\n", s.Title)
	fmt.Printf("  agent       %s (%s)\n", s.AgentProfile, orDash(s.AgentSessionID))
	if s.ForkedFrom != "" {
		// Said out loud because it is a different bargain from the one the other
		// agents make: this session holds a copy, so turns taken here do not
		// appear in the conversation it was made from.
		fmt.Printf("  forked from %s\n", s.ForkedFrom)
	}
	fmt.Printf("  repo        %s\n", s.RepoID)
	fmt.Printf("  worktree    %s\n", s.WorktreePath)
	fmt.Printf("  tmux        %s\n", s.TmuxSession)
	if res.Conversation.Cwd != "" {
		fmt.Printf("  came from   %s\n", shortenHome(res.Conversation.Cwd))
	}
	fmt.Printf("  carried     %s\n", res.Carried)
	fmt.Println("\nrun dma to see it on the board.")
	return nil
}

func runAttachList(agent string) error {
	conversations, err := convo.List(agent, 15)
	if err != nil {
		return err
	}
	if len(conversations) == 0 {
		fmt.Printf("no %s conversations recorded\n", agent)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tAGO\tWHERE\tTITLE")
	for _, c := range conversations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ID, core.FormatDuration(time.Since(c.Updated)),
			shortenHome(c.Cwd), truncateTitle(c.Title))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Printf("\nattach one with: dma attach %s <session-id>\n", agent)
	return nil
}

// hookURL is where an attached session should report its state.
//
// It is the configured port rather than a listener this process owns, because
// this process is about to exit: the session outlives it, and the board that
// picks the session up is the thing that will be listening. A board that had to
// fall back to an ephemeral port reinstalls the right address on its next
// restart of the session, which is the same recovery every other session gets.
func hookURL(cfg *core.Config) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", cfg.HookPort, hooks.Path)
}

// parseAnywhere parses flags that appear on either side of the positional
// arguments, returning the positionals in order.
//
// Go's flag package stops at the first argument that is not a flag, so
// `dma attach claude <id> -clean` would otherwise parse no flags at all and
// carry the work over regardless -- silently doing the opposite of what was
// asked. The agent and the id sit in the middle of that line naturally enough
// that requiring the flags in front of them is not a rule worth having.
func parseAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// shortenHome renders a path with the home directory as ~, so the list stays
// narrow enough to read on one line.
func shortenHome(p string) string {
	if p == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	if p == home {
		return "~"
	}
	if rest := strings.TrimPrefix(p, home); strings.HasPrefix(rest, string(filepath.Separator)) {
		return "~" + rest
	}
	return p
}

// truncateTitle keeps one opening prompt to a single readable cell. Prompts are
// paragraphs as often as they are sentences.
func truncateTitle(s string) string {
	const limit = 60
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	if len([]rune(s)) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
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
	symlink := fs.String("symlink", "", "comma-separated paths to share between worktrees")
	clonePaths := fs.String("clone", "", "comma-separated dependency trees to clone per worktree")
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

	// Explicit flags win; otherwise detect what a fresh worktree will need.
	if *symlink == "" && *clonePaths == "" && *copyPaths == "" {
		r.Bootstrap = ops.DetectBootstrap(ctx, path)
	} else {
		r.Bootstrap.Symlink = splitList(*symlink)
		r.Bootstrap.Clone = splitList(*clonePaths)
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
		// A session has no branch until its agent makes one.
		branch := s.Branch
		if branch == "" {
			branch = "-"
		}
		if cfg.MultiRepo() {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Title, s.RepoID, s.Lifecycle,
				s.AgentState, core.FormatDuration(s.TimeInState()), branch, pr, tm)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Title, s.Lifecycle,
				s.AgentState, core.FormatDuration(s.TimeInState()), branch, pr, tm)
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
		install   string
	}
	checks := []check{
		{"tmux", "hosts agent sessions", true, ""},
		{"git", "worktrees, branches, diffs", true, ""},
		{"gh", "pull request state", true, ""},
		{"delta", "nicer diff rendering (optional)", false, ""},
	}
	// The notifier is whatever this platform needs, so the check follows the
	// package that does the notifying rather than being restated here.
	for _, r := range notify.Requirements() {
		checks = append(checks, check{r.Tool, r.Why, r.Required, r.Install})
	}

	// One column wide enough for the longest name: terminal-notifier is three
	// times the width the fixed column used to assume.
	width := 0
	for _, c := range checks {
		width = max(width, len(c.name))
	}

	ok := true
	for _, c := range checks {
		path, err := exec.LookPath(c.name)
		switch {
		case err == nil:
			fmt.Printf("  ✓ %-*s %s\n", width, c.name, path)
		case c.required:
			fmt.Printf("  ✗ %-*s missing — %s\n", width, c.name, c.why)
			ok = false
		default:
			fmt.Printf("  · %-*s not installed — %s\n", width, c.name, c.why)
		}
		if err != nil && c.install != "" {
			fmt.Printf("    %-*s run: %s\n", width, "", c.install)
		}
	}

	if _, err := exec.LookPath("gh"); err == nil {
		if !ghAuthed() {
			fmt.Printf("  ✗ %-*s not authenticated — run: gh auth login\n", width, "gh")
			ok = false
		} else {
			fmt.Printf("  ✓ %-*s authenticated\n", width, "gh")
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
