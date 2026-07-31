package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// projectOptions is the list the picker would show, as the user sees it.
func projectOptions(m Model) []string { return m.dropdown.options }

// A session belongs to no project unless one was chosen. Projects are an
// optional grouping laid over a board that has to work before any exist.
func TestNewSessionsStartInNoProject(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}}
	m := testModel(cfg)

	req, err := m.newSessionRequest("do a thing")
	if err != nil {
		t.Fatalf("newSessionRequest: %v", err)
	}
	if req.Group != "" {
		t.Errorf("new session joined project %q, want none", req.Group)
	}
}

// Choosing a project is what aims new work at it -- the chip is the one control
// that says both "show me this project" and "start work in it".
func TestNewSessionsJoinTheSelectedProject(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "api", BaseBranch: "main"}}
	m := testModel(cfg)
	m.selectProject("auth")

	req, err := m.newSessionRequest("do a thing")
	if err != nil {
		t.Fatalf("newSessionRequest: %v", err)
	}
	if req.Group != "auth" {
		t.Errorf("new session joined project %q, want auth", req.Group)
	}
}

// twoRepos is the config the repo-following tests need: a project is only worth
// binding to a repo when there is more than one to be in.
func twoRepos() *core.Config {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{
		{ID: "api", BaseBranch: "main"},
		{ID: "web", BaseBranch: "main"},
	}
	cfg.DefaultRepo = "api"
	return cfg
}

// The whole point of a project's repo: switching to the project switches the
// repo with it, so starting a session takes one choice rather than two.
func TestSelectingAProjectSwitchesTheRepo(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "web")
	m := testModel(cfg)

	m.selectProject("frontend")

	if got := m.activeRepoID(); got != "web" {
		t.Errorf("repo chip = %q, want web", got)
	}
	req, err := m.newSessionRequest("do a thing")
	if err != nil {
		t.Fatalf("newSessionRequest: %v", err)
	}
	if req.RepoID != "web" {
		t.Errorf("new session repo = %q, want web", req.RepoID)
	}
}

// The repo filter is set from the chip, so it has to travel with it. A filter
// left pointing at the repo you just switched away from shows an empty board,
// which reads as sessions having vanished rather than as a stale filter.
func TestTheRepoFilterFollowsTheProject(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "web")
	m := testModel(cfg, sess("a", "frontend", core.LifecycleIdle, core.AgentIdle, "web"))
	m.repoFilter = "api"
	m.activeRepo = "api"

	m.selectProject("frontend")

	if m.repoFilter != "web" {
		t.Errorf("repo filter = %q, want it to follow to web", m.repoFilter)
	}
	if len(m.visible()) != 1 {
		t.Errorf("board shows %d sessions, want the project's one", len(m.visible()))
	}
}

// A filter aimed somewhere other than the chip was set deliberately and is left
// where the user put it.
func TestAnIndependentRepoFilterIsLeftAlone(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "web")
	m := testModel(cfg)
	m.activeRepo = "api"
	m.repoFilter = "web"

	m.setActiveRepo("api")

	if m.repoFilter != "web" {
		t.Errorf("repo filter = %q, want it left on web", m.repoFilter)
	}
}

// A project with no repo of its own must not clear the chip: the repo you were
// last in is a better default than none, and projects predating the binding all
// start unbound.
func TestSelectingAnUnboundProjectLeavesTheRepoAlone(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "")
	m := testModel(cfg)
	m.activeRepo = "web"

	m.selectProject("frontend")

	if got := m.activeRepoID(); got != "web" {
		t.Errorf("repo chip = %q, want it left on web", got)
	}
}

// Changing the repo while a project is selected says where that project's work
// happens now. Without it a binding is set once at creation and can never be
// corrected.
func TestChangingTheRepoRebindsTheSelectedProject(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "api")
	m := testModel(cfg)
	m.selectProject("frontend")

	m.openDropdown(focusRepo)
	m.dropdown.cursor = indexOf(m.dropdown.options, "web")
	m.applyDropdown()

	if got := m.cfg.ProjectRepo("frontend"); got != "web" {
		t.Errorf("project repo = %q, want web", got)
	}
	// And it sticks: reselecting the project comes back to the same repo.
	m.selectProject("")
	m.activeRepo = "api"
	m.selectProject("frontend")
	if got := m.activeRepoID(); got != "web" {
		t.Errorf("repo chip after reselecting = %q, want web", got)
	}
}

// With no project selected the repo chip is just the repo chip -- there is
// nothing for it to teach.
func TestChangingTheRepoWithNoProjectBindsNothing(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "api")
	m := testModel(cfg)

	m.setActiveRepo("web")

	if got := m.cfg.ProjectRepo("frontend"); got != "api" {
		t.Errorf("project repo = %q, want api left untouched", got)
	}
}

// A project named from the chip is born in the repo the chip names, so the next
// time it is selected the repo comes back with it.
func TestCreatingAProjectBindsItToTheActiveRepo(t *testing.T) {
	m := testModel(twoRepos())
	m.activeRepo = "web"

	m.openDropdown(focusProject)
	m.dropdown.cursor = indexOf(m.dropdown.options, projectNew)
	m.applyDropdown()
	m.prompt.input.SetValue("frontend")
	next, _ := m.keyPrompt(keyOf('\r'), "enter")
	m = next.(Model)

	if got := m.cfg.ProjectRepo("frontend"); got != "web" {
		t.Errorf("new project repo = %q, want web", got)
	}
}

// Named from a card instead, the session's own repo is the only evidence about
// where the project's work happens.
func TestCreatingAProjectFromACardBindsItToThatSessionsRepo(t *testing.T) {
	s := sess("a", "", core.LifecycleIdle, core.AgentIdle, "web")
	m := testModel(twoRepos(), s)

	m.openMoveProject(s)
	m.dropdown.cursor = indexOf(m.dropdown.options, projectNew)
	m.applyDropdown()
	m.prompt.input.SetValue("frontend")
	next, _ := m.keyPrompt(keyOf('\r'), "enter")
	m = next.(Model)

	if got := m.cfg.ProjectRepo("frontend"); got != "web" {
		t.Errorf("new project repo = %q, want the card's repo web", got)
	}
}

// Unregistering a repo leaves projects that named it pointing at nothing, which
// has to read as unbound rather than as a repo that will be found later.
func TestUnregisteringARepoUnbindsProjects(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "web")
	m := testModel(cfg)

	m.removeRepo("web")

	if got := m.cfg.ProjectRepo("frontend"); got != "" {
		t.Errorf("project repo = %q, want none once the repo is gone", got)
	}
}

// The picker has to say what selecting a row will do to the repo chip, since
// one selector quietly changing another is only acceptable if it is announced
// before the fact.
func TestProjectPickerNamesEachProjectsRepo(t *testing.T) {
	cfg := twoRepos()
	cfg.AddProject("frontend", "web")
	m := testModel(cfg)
	m.openDropdown(focusProject)

	label := m.dropdown.labels[indexOf(m.dropdown.options, "frontend")]
	if !strings.Contains(label, "web") {
		t.Errorf("project row reads %q, want it to name the repo", label)
	}
}

// A board with no sessions has no projects to derive, so without a row that
// creates one the picker is a dead end: the only entry is the unfiltered board.
func TestProjectPickerOffersToCreateOne(t *testing.T) {
	m := testModel(nil)
	m.openDropdown(focusProject)

	opts := projectOptions(m)
	if len(opts) == 0 || opts[len(opts)-1] != projectNew {
		t.Fatalf("project picker options = %q, want a create row last", opts)
	}
	if got := m.dropdown.labels[len(opts)-1]; !strings.Contains(got, "new project") {
		t.Errorf("create row reads %q, want it to name what it does", got)
	}
}

// Creating a project selects it, so the next session started goes there rather
// than needing the picker opened a second time.
func TestCreatingAProjectFromTheChipSelectsIt(t *testing.T) {
	m := testModel(nil)
	m.openDropdown(focusProject)
	m.dropdown.cursor = len(m.dropdown.options) - 1 // the create row
	m.applyDropdown()

	if m.mode != modePrompt || m.prompt.kind != promptNewProject {
		t.Fatalf("create row did not open the name prompt: mode %v kind %v", m.mode, m.prompt.kind)
	}
	m.prompt.input.SetValue("auth")
	next, _ := m.keyPrompt(keyOf('\r'), "enter")
	m = next.(Model)

	if m.projectFilter != "auth" {
		t.Errorf("project filter = %q, want auth", m.projectFilter)
	}
	if !hasProject(m.cfg, "auth") {
		t.Errorf("config projects = %v, want auth registered", projectNames(m.cfg))
	}
}

// A project with nothing in it yet still has to be pickable, or adding one by
// hand before starting work would not survive the picker being reopened.
func TestEmptyProjectsStayInThePicker(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []core.Project{{Name: "auth"}}
	m := testModel(cfg)
	m.openDropdown(focusProject)

	if !contains(projectOptions(m), "auth") {
		t.Errorf("picker options = %q, want the configured project", projectOptions(m))
	}
}

// Moving a card is a pick from the same list, not a retyped label: "auth" and
// "Auth" as two projects is what free text produces.
func TestMoveSessionToExistingProject(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []core.Project{{Name: "auth"}}
	s := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(cfg, s)

	m.openMoveProject(s)
	m.dropdown.cursor = indexOf(m.dropdown.options, "auth")
	m.applyDropdown()

	if s.Group != "auth" {
		t.Errorf("session project = %q, want auth", s.Group)
	}
	if m.projectFilter != "" {
		t.Errorf("moving a card also filtered the board to %q", m.projectFilter)
	}
}

// The first row moves a card back out of every project, since no project is
// where sessions start rather than a state you can only leave.
func TestMoveSessionOutOfEveryProject(t *testing.T) {
	s := sess("a", "auth", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(nil, s)

	m.openMoveProject(s)
	m.dropdown.cursor = 0
	m.applyDropdown()

	if s.Group != "" {
		t.Errorf("session project = %q, want none", s.Group)
	}
}

// Naming a project from a card both creates it and files the card under it --
// otherwise the label exists and the session that prompted it does not move.
func TestMoveSessionToANewProject(t *testing.T) {
	s := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(nil, s)

	m.openMoveProject(s)
	m.dropdown.cursor = indexOf(m.dropdown.options, projectNew)
	m.applyDropdown()
	m.prompt.input.SetValue("infra")
	next, _ := m.keyPrompt(keyOf('\r'), "enter")
	m = next.(Model)

	if s.Group != "infra" {
		t.Errorf("session project = %q, want infra", s.Group)
	}
	if !hasProject(m.cfg, "infra") {
		t.Errorf("config projects = %v, want infra registered", projectNames(m.cfg))
	}
}

// G is the board's route into that same list.
func TestGOpensTheProjectPickerForTheCard(t *testing.T) {
	s := sess("a", "", core.LifecycleIdle, core.AgentIdle, "r")
	m := press(testModel(nil, s), keyOf('G'))

	if !m.dropdown.open || m.dropdown.area != focusProject {
		t.Fatalf("G did not open the project picker: %+v", m.dropdown)
	}
	if m.dropdown.target != s.ID {
		t.Errorf("picker targets %q, want the selected session %q", m.dropdown.target, s.ID)
	}
}

// A label typed by mistake has no sessions to prune, so without removal in the
// picker itself there is no way to be rid of it.
func TestRemovingAnEmptyProject(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []core.Project{{Name: "typpo"}}
	m := testModel(cfg)
	m.openDropdown(focusProject)
	m.dropdown.cursor = indexOf(m.dropdown.options, "typpo")

	next, _ := m.keyDropdown("x")
	m = next.(Model)

	if hasProject(m.cfg, "typpo") {
		t.Errorf("config projects = %v, want the project gone", projectNames(m.cfg))
	}
	if contains(projectOptions(m), "typpo") {
		t.Errorf("picker still offers %q", "typpo")
	}
}

// Removing a project that still holds sessions would unfile them silently, so
// it is refused and says why.
func TestRemovingAProjectInUseIsRefused(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []core.Project{{Name: "auth"}}
	s := sess("a", "auth", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(cfg, s)
	m.openDropdown(focusProject)
	m.dropdown.cursor = indexOf(m.dropdown.options, "auth")

	next, cmd := m.keyDropdown("x")
	m = next.(Model)

	if !hasProject(m.cfg, "auth") {
		t.Errorf("config projects = %v, want auth kept", projectNames(m.cfg))
	}
	if s.Group != "auth" {
		t.Errorf("session project = %q, want it untouched", s.Group)
	}
	if cmd == nil {
		t.Fatal("refusal said nothing")
	}
	msg, ok := cmd().(statusMsg)
	if !ok || !msg.isErr {
		t.Fatalf("refusal message = %#v, want an error status", cmd())
	}
}

// The create row is not a project, and the row that means "no filter" is not a
// project either; x on either would otherwise remove whatever the sentinel
// happened to sort next to.
func TestRemovingIsAnoopOnTheRowsThatAreNotProjects(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []core.Project{{Name: "auth"}}
	m := testModel(cfg)
	m.openDropdown(focusProject)

	for _, cursor := range []int{0, len(m.dropdown.options) - 1} {
		m.dropdown.cursor = cursor
		next, _ := m.keyDropdown("x")
		m = next.(Model)
		if !hasProject(m.cfg, "auth") {
			t.Fatalf("x on row %d removed a project: projects = %v", cursor, projectNames(m.cfg))
		}
	}
}

func hasProject(cfg *core.Config, name string) bool {
	_, ok := cfg.Project(name)
	return ok
}

func projectNames(cfg *core.Config) []string {
	out := make([]string, 0, len(cfg.Groups))
	for _, p := range cfg.Groups {
		out = append(out, p.Name)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
