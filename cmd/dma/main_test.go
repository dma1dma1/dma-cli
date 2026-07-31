package main

import (
	"strings"
	"testing"

	"github.com/dma1dma1/dma-cli/internal/core"
	"github.com/dma1dma1/dma-cli/internal/notify"
)

// TestNotifierNoticeIsSaidOnce covers the promise the hint makes: a missing
// notifier is worth one line, not a line on every launch. The flag has to be
// persisted for that to hold across processes, since each launch is a new one.
func TestNotifierNoticeIsSaidOnce(t *testing.T) {
	if _, missing := notify.MissingRequirement(); !missing {
		// The notifier this platform wants is installed here, so drop it from
		// PATH -- the notice is what an unprepared machine sees.
		t.Setenv("PATH", t.TempDir())
	}
	if _, missing := notify.MissingRequirement(); !missing {
		t.Skip("no required notifier on this platform")
	}
	t.Setenv("DMA_HOME", t.TempDir())

	cfg := core.DefaultConfig()
	notice := notifierNotice(cfg)
	if notice == "" {
		t.Fatal("no notice for a machine with no notifier")
	}
	if !strings.Contains(notice, "dma doctor") {
		t.Errorf("notice %q does not point at dma doctor", notice)
	}
	if !cfg.NotifierHintShown {
		t.Error("notice did not mark itself shown")
	}

	if again := notifierNotice(cfg); again != "" {
		t.Errorf("notice repeated within a session: %q", again)
	}
	// A later launch reads the config back rather than inheriting the struct.
	reloaded, err := core.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reloaded.NotifierHintShown {
		t.Fatal("notifier_hint_shown did not survive the config write")
	}
	if again := notifierNotice(reloaded); again != "" {
		t.Errorf("notice repeated on a later launch: %q", again)
	}
}
