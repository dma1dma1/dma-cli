package core

import "testing"

// readyPR is a pull request with nothing left blocking it. Each case below
// spoils exactly one of those facts, so the test says which fact mattered.
func readyPR() *Session {
	return &Session{
		Title: "auth", PRNumber: 7, PRState: PROpen,
		PRMergeable: MergeClean, PRCI: CIPass, PRReview: ReviewApproved,
	}
}

func TestPRReadyToMergeAcceptsAGreenOpenPR(t *testing.T) {
	if !readyPR().PRReadyToMerge() {
		t.Error("a clean, passing, approved open PR was not read as ready")
	}
}

// An unreviewed PR still merges: review is only blocking when a reviewer has
// actually asked for changes.
func TestPRReadyToMergeAcceptsAnUnreviewedPR(t *testing.T) {
	s := readyPR()
	s.PRReview = ReviewNone
	if !s.PRReadyToMerge() {
		t.Error("an unreviewed PR was not read as ready")
	}
}

// A repo with no checks configured has nothing to wait for, and must not wait
// forever for a rollup that will never arrive.
func TestPRReadyToMergeAcceptsARepoWithNoChecks(t *testing.T) {
	s := readyPR()
	s.PRCI = CINone
	if !s.PRReadyToMerge() {
		t.Error("a PR in a repo with no CI was not read as ready")
	}
}

func TestPRReadyToMergeRejectsWhatIsStillBlocked(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(*Session)
	}{
		{"conflicts", func(s *Session) { s.PRMergeable = MergeConflicts }},
		// Unknown is GitHub still computing the merge commit. Treating it as clean
		// would announce PRs that turn out to conflict.
		{"mergeability not yet computed", func(s *Session) { s.PRMergeable = MergeUnknown }},
		{"failing ci", func(s *Session) { s.PRCI = CIFail }},
		{"unfinished ci", func(s *Session) { s.PRCI = CIPending }},
		{"changes requested", func(s *Session) { s.PRReview = ReviewChangesRequested }},
		{"draft", func(s *Session) { s.PRState = PRDraft }},
		{"merged", func(s *Session) { s.PRState = PRMerged }},
		{"closed", func(s *Session) { s.PRState = PRClosed }},
		// The queue merges it without the user, so there is nothing to hand back.
		{"queued", func(s *Session) { s.PRQueued = true }},
		{"no pr at all", func(s *Session) { s.PRNumber, s.PRState = 0, PRNone }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := readyPR()
			tc.spoil(s)
			if s.PRReadyToMerge() {
				t.Errorf("%s was read as ready to merge", tc.name)
			}
		})
	}
}

// The notification belongs to the transition. A PR sits ready for as long as the
// user takes to merge it, and the board polls throughout.
func TestClaimMergeableNoticeFiresOncePerTransition(t *testing.T) {
	s := readyPR()
	if !s.ClaimMergeableNotice() {
		t.Fatal("the first claim on a ready PR was refused")
	}
	for i := 0; i < 3; i++ {
		if s.ClaimMergeableNotice() {
			t.Fatalf("claim %d re-announced a PR that was already announced", i+2)
		}
	}
}

// A PR that goes red and comes back green is news again, so the claim has to be
// released on the way out.
func TestClaimMergeableNoticeIsReleasedWhenThePRStopsBeingReady(t *testing.T) {
	s := readyPR()
	s.ClaimMergeableNotice()

	s.PRCI = CIPending
	if s.ClaimMergeableNotice() {
		t.Fatal("a PR with unfinished CI was announced")
	}
	if s.PRMergeableNotified {
		t.Fatal("the claim survived the PR ceasing to be ready")
	}

	s.PRCI = CIPass
	if !s.ClaimMergeableNotice() {
		t.Error("a PR that came back green was not announced again")
	}
}

// A PR that was already ready when the board last shut down must not be
// re-announced on launch, which is why the claim is persisted.
func TestMergeableClaimSurvivesARoundTrip(t *testing.T) {
	s := readyPR()
	s.ClaimMergeableNotice()

	if err := SaveSessions([]*Session{s}); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadSessions()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d sessions, want 1", len(loaded))
	}
	if loaded[0].ClaimMergeableNotice() {
		t.Error("a reloaded PR was announced a second time")
	}
}
