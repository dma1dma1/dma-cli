package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var errDirenvUnavailable = errors.New("direnv is unavailable")

type direnvRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// authorizeMatchingDirenv carries an existing direnv decision from the
// registered checkout into a new worktree. direnv keys trust by path, so an
// identical tracked .envrc is blocked when git checks it out at a new path.
//
// Trust is inherited only when the registered checkout is already allowed and
// both files have identical bytes. In particular, a newly fetched .envrc is
// never approved merely because the old checkout was: that would execute
// unreviewed repository code in the user's shell before the agent sandbox
// starts.
func authorizeMatchingDirenv(ctx context.Context, repoPath, worktree string) error {
	return authorizeMatchingDirenvWith(ctx, repoPath, worktree, runDirenv)
}

func authorizeMatchingDirenvWith(ctx context.Context, repoPath, worktree string, run direnvRunner) error {
	source := filepath.Join(repoPath, ".envrc")
	target := filepath.Join(worktree, ".envrc")

	sourceData, err := os.ReadFile(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read registered .envrc: %w", err)
	}
	targetData, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read worktree .envrc: %w", err)
	}
	if !bytes.Equal(sourceData, targetData) {
		return nil
	}

	out, err := run(ctx, repoPath, "status", "--json")
	if errors.Is(err, errDirenvUnavailable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect registered checkout: %w", err)
	}
	var status struct {
		State struct {
			FoundRC *struct {
				Allowed int    `json:"allowed"`
				Path    string `json:"path"`
			} `json:"foundRC"`
		} `json:"state"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return fmt.Errorf("parse status: %w", err)
	}
	if status.State.FoundRC == nil || status.State.FoundRC.Allowed != 0 ||
		!sameFilePath(status.State.FoundRC.Path, source) {
		return nil
	}

	if _, err := run(ctx, worktree, "allow"); err != nil {
		return fmt.Errorf("allow worktree .envrc: %w", err)
	}
	return nil
}

func runDirenv(ctx context.Context, dir string, args ...string) ([]byte, error) {
	path, err := exec.LookPath("direnv")
	if err != nil {
		return nil, errDirenvUnavailable
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) == 0 {
			msg = bytes.TrimSpace(stdout.Bytes())
		}
		if len(msg) > 0 {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func sameFilePath(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(infoA, infoB)
	}
	a, errA = filepath.Abs(a)
	b, errB = filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
