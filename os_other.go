//go:build !(darwin || ios || linux || android || freebsd)

package sockat

import (
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// dialSteps is the connect order on the Unix systems without a
// dirfd-relative connect or per-thread working directories (NetBSD, OpenBSD,
// illumos, ...; SPEC "connect の順"): relative, link-file.
func dialSteps(bool) []dialStep {
	return []dialStep{
		{routeRelative, (*dialCall).relative},
		{routeLinkFile, (*dialCall).linkFile},
	}
}

// openDir opens dir O_RDONLY | O_DIRECTORY.
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}

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
