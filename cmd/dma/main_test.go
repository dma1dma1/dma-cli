package main

import (
	"flag"
	"io"
	"path/filepath"
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

// Go's flag package stops at the first positional argument, so a flag written
// after the session id would be dropped -- and -clean being dropped means the
// work in progress is carried when the user asked for it not to be.
func TestFlagsParseOnEitherSideOfTheArguments(t *testing.T) {
	cases := [][]string{
		{"-clean", "-repo", "web", "claude", "abc-123"},
		{"claude", "abc-123", "-clean", "-repo", "web"},
		{"claude", "-clean", "abc-123", "-repo", "web"},
	}
	for _, args := range cases {
		fs := flag.NewFlagSet("attach", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		repo := fs.String("repo", "", "")
		clean := fs.Bool("clean", false, "")

		positional, err := parseAnywhere(fs, args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if len(positional) != 2 || positional[0] != "claude" || positional[1] != "abc-123" {
			t.Errorf("%v: positional = %v", args, positional)
		}
		if !*clean {
			t.Errorf("%v: -clean was dropped", args)
		}
		if *repo != "web" {
			t.Errorf("%v: -repo = %q", args, *repo)
		}
	}
}

func TestParseAnywhereReportsUnknownFlags(t *testing.T) {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if _, err := parseAnywhere(fs, []string{"claude", "-nonsense"}); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

func TestShortenHomeAbbreviatesOnlyWholeSegments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := shortenHome(filepath.Join(home, "code", "proj")); got != filepath.Join("~", "code", "proj") {
		t.Errorf("shortenHome = %q", got)
	}
	// A path that merely starts with the same characters is not under it.
	if got := shortenHome(home + "-other"); got != home+"-other" {
		t.Errorf("shortenHome abbreviated a sibling directory: %q", got)
	}
	if got := shortenHome(""); got != "-" {
		t.Errorf("shortenHome(\"\") = %q", got)
	}
}
