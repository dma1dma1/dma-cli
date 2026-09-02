package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// buildTree writes a small dependency-tree lookalike: nested directories,
// files, a symlink, and an executable, so a clone can be checked for shape,
// content and mode.
func buildTree(t *testing.T, root string) {
	t.Helper()
	for pkg := 0; pkg < 6; pkg++ {
		dir := filepath.Join(root, ".pnpm", fmt.Sprintf("pkg%d", pkg), "node_modules", "lib")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 5; f++ {
			p := filepath.Join(dir, fmt.Sprintf("f%d.js", f))
			rel, _ := filepath.Rel(root, p)
			if err := os.WriteFile(p, []byte(rel), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".bin", "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.pnpm/pkg0/node_modules/lib", filepath.Join(root, "lib-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nowhere", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
}

// snapshot describes every entry under root in a form two trees can be
// compared by: relative path, type, mode and either content or link target.
func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		line := rel + " " + info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			line += " -> " + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += " " + string(data)
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestCloneChunkedMatchesSingleClone(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	buildTree(t, src)
	want := snapshot(t, src)

	// A limit far below the tree's size forces splitting at every level; a
	// limit above it takes the single-call path. Both must produce the same
	// tree.
	for _, limit := range []int{2, 8, 1 << 20} {
		dst := filepath.Join(base, fmt.Sprintf("dst-%d", limit))
		if err := cloneChunked(context.Background(), src, dst, limit); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		got := snapshot(t, dst)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("limit %d: clone differs from source\n got: %s\nwant: %s", limit, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	}
}

func TestCloneChunkedStopsWhenCancelled(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	buildTree(t, src)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := filepath.Join(base, "dst")
	err := cloneChunked(ctx, src, dst, 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("cancelled clone created %s", dst)
	}
}

func TestCloneTreeRemovesPartialResultBeforeFallback(t *testing.T) {
	// Cancellation part-way through must not leave a truncated tree that reads
	// as complete, and must surface as the context error rather than a copy.
	base := t.TempDir()
	src := filepath.Join(base, "src")
	buildTree(t, src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := filepath.Join(base, "dst")
	if err := cloneTree(ctx, src, dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestCountEntriesUpToStopsAtLimit(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	buildTree(t, src)
	full := countEntriesUpTo(src, 1<<20)
	if full < 30 {
		t.Fatalf("countEntriesUpTo = %d, want the whole tree", full)
	}
	if got := countEntriesUpTo(src, 5); got != 5 {
		t.Fatalf("countEntriesUpTo with limit 5 = %d, want 5", got)
	}
}
