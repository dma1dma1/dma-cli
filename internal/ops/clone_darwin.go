package ops

import "golang.org/x/sys/unix"

// cloneTree copies src to dst with APFS's clonefile(2), which is what makes
// per-worktree dependency trees affordable.
//
// One call clones a whole directory tree: the entire node_modules of a large
// monorepo lands in about twenty seconds and costs no disk, since every file
// shares the original's blocks until something writes to it. The alternative --
// letting the repo's own installer rebuild the tree in each worktree -- is
// minutes, and reading one shared copy is what this exists to stop doing.
//
// dst must not exist; clonefile creates it.
func cloneTree(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}
