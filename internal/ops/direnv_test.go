package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type direnvCall struct {
	dir  string
	args []string
}

func writeEnvrc(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func trustedStatus(path string) []byte {
	return []byte(fmt.Sprintf(`{"state":{"foundRC":{"allowed":0,"path":%q}}}`, path))
}

func TestAuthorizeMatchingDirenvAllowsIdenticalTrustedEnvrc(t *testing.T) {
	repoPath := t.TempDir()
	worktree := t.TempDir()
	writeEnvrc(t, repoPath, "export PROJECT=devops\n")
	writeEnvrc(t, worktree, "export PROJECT=devops\n")

	var calls []direnvCall
	run := func(_ context.Context, dir string, args ...string) ([]byte, error) {
		calls = append(calls, direnvCall{dir: dir, args: append([]string(nil), args...)})
		if reflect.DeepEqual(args, []string{"status", "--json"}) {
			return trustedStatus(filepath.Join(repoPath, ".envrc")), nil
		}
		return nil, nil
	}

	if err := authorizeMatchingDirenvWith(context.Background(), repoPath, worktree, run); err != nil {
		t.Fatalf("authorizeMatchingDirenvWith: %v", err)
	}
	want := []direnvCall{
		{dir: repoPath, args: []string{"status", "--json"}},
		{dir: worktree, args: []string{"allow"}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %#v, want %#v", calls, want)
	}
}

func TestAuthorizeMatchingDirenvDoesNotCarryUntrustedDecision(t *testing.T) {
	repoPath := t.TempDir()
	worktree := t.TempDir()
	writeEnvrc(t, repoPath, "export PROJECT=devops\n")
	writeEnvrc(t, worktree, "export PROJECT=devops\n")

	var calls int
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if !reflect.DeepEqual(args, []string{"status", "--json"}) {
			t.Fatalf("unexpected direnv command: %v", args)
		}
		return []byte(fmt.Sprintf(`{"state":{"foundRC":{"allowed":1,"path":%q}}}`,
			filepath.Join(repoPath, ".envrc"))), nil
	}

	if err := authorizeMatchingDirenvWith(context.Background(), repoPath, worktree, run); err != nil {
		t.Fatalf("authorizeMatchingDirenvWith: %v", err)
	}
	if calls != 1 {
		t.Errorf("direnv calls = %d, want only the status check", calls)
	}
}

func TestAuthorizeMatchingDirenvDoesNotAllowChangedEnvrc(t *testing.T) {
	repoPath := t.TempDir()
	worktree := t.TempDir()
	writeEnvrc(t, repoPath, "export REVIEWED=1\n")
	writeEnvrc(t, worktree, "export UNREVIEWED=1\n")

	run := func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("direnv must not run for mismatched .envrc files")
		return nil, nil
	}
	if err := authorizeMatchingDirenvWith(context.Background(), repoPath, worktree, run); err != nil {
		t.Fatalf("authorizeMatchingDirenvWith: %v", err)
	}
}

func TestAuthorizeMatchingDirenvSkipsMissingEnvrc(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("direnv must not run without a registered .envrc")
		return nil, nil
	}
	if err := authorizeMatchingDirenvWith(context.Background(), t.TempDir(), t.TempDir(), run); err != nil {
		t.Fatalf("authorizeMatchingDirenvWith: %v", err)
	}
}

func TestAuthorizeMatchingDirenvSkipsWhenDirenvIsUnavailable(t *testing.T) {
	repoPath := t.TempDir()
	worktree := t.TempDir()
	writeEnvrc(t, repoPath, "export PROJECT=devops\n")
	writeEnvrc(t, worktree, "export PROJECT=devops\n")

	run := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errDirenvUnavailable
	}
	if err := authorizeMatchingDirenvWith(context.Background(), repoPath, worktree, run); err != nil {
		t.Fatalf("unavailable direnv should be ignored, got %v", err)
	}
}

func TestAuthorizeMatchingDirenvReportsAllowFailure(t *testing.T) {
	repoPath := t.TempDir()
	worktree := t.TempDir()
	writeEnvrc(t, repoPath, "export PROJECT=devops\n")
	writeEnvrc(t, worktree, "export PROJECT=devops\n")

	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if reflect.DeepEqual(args, []string{"status", "--json"}) {
			return trustedStatus(filepath.Join(repoPath, ".envrc")), nil
		}
		return nil, errors.New("permission denied")
	}
	err := authorizeMatchingDirenvWith(context.Background(), repoPath, worktree, run)
	if err == nil || !strings.Contains(err.Error(), "allow worktree .envrc: permission denied") {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeMatchingDirenvWithRealDirenv(t *testing.T) {
	if _, err := exec.LookPath("direnv"); err != nil {
		t.Skip("direnv is not installed")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	repoPath := t.TempDir()
	worktree := t.TempDir()
	writeEnvrc(t, repoPath, "export PROJECT=devops\n")
	writeEnvrc(t, worktree, "export PROJECT=devops\n")

	ctx := context.Background()
	if _, err := runDirenv(ctx, repoPath, "allow"); err != nil {
		t.Fatalf("allow registered checkout: %v", err)
	}
	if err := authorizeMatchingDirenv(ctx, repoPath, worktree); err != nil {
		t.Fatalf("authorizeMatchingDirenv: %v", err)
	}
	out, err := runDirenv(ctx, worktree, "status", "--json")
	if err != nil {
		t.Fatalf("worktree status: %v", err)
	}
	if !strings.Contains(string(out), `"allowed": 0`) {
		t.Fatalf("worktree was not authorized:\n%s", out)
	}
}
