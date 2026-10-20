package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// N starts the next task in the selected session's worktree, and the card and
// panel say the two agents share it.
func TestNStartsInTheSelectedWorktree(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}, {ID: "web", BaseBranch: "main"}}
	src := sess("a", "auth", core.LifecycleActive, core.AgentWorking, "web")
	src.WorktreePath, src.Branch, src.BaseBranch, src.TmuxAlive = "/wt/token-refresh", "feat/token-refresh", "develop", true
	m := testModel(cfg, src)
	m.selectSession(src)

	next, _ := m.keyBoard("N")
	m = next.(Model)
	if m.focus != focusInput {
		t.Fatal("N did not open the composer")
	}
	if got := m.newSessionTarget(); !strings.Contains(got, "feat/token-refresh") {
		t.Errorf("composer target = %q, want it to name the shared branch", got)
	}
	req, err := m.newSessionRequest("write tests")
	if err != nil {
		t.Fatal(err)
	}
	if req.Worktree != src.WorktreePath || req.RepoID != "web" || req.BaseBranch != "develop" || req.Group != "auth" {
		t.Fatalf("request = %+v, want it to join %s", req, src.WorktreePath)
	}

	sib := startingSession("b", req)
	sib.Starting, sib.StartFailure = false, nil
	m.sessions = append(m.sessions, sib)
	if _, sub := m.panelTitle(); !strings.Contains(sub, "also here: write tests") {
		t.Errorf("panel subtitle = %q, want it to name the other agent", sub)
	}

	// i forgets the target, so the task starts a worktree of its own.
	next, _ = m.keyBoard("i")
	m = next.(Model)
	if req, _ := m.newSessionRequest("other"); req.Worktree != "" {
		t.Error("i after N still joined the shared worktree")
	}
}
