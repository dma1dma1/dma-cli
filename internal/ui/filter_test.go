package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// What a filter does to the selection, for both the project chip and the repo
// filter: the panel shows the selected card's session, so a filter that hides
// that card has to take the panel with it. Narrowing lands on the empty panel;
// widening leaves the selection where the user put it.

// twoProjects is a board with a session filed under each of two projects, which
// is what the panel-emptying tests need: something to switch away from, and
// something on the other side that the switch must not silently open.
func twoProjects() (*core.Config, *core.Session, *core.Session) {
	cfg := core.DefaultConfig()
	cfg.Groups = []core.Project{{Name: "auth"}, {Name: "infra"}}
	return cfg,
		sess("a", "auth", core.LifecycleIdle, core.AgentIdle, "r"),
		sess("b", "infra", core.LifecycleIdle, core.AgentIdle, "r")
}

// Switching project switches what the panel shows too. A session filtered off
// the board is still selected as far as its id goes, so the project you left
// used to keep its agent on screen with no card above it to explain why -- and it
// only let go once you clicked or moved onto something else.
func TestSwitchingProjectEmptiesThePanel(t *testing.T) {
	cfg, a, b := twoProjects()
	m := testModel(cfg, a, b)
	m.selectProject("auth")
	m.preview = "output from auth's agent"

	m.selectProject("infra")

	if m.selected() != nil {
		t.Errorf("panel still shows %q after switching project", m.selectedID)
	}
	if m.preview != "" {
		t.Errorf("preview kept the previous session's output: %q", m.preview)
	}
	body := strings.Join(m.previewBody(6, 60), "\n")
	if !strings.Contains(body, "no session selected") {
		t.Errorf("panel body = %q, want the same empty state a board with no sessions shows", body)
	}
}

// The empty panel has to outlast the next poll. Nothing selected is otherwise
// read as a cursor to repair, and the board's own refresh would open a session
// a second after the switch appeared to have cleared it.
func TestTheEmptyPanelSurvivesARefresh(t *testing.T) {
	cfg, a, b := twoProjects()
	m := testModel(cfg, a, b)
	m.selectProject("auth")

	m.selectProject("infra")
	m.rebuild()

	if m.selectedID != "" {
		t.Errorf("refresh selected %q, want the panel left empty", m.selectedID)
	}
}

// Empty is where the switch lands, not where it leaves you: the first move goes
// to the project just switched to, so the emptiness costs one keystroke and no
// backtracking.
func TestMovingAfterASwitchPicksFromTheNewProject(t *testing.T) {
	cfg, a, b := twoProjects()
	m := testModel(cfg, a, b)
	m.selectProject("auth")
	m.selectProject("infra")

	m = press(m, keyOf('j'))

	if m.selectedID != b.ID {
		t.Errorf("selected %q after moving, want the new project's session %q", m.selectedID, b.ID)
	}
}

// Widening to every project is not switching away from anything -- the card you
// were on is still on the board, and blanking the panel under it would throw
// away your place for nothing.
func TestWideningToAllProjectsKeepsTheSelection(t *testing.T) {
	cfg, a, b := twoProjects()
	m := testModel(cfg, a, b)
	m.selectProject("auth")

	m.selectProject("")

	if m.selectedID != a.ID {
		t.Errorf("selected %q, want %q kept: its card never left the board", m.selectedID, a.ID)
	}
}

// repoBoard is a session in each of two registered repos, with the chip aimed at
// the second: pressing f from here hides the selected card.
func repoBoard() (Model, *core.Session, *core.Session) {
	a := sess("a", "", core.LifecycleIdle, core.AgentIdle, "api")
	b := sess("b", "", core.LifecycleIdle, core.AgentIdle, "web")
	m := testModel(twoRepos(), a, b)
	m.selectedID = a.ID
	m.activeRepo = "web"
	return m, a, b
}

// The repo filter is the same rule as the project chip: f narrows the board to
// the chip's repo, and a session from the other one has no card left to sit
// under.
func TestFilteringToARepoEmptiesThePanel(t *testing.T) {
	m, _, _ := repoBoard()

	m = press(m, keyOf('f'))

	if m.selectedID != "" {
		t.Errorf("panel still shows %q, want it emptied with the card", m.selectedID)
	}
}

// Only the hidden card is given up. A filter that keeps the selected session on
// the board is not a change of what you are looking at.
func TestFilteringToTheSelectedSessionsRepoKeepsIt(t *testing.T) {
	m, a, _ := repoBoard()
	m.activeRepo = "api"

	m = press(m, keyOf('f'))

	if m.selectedID != a.ID {
		t.Errorf("selected %q, want %q kept: the filter is aimed at its own repo", m.selectedID, a.ID)
	}
}

// The filter travels with the repo chip, so changing repo refilters the board --
// and takes the panel with it just as pressing f would.
func TestSwitchingRepoUnderAFilterEmptiesThePanel(t *testing.T) {
	m, _, _ := repoBoard()
	m.activeRepo, m.repoFilter = "api", "api"

	m.setActiveRepo("web")

	if m.selectedID != "" {
		t.Errorf("panel still shows %q after the filter followed the chip", m.selectedID)
	}
}

// Turning the filter off only ever puts cards back, and the one you were on is
// among them.
func TestClearingTheRepoFilterKeepsTheSelection(t *testing.T) {
	m, a, _ := repoBoard()
	m.activeRepo, m.repoFilter = "api", "api"

	m = press(m, keyOf('f'))

	if m.repoFilter != "" {
		t.Fatalf("repo filter = %q, want it cleared", m.repoFilter)
	}
	if m.selectedID != a.ID {
		t.Errorf("selected %q, want %q kept: clearing a filter hides nothing", m.selectedID, a.ID)
	}
}
