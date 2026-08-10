package ops

// Restart is the operation a machine reboot needs: tmux is gone, so every agent
// went with it, and what is left is a worktree with no process in it. These tests
// stand in for that by killing the tmux session under a real created session and
// asking for it back.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/gitx"
	"github.com/dma1dma1/dma-cli/internal/tmuxx"
)

// recorder writes a stand-in agent that appends one line per launch -- the
// arguments it was given, then the directory it was launched in -- and then sits
// there like a real agent would, so a restart has something to stop.
func recorder(t *testing.T) (script, out string) {
	t.Helper()
	dir := t.TempDir()
	out = filepath.Join(dir, "launches")
	script = filepath.Join(dir, "agent.sh")
	body := "#!/bin/sh\necho \"$* $PWD\" >> " + out + "\nexec sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, out
}

// waitLaunches waits for the stand-in agent to have been launched n times. The
// launch is a line typed into a pane, so it lands a moment after the call that
// sent it returns.
func waitLaunches(t *testing.T, out string, n int) []string {
	t.Helper()
	var lines []string
	for range 200 {
		if b, err := os.ReadFile(out); err == nil {
			lines = strings.Split(strings.TrimSpace(string(b)), "\n")
			if len(lines) >= n {
				return lines
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("agent was launched %d times, want %d: %q", len(lines), n, lines)
	return nil
}

// restartFixture is a created session whose agent records its launches.
type restartFixture struct {
	cfg      *core.Config
	session  *core.Session
	launches string
}

func newRestartFixture(t *testing.T, resume string) restartFixture {
	t.Helper()
	if !tmuxx.Available() {
		t.Skip("tmux not installed")
	}
	repoPath := newTestRepo(t, "restart")
	agent, out := recorder(t)

	cfg := &core.Config{
		Repos: []core.Repo{{
			ID: "testrepo", Path: repoPath, BaseBranch: "main",
			WorktreeRoot: filepath.Join(t.TempDir(), "worktrees"),
		}},
		DefaultRepo: "testrepo",
		AgentProfiles: []core.AgentProfile{{
			Name:          "recorder",
			Command:       agent + " fresh",
			ResumeCommand: strings.ReplaceAll(resume, "{agent}", agent),
		}},
		DefaultProfile: "recorder",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Create(ctx, cfg, CreateRequest{Title: "Rate Limiter"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = tmuxx.KillSession(context.Background(), res.Session.TmuxSession) })
	waitLaunches(t, out, 1)

	return restartFixture{cfg: cfg, session: res.Session, launches: out}
}

// samePath compares two paths through their symlinks, because the agent reports
// the directory as its shell resolved it: on macOS a temp directory is reached
// through /var and reported as /private/var.
func samePath(t *testing.T, got, want string) bool {
	t.Helper()
	g, err := filepath.EvalSymlinks(got)
	if err != nil {
		return false
	}
	w, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	return g == w
}

// The whole point: the agent comes back in the worktree it was working in, on the
// line that continues the conversation it was having there. The working directory
// is what identifies that conversation -- see core.AgentProfile.ResumeCommand --
// so launching anywhere else would resume the wrong session or none at all.
func TestRestartResumesTheAgentInItsWorktree(t *testing.T) {
	f := newRestartFixture(t, "{agent} resumed")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The reboot: the agent's terminal is gone, and nothing else is.
	if err := tmuxx.KillSession(ctx, f.session.TmuxSession); err != nil {
		t.Fatal(err)
	}

	res, err := Restart(ctx, f.cfg, f.session, RestartRequest{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !res.Resumed {
		t.Error("Resumed = false for a profile that has a resume command")
	}
	// The name is reclaimed rather than replaced: it is what the session has been
	// referred to by since it was created.
	if res.TmuxSession != f.session.TmuxSession {
		t.Errorf("tmux session = %q, want the name it already had (%q)",
			res.TmuxSession, f.session.TmuxSession)
	}
	if !tmuxx.HasSession(ctx, res.TmuxSession) {
		t.Fatal("no terminal after a restart that reported success")
	}

	lines := waitLaunches(t, f.launches, 2)
	fields := strings.Fields(lines[1])
	if len(fields) != 2 || fields[0] != "resumed" {
		t.Fatalf("second launch = %q, want the resume command", lines[1])
	}
	if !samePath(t, fields[1], f.session.WorktreePath) {
		t.Errorf("agent restarted in %q, want its worktree %q", fields[1], f.session.WorktreePath)
	}
}

// A worktree with no conversation in it -- a session killed before its agent
// reached a first turn -- must still come back as a running agent. Left at the
// failed resume, the pane would sit at a shell prompt, which the board reads as an
// agent that is running.
func TestRestartFallsBackWhenThereIsNothingToResume(t *testing.T) {
	f := newRestartFixture(t, "false")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := tmuxx.KillSession(ctx, f.session.TmuxSession); err != nil {
		t.Fatal(err)
	}
	if _, err := Restart(ctx, f.cfg, f.session, RestartRequest{}); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	lines := waitLaunches(t, f.launches, 2)
	if !strings.HasPrefix(lines[1], "fresh ") {
		t.Errorf("second launch = %q, want the plain launch the failed resume falls back to", lines[1])
	}
}

// Restarting a live agent is how a wedged one is dealt with, so the old terminal
// has to go first: tmux would otherwise refuse the name, and the session would be
// left pointing at the process it was trying to replace.
func TestRestartStopsALiveAgentFirst(t *testing.T) {
	f := newRestartFixture(t, "{agent} resumed")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if !tmuxx.HasSession(ctx, f.session.TmuxSession) {
		t.Fatal("fixture agent is not running")
	}
	res, err := Restart(ctx, f.cfg, f.session, RestartRequest{})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if res.TmuxSession != f.session.TmuxSession {
		t.Errorf("tmux session = %q, want the name it already had", res.TmuxSession)
	}
	lines := waitLaunches(t, f.launches, 2)
	if !strings.HasPrefix(lines[1], "resumed ") {
		t.Errorf("second launch = %q, want the resume command", lines[1])
	}
}

// The worktree is the session's work in progress. A restart runs the agent again
// and touches nothing else: no fetch, no rebase, no bootstrap, and above all
// nothing that would discard what is uncommitted in it.
func TestRestartLeavesTheWorktreeAlone(t *testing.T) {
	f := newRestartFixture(t, "{agent} resumed")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	scratch := filepath.Join(f.session.WorktreePath, "wip.txt")
	if err := os.WriteFile(scratch, []byte("half a feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tmuxx.KillSession(ctx, f.session.TmuxSession); err != nil {
		t.Fatal(err)
	}
	if _, err := Restart(ctx, f.cfg, f.session, RestartRequest{}); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if b, err := os.ReadFile(scratch); err != nil || string(b) != "half a feature\n" {
		t.Errorf("uncommitted work did not survive the restart: %q, %v", b, err)
	}
	if dirty, err := gitx.IsDirty(ctx, f.session.WorktreePath); err != nil || !dirty {
		t.Errorf("worktree is no longer dirty after a restart: dirty=%v err=%v", dirty, err)
	}
}

// The hook address is not stable across launches of the board: one that found its
// configured port taken listens on an ephemeral one. So the settings in the
// worktree are rewritten on every restart rather than trusted, or the restarted
// agent reports its state to a port nothing is on.
func TestRestartReinstallsHooksAtTheBoardsCurrentAddress(t *testing.T) {
	f := newRestartFixture(t, "{agent} resumed")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := tmuxx.KillSession(ctx, f.session.TmuxSession); err != nil {
		t.Fatal(err)
	}
	const url = "http://127.0.0.1:54321/hook"
	res, err := Restart(ctx, f.cfg, f.session, RestartRequest{HookURL: url})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(res.Warnings) > 0 {
		t.Errorf("warnings = %v", res.Warnings)
	}

	settings, err := os.ReadFile(filepath.Join(f.session.WorktreePath, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read installed hooks: %v", err)
	}
	if !strings.Contains(string(settings), url) {
		t.Errorf("worktree hooks do not point at this board:\n%s", settings)
	}
}

// A session whose worktree has gone -- pruned by hand, or a prune whose state
// write did not land -- has nowhere for an agent to be. Starting tmux at a missing
// directory would produce a live session whose pane is an error message, which the
// board reads as running, so this is refused and named for what it is.
func TestRestartRefusesAMissingWorktree(t *testing.T) {
	f := newRestartFixture(t, "{agent} resumed")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := Teardown(ctx, f.cfg, f.session, TeardownOptions{Force: true}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	// Teardown moves the worktree aside and the sweep unlinks it; either way it is
	// no longer where the session says it is.
	_ = SweepTrash(ctx, f.cfg.Repos[0].WorktreeRoot)

	res, err := Restart(ctx, f.cfg, f.session, RestartRequest{})
	if _, ok := err.(*WorktreeMissingError); !ok {
		t.Fatalf("Restart of a missing worktree returned (%v, %v), want WorktreeMissingError", res, err)
	}
	if tmuxx.HasSession(ctx, f.session.TmuxSession) {
		t.Error("a refused restart still started a terminal")
	}
}

// An agent profile that has been removed from the config leaves nothing to launch.
// The session record still names it, so this is the one lookup that has to fail
// rather than fall back to whatever the default profile happens to be now.
func TestRestartRefusesAnUnknownProfile(t *testing.T) {
	f := newRestartFixture(t, "{agent} resumed")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	f.cfg.AgentProfiles = nil
	if _, err := Restart(ctx, f.cfg, f.session, RestartRequest{}); err == nil {
		t.Fatal("Restart with no such profile succeeded")
	}
}
