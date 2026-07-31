package ui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
	"github.com/dma1dma1/dma-cli/internal/ops"
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

// --- setting the line in the app ---

// The whole point of the toggle: turning shepherding on for an agent, everywhere,
// without typing a command name. o has to write a real line and save it.
func TestToggleProfileLineFromTheAgentList(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	s := shepherdSess("a", 412)
	m := testModel(shepherdCfg(""), s)
	m.shepherdSkill = "/cdl-pr:pr-shepherd"

	if m.shepherdCmdFor(s) != nil {
		t.Fatal("shepherding was on before anything was configured")
	}

	m.openDropdown(focusAgent)
	m.dropdown.cursor = indexOf(m.dropdown.options, "claude")
	mm, _ := m.keyDropdown("o")
	m = mm.(Model)

	if got, _ := m.cfg.Profile("claude"); got.OnPROpen != "/cdl-pr:pr-shepherd {pr}" {
		t.Errorf("profile line = %q, want the detected command", got.OnPROpen)
	}
	if m.mode == modePrompt {
		t.Error("the toggle opened a text prompt")
	}
	// The list stays open so the row reads back what just happened.
	if !m.dropdown.open || m.dropdown.area != focusAgent {
		t.Error("the agent list closed on toggle")
	}
	if m.shepherdCmdFor(s) == nil {
		t.Error("the session was not armed by the toggle")
	}
	saved, err := core.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := saved.Profile("claude"); got.OnPROpen != "/cdl-pr:pr-shepherd {pr}" {
		t.Errorf("saved profile line = %q, want the detected command", got.OnPROpen)
	}

	// And back off again, from the same key.
	mm, _ = m.keyDropdown("o")
	m = mm.(Model)
	if got, _ := m.cfg.Profile("claude"); got.OnPROpen != "" {
		t.Errorf("profile line = %q after a second toggle, want it cleared", got.OnPROpen)
	}
}

// An agent that cannot be handed a slash command still has to be switchable, or
// "shepherd everything" would quietly mean "shepherd the Claude sessions".
func TestToggleFallsBackToWordsForANonClaudeAgent(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	m := testModel(shepherdCfg(""))
	m.shepherdSkill = "/cdl-pr:pr-shepherd"

	m.toggleProfileShepherd("codex")

	got, _ := m.cfg.Profile("codex")
	if got.OnPROpen != ops.ShepherdFallback {
		t.Errorf("codex line = %q, want the worded fallback", got.OnPROpen)
	}
}

// With no skill installed there is no command to offer, and the toggle still has
// to do something rather than write an empty line.
func TestToggleFallsBackWithNoSkillInstalled(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	m := testModel(shepherdCfg(""))
	m.shepherdSkill = ""

	m.toggleProfileShepherd("claude")

	got, _ := m.cfg.Profile("claude")
	if got.OnPROpen != ops.ShepherdFallback {
		t.Errorf("claude line = %q, want the worded fallback", got.OnPROpen)
	}
}

// o in the repo list walks the three settings a repo can hold, so neither
// exception needs typing.
func TestCycleRepoShepherdFromTheRepoList(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	s := shepherdSess("a", 412)
	m := testModel(shepherdRepoCfg("/pr-shepherd {pr}", nil), s)
	m.shepherdSkill, m.mode = "/cdl-pr:pr-shepherd", modeRepos
	m.repos.cursor = indexOfRepo(m.cfg, "r1")

	// inherit -> off here
	mm, _ := m.keyRepos("o")
	m = mm.(Model)
	r, _ := m.cfg.Repo("r1")
	if r.OnPROpen == nil || *r.OnPROpen != "" {
		t.Fatalf("first press gave %v, want off here", r.OnPROpen)
	}
	if m.shepherdCmdFor(s) != nil {
		t.Error("a repo switched off was still armed")
	}

	// off here -> on here, with the detected command
	mm, _ = m.keyRepos("o")
	m = mm.(Model)
	r, _ = m.cfg.Repo("r1")
	if r.OnPROpen == nil || *r.OnPROpen != "/cdl-pr:pr-shepherd {pr}" {
		t.Fatalf("second press gave %v, want the detected command", r.OnPROpen)
	}

	// on here -> follows its agent again
	mm, _ = m.keyRepos("o")
	m = mm.(Model)
	r, _ = m.cfg.Repo("r1")
	if r.OnPROpen != nil {
		t.Fatalf("third press gave %q, want the override cleared", *r.OnPROpen)
	}
	if m.shepherdCmdFor(s) == nil {
		t.Error("returning to inherit did not restore the agent's line")
	}
	if m.mode != modeRepos {
		t.Error("cycling left the repo list")
	}
}

// The typed line keeps a key, for the line nobody could have detected. It is
// seeded so it is an edit rather than a blank field.
func TestCustomLineEditorsAreOnTheShiftedKey(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	m := testModel(shepherdRepoCfg("/pr-shepherd {pr}", nil))
	m.shepherdSkill, m.mode = "/cdl-pr:pr-shepherd", modeRepos
	m.repos.cursor = indexOfRepo(m.cfg, "r1")

	mm, _ := m.keyRepos("O")
	rm := mm.(Model)
	if rm.prompt.kind != promptRepoShepherd || rm.prompt.target != "r1" {
		t.Fatalf("O in the repo list did not open the editor: kind=%v target=%q",
			rm.prompt.kind, rm.prompt.target)
	}
	if got := rm.prompt.input.Value(); got != "/pr-shepherd {pr}" {
		t.Errorf("repo editor seeded with %q, want the line in force", got)
	}

	m2 := testModel(shepherdCfg(""), nil...)
	m2.shepherdSkill = "/cdl-pr:pr-shepherd"
	m2.openDropdown(focusAgent)
	m2.dropdown.cursor = indexOf(m2.dropdown.options, "claude")
	mm2, _ := m2.keyDropdown("O")
	am := mm2.(Model)
	if am.prompt.kind != promptProfileShepherd || am.prompt.target != "claude" {
		t.Fatalf("O in the agent list did not open the editor: kind=%v target=%q",
			am.prompt.kind, am.prompt.target)
	}
	if got := am.prompt.input.Value(); got != "/cdl-pr:pr-shepherd {pr}" {
		t.Errorf("agent editor seeded with %q, want the detected command", got)
	}
}

// A submitted empty line in the repo editor is still "nothing here" -- esc is how
// you back out, and o is how you get to the other two states.
func TestRepoEditorSubmittedEmptyTurnsShepherdingOff(t *testing.T) {
	t.Setenv("DMA_HOME", t.TempDir())
	s := shepherdSess("a", 412)
	m := testModel(shepherdRepoCfg("/pr-shepherd {pr}", nil), s)
	m.mode, m.repos.cursor = modeRepos, indexOfRepo(m.cfg, "r1")

	mm, _ := m.keyRepos("O")
	m = mm.(Model)
	m.prompt.input.SetValue("")
	mm, _ = m.keyPrompt(tea.KeyPressMsg{}, "enter")
	m = mm.(Model)

	r, _ := m.cfg.Repo("r1")
	if r.OnPROpen == nil || *r.OnPROpen != "" {
		t.Fatalf("repo override = %v, want a set-but-empty override", r.OnPROpen)
	}
	if m.shepherdCmdFor(s) != nil {
		t.Error("a repo that was turned off was still armed")
	}
}

// The agent row has to say what the toggle would turn on, or o is a key with an
// unknown effect.
func TestShepherdLabelNamesWhatTheToggleWouldDo(t *testing.T) {
	m := testModel(shepherdCfg(""))
	m.shepherdSkill = "/cdl-pr:pr-shepherd"

	prof, _ := m.cfg.Profile("claude")
	label := m.shepherdLabel(prof)
	if !strings.Contains(label, "off") || !strings.Contains(label, "/cdl-pr:pr-shepherd") {
		t.Errorf("label = %q, want it to name both the state and the offer", label)
	}

	prof.OnPROpen = "/deploy-watch {pr}"
	if got := m.shepherdLabel(prof); !strings.Contains(got, "/deploy-watch {pr}") {
		t.Errorf("label = %q, want the configured line", got)
	}
}

// The count reported when a line is switched on has to come from the same
// question the poll asks, or it will describe something other than what happens.
func TestPendingShepherdsCountsWhatWouldFire(t *testing.T) {
	armed := shepherdSess("a", 412)
	already := shepherdSess("b", 413)
	already.ShepherdedPR = 413
	dead := shepherdSess("c", 414)
	dead.TmuxAlive = false
	m := testModel(shepherdCfg("/pr-shepherd {pr}"), armed, already, dead)

	if got := m.pendingShepherds(); got != 1 {
		t.Errorf("pendingShepherds = %d, want 1", got)
	}
}

// The repo row has to distinguish all three states, since that is the only place
// the difference between "off here" and "inherited" is visible.
func TestShepherdSummaryNamesTheSource(t *testing.T) {
	line, off := "/deploy-watch {pr}", ""
	cfg := shepherdCfg("/pr-shepherd {pr}")
	cfg.Repos = []core.Repo{
		{ID: "custom", OnPROpen: &line},
		{ID: "off", OnPROpen: &off},
		{ID: "inherit"},
	}
	m := testModel(cfg)

	for _, tc := range []struct{ repo, want string }{
		{"custom", "/deploy-watch {pr}"},
		{"off", "off for this repo"},
		{"inherit", "from claude"},
	} {
		r, _ := m.cfg.Repo(tc.repo)
		if got := m.shepherdSummary(r); !strings.Contains(got, tc.want) {
			t.Errorf("summary for %s = %q, want it to mention %q", tc.repo, got, tc.want)
		}
	}
}

// indexOfRepo finds a repo's position in the config for the picker's cursor.
func indexOfRepo(cfg *core.Config, id string) int {
	for i, r := range cfg.Repos {
		if r.ID == id {
			return i
		}
	}
	return 0
}
