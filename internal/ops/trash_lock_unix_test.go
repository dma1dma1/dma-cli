//go:build android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package ops

import "testing"

func TestTrashLockHasOneCrossProcessOwner(t *testing.T) {
	root := t.TempDir()
	unlockFirst, acquired, err := tryTrashLock(root)
	if err != nil || !acquired {
		t.Fatalf("first lock = acquired %v, err %v", acquired, err)
	}
	defer unlockFirst()

	unlockSecond, acquired, err := tryTrashLock(root)
	if err != nil {
		t.Fatalf("second lock: %v", err)
	}
	defer unlockSecond()
	if acquired {
		t.Fatal("second lock acquired while first owner still held it")
	}
}
