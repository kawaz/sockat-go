//go:build linux || android || darwin || ios || freebsd

package sockat

import "golang.org/x/sys/unix"

// symlinkat is symlinkat(2).
func symlinkat(target string, dirfd int, name string) error {
	return unix.Symlinkat(target, dirfd, name)
}
