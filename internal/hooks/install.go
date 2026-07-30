package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/dma1dma1/dma-cli/internal/gitx"
)

// hookEntry is one HTTP hook target.
type hookEntry struct {
	Type          string `json:"type"`
	URL           string `json:"url"`
	Timeout       int    `json:"timeout,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
}

// matcherGroup binds a matcher to a list of hooks.
type matcherGroup struct {
	Matcher string      `json:"matcher,omitempty"`
	Hooks   []hookEntry `json:"hooks"`
}

// Settings is the fragment written into a worktree's Claude settings.
type Settings struct {
	Hooks map[string][]matcherGroup `json:"hooks"`
}

// BuildSettings produces the hook configuration pointing at the running board.
//
// Notification deliberately carries no matcher: notification events do not
// support matchers, and the permission_prompt / idle_prompt distinction arrives
// as notification_type in the payload instead.
func BuildSettings(url string) Settings {
	h := []hookEntry{{Type: "http", URL: url, Timeout: 5}}
	return Settings{Hooks: map[string][]matcherGroup{
		"SessionStart": {{Matcher: "startup|resume|clear|compact", Hooks: h}},
		"Notification": {{Hooks: h}},
		"PreToolUse":   {{Matcher: "*", Hooks: h}},
		"PostToolUse":  {{Matcher: "*", Hooks: h}},
		"Stop":         {{Hooks: h}},
		"SessionEnd":   {{Hooks: h}},
	}}
}

// SettingsJSON renders the hook configuration for display or manual install.
func SettingsJSON(url string) (string, error) {
	b, err := json.MarshalIndent(BuildSettings(url), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

const localSettingsRel = ".claude/settings.local.json"

// InstallInWorktree writes the hook configuration into a single worktree.
//
// Scoping hooks per worktree rather than to the user's global settings means
// only agents this tool launched report to it, and an unrelated Claude Code
// session in another terminal is untouched.
func InstallInWorktree(ctx context.Context, worktree, url string) error {
	path := filepath.Join(worktree, localSettingsRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Merge into whatever is already there rather than clobbering it.
	merged := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &merged)
	}
	hooksJSON, err := json.Marshal(BuildSettings(url).Hooks)
	if err != nil {
		return err
	}
	var hooksMap map[string]any
	if err := json.Unmarshal(hooksJSON, &hooksMap); err != nil {
		return err
	}
	merged["hooks"] = hooksMap

	out, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return err
	}

	// The settings file is tooling state, not the user's work. Excluding it
	// locally keeps the worktree reading as clean, which the teardown guard and
	// the dirty chip both depend on.
	_ = gitx.AddLocalExclude(ctx, worktree, localSettingsRel)
	return nil
}
