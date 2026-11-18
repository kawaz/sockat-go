//go:build !(darwin || ios || linux || android || freebsd)

package sockat

import (
	"os"

	"golang.org/x/sys/unix"
)

// runLong is the routes for a path longer than directMax on the Unix systems
// without a dirfd-relative primitive or per-thread working directories
// (NetBSD, OpenBSD, illumos, ...): the POSIX order (posixRun).
func runLong(o op, path string, cfg *config) (endpoint, route, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	return posixRun(o, path, dir, name, nil, cfg)
}

// openDir opens dir O_RDONLY | O_DIRECTORY.
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}
