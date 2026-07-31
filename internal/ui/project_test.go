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
	if !contains(m.cfg.Groups, "auth") {
		t.Errorf("config groups = %q, want auth registered", m.cfg.Groups)
	}
}

// A project with nothing in it yet still has to be pickable, or adding one by
// hand before starting work would not survive the picker being reopened.
func TestEmptyProjectsStayInThePicker(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []string{"auth"}
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
	cfg.Groups = []string{"auth"}
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
	if !contains(m.cfg.Groups, "infra") {
		t.Errorf("config groups = %q, want infra registered", m.cfg.Groups)
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
	cfg.Groups = []string{"typpo"}
	m := testModel(cfg)
	m.openDropdown(focusProject)
	m.dropdown.cursor = indexOf(m.dropdown.options, "typpo")

	next, _ := m.keyDropdown("x")
	m = next.(Model)

	if contains(m.cfg.Groups, "typpo") {
		t.Errorf("config groups = %q, want the project gone", m.cfg.Groups)
	}
	if contains(projectOptions(m), "typpo") {
		t.Errorf("picker still offers %q", "typpo")
	}
}

// Removing a project that still holds sessions would unfile them silently, so
// it is refused and says why.
func TestRemovingAProjectInUseIsRefused(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Groups = []string{"auth"}
	s := sess("a", "auth", core.LifecycleIdle, core.AgentIdle, "r")
	m := testModel(cfg, s)
	m.openDropdown(focusProject)
	m.dropdown.cursor = indexOf(m.dropdown.options, "auth")

	next, cmd := m.keyDropdown("x")
	m = next.(Model)

	if !contains(m.cfg.Groups, "auth") {
		t.Errorf("config groups = %q, want auth kept", m.cfg.Groups)
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
	cfg.Groups = []string{"auth"}
	m := testModel(cfg)
	m.openDropdown(focusProject)

	for _, cursor := range []int{0, len(m.dropdown.options) - 1} {
		m.dropdown.cursor = cursor
		next, _ := m.keyDropdown("x")
		m = next.(Model)
		if !contains(m.cfg.Groups, "auth") {
			t.Fatalf("x on row %d removed a project: groups = %q", cursor, m.cfg.Groups)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
