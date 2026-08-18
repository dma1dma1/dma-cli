package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeJSONL writes a transcript, creating the directories above it.
func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeHome points the package at a temp transcript store and returns the
// projects directory inside it.
func claudeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	return filepath.Join(dir, "projects")
}

func codexHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	return filepath.Join(dir, "sessions")
}

func TestFindClaudeReadsCwdAndTitle(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-Users-someone-proj", "abc-123.jsonl"),
		`{"type":"mode","sessionId":"abc-123"}`,
		`{"type":"user","cwd":"/Users/someone/proj","message":{"content":"Fix the flaky login test"}}`,
		`{"type":"assistant","cwd":"/Users/someone/proj","message":{"content":"sure"}}`,
	)

	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Cwd != "/Users/someone/proj" {
		t.Errorf("cwd = %q", c.Cwd)
	}
	if c.Title != "Fix the flaky login test" {
		t.Errorf("title = %q", c.Title)
	}
	if c.ID != "abc-123" || c.Profile != "claude" {
		t.Errorf("id/profile = %q/%q", c.ID, c.Profile)
	}
}

// The directory a transcript sits in is the working directory with its
// separators replaced, which cannot be decoded back into a path -- a project
// named "dma-cli" and one at "dma/cli" produce the same directory name. The cwd
// has to come from inside the file.
func TestFindClaudePrefersRecordedCwdOverTheDirectoryName(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-Users-someone-dma-cli", "abc-123.jsonl"),
		`{"type":"user","cwd":"/Users/someone/dma/cli","message":{"content":"hi"}}`,
	)
	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Cwd != "/Users/someone/dma/cli" {
		t.Errorf("cwd = %q, want the path the record carries", c.Cwd)
	}
}

// The agent's own generated title is a name; the opening prompt is a paragraph.
func TestFindClaudePrefersTheGeneratedTitle(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-p", "abc-123.jsonl"),
		`{"type":"user","cwd":"/p","message":{"content":"please could you have a look at the login test, it fails maybe one run in five"}}`,
		`{"type":"ai-title","aiTitle":"Flaky login test"}`,
	)
	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Title != "Flaky login test" {
		t.Errorf("title = %q", c.Title)
	}
}

func TestFindClaudeReadsBlockContent(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-p", "abc-123.jsonl"),
		`{"type":"user","cwd":"/p","message":{"content":[{"type":"text","text":"Add a retry"}]}}`,
	)
	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Title != "Add a retry" {
		t.Errorf("title = %q", c.Title)
	}
}

// Both agents inject context of their own into the opening turn. What the
// person typed is the first line that is not one of those.
func TestFindSkipsInjectedOpeningText(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-p", "abc-123.jsonl"),
		`{"type":"user","cwd":"/p","message":{"content":"<system-reminder>be nice</system-reminder>\nActually fix the parser"}}`,
	)
	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Title != "Actually fix the parser" {
		t.Errorf("title = %q", c.Title)
	}
}

func TestFindCodexReadsMetaAndUserEvent(t *testing.T) {
	root := codexHome(t)
	writeJSONL(t, filepath.Join(root, "2026", "08", "11", "rollout-2026-08-11T19-26-01-019ff3ca-7e37.jsonl"),
		`{"type":"session_meta","payload":{"session_id":"019ff3ca-7e37","cwd":"/Users/someone/proj"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>ignore me</recommended_plugins>"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"Rewrite the retry loop"}}`,
	)
	c, err := Find("codex", "019ff3ca-7e37")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Cwd != "/Users/someone/proj" {
		t.Errorf("cwd = %q", c.Cwd)
	}
	// The injected message above is a user message as far as the model's own
	// list is concerned, and is not what anyone typed.
	if c.Title != "Rewrite the retry loop" {
		t.Errorf("title = %q", c.Title)
	}
}

func TestFindReportsMissingSessions(t *testing.T) {
	claudeHome(t)
	_, err := Find("claude", "nope")
	var notFound *NotFoundError
	if !asError(err, &notFound) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}

func TestFindRejectsAgentsWithNoReader(t *testing.T) {
	_, err := Find("aider", "abc")
	var unsupported *UnsupportedError
	if !asError(err, &unsupported) {
		t.Fatalf("err = %v, want UnsupportedError", err)
	}
	// The message has to name what can be attached; the id is not the problem.
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("message does not name a supported agent: %v", err)
	}
}

// A session id is used as a glob, so one holding a pattern character must fail
// to match rather than matching somebody else's transcript.
func TestFindDoesNotTreatTheIDAsAPattern(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-p", "abc-123.jsonl"), `{"type":"user","cwd":"/p","message":{"content":"hi"}}`)
	if _, err := Find("claude", "abc-*"); err == nil {
		t.Fatal("a wildcard id matched a real transcript")
	}
}

func TestListIsNewestFirst(t *testing.T) {
	root := claudeHome(t)
	for i, id := range []string{"old", "middle", "new"} {
		path := filepath.Join(root, "-p", id+".jsonl")
		writeJSONL(t, path, `{"type":"user","cwd":"/p","message":{"content":"prompt `+id+`"}}`)
		when := time.Now().Add(time.Duration(i-3) * time.Hour)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}

	got, err := List("claude", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d conversations, want 3", len(got))
	}
	want := []string{"new", "middle", "old"}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestListHonoursTheLimit(t *testing.T) {
	root := claudeHome(t)
	for _, id := range []string{"a", "b", "c", "d"} {
		writeJSONL(t, filepath.Join(root, "-p", id+".jsonl"), `{"type":"user","cwd":"/p","message":{"content":"x"}}`)
	}
	got, err := List("claude", 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d conversations, want 2", len(got))
	}
}

func TestListSurvivesAnUnreadableStore(t *testing.T) {
	// Nothing has ever been recorded: an empty list, not an error, since a user
	// who has not used that agent should be told so plainly.
	claudeHome(t)
	codexHome(t)
	for _, agent := range Supported() {
		got, err := List(agent, 5)
		if err != nil {
			t.Errorf("List(%s): %v", agent, err)
		}
		if len(got) != 0 {
			t.Errorf("List(%s) = %d, want none", agent, len(got))
		}
	}
}

// A transcript carries pasted files and encoded images inline, so one record
// can be megabytes. An oversized line is skipped without derailing the scan of
// the records around it.
func TestScanSkipsOversizedRecordsAndKeepsGoing(t *testing.T) {
	root := claudeHome(t)
	huge := `{"type":"user","cwd":"/p","message":{"content":"` + strings.Repeat("x", maxLineBytes+10) + `"}}`
	writeJSONL(t, filepath.Join(root, "-p", "abc-123.jsonl"),
		huge,
		`{"type":"user","cwd":"/p","message":{"content":"the real prompt"}}`,
	)
	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Title != "the real prompt" {
		t.Errorf("title = %q", c.Title)
	}
}

// A record shape this build does not recognize is skipped rather than fatal:
// the transcript formats belong to the agents, not to dma.
func TestScanIgnoresUnparseableRecords(t *testing.T) {
	root := claudeHome(t)
	writeJSONL(t, filepath.Join(root, "-p", "abc-123.jsonl"),
		`not json at all`,
		`{"type":`,
		`{"type":"user","cwd":"/p","message":{"content":"still found"}}`,
	)
	c, err := Find("claude", "abc-123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if c.Title != "still found" {
		t.Errorf("title = %q", c.Title)
	}
}

// asError is errors.As without importing errors into every assertion.
func asError[T error](err error, target *T) bool {
	for err != nil {
		if t, ok := err.(T); ok {
			*target = t
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
