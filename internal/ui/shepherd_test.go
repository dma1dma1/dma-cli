package ui

import (
	"errors"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
)

// shepherdCfg configures the claude profile with an on-PR-open line.
func shepherdCfg(line string) *core.Config {
	cfg := core.DefaultConfig()
	for i := range cfg.AgentProfiles {
		if cfg.AgentProfiles[i].Name == "claude" {
			cfg.AgentProfiles[i].OnPROpen = line
		}
	}
	return cfg
}

// shepherdRepoCfg puts a line on the claude profile and an override on repo r1,
// which is the repo every session in these tests belongs to.
func shepherdRepoCfg(profileLine string, repoLine *string) *core.Config {
	cfg := shepherdCfg(profileLine)
	cfg.Repos = append(cfg.Repos, core.Repo{ID: "r1", Path: "/tmp/r1", OnPROpen: repoLine})
	return cfg
}

// shepherdSess is an open pull request whose agent is still running.
func shepherdSess(id string, number int) *core.Session {
	s := prSess(id, number, "https://github.com/o/r/pull/"+strconv.Itoa(number))
	s.AgentProfile, s.TmuxSession, s.TmuxAlive = "claude", "dma-"+id, true
	return s
}

// The whole point is that the line does not depend on what the agent was told
// at launch, so a plain open pull request is enough to trigger it.
func TestShepherdFiresOnAnOpenPullRequest(t *testing.T) {
	s := shepherdSess("a", 412)
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), s)

	if m.shepherdCmdFor(s) == nil {
		t.Fatal("an open pull request did not trigger the on-PR-open line")
	}
}

// Nothing is sent unless a profile asks for it: the default config must leave
// every agent alone.
func TestShepherdIsOffByDefault(t *testing.T) {
	s := shepherdSess("a", 412)
	m := testModel(nil, s)

	if m.shepherdCmdFor(s) != nil {
		t.Error("an unconfigured profile sent something on PR open")
	}
}

// The poll re-applies the same open pull request every tick. Sending the line
// once per tick would be a new turn every poll_interval_secs.
func TestShepherdDoesNotRepeatForTheSamePullRequest(t *testing.T) {
	s := shepherdSess("a", 412)
	s.ShepherdedPR = 412
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), s)

	if m.shepherdCmdFor(s) != nil {
		t.Error("an already-shepherded pull request was sent the line again")
	}
}

// Closing a pull request and opening another never makes HasPR false, so a
// flag would read the second one as already handled. The marker is the number.
func TestShepherdRearmsForANewPullRequest(t *testing.T) {
	s := shepherdSess("a", 413)
	s.ShepherdedPR = 412
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), s)

	if m.shepherdCmdFor(s) == nil {
		t.Error("a replacement pull request was treated as already shepherded")
	}
}

// With no terminal there is nobody to type to. The pull request has to stay
// armed rather than be marked done, so reviving the session still shepherds it.
func TestShepherdWaitsForATerminal(t *testing.T) {
	s := shepherdSess("a", 412)
	s.TmuxAlive = false
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), s)

	if m.shepherdCmdFor(s) != nil {
		t.Fatal("the line was sent to a session with no terminal")
	}
	if s.ShepherdedPR != 0 {
		t.Error("a pull request that was never sent the line was marked as shepherded")
	}
}

// The case an instruction in the launch prompt cannot cover: the agent ran
// gh pr create itself, so the board learns about the PR from the poll.
//
// The unconfigured run is what makes the configured one mean anything -- this
// poll has no other follow-up to make, so the command can only be the line.
func TestShepherdFiresOnAPullRequestTheBoardDidNotOpen(t *testing.T) {
	discover := func(cfg *core.Config) (*core.Session, tea.Cmd) {
		s := branchSess("a", "r1", "feat-a", core.LifecycleActive)
		s.AgentProfile, s.TmuxSession, s.TmuxAlive = "claude", "dma-a", true
		m := testModel(cfg, s)
		_, cmd := m.handlePRSync(prSyncMsg{
			repoID: "r1",
			poll: ghx.Poll{
				Open:     map[string]ghx.PR{"feat-a": {Number: 412, Branch: "feat-a", State: core.PROpen}},
				Answered: map[string]bool{"feat-a": true},
			},
		})
		return s, cmd
	}

	if _, cmd := discover(nil); cmd != nil {
		t.Error("an unconfigured profile produced a follow-up on PR discovery")
	}
	s, cmd := discover(shepherdCfg("/pr-shepherd {pr}"))
	if cmd == nil {
		t.Fatal("a pull request discovered by the poll did not trigger the on-PR-open line")
	}
	if s.Lifecycle != core.LifecyclePROpen {
		t.Errorf("lifecycle = %q, want pr_open", s.Lifecycle)
	}
}

func TestShepherdedMarksThePullRequest(t *testing.T) {
	s := shepherdSess("a", 412)
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), s)

	m.handleShepherded(shepherdedMsg{id: "a", pr: 412})

	if s.ShepherdedPR != 412 {
		t.Errorf("ShepherdedPR = %d, want 412", s.ShepherdedPR)
	}
	if m.shepherdCmdFor(s) != nil {
		t.Error("the line was sent a second time after being marked")
	}
}

// A failed send must not consume the trigger, or an agent that happened to be
// restarting turns into a pull request nobody shepherds.
func TestShepherdFailureLeavesThePullRequestArmed(t *testing.T) {
	s := shepherdSess("a", 412)
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), s)

	m.handleShepherded(shepherdedMsg{id: "a", pr: 412, err: errors.New("no server running")})

	if s.ShepherdedPR != 0 {
		t.Errorf("ShepherdedPR = %d after a failed send, want 0", s.ShepherdedPR)
	}
	if m.shepherdCmdFor(s) == nil {
		t.Error("a failed send left the pull request unarmed")
	}
}

// A repo whose review flow differs from the agent's default replaces the line.
func TestShepherdUsesARepoOverride(t *testing.T) {
	line := "/deploy-watch {pr}"
	s := shepherdSess("a", 412)
	m := testModel(shepherdRepoCfg("/pr-shepherd {pr}", &line), s)

	if m.shepherdCmdFor(s) == nil {
		t.Fatal("a repo override did not trigger the on-PR-open line")
	}
	if got := core.ExpandPROpen(m.cfg.PROpenLine("r1", "claude"), 412, ""); got != "/deploy-watch 412" {
		t.Errorf("resolved line = %q, want the repo override", got)
	}
}

// A repo with nothing to shepherd opts out with an empty override, even where
// the profile sets a line for every other repo.
func TestShepherdRepoCanOptOut(t *testing.T) {
	off := ""
	s := shepherdSess("a", 412)
	m := testModel(shepherdRepoCfg("/pr-shepherd {pr}", &off), s)

	if m.shepherdCmdFor(s) != nil {
		t.Error("a repo that opted out was still sent the line")
	}
}

// The override reaches the send path, not just the resolver: a repo that turns
// shepherding on for itself alone has to work with no profile default at all.
func TestShepherdRepoCanOptInAlone(t *testing.T) {
	line := "/pr-shepherd {pr}"
	on := shepherdSess("a", 412)
	off := shepherdSess("b", 413)
	off.RepoID = "r2"
	m := testModel(shepherdRepoCfg("", &line), on, off)

	if m.shepherdCmdFor(on) == nil {
		t.Error("the repo that opted in was not sent the line")
	}
	if m.shepherdCmdFor(off) != nil {
		t.Error("a repo with no override inherited a line the profile never set")
	}
}
