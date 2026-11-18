//go:build !(darwin || ios || linux || android || freebsd)

package sockat

import (
	"os"

	"golang.org/x/sys/unix"
)

// fallbackDial is a dial's fallback routes on the Unix systems without a
// dirfd-relative primitive or per-thread working directories (NetBSD,
// OpenBSD, illumos, ...): the path relative to the cwd, then symlinks to the
// file in safe places. The central route has no means here, so a listen of a
// long path is ENAMETOOLONG (Go has no fork route).
func fallbackDial(path, dir, _ string, _ *os.File, cfg *config) (endpoint, route, error) {
	return posixFallbackDial(path, dir, true, cfg)
}

// procFDUsableFor: no /proc route here.
func procFDUsableFor(*os.File, string) bool { return false }

// dOpenFlags is how D is opened.
const dOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// openDir opens dir O_RDONLY | O_DIRECTORY.
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}

// heldLinkRoute needs /proc: not here.
func heldLinkRoute(string, string, string) (endpoint, error) { return nil, errUnusable }

// dOpenFlagsProc: no /proc here, so as without it.
const dOpenFlagsProc = dOpenFlags

// dOpenFlagsRetry: no second way of opening D here.
const dOpenFlagsRetry = 0
