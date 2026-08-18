package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// carrySetup builds the shape a carry happens in: a repo with work in progress
// in it, and a second worktree cut from the same commit for that work to be
// carried into. Cutting from the same commit is what Attach does, and what makes
// the patch apply with nothing to merge.
func carrySetup(t *testing.T) (src, dst string) {
	t.Helper()
	src = testRepo(t)
	ctx := context.Background()
	head, err := Head(ctx, src)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	dst = filepath.Join(t.TempDir(), "wt")
	if err := AddDetachedWorktree(ctx, src, dst, head); err != nil {
		t.Fatalf("AddDetachedWorktree: %v", err)
	}
	return src, dst
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestCarryMovesModificationsAndNewFiles(t *testing.T) {
	src, dst := carrySetup(t)
	write(t, src, "keep.txt", "alpha\nCHANGED\ncharlie\n")
	write(t, src, "brand-new.txt", "fresh\n")
	if err := os.Remove(filepath.Join(src, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	got, err := Carry(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("Carry: %v", err)
	}

	if body := readFile(t, dst, "keep.txt"); body != "alpha\nCHANGED\ncharlie\n" {
		t.Errorf("keep.txt = %q", body)
	}
	if body := readFile(t, dst, "brand-new.txt"); body != "fresh\n" {
		t.Errorf("brand-new.txt = %q", body)
	}
	// A deletion is part of the work in progress too, and the patch carries it.
	if _, err := os.Stat(filepath.Join(dst, "gone.txt")); !os.IsNotExist(err) {
		t.Errorf("gone.txt survived the carry")
	}
	if got.Modified != 2 || got.Added != 1 {
		t.Errorf("carried %+v, want 2 modified and 1 added", got)
	}
}

// The source is a directory someone is working in, frequently their own
// checkout. Nothing in a carry may touch it.
func TestCarryLeavesTheSourceAlone(t *testing.T) {
	src, dst := carrySetup(t)
	write(t, src, "keep.txt", "alpha\nCHANGED\ncharlie\n")
	write(t, src, "brand-new.txt", "fresh\n")

	before, err := StatusPorcelain(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Carry(context.Background(), src, dst); err != nil {
		t.Fatalf("Carry: %v", err)
	}
	after, err := StatusPorcelain(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("source status changed:\nbefore %q\nafter  %q", before, after)
	}
	if body := readFile(t, src, "brand-new.txt"); body != "fresh\n" {
		t.Errorf("source lost its new file: %q", body)
	}
}

// Staged changes are work in progress as much as unstaged ones. Diffing against
// HEAD rather than against the index is what picks both up.
func TestCarryTakesStagedChangesToo(t *testing.T) {
	src, dst := carrySetup(t)
	write(t, src, "old.txt", "one\ntwo\nthree\n")
	if _, err := Run(context.Background(), src, "add", "old.txt"); err != nil {
		t.Fatal(err)
	}

	if _, err := Carry(context.Background(), src, dst); err != nil {
		t.Fatalf("Carry: %v", err)
	}
	if body := readFile(t, dst, "old.txt"); body != "one\ntwo\nthree\n" {
		t.Errorf("old.txt = %q, want the staged content", body)
	}
}

// Ignored paths are the repo's dependencies and build output. The bootstrap
// materializes those into a worktree; copying them here would be both wrong and
// enormous.
func TestCarrySkipsIgnoredFiles(t *testing.T) {
	src, dst := carrySetup(t)
	write(t, src, ".gitignore", "node_modules/\n")
	if err := os.MkdirAll(filepath.Join(src, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, src, filepath.Join("node_modules", "left-pad", "index.js"), "module.exports = 1\n")

	if _, err := Carry(context.Background(), src, dst); err != nil {
		t.Fatalf("Carry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "node_modules")); !os.IsNotExist(err) {
		t.Error("an ignored dependency tree was carried across")
	}
	// The ignore file itself is untracked work and does come along.
	if body := readFile(t, dst, ".gitignore"); body != "node_modules/\n" {
		t.Errorf(".gitignore = %q", body)
	}
}

func TestCarryOnACleanSourceDoesNothing(t *testing.T) {
	src, dst := carrySetup(t)
	got, err := Carry(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("Carry: %v", err)
	}
	if got.Any() {
		t.Errorf("carried %+v from a clean worktree", got)
	}
	if got.String() != "nothing to carry" {
		t.Errorf("String() = %q", got.String())
	}
}

func TestCarryKeepsFileModes(t *testing.T) {
	src, dst := carrySetup(t)
	write(t, src, "run.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(src, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Carry(context.Background(), src, dst); err != nil {
		t.Fatalf("Carry: %v", err)
	}
	info, err := os.Stat(filepath.Join(dst, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("run.sh arrived as %v, want it still executable", info.Mode().Perm())
	}
}

// An agent's newest file is as likely to be three directories down as at the
// root, and the directories above it do not exist in the destination yet.
func TestCarryCreatesDirectoriesForNewFiles(t *testing.T) {
	src, dst := carrySetup(t)
	if err := os.MkdirAll(filepath.Join(src, "internal", "thing"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, src, filepath.Join("internal", "thing", "new.go"), "package thing\n")

	if _, err := Carry(context.Background(), src, dst); err != nil {
		t.Fatalf("Carry: %v", err)
	}
	if body := readFile(t, dst, filepath.Join("internal", "thing", "new.go")); body != "package thing\n" {
		t.Errorf("nested file = %q", body)
	}
}

// A symlink is recreated as a link. Following it would copy whatever it points
// at -- possibly something outside the tree entirely.
func TestCarryRecreatesSymlinks(t *testing.T) {
	src, dst := carrySetup(t)
	if err := os.Symlink("keep.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Carry(context.Background(), src, dst); err != nil {
		t.Fatalf("Carry: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatalf("carried link is not a link: %v", err)
	}
	if target != "keep.txt" {
		t.Errorf("link points at %q", target)
	}
}

func TestCarriedReadsAsASentence(t *testing.T) {
	cases := []struct {
		in   Carried
		want string
	}{
		{Carried{}, "nothing to carry"},
		{Carried{Modified: 1}, "carried 1 modified file"},
		{Carried{Modified: 3}, "carried 3 modified files"},
		{Carried{Added: 1}, "carried 1 new file"},
		{Carried{Modified: 2, Added: 1}, "carried 2 modified files and 1 new file"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("%+v = %q, want %q", c.in, got, c.want)
		}
	}
}
