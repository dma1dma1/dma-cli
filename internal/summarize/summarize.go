// Package summarize turns the task a session was started from into the short
// title its card carries.
//
// The board earns its keep by being scannable, and a card is about thirty
// columns wide. A pasted paragraph arrives there as its first four words --
// "the login test flakes on" -- which is the least distinguishing part of it.
// A summary is not a shorter version of that text; it is a different sentence,
// so no amount of local trimming produces one. Claude Code is already a hard
// requirement for running an agent, so the title comes from a one-shot headless
// call to it.
//
// The call is best-effort throughout: every failure path ends at the text the
// user typed, shortened. A title is a nicety, and no session should fail to
// start because a model could not name it.
package summarize

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Model names the agent that writes titles. Naming a task is a small job on a
// tight clock -- the session is waiting on the answer -- so it runs on the
// cheapest and fastest model rather than on whichever one the session itself
// uses. The alias tracks the current Haiku release, which is what a title
// generator wants.
const Model = "haiku"

// Timeout bounds the call. Roughly four seconds of it is the CLI starting up,
// so this is generous by comparison; it exists to stop a wedged process from
// holding up a session, not to police a slow model.
const Timeout = 20 * time.Second

// A title is at most this long. The same limits decide three things: whether
// the typed text already is a title, whether the model's answer is one, and
// where the fallback cuts.
const (
	maxWords = 8
	maxRunes = 48
)

const instructions = `You name coding tasks for a kanban board.

The user's message is a task about to be handed to a coding agent. Reply with a title for it and nothing else: at most six words, lower case, no trailing punctuation, no quotes, no preamble.

Name the work rather than the request -- "flaky CI login test", not "look into why the login test is flaky". Keep whichever file, feature or symptom the task names: that word is what makes one card tell itself apart from the nine beside it.`

// Title condenses task into something that fits on a card. It is safe to call
// with anything, including text that is already short, and it never fails --
// the worst case is the first few words of what was typed.
func Title(ctx context.Context, task string) string {
	return condense(task, func(task string) (string, error) { return ask(ctx, task) })
}

// condense holds the decisions; ask is the part that talks to a model, so tests
// can drive every path without launching one.
func condense(task string, ask func(string) (string, error)) string {
	task = strings.TrimSpace(task)
	// Text that is already the length of a title is one. Sending "rate limiter"
	// to a model costs seconds and comes back as "implement rate limiter",
	// which is no better and is no longer what the user wrote.
	if isTitle(task) {
		return task
	}
	out, err := ask(task)
	if err != nil {
		return Shorten(task)
	}
	if title := clean(out); title != "" {
		return title
	}
	return Shorten(task)
}

// ask runs the model and returns its raw stdout.
func ask(ctx context.Context, task string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", task,
		"--model", Model,
		"--system-prompt", instructions,
		"--output-format", "text",
		// Naming a task needs no filesystem, no session record and no MCP
		// server. Each of those is startup time, and the first is a permission
		// prompt that nobody is watching a pane to answer.
		"--allowed-tools", "",
		"--no-session-persistence",
		"--strict-mcp-config",
	)
	// Somewhere with no project in it: the board's own working directory would
	// pull that project's CLAUDE.md into a prompt that is one sentence long.
	cmd.Dir = os.TempDir()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

// clean reduces the model's output to a title, or to "" if what came back is
// not one. A model that ignored the instructions returns a sentence, a refusal
// or a bulleted list, and all three are worse on a card than the typed text.
func clean(out string) string {
	// Models wrap a title in the punctuation of whatever format they think they
	// are producing -- a quoted string, a bullet, a sentence.
	line := strings.Trim(out, " \t\n\"'`*.:-")
	// One line was asked for. An answer that is not one is a model doing
	// something other than naming the task, and its first line is a guess at
	// which part of that was the answer.
	if strings.ContainsRune(line, '\n') {
		return ""
	}
	line = strings.Join(strings.Fields(line), " ")
	if !isTitle(line) {
		return ""
	}
	return line
}

// isTitle reports whether s is short enough to sit on a card as it is.
func isTitle(s string) bool {
	return s != "" && len([]rune(s)) <= maxRunes && len(strings.Fields(s)) <= maxWords
}

// Shorten is the local answer, for wherever a name is needed before a model can
// be asked for one and for wherever asking failed: the opening of the task, cut
// at a word rather than mid-word.
func Shorten(task string) string {
	words := strings.Fields(firstLine(task))
	if len(words) > maxWords {
		words = words[:maxWords]
	}
	for len(words) > 1 && len([]rune(strings.Join(words, " "))) > maxRunes {
		words = words[:len(words)-1]
	}
	out := strings.Join(words, " ")
	if r := []rune(out); len(r) > maxRunes {
		out = strings.TrimSpace(string(r[:maxRunes]))
	}
	return out
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
