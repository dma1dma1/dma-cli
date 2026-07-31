package notify

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRequirementsAreActionable guards the contract dma doctor prints: every
// notifier it calls required has to come with the command that installs it,
// otherwise the failing line tells the user to fix something without saying how.
func TestRequirementsAreActionable(t *testing.T) {
	reqs := Requirements()
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if len(reqs) == 0 {
			t.Fatalf("no notifier requirement on %s", runtime.GOOS)
		}
	}
	for _, r := range reqs {
		if r.Tool == "" || r.Why == "" {
			t.Errorf("requirement %+v needs a tool and a reason", r)
		}
		if r.Required && r.Install == "" {
			t.Errorf("%s is required but has no install command", r.Tool)
		}
		if r.Install != "" && !strings.Contains(r.Hint(), r.Install) {
			t.Errorf("Hint() for %s drops the install command: %q", r.Tool, r.Hint())
		}
	}
}

// TestMissingRequirementFollowsPATH covers the check itself. The notice and the
// doctor line both hang off it, so a lookup that ignored PATH would either nag
// users who already installed the notifier or stay silent for those who have
// not.
func TestMissingRequirementFollowsPATH(t *testing.T) {
	reqs := Requirements()
	var want Requirement
	for _, r := range reqs {
		if r.Required {
			want = r
			break
		}
	}
	if want.Tool == "" {
		t.Skip("no required notifier on " + runtime.GOOS)
	}

	t.Setenv("PATH", t.TempDir()) // empty, so nothing resolves
	got, missing := MissingRequirement()
	if !missing {
		t.Fatalf("MissingRequirement() found %s on an empty PATH", want.Tool)
	}
	if got.Tool != want.Tool {
		t.Fatalf("missing tool is %q, want %q", got.Tool, want.Tool)
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, want.Tool)
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir)
	if _, missing := MissingRequirement(); missing {
		t.Fatalf("MissingRequirement() reports %s missing with %s on PATH", want.Tool, stub)
	}
}
