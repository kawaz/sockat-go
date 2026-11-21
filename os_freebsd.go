//go:build freebsd

package sockat

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// atSupported: at binds with bindat(2) on D, at-direct connects with
// connectat(2) on the socket's directory.
const atSupported = true

// atOp binds (and listens) or connects a fresh socket through spelling
// relative to the directory dirfd: bindat(2) / connectat(2).
func atOp(o op, dirfd int, spelling string) (endpoint, error) {
	if len(spelling) > directMax {
		return nil, errNameTooLong(o)
	}
	// The standard form: sun_len, family, the spelling and a NUL.
	sa := make([]byte, 2+len(spelling)+1)
	sa[0] = byte(len(sa))
	sa[1] = unix.AF_UNIX
	copy(sa[2:], spelling)
	return rawOp(o, func(sock int) error {
		trap, sysName := uintptr(unix.SYS_BINDAT), "bindat"
		if !o.binds() {
			trap, sysName = unix.SYS_CONNECTAT, "connectat"
		}
		_, _, e := syscall.Syscall6(trap, uintptr(dirfd), uintptr(sock), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)), 0, 0)
		if e != 0 {
			return syscallError(sysName, e)
		}
		return nil
	})
}

// dialSteps is the connect order on FreeBSD (SPEC "connect の順"):
// at-direct, link-file.
//
// Design rationale: link-file of a connect on FreeBSD is SPEC's "未確定"; it
// is made because a connect follows a symlink in the last component
// (SPEC-routes "link-file"), to be checked on FreeBSD's VM in CI.
func dialSteps(bool) []dialStep {
	return []dialStep{
		{routeAtDirect, (*dialCall).atDirect},
		{routeLinkFile, (*dialCall).linkFile},
	}
}

// atDirect connects with connectat(2) on the socket's directory and the file
// name, with no E: a connect leaves no spelling behind. Unusable when the
// socket's directory was not opened or the name does not fit the sockaddr.
func (c *dialCall) atDirect() (endpoint, error) {
	if c.sdir == nil {
		return nil, unusable("socket directory not opened")
	}
	if len(c.name) > directMax {
		return nil, unusable("spelling too long")
	}
	return atOp(c.o, int(c.sdir.Fd()), c.name)
}

// openDir opens dir O_RDONLY | O_DIRECTORY.
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}

// newSocket creates a socket of o's type (stream, or datagram for
// opListenGram and opDialGram) set up as the net package sets up its own:
// close-on-exec and nonblocking.
func newSocket(o op) (int, error) {
	typ := unix.SOCK_STREAM
	if o.network() == "unixgram" {
		typ = unix.SOCK_DGRAM
	}
	fd, err := unix.Socket(unix.AF_UNIX, typ|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, syscallError("socket", err)
	}
	return fd, nil
}

// listenBacklog is the backlog the net package passes to listen(2) here.
func listenBacklog() int { return somaxconn("kern.ipc.soacceptqueue") }

// hasProc: no /proc routes here.
const hasProc = false

// bindFollowsHere: bind(2) here does not follow a symlink in the last
// component (measured, SPEC "bind の順").
const bindFollowsHere = followsNo
