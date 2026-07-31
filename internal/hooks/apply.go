package hooks

import (
	"path/filepath"
	"strings"

	"github.com/dma1dma1/dma-cli/internal/core"
)

// Outcome is the interpretation of a hook event for one session.
type Outcome struct {
	State  core.AgentState
	Detail string
	// Ended marks SessionEnd, after which liveness is the only signal left.
	Ended bool
	// Stopped marks the Stop hook, which may optionally advance lifecycle to
	// review when the user has opted in.
	Stopped bool
	// Known is false for events this tool does not act on.
	Known bool
}

// askingTools are the tools whose whole purpose is to put a question to the
// user: the agent has stopped and cannot continue until it is answered. These
// are the only PreToolUse events that mean needs_you.
var askingTools = map[string]string{
	"askuserquestion": "asking you a question",
	"exitplanmode":    "plan needs your approval",
}

// Interpret maps a hook event to an agent state.
func Interpret(ev Event) Outcome {
	switch ev.EventName {
	case "SessionStart":
		return Outcome{State: core.AgentWorking, Known: true}

	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt", "worker_permission_prompt", "elicitation_dialog", "elicitation_url_dialog":
			detail := ev.Message
			if detail == "" {
				detail = "permission request"
			}
			return Outcome{State: core.AgentNeedsYou, Detail: truncate(detail, 60), Known: true}
		case "agent_needs_input":
			detail := ev.Message
			if detail == "" {
				detail = "waiting for input"
			}
			return Outcome{State: core.AgentNeedsYou, Detail: truncate(detail, 60), Known: true}
		case "agent_completed":
			return Outcome{State: core.AgentDone, Known: true}
		}
		// idle_prompt is deliberately ignored. It fires on a timer once the
		// prompt has sat untouched, so it reports that *you* have been away, not
		// that the agent is asking anything -- a finished session would drift
		// from done to needs_you just by being left alone.
		return Outcome{}

	case "PreToolUse", "PostToolUse", "UserPromptSubmit":
		detail := ""
		if ev.ToolName != "" {
			detail = strings.ToLower(ev.ToolName)
		}
		// A question tool is only pending before it runs; once it returns, the
		// answer is in and the agent is working again.
		if ev.EventName == "PreToolUse" {
			if ask, ok := askingTools[detail]; ok {
				return Outcome{State: core.AgentNeedsYou, Detail: ask, Known: true}
			}
		}
		// Heartbeat: the agent is demonstrably doing something.
		return Outcome{State: core.AgentWorking, Detail: detail, Known: true}

	case "Stop":
		return Outcome{State: core.AgentDone, Stopped: true, Known: true}

	case "SessionEnd":
		return Outcome{State: core.AgentIdle, Ended: true, Known: true}
	}
	return Outcome{}
}

// Correlate finds the session a hook payload belongs to.
//
// The working directory is the primary key: each session owns a distinct
// worktree, and the agent runs with that worktree as its cwd. The Claude
// session id is used as a fallback for events whose cwd has drifted (the agent
// may cd during a run).
func Correlate(sessions []*core.Session, ev Event) *core.Session {
	if ev.ClaudeSessionID != "" {
		for _, s := range sessions {
			if s.ClaudeSessionID != "" && s.ClaudeSessionID == ev.ClaudeSessionID {
				return s
			}
		}
	}
	if ev.Cwd == "" {
		return nil
	}
	cwd := filepath.Clean(ev.Cwd)
	// Prefer the longest matching worktree prefix, so nested worktree roots
	// cannot steal each other's events.
	var best *core.Session
	for _, s := range sessions {
		wt := filepath.Clean(s.WorktreePath)
		if cwd == wt || strings.HasPrefix(cwd, wt+string(filepath.Separator)) {
			if best == nil || len(wt) > len(filepath.Clean(best.WorktreePath)) {
				best = s
			}
		}
	}
	return best
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
