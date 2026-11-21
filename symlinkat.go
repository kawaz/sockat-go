//go:build !(illumos || solaris)

package sockat

import "golang.org/x/sys/unix"

// symlinkat is symlinkat(2): a symlink name in the directory dirfd (whose
// path, dirPath, it does not need) pointing to target.
func symlinkat(target string, dirfd int, _, name string) error {
	return unix.Symlinkat(target, dirfd, name)
}

// readlinkat is readlinkat(2): what the symlink name in the directory dirfd
// (whose path, dirPath, it does not need) points to.
func readlinkat(dirfd int, _, name string) (string, error) {
	return readlinkWith(func(buf []byte) (int, error) { return unix.Readlinkat(dirfd, name, buf) })
}
