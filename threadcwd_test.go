//go:build linux || darwin

package sockat

// The thread-cwd route: the last route on Linux (without /proc) and macOS. A
// goroutine locked to its OS thread gives that thread, and no other, the
// socket's directory as its cwd (Linux: unshare(CLONE_FS) + fchdir; macOS:
// __pthread_fchdir) and binds / connects the bare file name. The goroutine
// exits without unlocking, so the runtime discards the thread rather than
// hand its cwd to other goroutines. It stands in for SPEC's fork route, which
// Go cannot have, without the fork's side effects: the peer the server sees is
// this process, and no fd is duplicated.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// needThreadCwd skips a test of the route itself where the system refuses it
// (Linux under Docker's default seccomp profile, where unshare(2) is EPERM);
// what happens then is checked by TestThreadCwdRefusedByTheSystemIsENAMETOOLONG.
func needThreadCwd(t *testing.T) {
	t.Helper()
	if !threadCwdAvailable() {
		t.Skip("this system refuses a thread a cwd of its own (unshare(2) under a seccomp filter)")
	}
}

// openTestDir opens dir as the routes open it, closed at the end of the test.
func openTestDir(t *testing.T, dir string) *os.File {
	t.Helper()
	f, err := openDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// Forced directly, the route binds the bare file name from a thread whose cwd
// is the directory: the socket is at the given path (600 bytes, so the path
// itself fits no sockaddr), a dial the same way reaches it, the two are the
// net package's types in working order, and the server sees this process as
// the peer (a forked child would show its own pid). Through the route order
// with nothing earlier usable (far from the cwd, no place for a symlink, on
// macOS the widened sockaddr off, on Linux no /proc) the same path takes this
// route for both.
func TestTheThreadCwdRouteListensAndDialsAtThePath(t *testing.T) {
	t.Parallel()
	needThreadCwd(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := openTestDir(t, filepath.Dir(p))
	l := must{t}.listener(threadCwdRoute(opListen, dir, "s.sock", false))
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	c := must{t}.conn(threadCwdRoute(opDial, dir, "s.sock", false))
	checkStdTypes(t, l, c)

	l.SetDeadline(time.Now().Add(guard))
	c2 := must{t}.conn(threadCwdRoute(opDial, dir, "s.sock", false))
	s, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if pid := peerPid(t, s); pid != os.Getpid() {
		t.Fatalf("the peer is pid %d, not this process (%d)", pid, os.Getpid())
	}
	pingConns(t, s, c2)

	q := filepath.Dir(p) + "/q.sock"
	l, r := listenLong(t, q, plainCfg())
	if r != routeThreadCwd || !isSocket(q) {
		t.Fatalf("listen through the order: %s", r)
	}
	c, r = dialLong(t, q, plainCfg())
	if r != routeThreadCwd {
		t.Fatalf("dial through the order: %s", r)
	}
	ping(t, l, c)
}

// The route's failures are the kernel's, returned as they are (it is the last
// route, so there is nothing to move on to anyway): a missing socket is
// ENOENT, an existing file EADDRINUSE, a socket nobody listens on
// ECONNREFUSED. A missing directory is ENOENT from opening it, before the
// route is chosen.
func TestThreadCwdFailuresAreTheKernelsErrors(t *testing.T) {
	t.Parallel()
	needThreadCwd(t)
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir := filepath.Dir(p)
	f := openTestDir(t, dir)
	_, err := threadCwdRoute(opDial, f, "s.sock", false)
	wantErrno(t, "dial a missing socket", err, syscall.ENOENT)
	must{t}.listener(threadCwdRoute(opListen, f, "s.sock", false))
	_, err = threadCwdRoute(opListen, f, "s.sock", false)
	wantErrno(t, "listen on an existing socket", err, syscall.EADDRINUSE)
	staleSocket(t, dir+"/stale.sock")
	_, err = threadCwdRoute(opDial, f, "stale.sock", false)
	wantErrno(t, "dial a socket nobody listens on", err, syscall.ECONNREFUSED)
	for _, o := range []op{opListen, opDial} {
		_, _, err = runLong(o, dir+"/nope/s.sock", plainCfg())
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
}

// A file name that starts with '@' goes to the kernel as "./@...": Go's net
// package would read "@s.sock" as an abstract socket name on Linux, and the
// socket would not be in the directory at all.
func TestThreadCwdKeepsAFileNameStartingWithAtAFile(t *testing.T) {
	t.Parallel()
	needThreadCwd(t)
	p := nest(t, tmpDir(t), 600, "@s.sock")
	dir := openTestDir(t, filepath.Dir(p))
	l := must{t}.listener(threadCwdRoute(opListen, dir, "@s.sock", false))
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	c := must{t}.conn(threadCwdRoute(opDial, dir, "@s.sock", false))
	ping(t, l, c)
}

// A file name longer than the plain sockaddr carries, with the widened one not
// in play (a listen, or a kernel that refuses it), is ENAMETOOLONG before any
// thread is started or anything created.
func TestThreadCwdRefusesANameTooLongForTheSockaddr(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	markUntouched(t, d)
	f := openTestDir(t, d)
	for _, o := range []op{opListen, opDial} {
		_, err := threadCwdRoute(o, f, strings.Repeat("n", directMax+1), false)
		wantErrno(t, o.sysName(), err, syscall.ENAMETOOLONG)
	}
	assertUntouched(t, d)
}
