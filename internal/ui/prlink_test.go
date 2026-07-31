package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/ghx"
)

func prSess(id string, number int, url string) *core.Session {
	s := branchSess(id, "r1", "feat-"+id, core.LifecyclePROpen)
	s.PRNumber, s.PRState, s.PRURL = number, core.PROpen, url
	return s
}

// The link the session already knows is used as it stands: opening a PR must
// not wait on a network round trip when the poll has already answered.
//
// These assert on the target rather than on the command, because running the
// command is the point at which a browser opens and the clipboard changes --
// not something a test run should do to the machine it runs on.
func TestPRLinkUsesTheKnownAddress(t *testing.T) {
	m := testModel(nil, prSess("a", 42, "https://github.com/owner/name/pull/42"))

	url, remote, err := m.prLinkTarget(m.selected())
	if err != nil {
		t.Fatalf("prLinkTarget: %v", err)
	}
	if url != "https://github.com/owner/name/pull/42" {
		t.Errorf("url = %q, want the stored address", url)
	}
	if remote != "" {
		t.Errorf("a known link was still going to be looked up against %q", remote)
	}
}

// A PR that predates the stored address -- most often one already merged, which
// is no longer polled -- is resolved on demand rather than left unopenable.
func TestPRLinkResolvesAnUnknownAddress(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "r1", Path: "/tmp/r1", Remote: "owner/name"}}
	m := testModel(cfg, prSess("a", 42, ""))

	url, remote, err := m.prLinkTarget(m.selected())
	if err != nil {
		t.Fatalf("prLinkTarget: %v", err)
	}
	if url != "" || remote != "owner/name" {
		t.Errorf("target = (%q, %q), want a lookup against owner/name", url, remote)
	}
}

// Without a remote there is nothing to ask, and the reason has to reach the
// footer -- the key would otherwise look broken.
func TestPRLinkReportsAMissingRemote(t *testing.T) {
	cfg := core.DefaultConfig()
	cfg.Repos = []core.Repo{{ID: "r1", Path: "/tmp/r1"}}
	m := testModel(cfg, prSess("a", 42, ""))

	if _, _, err := m.prLinkTarget(m.selected()); err == nil {
		t.Fatal("a PR with no remote to resolve against reported no problem")
	}
}

// Pressing the key on a session that has not shipped yet says so, and says what
// to press instead.
func TestPRLinkOnASessionWithNoPR(t *testing.T) {
	m := testModel(nil, branchSess("a", "r1", "feat-a", core.LifecycleActive))

	_, _, err := m.prLinkTarget(m.selected())
	if err == nil {
		t.Fatal("a session with no PR reported no problem")
	}
	if !strings.Contains(err.Error(), "press s") {
		t.Errorf("the message does not point anywhere useful: %q", err)
	}
}

// o and y reach the same place from the board and from the diff, since the diff
// is where you decide the PR is worth sharing.
func TestPRLinkKeysAreBoundInBothModes(t *testing.T) {
	for _, key := range []string{"o", "y"} {
		m := testModel(nil, prSess("a", 42, "https://example.com/pull/42"))
		if _, cmd := m.keyBoard(key); cmd == nil {
			t.Errorf("%q did nothing on the board", key)
		}
		m.mode = modeDiff
		if _, cmd := m.keyDiff(tea.KeyPressMsg{}, key); cmd == nil {
			t.Errorf("%q did nothing in the diff", key)
		}
	}
}

// A resolved address is kept, so the next open or copy of that PR is local.
func TestResolvedLinkIsCachedOnTheSession(t *testing.T) {
	s := prSess("a", 42, "")
	m := testModel(nil, s)

	m.Update(prLinkMsg{id: "a", url: "https://github.com/owner/name/pull/42", action: linkOpen})
	if s.PRURL == "" {
		t.Error("the resolved link was not stored on the session")
	}
}

// The poll is the normal source of the link: every card that has a PR should
// know how to reach it without anyone pressing anything.
func TestPRSyncStoresTheLink(t *testing.T) {
	s := branchSess("a", "r1", "feat-a", core.LifecycleActive)
	m := testModel(nil, s)

	m.handlePRSync(prSyncMsg{
		repoID: "r1",
		poll: ghx.Poll{
			Open: map[string]ghx.PR{"feat-a": {
				Number: 42, Branch: "feat-a", State: core.PROpen,
				URL: "https://github.com/owner/name/pull/42",
			}},
			Answered: map[string]bool{"feat-a": true},
		},
	})
	if s.PRURL != "https://github.com/owner/name/pull/42" {
		t.Errorf("PRURL = %q, want the polled address", s.PRURL)
	}
}

// Opening a PR from the board is the one moment the link is certainly wanted,
// and it arrives with the PR itself rather than a poll interval later.
func TestShippingStoresTheLink(t *testing.T) {
	s := branchSess("a", "r1", "feat-a", core.LifecycleActive)
	m := testModel(nil, s)

	m.handleShipped(shippedMsg{id: "a", branch: "feat-a", number: 42,
		url: "https://github.com/owner/name/pull/42"})
	if s.PRURL != "https://github.com/owner/name/pull/42" {
		t.Errorf("PRURL = %q, want the address gh printed", s.PRURL)
	}
}
