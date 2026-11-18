//go:build darwin || ios

package sockat

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// spellingMax is the longest spelling a sockaddr carries here: the widened
// form's.
const spellingMax = wideMax

// runLong is the routes for a path longer than directMax on macOS: the POSIX
// order (posixRun), with the widened sockaddr.
func runLong(o op, path string, cfg *config) (endpoint, route, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	return posixRun(o, path, dir, name, nil, cfg)
}

// openDir opens dir to serve as the cwd of the thread-cwd route: O_SEARCH,
// which needs only search permission on the directory, the same as a plain
// bind / connect through the path. A kernel that does not take O_SEARCH on a
// directory answers EISDIR (as it does for O_EXEC alone) or EINVAL; then the
// directory is opened O_RDONLY, which needs read permission.
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_SEARCH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	return searchOrReadOnly(dir, fd, err)
}

// searchOrReadOnly keeps the outcome of opening dir O_SEARCH, unless it is
// the answer of a kernel that does not take O_SEARCH on a directory (EISDIR or
// EINVAL); then it opens dir O_RDONLY instead. Any other error (ENOENT,
// EACCES, ...) is the real outcome. The outcome is taken as a value so the
// tests can stand in for such a kernel, which the running one is not.
func searchOrReadOnly(dir string, fd int, err error) (*os.File, error) {
	if errors.Is(err, unix.EISDIR) || errors.Is(err, unix.EINVAL) {
		fd, err = unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}

// threadChdir makes the cwd of the calling thread, and of no other, the
// directory dirfd: __pthread_fchdir(2), the system call behind libSystem's
// pthread_fchdir_np(3), gives the thread a working directory of its own.
//
// errUnusable when the kernel does not have it (ENOSYS).
func threadChdir(dirfd int) error {
	// Design rationale: there is no wrapper for pthread_fchdir_np in the
	// standard library or golang.org/x/sys, and calling libSystem directly
	// needs cgo. The system call number is stable (XNU's syscalls.master) and
	// syscall.Syscall goes through libSystem's syscall(2).
	_, _, e := syscall.Syscall(unix.SYS___PTHREAD_FCHDIR, uintptr(dirfd), 0, 0)
	switch e {
	case 0:
		return nil
	case syscall.ENOSYS:
		return errUnusable
	}
	return syscallError("fchdir", e)
}

// wideUsable reports whether the widened sockaddr can be used for o: only
// for a connect.
//
// A socket bound in the widened form cannot be handed to the net package:
// net.FileListener reads the socket's address with getsockname(2), which then
// reports more than a struct sockaddr_un holds, and the syscall package
// refuses that with EINVAL. A connected socket's own address is unbound, so
// net.FileConn takes it (the peer's address is read but its error ignored).
func wideUsable(o op) bool { return o == opDial }

// wideMax is the longest path the widened sockaddr carries.
//
// XNU accepts a socket address of up to SOCK_MAXADDRLEN (255) bytes in
// bind(2) / connect(2) and takes sun_len - offsetof(sun_path) bytes as the
// path, requiring that to be less than SOCK_MAXADDRLEN. With the length byte
// and the family byte in front, 255 - 2 = 253 bytes of path fit, without a NUL
// (the kernel terminates its own copy). libuv uses the same layout.
const wideMax = 255 - 2

// wideOp binds (and listens) or connects a fresh socket to path in a widened
// sockaddr: sun_len and the address length both 2 + the path length, no NUL.
func wideOp(o op, path string) (endpoint, error) {
	if !wideUsable(o) {
		return nil, errUnusable
	}
	if len(path) > wideMax {
		return nil, errNameTooLong(o)
	}
	sa := make([]byte, 2+len(path))
	sa[0] = byte(len(sa))
	sa[1] = unix.AF_UNIX
	copy(sa[2:], path)
	return rawOp(o, func(sock int) error {
		trap := uintptr(unix.SYS_BIND)
		if o == opDial {
			trap = unix.SYS_CONNECT
		}
		_, _, e := syscall.Syscall(trap, uintptr(sock), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)))
		if e != 0 {
			return syscallError(o.sysName(), e)
		}
		return nil
	})
}

// refusesWide reports whether an error from a widened-address call means the
// kernel does not take the widened form (as opposed to a real outcome such as
// ENOENT, EADDRINUSE or ECONNREFUSED). XNU reports an address it will not
// take with EINVAL, or ENAMETOOLONG for one longer than SOCK_MAXADDRLEN.
func refusesWide(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENAMETOOLONG)
}

// newSocket creates a stream socket set up as the net package sets up its
// own on this system: close-on-exec (under syscall.ForkLock, as there is no
// SOCK_CLOEXEC) and nonblocking.
func newSocket() (int, error) {
	syscall.ForkLock.RLock()
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return -1, syscallError("socket", err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return -1, syscallError("setnonblock", err)
	}
	return fd, nil
}

// listenBacklog is the backlog the net package passes to listen(2) here.
func listenBacklog() int { return somaxconn("kern.ipc.somaxconn") }
