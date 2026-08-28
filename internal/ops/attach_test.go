package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// recordClaudeSession writes a transcript where the real agent would put one,
// so attaching has something to find. The store is redirected to a temp
// directory for the duration of the test.
func recordClaudeSession(t *testing.T, id, cwd, prompt string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "-"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + cwd + `","message":{"content":"` + prompt + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// attachConfig is a config whose agent does nothing when launched, so the tests
// exercise dma's own work rather than an agent's.
func attachConfig(t *testing.T, repoPath string) *core.Config {
	t.Helper()
	return &core.Config{
		Repos: []core.Repo{{
			ID: "testrepo", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		}},
		DefaultRepo: "testrepo",
		AgentProfiles: []core.AgentProfile{{
			Name: "claude", Command: "true", ResumeIDCommand: "true {session}",
		}},
		DefaultProfile: "claude",
	}
}

// recordPiSession writes a pi session file where the real agent would put one:
// under a directory named after the directory the conversation was held in.
func recordPiSession(t *testing.T, id, cwd, prompt string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", home)
	dir := filepath.Join(home, "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")+"--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session","version":3,"id":"` + id + `","cwd":"` + cwd + `"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"` + prompt + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-08-21T13-39-02-451Z_"+id+".jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
}

// attachForkConfig is a config whose agent has to be forked to be attached, which
// is what an agent that records its own working directory needs. Its commands do
// nothing when launched.
func attachForkConfig(t *testing.T, repoPath string) *core.Config {
	t.Helper()
	cfg := attachConfig(t, repoPath)
	cfg.AgentProfiles = []core.AgentProfile{{
		Name: "pi", Command: "true",
		ResumeIDCommand: "true --session {session}",
		ForkCommand:     "true --fork {session} --session-id {new}",
	}}
	cfg.DefaultProfile = "pi"
	return cfg
}

// The session ends up holding the copy, not the original. Recording the original
// instead would send every later restart back to a conversation that stopped
// where the fork began.
func TestAttachForksForAnAgentThatCarriesItsOwnDirectory(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachfork")
	recordPiSession(t, "01a026e1-source", repoPath, "Rewrite the retry loop")
	cfg := attachForkConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "pi", SessionID: "01a026e1-source"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), s.TmuxSession) })

	if s.ForkedFrom != "01a026e1-source" {
		t.Errorf("forked from = %q, want the conversation it was made from", s.ForkedFrom)
	}
	if s.AgentSessionID == "" || s.AgentSessionID == "01a026e1-source" {
		t.Errorf("agent session id = %q, want the minted id of the copy", s.AgentSessionID)
	}
	if s.Title != "Rewrite the retry loop" {
		t.Errorf("title = %q", s.Title)
	}
}

// A copy already on the board is the same work as the conversation it was made
// from, so attaching that conversation again is refused.
func TestAttachRefusesAConversationAlreadyForkedOntoTheBoard(t *testing.T) {
	repoPath := newTestRepo(t, "attachforkdup")
	recordPiSession(t, "01a026e1-twice", repoPath, "Twice over")
	cfg := attachForkConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := Attach(ctx, cfg, AttachRequest{
		Profile:   "pi",
		SessionID: "01a026e1-twice",
		Existing: []*core.Session{{
			Title: "Twice over", AgentSessionID: "some-minted-id",
			ForkedFrom: "01a026e1-twice", WorktreePath: "/somewhere",
		}},
	})
	var already *AlreadyAttachedError
	if !errors.As(err, &already) {
		t.Fatalf("err = %v, want AlreadyAttachedError", err)
	}
}

func TestAttachCarriesWorkInProgress(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachcarry")
	recordClaudeSession(t, "conv-1", repoPath, "Fix the flaky login test")
	cfg := attachConfig(t, repoPath)

	// The conversation has been editing files where it was running, and none of
	// it is committed. That is the state attaching exists to relocate.
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-1"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), s.TmuxSession) })

	if got, err := os.ReadFile(filepath.Join(s.WorktreePath, "README.md")); err != nil || string(got) != "edited\n" {
		t.Errorf("README.md in the worktree = %q (%v), want the uncommitted edit", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(s.WorktreePath, "new.txt")); err != nil || string(got) != "brand new\n" {
		t.Errorf("new.txt in the worktree = %q (%v)", got, err)
	}
	if !res.Carried.Any() {
		t.Errorf("carried %+v, want the work reported", res.Carried)
	}

	// The directory it came from keeps everything it had.
	if got, err := os.ReadFile(filepath.Join(repoPath, "new.txt")); err != nil || string(got) != "brand new\n" {
		t.Errorf("the source lost its work: %q (%v)", got, err)
	}
}

func TestAttachRecordsTheConversationOnTheSession(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachrecord")
	recordClaudeSession(t, "conv-2", repoPath, "Rewrite the retry loop")
	cfg := attachConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-2"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), s.TmuxSession) })

	if s.AgentSessionID != "conv-2" {
		t.Errorf("agent_session_id = %q", s.AgentSessionID)
	}
	if !s.Attached() {
		t.Error("session does not report itself as attached")
	}
	// The title comes off the conversation, so the card is recognizable without
	// the user naming it again.
	if s.Title != "Rewrite the retry loop" {
		t.Errorf("title = %q", s.Title)
	}
	// A resumed conversation opens on its history and waits: nothing was sent
	// with it, so it is not working.
	if s.AgentState != core.AgentIdle || s.Lifecycle != core.LifecycleIdle {
		t.Errorf("state = %s/%s, want an idle card", s.AgentState, s.Lifecycle)
	}
	if b := gitx.CurrentBranch(ctx, s.WorktreePath); b != "" {
		t.Errorf("worktree is on branch %q, want a detached HEAD like every other session", b)
	}
}

// -clean is the opt-out: the worktree is cut from the base branch and the work
// in progress stays where it is.
func TestAttachCleanCarriesNothing(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachclean")
	recordClaudeSession(t, "conv-3", repoPath, "Look at the parser")
	cfg := attachConfig(t, repoPath)
	if err := os.WriteFile(filepath.Join(repoPath, "new.txt"), []byte("brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-3", Clean: true})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	if _, err := os.Stat(filepath.Join(res.Session.WorktreePath, "new.txt")); !os.IsNotExist(err) {
		t.Error("-clean carried the work over anyway")
	}
	if res.Carried.Any() {
		t.Errorf("carried %+v, want nothing", res.Carried)
	}
}

// Committed work is carried by cutting the worktree from the same commit, which
// is what keeps the resumed agent looking at the history it remembers.
func TestAttachStartsFromTheConversationsCommit(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachcommit")
	recordClaudeSession(t, "conv-4", repoPath, "Keep going")
	cfg := attachConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A commit the base branch's fetched tip does not have -- the agent's work
	// from before it was attached.
	if err := os.WriteFile(filepath.Join(repoPath, "earlier.txt"), []byte("done already\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repoPath, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(ctx, repoPath, "commit", "-q", "-m", "earlier work"); err != nil {
		t.Fatal(err)
	}

	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-4"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	if _, err := os.Stat(filepath.Join(res.Session.WorktreePath, "earlier.txt")); err != nil {
		t.Errorf("the conversation's committed work is not in the worktree: %v", err)
	}
}

func TestAttachRefusesAConversationAlreadyOnTheBoard(t *testing.T) {
	repoPath := newTestRepo(t, "attachdup")
	recordClaudeSession(t, "conv-5", repoPath, "Twice over")
	cfg := attachConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := Attach(ctx, cfg, AttachRequest{
		Profile:   "claude",
		SessionID: "conv-5",
		Existing:  []*core.Session{{Title: "Twice over", AgentSessionID: "conv-5", WorktreePath: "/somewhere"}},
	})
	var already *AlreadyAttachedError
	if !errors.As(err, &already) {
		t.Fatalf("err = %v, want AlreadyAttachedError", err)
	}
}

// A profile with no by-id resume line cannot be attached, and says so before
// anything is created. Falling back to a plain launch would put an agent with no
// memory of the task behind a card named after the conversation.
func TestAttachRefusesAProfileThatCannotResumeByID(t *testing.T) {
	repoPath := newTestRepo(t, "attachnoresume")
	recordClaudeSession(t, "conv-6", repoPath, "No resume line")
	cfg := attachConfig(t, repoPath)
	cfg.AgentProfiles[0].ResumeIDCommand = ""

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-6"})
	if err == nil || !strings.Contains(err.Error(), "resume_id_command") {
		t.Fatalf("err = %v, want a refusal naming resume_id_command", err)
	}
}

func TestAttachRejectsAnUnknownConversation(t *testing.T) {
	repoPath := newTestRepo(t, "attachmissing")
	recordClaudeSession(t, "conv-7", repoPath, "Present")
	cfg := attachConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "not-a-session"}); err == nil {
		t.Fatal("attaching a session that does not exist succeeded")
	}
}

// The repo is inferred from where the conversation was running, and registered
// if this is the first dma has heard of it -- the same bargain the board makes
// with the directory it is launched from.
func TestAttachRegistersTheRepoTheConversationWasIn(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	t.Setenv("DMA_HOME", t.TempDir())
	repoPath := newTestRepo(t, "attachadopt")
	recordClaudeSession(t, "conv-8", repoPath, "Unregistered repo")

	cfg := &core.Config{
		AgentProfiles: []core.AgentProfile{{
			Name: "claude", Command: "true", ResumeIDCommand: "true {session}",
		}},
		DefaultProfile: "claude",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-8"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	if len(cfg.Repos) != 1 {
		t.Fatalf("registered %d repos, want 1", len(cfg.Repos))
	}
	if res.Session.RepoID != cfg.Repos[0].ID {
		t.Errorf("session repo = %q, registered %q", res.Session.RepoID, cfg.Repos[0].ID)
	}
	// Registering is never silent.
	if len(res.Warnings) == 0 {
		t.Error("registering a repo was not reported")
	}
}

// A conversation held somewhere that is not a repository has no repo to infer,
// and the error has to say which flag answers that.
func TestAttachAsksForARepoWhenItCannotInferOne(t *testing.T) {
	loose := t.TempDir()
	recordClaudeSession(t, "conv-9", loose, "Held outside a repo")
	cfg := attachConfig(t, newTestRepo(t, "attachelsewhere"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-9"})
	if err == nil || !strings.Contains(err.Error(), "-repo") {
		t.Fatalf("err = %v, want a refusal pointing at -repo", err)
	}
}

// Naming a repo explicitly covers the case above, and the conversation's own
// directory is then not carried from -- it belongs to a different tree.
func TestAttachAcceptsAnExplicitRepo(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	loose := t.TempDir()
	recordClaudeSession(t, "conv-10", loose, "Held outside a repo")
	repoPath := newTestRepo(t, "attachexplicit")
	cfg := attachConfig(t, repoPath)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-10", RepoID: "testrepo"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	if res.Session.RepoID != "testrepo" {
		t.Errorf("repo = %q", res.Session.RepoID)
	}
	if res.Carried.Any() {
		t.Errorf("carried %+v out of a directory in another tree", res.Carried)
	}
}

// A conversation held in some other checkout would produce a patch against
// commits this repo has never seen, so nothing is carried from it.
func TestAttachDoesNotCarryFromAnotherRepo(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	other := newTestRepo(t, "attachother")
	if err := os.WriteFile(filepath.Join(other, "elsewhere.txt"), []byte("not ours\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recordClaudeSession(t, "conv-11", other, "Somewhere else entirely")
	cfg := attachConfig(t, newTestRepo(t, "attachtarget"))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-11", RepoID: "testrepo"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	if _, err := os.Stat(filepath.Join(res.Session.WorktreePath, "elsewhere.txt")); !os.IsNotExist(err) {
		t.Error("work from an unrelated repo was carried in")
	}
}

// The launch line is the operation: attaching that opened a fresh agent, or
// opened the wrong conversation, would look identical on the board.
func TestAttachLaunchesTheAgentOnThatConversation(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachlaunch")
	recordClaudeSession(t, "conv-12", repoPath, "Resume me")
	agent, out := recorder(t)
	cfg := attachConfig(t, repoPath)
	cfg.AgentProfiles[0].Command = agent + " fresh"
	cfg.AgentProfiles[0].ResumeIDCommand = agent + " resumed {session}"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-12"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })

	lines := waitLaunches(t, out, 1)
	fields := strings.Fields(lines[0])
	if len(fields) != 3 || fields[0] != "resumed" || fields[1] != "conv-12" {
		t.Fatalf("launch = %q, want the conversation resumed by id", lines[0])
	}
	if !samePath(t, fields[2], res.Session.WorktreePath) {
		t.Errorf("agent launched in %q, want the new worktree %q", fields[2], res.Session.WorktreePath)
	}
}

// The transcript of an attached conversation lives where that conversation
// began, so the by-directory resume every other session restarts with finds
// nothing in this worktree. A restart has to name the id.
func TestRestartOfAnAttachedSessionResumesByID(t *testing.T) {
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "attachrestart")
	recordClaudeSession(t, "conv-13", repoPath, "Restart me")
	agent, out := recorder(t)
	cfg := attachConfig(t, repoPath)
	cfg.AgentProfiles[0].Command = agent + " fresh"
	cfg.AgentProfiles[0].ResumeCommand = agent + " by-directory"
	cfg.AgentProfiles[0].ResumeIDCommand = agent + " by-id {session}"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Attach(ctx, cfg, AttachRequest{Profile: "claude", SessionID: "conv-13"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	s := res.Session
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), s.TmuxSession) })
	waitLaunches(t, out, 1)

	if err := tmuxx.KillSession(ctx, s.TmuxSession); err != nil {
		t.Fatal(err)
	}
	restarted, err := Restart(ctx, cfg, s, RestartRequest{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), restarted.TmuxSession) })
	if !restarted.Resumed {
		t.Error("Resumed = false, but the conversation was reopened by id")
	}

	lines := waitLaunches(t, out, 2)
	if !strings.HasPrefix(lines[1], "by-id conv-13 ") {
		t.Errorf("restart launched %q, want the conversation resumed by id", lines[1])
	}
}
