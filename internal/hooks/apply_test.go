package hooks

import (
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
)

func TestInterpretNotificationTypes(t *testing.T) {
	// The notification kind arrives as a payload field, since Notification
	// events do not support matchers.
	perm := Interpret(Event{EventName: "Notification", NotificationType: "permission_prompt", Message: "Bash(rm -rf)"})
	if perm.State != core.AgentNeedsYou || perm.Detail != "Bash(rm -rf)" {
		t.Fatalf("permission_prompt = %+v", perm)
	}

	other := Interpret(Event{EventName: "Notification", NotificationType: "auth_success"})
	if other.Known {
		t.Fatalf("auth_success should not drive agent state: %+v", other)
	}
}

// idle_prompt fires on a timer once the prompt has sat untouched, so it says the
// user stepped away, not that the agent asked for anything. Acting on it made
// every finished session drift into needs_you.
func TestInterpretIgnoresIdlePrompt(t *testing.T) {
	idle := Interpret(Event{
		EventName:        "Notification",
		NotificationType: "idle_prompt",
		Message:          "Claude is waiting for your input",
	})
	if idle.Known {
		t.Fatalf("idle_prompt should not drive agent state: %+v", idle)
	}
	if needsAttention(Event{EventName: "Notification", NotificationType: "idle_prompt"}) {
		t.Error("idle_prompt should not ring the bell")
	}
}

// The tools that exist to put a question to the user are the positive signal:
// the agent is stopped until it is answered.
func TestInterpretQuestionToolsNeedYou(t *testing.T) {
	for _, tool := range []string{"AskUserQuestion", "ExitPlanMode"} {
		pre := Interpret(Event{EventName: "PreToolUse", ToolName: tool})
		if pre.State != core.AgentNeedsYou || pre.Detail == "" {
			t.Errorf("PreToolUse %s = %+v, want needs_you", tool, pre)
		}
		if !needsAttention(Event{EventName: "PreToolUse", ToolName: tool}) {
			t.Errorf("PreToolUse %s should ring the bell", tool)
		}
		// Answered: the tool returned, so the agent is moving again.
		if post := Interpret(Event{EventName: "PostToolUse", ToolName: tool}); post.State != core.AgentWorking {
			t.Errorf("PostToolUse %s = %+v, want working", tool, post)
		}
	}
}

func TestInterpretLifecycleEvents(t *testing.T) {
	if got := Interpret(Event{EventName: "SessionStart"}); got.State != core.AgentWorking {
		t.Errorf("SessionStart = %+v", got)
	}
	if got := Interpret(Event{EventName: "PreToolUse", ToolName: "Bash"}); got.State != core.AgentWorking {
		t.Errorf("PreToolUse = %+v", got)
	}
	stop := Interpret(Event{EventName: "Stop"})
	if stop.State != core.AgentDone || !stop.Stopped {
		t.Errorf("Stop = %+v", stop)
	}
	end := Interpret(Event{EventName: "SessionEnd"})
	if !end.Ended {
		t.Errorf("SessionEnd = %+v", end)
	}
	if got := Interpret(Event{EventName: "PostCompact"}); got.Known {
		t.Errorf("unknown event should be ignored: %+v", got)
	}
}

func TestCorrelateByWorkingDirectory(t *testing.T) {
	a := &core.Session{ID: "a", WorktreePath: "/wt/api-auth"}
	b := &core.Session{ID: "b", WorktreePath: "/wt/web-auth"}
	sessions := []*core.Session{a, b}

	if got := Correlate(sessions, Event{Cwd: "/wt/api-auth"}); got != a {
		t.Fatalf("exact cwd matched %v, want a", got)
	}
	// The agent may cd into a subdirectory during a run.
	if got := Correlate(sessions, Event{Cwd: "/wt/api-auth/internal/server"}); got != a {
		t.Fatalf("nested cwd matched %v, want a", got)
	}
	if got := Correlate(sessions, Event{Cwd: "/somewhere/else"}); got != nil {
		t.Fatalf("unrelated cwd matched %v, want nil", got)
	}
}

// A sibling worktree whose path is a string prefix of another must not steal
// its events.
func TestCorrelateDoesNotMatchSiblingPrefix(t *testing.T) {
	a := &core.Session{ID: "a", WorktreePath: "/wt/auth"}
	b := &core.Session{ID: "b", WorktreePath: "/wt/auth-refresh"}
	sessions := []*core.Session{a, b}

	if got := Correlate(sessions, Event{Cwd: "/wt/auth-refresh"}); got != b {
		t.Fatalf("matched %v, want b — prefix matching leaked across siblings", got)
	}
}

func TestCorrelatePrefersClaudeSessionID(t *testing.T) {
	a := &core.Session{ID: "a", WorktreePath: "/wt/a", ClaudeSessionID: "cs-1"}
	b := &core.Session{ID: "b", WorktreePath: "/wt/b"}
	sessions := []*core.Session{a, b}

	// cwd points at b, but the claude session id identifies a.
	if got := Correlate(sessions, Event{Cwd: "/wt/b", ClaudeSessionID: "cs-1"}); got != a {
		t.Fatalf("matched %v, want a", got)
	}
}

func TestBuildSettingsShape(t *testing.T) {
	s := BuildSettings("http://127.0.0.1:8787/hook")

	for _, ev := range []string{"SessionStart", "Notification", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"} {
		groups, ok := s.Hooks[ev]
		if !ok || len(groups) == 0 || len(groups[0].Hooks) == 0 {
			t.Fatalf("no hook registered for %s", ev)
		}
		if groups[0].Hooks[0].Type != "http" {
			t.Errorf("%s hook type = %q, want http", ev, groups[0].Hooks[0].Type)
		}
	}

	// Notification must not carry a matcher: the event does not support them.
	if m := s.Hooks["Notification"][0].Matcher; m != "" {
		t.Errorf("Notification matcher = %q, want empty", m)
	}
	if m := s.Hooks["SessionStart"][0].Matcher; m != "startup|resume|clear|compact" {
		t.Errorf("SessionStart matcher = %q", m)
	}
}
