//go:build darwin || ios

package sockat

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// dialSteps is the connect order on Apple's systems (SPEC "connect の順"):
// relative, widened (not made: skipped), thread-cwd-direct, link-file.
func dialSteps(bool) []dialStep {
	return []dialStep{
		{routeRelative, (*dialCall).relative},
		{routeWidened, (*dialCall).widened},
		{routeThreadCwdDirect, (*dialCall).threadCwdDirect},
		{routeLinkFile, (*dialCall).linkFile},
	}
}

// noWidenedRoute is the reason widened is skipped: sockat-go does not make it.
const noWidenedRoute = "no widened route in sockat-go"

// widened is SPEC's widened sockaddr, which sockat-go does not make: always
// unusable, so the order goes on to thread-cwd-direct.
//
// Design rationale: the kernel takes the widened form (sun_len and the
// address length past sizeof(struct sockaddr_un), up to 253 bytes of path),
// but Go cannot hand such a socket over intact: net.FileConn on a socket
// connected that way to a peer bound that way leaves RemoteAddr() a nil
// net.Addr, whose String() panics, where net.DialUnix would report the
// spelling (DR-0005). What is lost is a dial that neither relative nor
// thread-cwd-direct can make (no thread cwd of its own, or a file name over
// 103 bytes) and link-file cannot either (no B can be made), to a path of at
// most 253 bytes.
func (c *dialCall) widened() (endpoint, error) { return nil, unusable(noWidenedRoute) }

// threadCwdPossible: __pthread_fchdir is tried (threadChdir tells when the
// kernel lacks it).
func threadCwdPossible() bool { return true }

// openDir opens dir to serve as the cwd of thread-cwd-direct: O_SEARCH,
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
// errUnusable whenever it fails, the kernel not having it (ENOSYS) or
// refusing it (EACCES, EPERM in a sandbox, ...): the thread cwd cannot be
// used then (SPEC-routes "thread-cwd"), and the thread's cwd is left as it
// was, so the next route is tried.
func threadChdir(dirfd int) error {
	// Design rationale: there is no wrapper for pthread_fchdir_np in the
	// standard library or golang.org/x/sys, and calling libSystem directly
	// needs cgo. The system call number is stable (XNU's syscalls.master) and
	// syscall.Syscall goes through libSystem's syscall(2).
	if _, _, e := syscall.Syscall(unix.SYS___PTHREAD_FCHDIR, uintptr(dirfd), 0, 0); e != 0 {
		return unusable("pthread_fchdir: " + e.Error())
	}
	return nil
}

// threadSelf is the calling thread's id (thread_selfid(2), the id
// pthread_threadid_np(3) gives).
func threadSelf() uint64 {
	// Design rationale: syscall.Syscall for want of a wrapper without cgo, as
	// in threadChdir.
	id, _, _ := syscall.Syscall(unix.SYS_THREAD_SELFID, 0, 0, 0)
	return uint64(id)
}

// onMainThread reports whether the calling goroutine, locked to its thread,
// runs on the main thread, the one the package was initialized on
// (mainThread).
func onMainThread() bool { return threadSelf() == mainThread }

// hasProc: no /proc routes here.
const hasProc = false

// bindFollowsHere: bind(2) here follows a symlink in the last component
// (measured, SPEC "bind の順").
const bindFollowsHere = followsYes
