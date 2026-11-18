//go:build darwin || ios

package sockat

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// runLong is the routes for a path longer than directMax on macOS: the POSIX
// order (posixRun).
//
// Design rationale: SPEC also has the widened sockaddr here (sun_len and the
// address length past sizeof(struct sockaddr_un), up to 253 bytes of path).
// Go cannot hand such a socket over intact: net.FileListener refuses a socket
// bound that way (getsockname reports more than the syscall package takes:
// EINVAL), and net.FileConn on a socket connected that way to a peer bound
// that way leaves RemoteAddr() a nil net.Addr, whose String() panics, where
// net.DialUnix would report the spelling. What is lost is a dial with no
// usable place for the symlink to a file name of 104 to 253 bytes.
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
