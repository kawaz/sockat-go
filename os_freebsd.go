//go:build freebsd

package sockat

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// atSupported: the central route binds / connects with bindat(2) /
// connectat(2) on D.
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
		if o == opDial {
			trap, sysName = unix.SYS_CONNECTAT, "connectat"
		}
		_, _, e := syscall.Syscall6(trap, uintptr(dirfd), uintptr(sock), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)), 0, 0)
		if e != 0 {
			return syscallError(sysName, e)
		}
		return nil
	})
}

// fallbackDial is a dial's fallback route on FreeBSD: connectat(2) on the
// socket's directory with the file name.
func fallbackDial(_, _, name string, sdir *os.File, _ *config) (endpoint, route, error) {
	ep, err := atOp(opDial, int(sdir.Fd()), name)
	return ep, routeConnectAt, err
}

// openDir opens dir O_RDONLY | O_DIRECTORY.
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}

// procFDUsableFor: no /proc route here.
func procFDUsableFor(*os.File, string) bool { return false }

// dOpenFlags is how D is opened.
const dOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// newSocket creates a stream socket set up as the net package sets up its
// own: close-on-exec and nonblocking.
func newSocket() (int, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, syscallError("socket", err)
	}
	return fd, nil
}

// listenBacklog is the backlog the net package passes to listen(2) here.
func listenBacklog() int { return somaxconn("kern.ipc.soacceptqueue") }

// dOpenFlagsProc: no /proc here, so as without it.
const dOpenFlagsProc = dOpenFlags

// dOpenFlagsRetry: no second way of opening D here.
const dOpenFlagsRetry = 0
