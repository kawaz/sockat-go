//go:build !(darwin || ios || linux || android || freebsd)

package sockat

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// dialSteps is the connect order on the Unix systems without a
// dirfd-relative primitive or per-thread working directories (NetBSD,
// OpenBSD, illumos, ...; SPEC "connect の順"): relative, symlink-file. The
// routes through D have no means here, so a listen of a long path has only
// symlink-file, where bind(2) follows the symlink, and is ENAMETOOLONG
// otherwise (Go has no fork route).
func dialSteps(bool) []dialStep {
	return []dialStep{
		{routeRelative, (*dialer).relative},
		{routeSymlinkFile, (*dialer).symlinkFile},
	}
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

// setPrivate sets D (fd, which passed the check) to mode 0700: fchmod(2).
func setPrivate(_ int, _ string, fd int, _ *unix.Stat_t, _ bool, _ string) error {
	return unix.Fchmod(fd, 0o700)
}

// dOpenFlagsRetry: no second way of opening D here.
const dOpenFlagsRetry = 0

// hasProc: no /proc routes here.
const hasProc = false

// bindFollowsHere is whether bind(2) here follows a symlink in the last
// component: measured to on NetBSD and not to on OpenBSD and illumos (SPEC
// "bind の順"); told at run time elsewhere.
var bindFollowsHere = func() follows {
	switch runtime.GOOS {
	case "netbsd":
		return followsYes
	case "openbsd", "illumos", "solaris":
		return followsNo
	}
	return followsUnknown
}()
