package summarize

import (
	"errors"
	"strings"
	"testing"
)

// never stands in for the model on paths that must not reach one.
func never(t *testing.T) func(string) (string, error) {
	t.Helper()
	return func(string) (string, error) {
		t.Fatal("asked a model to name a task that was already named")
		return "", nil
	}
}

// The whole point: a card carries the shape of the work, not the opening words
// of the paragraph it was described in.
func TestASummaryReplacesThePastedTask(t *testing.T) {
	task := "the login test flakes on CI about 1 in 5 runs, can you look at why and fix it"
	got := condense(task, func(string) (string, error) { return "flaky CI login test\n", nil })
	if got != "flaky CI login test" {
		t.Errorf("title = %q, want %q", got, "flaky CI login test")
	}
}

// Text already the length of a title is one. Paying seconds to have a model
// rewrite "rate limiter" is worse than free -- it also stops being what the
// user typed.
func TestShortTasksAreLeftAlone(t *testing.T) {
	for _, task := range []string{"rate limiter", "  audit logging  ", "fix the flaky login test on CI"} {
		want := strings.TrimSpace(task)
		if got := condense(task, never(t)); got != want {
			t.Errorf("condense(%q) = %q, want %q", task, got, want)
		}
	}
}

// The model is optional infrastructure: no claude on PATH, no network, no auth,
// and a session still starts with a readable card.
func TestAFailedCallFallsBackToTheTypedText(t *testing.T) {
	task := "please go and rewrite the whole billing webhook handler so that retries are idempotent"
	got := condense(task, func(string) (string, error) { return "", errors.New("exec: claude: not found") })

	if got == "" {
		t.Fatal("a failed summary left the card with no title at all")
	}
	if !strings.HasPrefix(task, got) {
		t.Errorf("fallback %q is not the opening of the task", got)
	}
	if !isTitle(got) {
		t.Errorf("fallback %q is too long for a card", got)
	}
}

// A model that ignores the instructions answers with a sentence, a refusal or a
// list. Any of those on a card is worse than the words the user typed, so the
// answer has to look like a title before it is used as one.
func TestAnAnswerThatIsNotATitleIsRejected(t *testing.T) {
	task := "figure out why the nightly export job silently drops rows when the source table is being vacuumed"
	answers := []string{
		"Sure! Here is a title for your task: nightly export drops rows",
		"- nightly export\n- dropped rows\n- vacuum",
		"",
		"   \n  \n",
	}
	for _, answer := range answers {
		got := condense(task, func(string) (string, error) { return answer, nil })
		if !strings.HasPrefix(task, got) {
			t.Errorf("answer %q was used as the title %q instead of falling back", answer, got)
		}
	}
}

// Models wrap titles in the punctuation of whatever format they think they are
// producing. The words are fine; the quotes and bullets are not.
func TestCleanStripsTheDecorationAroundATitle(t *testing.T) {
	for answer, want := range map[string]string{
		`"flaky CI login test"`:     "flaky CI login test",
		"`token refresh`":           "token refresh",
		"* audit logging":           "audit logging",
		"idempotent retries.":       "idempotent retries",
		"  session   cookies  \n\n": "session cookies",
	} {
		if got := clean(answer); got != want {
			t.Errorf("clean(%q) = %q, want %q", answer, got, want)
		}
	}
}

// The fallback cuts at a word. A card that reads "the nightly export job sile"
// looks like a rendering bug rather than a shortened title.
func TestTheFallbackCutsAtAWordBoundary(t *testing.T) {
	got := Shorten("investigate the intermittent authentication failures reported by several enterprise customers")
	if got == "" {
		t.Fatal("Shorten dropped the whole task")
	}
	if !isTitle(got) {
		t.Errorf("Shorten returned %q, which is still too long", got)
	}
	if !strings.HasSuffix(got, "authentication") && strings.HasSuffix(got, "authenticat") {
		t.Errorf("Shorten cut mid-word: %q", got)
	}
	for _, word := range strings.Fields(got) {
		if !strings.Contains("investigate the intermittent authentication failures reported by several enterprise customers", word) {
			t.Errorf("Shorten invented the word %q", word)
		}
	}
}

// A one-word task longer than a card is the only case where cutting mid-word
// beats returning nothing.
func TestASingleUnbrokenWordIsStillCut(t *testing.T) {
	got := Shorten(strings.Repeat("x", 200))
	if len([]rune(got)) != maxRunes {
		t.Errorf("Shorten returned %d runes, want %d", len([]rune(got)), maxRunes)
	}
}
