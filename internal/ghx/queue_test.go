package ghx

import "testing"

func TestParseQueueState(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want QueueState
	}{
		{
			"queue enabled and waiting",
			`{"data":{"repository":{"pullRequest":{"isInMergeQueue":true,"isMergeQueueEnabled":true}}}}`,
			QueueState{Enabled: true, InQueue: true},
		},
		{
			"queue enabled, not yet joined",
			`{"data":{"repository":{"pullRequest":{"isInMergeQueue":false,"isMergeQueueEnabled":true}}}}`,
			QueueState{Enabled: true},
		},
		{
			"no queue on the base branch",
			`{"data":{"repository":{"pullRequest":{"isInMergeQueue":false,"isMergeQueueEnabled":false}}}}`,
			QueueState{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseQueueState(c.out, "owner/name")
			if err != nil {
				t.Fatalf("parseQueueState: %v", err)
			}
			if got != c.want {
				t.Errorf("parseQueueState = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseQueueStateRejectsGarbage(t *testing.T) {
	if _, err := parseQueueState("not json", "owner/name"); err == nil {
		t.Fatal("expected an error for unparseable output")
	}
}

func TestSplitRemote(t *testing.T) {
	cases := []struct {
		remote, owner, name string
		ok                  bool
	}{
		{"owner/name", "owner", "name", true},
		// gh accepts a host-qualified remote; the pair is still the tail.
		{"ghe.example.com/owner/name", "owner", "name", true},
		{"name", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		owner, name, ok := splitRemote(c.remote)
		if owner != c.owner || name != c.name || ok != c.ok {
			t.Errorf("splitRemote(%q) = %q, %q, %v, want %q, %q, %v",
				c.remote, owner, name, ok, c.owner, c.name, c.ok)
		}
	}
}

// gh refuses a direct merge on a queued branch rather than quietly doing
// something else, and that refusal is what tells us to enqueue instead when the
// queue query could not answer.
func TestRefusedForMergeQueue(t *testing.T) {
	refusal := &Error{Kind: ErrOther, Msg: "X Cannot use `-d` or `--delete-branch` when merge queue enabled"}
	if !refusedForMergeQueue(refusal) {
		t.Error("gh's merge queue refusal was not recognized")
	}
	if refusedForMergeQueue(&Error{Kind: ErrOffline, Msg: "dial tcp: no such host"}) {
		t.Error("an unrelated failure was read as a merge queue refusal")
	}
	if refusedForMergeQueue(nil) {
		t.Error("a nil error was read as a merge queue refusal")
	}
}

// A pull request that was already queued is a warning and a zero exit, so the
// outcome has to be read out of what gh said.
func TestMergeOutcomeReadsAlreadyQueued(t *testing.T) {
	stderr := "! Pull request owner/name#7 is already queued to merge\n"
	if got := mergeOutcome(stderr, MergeQueued); got != MergeAlreadyQueued {
		t.Errorf("outcome = %v, want MergeAlreadyQueued", got)
	}
	if got := mergeOutcome("", MergeCompleted); got != MergeCompleted {
		t.Errorf("outcome = %v, want MergeCompleted", got)
	}
	if got := mergeOutcome("", MergeQueued); got != MergeQueued {
		t.Errorf("outcome = %v, want MergeQueued", got)
	}
}
