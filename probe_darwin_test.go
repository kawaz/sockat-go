package sockat

import (
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

// threadCwdAvailable reports whether this system lets a thread have a working
// directory of its own: macOS does (__pthread_fchdir, behind libSystem's
// pthread_fchdir_np). Fixed rather than probed, so a route that
// wrongly gives up fails the tests.
func threadCwdAvailable() bool { return true }

// peerPid is the pid the server's peer-credential query (LOCAL_PEERPID) names
// for the accepted connection s.
func peerPid(t *testing.T, s *net.UnixConn) int {
	t.Helper()
	rc, err := s.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	var gerr error
	if err := rc.Control(func(fd uintptr) { pid, gerr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) }); err != nil {
		t.Fatal(err)
	}
	if gerr != nil {
		t.Fatal(gerr)
	}
	return pid
}

// fullBacklog is what a dial answers while the listener's backlog is full:
// ECONNREFUSED on macOS, for a blocking connect as well.
const fullBacklog = unix.ECONNREFUSED
