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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// needThreadCwd skips a test of the route itself where the system refuses it
// (Linux under a seccomp filter, such as Docker's default profile); what
// happens then is checked by TestThreadCwdUnderASeccompFilterIsENAMETOOLONG.
func needThreadCwd(t *testing.T) {
	t.Helper()
	if !threadCwdAvailable() {
		t.Skip("this system refuses a thread a cwd of its own (a seccomp filter)")
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
// Linux no /proc) the same path takes this
// route for both.
func TestTheThreadCwdRouteListensAndDialsAtThePath(t *testing.T) {
	t.Parallel()
	needThreadCwd(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := openTestDir(t, filepath.Dir(p))
	l := must{t}.listener(threadCwdRoute(opListen, dir, "s.sock"))
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	c := must{t}.conn(threadCwdRoute(opDial, dir, "s.sock"))
	checkStdTypes(t, l, c)

	l.SetDeadline(time.Now().Add(guard))
	c2 := must{t}.conn(threadCwdRoute(opDial, dir, "s.sock"))
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
	l, r := listenLong(t, q, posixCfg())
	if r != routeThreadCwd || !isSocket(q) {
		t.Fatalf("listen through the order: %s", r)
	}
	c, r = dialLong(t, q, posixCfg())
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
	_, err := threadCwdRoute(opDial, f, "s.sock")
	wantErrno(t, "dial a missing socket", err, syscall.ENOENT)
	must{t}.listener(threadCwdRoute(opListen, f, "s.sock"))
	_, err = threadCwdRoute(opListen, f, "s.sock")
	wantErrno(t, "listen on an existing socket", err, syscall.EADDRINUSE)
	staleSocket(t, dir+"/stale.sock")
	_, err = threadCwdRoute(opDial, f, "stale.sock")
	wantErrno(t, "dial a socket nobody listens on", err, syscall.ECONNREFUSED)
	for _, o := range []op{opListen, opDial} {
		_, _, err = runLong(o, dir+"/nope/s.sock", posixCfg())
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
	l := must{t}.listener(threadCwdRoute(opListen, dir, "@s.sock"))
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	c := must{t}.conn(threadCwdRoute(opDial, dir, "@s.sock"))
	ping(t, l, c)
}

// A file name longer than the sockaddr carries is ENAMETOOLONG before any
// thread is started or anything created.
func TestThreadCwdRefusesANameTooLongForTheSockaddr(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	markUntouched(t, d)
	f := openTestDir(t, d)
	for _, o := range []op{opListen, opDial} {
		_, err := threadCwdRoute(o, f, strings.Repeat("n", directMax+1))
		wantErrno(t, o.sysName(), err, syscall.ENAMETOOLONG)
	}
	assertUntouched(t, d)
}

// Every spelling a route hands to the kernel goes in the plain sockaddr
// (directMax bytes: 103 on macOS, which does not use its widened form, 107 on
// Linux), and that sets how far Listen and Dial reach for a file name:
//
//   - a file name of directMax bytes is listened on (the thread-cwd route
//     binds the bare name) and dialed;
//   - a file name of directMax + 1 bytes is ENAMETOOLONG for a listen, with or
//     without a place for the symlink (the link to the directory spells
//     <place>/sockat.XXXXXXXXXX/<name>), and for a dial with no place;
//   - a dial through the link to the socket file (<place>/sockat.XXXXXXXXXX,
//     whatever the name) reaches any name, here directMax + 1 and 200 bytes.
//
// On Linux /proc is made to look absent here; the /proc route would not change
// this, since its spelling holds the name too.
func TestAFileNameOverTheSockaddrIsReachedOnlyByADialThroughTheSymlink(t *testing.T) {
	t.Parallel()
	needThreadCwd(t)
	d, base := tmpDir(t), tmpDir(t)
	dir := filepath.Dir(nest(t, d, 600, "x"))
	fits := dir + "/" + strings.Repeat("f", directMax)
	l, r := listenLong(t, fits, posixCfg())
	if r != routeThreadCwd || !isSocket(fits) {
		t.Fatalf("a %d-byte name: listen %s", directMax, r)
	}
	c, r := dialLong(t, fits, posixCfg())
	if r != routeThreadCwd {
		t.Fatalf("a %d-byte name: dial %s", directMax, r)
	}
	ping(t, l, c)

	for _, n := range []int{directMax + 1, 200} {
		p := dir + "/" + strings.Repeat("n", n)
		for _, cfg := range []*config{posixCfg(), posixCfg(base)} {
			_, _, err := runLong(opListen, p, cfg)
			wantErrno(t, fmt.Sprintf("listen on a %d-byte name, places %v", n, cfg.tmpBases), err, syscall.ENAMETOOLONG)
		}
		if exists(p) {
			t.Fatal("something was created at the path")
		}
		short := dir + "/s.sock"
		l, _ := listenLong(t, short, posixCfg())
		if err := os.Rename(short, p); err != nil {
			t.Fatal(err)
		}
		_, _, err := runLong(opDial, p, posixCfg())
		wantErrno(t, fmt.Sprintf("dial a %d-byte name with no place", n), err, syscall.ENAMETOOLONG)
		c, r := dialLong(t, p, posixCfg(base))
		if r != routeSymlinkFile {
			t.Fatalf("dial a %d-byte name: %s", n, r)
		}
		ping(t, l, c)
		c2, err := Dial(p)
		if err != nil {
			t.Fatalf("Dial a %d-byte name through the public order: %v", n, err)
		}
		c2.Close()
	}
	assertEmpty(t, base)
}
