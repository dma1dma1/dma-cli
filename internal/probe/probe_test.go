package probe

import (
	"testing"
)

func TestAwaitingInputRecognizesApprovalShapes(t *testing.T) {
	cases := []string{
		"Do you want to proceed? [y/n]",
		"Allow this command?",
		"Approve the edit to main.go?",
		"  1. Yes  2. No",
		"waiting for your approval",
		"Press enter to continue",
	}
	for _, line := range cases {
		if _, ok := awaitingInput("some earlier output\n" + line + "\n"); !ok {
			t.Errorf("did not recognize a prompt in %q", line)
		}
	}
}

func TestAwaitingInputIgnoresOrdinaryOutput(t *testing.T) {
	content := `Reading files…
  wrote internal/server/http.go
  ran tests: 42 passed
Done in 18s.`
	if detail, ok := awaitingInput(content); ok {
		t.Errorf("false positive on ordinary output: %q", detail)
	}
}

// A prompt printed long ago is not a live prompt. Only the tail of the pane is
// where a real request for input would sit.
func TestAwaitingInputOnlyLooksAtTheTail(t *testing.T) {
	var content string
	content += "Do you want to proceed? [y/n]\n"
	for i := 0; i < 30; i++ {
		content += "later unrelated output line\n"
	}
	if _, ok := awaitingInput(content); ok {
		t.Error("matched a prompt that had scrolled far up the pane")
	}
}

func TestAwaitingInputSeesThroughANSI(t *testing.T) {
	line := "\x1b[1;33mAllow this command?\x1b[0m"
	if _, ok := awaitingInput(line + "\n"); !ok {
		t.Error("styling hid a prompt from the matcher")
	}
}

func TestStripANSI(t *testing.T) {
	if got := stripANSI("\x1b[31mred\x1b[0m"); got != "red" {
		t.Fatalf("stripANSI = %q, want \"red\"", got)
	}
}

func TestForgetDropsUnknownSessions(t *testing.T) {
	p := New()
	p.last["gone"] = sample{}
	p.last["kept"] = sample{}
	p.Forget(map[string]bool{"kept": true})
	if _, ok := p.last["gone"]; ok {
		t.Error("state for a removed session was retained")
	}
	if _, ok := p.last["kept"]; !ok {
		t.Error("state for a live session was dropped")
	}
}
