//go:build !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package ops

func tryTrashLock(string) (func(), bool, error) {
	return func() {}, true, nil
}
