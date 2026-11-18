//go:build freebsd

package sockat

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// runLong is the routes for a path longer than directMax on FreeBSD:
// bindat(2) / connectat(2) with the file name relative to the directory fd.
// Like Linux's /proc, it needs no cwd, temporary file or thread, so nothing
// else is tried.
func runLong(o op, path string, _ *config) (endpoint, route, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	if len(name) > directMax {
		return nil, "", errNameTooLong(o)
	}
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", syscallError("open", err)
	}
	defer unix.Close(dfd)
	// The standard form: sun_len, family, the name and a NUL.
	sa := make([]byte, 2+len(name)+1)
	sa[0] = byte(len(sa))
	sa[1] = unix.AF_UNIX
	copy(sa[2:], name)
	ep, err := rawOp(o, func(sock int) error {
		trap, sysName := uintptr(unix.SYS_BINDAT), "bindat"
		if o == opDial {
			trap, sysName = unix.SYS_CONNECTAT, "connectat"
		}
		_, _, e := syscall.Syscall6(trap, uintptr(dfd), uintptr(sock), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)), 0, 0)
		if e != 0 {
			return syscallError(sysName, e)
		}
		return nil
	})
	return ep, routeAt, err
}

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
