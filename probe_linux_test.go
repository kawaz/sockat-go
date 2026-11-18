package sockat

import (
	"net"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// threadCwdAvailable reports whether this system lets a thread have a working
// directory of its own: unshare(2) with CLONE_FS is refused by Docker's
// default seccomp profile (EPERM) and allowed elsewhere. Probed with the
// system call itself rather than through the route, so a route that wrongly
// gives up is not mistaken for a system that refuses it.
func threadCwdAvailable() bool { return unshareAllowed() }

var unshareAllowed = sync.OnceValue(func() bool {
	done := make(chan error, 1)
	go func() {
		// Never unlocked: the thread may have a cwd of its own now, so it is
		// discarded with the goroutine, as the route discards its thread.
		runtime.LockOSThread()
		done <- unix.Unshare(unix.CLONE_FS)
	}()
	return <-done == nil
})

// peerPid is the pid the server's peer-credential query (SO_PEERCRED) names
// for the accepted connection s.
func peerPid(t *testing.T, s *net.UnixConn) int {
	t.Helper()
	rc, err := s.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var cred *unix.Ucred
	var gerr error
	if err := rc.Control(func(fd uintptr) { cred, gerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		t.Fatal(err)
	}
	if gerr != nil {
		t.Fatal(gerr)
	}
	return int(cred.Pid)
}

// fullBacklog is what a dial answers while the listener's backlog is full:
// EAGAIN on Linux (the net package's connect is nonblocking; a blocking
// connect would wait for room instead).
const fullBacklog = unix.EAGAIN
