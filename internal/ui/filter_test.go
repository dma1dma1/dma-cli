package ui

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// What the project filter does to the selection: the panel shows the selected
// card's session, so a filter that hides that card has to take the panel with
// it. Narrowing lands on the empty panel; widening leaves the selection where
// the user put it.

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

