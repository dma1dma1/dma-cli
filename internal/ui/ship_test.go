package ui

import (
	"strings"
	"testing"
	"time"
)

// s hands the work to the agent rather than doing the git itself, so it behaves
// like the other keys that type into a session: the keystroke is attributed to
// the user and the panel re-reads the pane while the agent answers.
func TestShipKeyIsAttributedToTheUser(t *testing.T) {
	m := testModel(nil, liveSess("a"))

	before := time.Now()
	next, cmd := m.sessionAction("s")
	got := next.(Model)

	if cmd == nil {
		t.Fatal("s produced no command; nothing was sent to the agent")
	}
	if got.touchedAt["a"].Before(before) {
		t.Error("the request was not recorded against the session, so the prober will read the reply as unprompted output")
	}
	if !got.echoing {
		t.Error("s did not start the echo ticker; the panel would hold a stale frame while the agent starts")
	}
}

// Nothing is typed into a terminal that is gone. The old s did its own git and
// so worked on a dead session; this one has nobody to ask, and says so instead
// of looking like it worked.
func TestShipKeyNeedsALiveTerminal(t *testing.T) {
	s := liveSess("a")
	s.TmuxAlive = false
	m := testModel(nil, s)

	next, cmd := m.sessionAction("s")
	notice, ok := drainNotice(t, cmd)
	if !ok {
		t.Fatal("s on a dead session said nothing")
	}
	if !strings.Contains(notice.text, "not running") {
		t.Errorf("notice = %q, want it to name the dead terminal", notice.text)
	}
	if got := next.(Model); got.echoing {
		t.Error("started capturing a pane that is not there")
	}
}

// The message is the whole feature: the permission and the instruction not to
// report back are what keep the agent from parking the card on a confirmation.
func TestShipRequestGrantsPermissionAndDoesNotHandBack(t *testing.T) {
	for _, want := range []string{"Commit, push, and open a PR", "full permission", "Do not come back to me"} {
		if !strings.Contains(shipRequest, want) {
			t.Errorf("ship request lost %q; it reads:\n%s", want, shipRequest)
		}
	}
	if strings.Contains(shipRequest, "\n") {
		t.Error("ship request spans lines; a newline mid-message submits half of it")
	}
}

func TestShepherdRequestShipsAndConvergesWithoutMerging(t *testing.T) {
	for _, want := range []string{
		"Commit, push, and open a PR",
		"full permission",
		"shepherd",
		"monitor CI and review threads",
		"fix valid failures and feedback",
		"commit and push each fix",
		"CI passes and all review threads are resolved",
		"Do not merge",
		"ready to merge or you are genuinely blocked",
	} {
		if !strings.Contains(shepherdRequest, want) {
			t.Errorf("shepherd request lost %q; it reads:\n%s", want, shepherdRequest)
		}
	}
	if strings.Contains(shepherdRequest, "\n") {
		t.Error("shepherd request spans lines; a newline mid-message submits half of it")
	}
	if strings.Contains(strings.ToLower(shipRequest), "shepherd") {
		t.Error("lowercase s unexpectedly shepherds; the two shortcuts should remain distinct")
	}
}

func TestShepherdKeyUsesTheShipPath(t *testing.T) {
	m := testModel(nil, liveSess("a"))

	before := time.Now()
	next, cmd := m.sessionAction("S")
	got := next.(Model)

	if cmd == nil {
		t.Fatal("S produced no command; nothing was sent to the agent")
	}
	if got.touchedAt["a"].Before(before) {
		t.Error("the shepherd request was not recorded against the session")
	}
	if !got.echoing {
		t.Error("S did not start the echo ticker")
	}
}

func TestShepherdKeyNeedsALiveTerminal(t *testing.T) {
	s := liveSess("a")
	s.TmuxAlive = false
	m := testModel(nil, s)

	next, cmd := m.sessionAction("S")
	notice, ok := drainNotice(t, cmd)
	if !ok {
		t.Fatal("S on a dead session said nothing")
	}
	if !strings.Contains(notice.text, "not running") {
		t.Errorf("notice = %q, want it to name the dead terminal", notice.text)
	}
	if got := next.(Model); got.echoing {
		t.Error("started capturing a pane that is not there")
	}
}
