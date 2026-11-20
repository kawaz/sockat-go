package sockat

// Helpers shared by the tests of every system.
//
// What the tests lean on:
//
//   - The socket must appear at the path the caller gave (checked with
//     os.Lstat and fs.ModeSocket), not at whatever spelling was handed to the
//     kernel. A route that "works" but creates the socket elsewhere is the
//     failure these tests exist to catch.
//   - Temporary directories have random names under /tmp (short, so the tests
//     decide the path lengths) and are removed when the test ends.
//   - Settings that belong to the whole process (the cwd, the environment) are
//     never changed in the test process: the tests run in parallel, and a cwd
//     changed under them would send their relative lookups elsewhere. Tests that
//     need a chosen cwd, or change it, run in a child process of this test
//     binary (runChild).
//   - Nothing waits by sleeping or polling. Calls that must come back at once
//     are run with a guard (atOnce) that only fails a test that would otherwise
//     hang.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// tmpDir is a fresh, randomly named directory under /tmp, removed at the end
// of the test.
func tmpDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "sgt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// belowCwd is a fresh, randomly named directory below the working directory
// of the process, for tests that need a socket near the cwd so that its
// spelling relative to the cwd is short. It is only for a child process whose
// cwd is itself a temporary directory (inTmpCwd): the test process's own cwd
// is the package directory, which may be read-only (the module cache under
// `go test all`). It returns the directory as an absolute path starting with
// the cwd as getcwd(3) reports it, and its spelling relative to the cwd.
func belowCwd(t *testing.T) (abs, rel string) {
	t.Helper()
	if os.Getenv(childEnv) == "" {
		t.Fatal("belowCwd: only in a child process whose cwd is a temporary directory (inTmpCwd)")
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	d, err := os.MkdirTemp(cwd, "s-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	rel, ok := below(d, cwd)
	if !ok {
		t.Fatalf("%s is not below %s", d, cwd)
	}
	return d, rel
}

// belowCwdPath is a socket path below the cwd whose spelling relative to the
// cwd is rel bytes.
func belowCwdPath(t *testing.T, rel int, name string) string {
	t.Helper()
	abs, r := belowCwd(t)
	return abs + nest(t, r, rel, name)[len(r):]
}

// nest creates directories under base so that `<dirs>/<name>` is exactly
// total bytes long, and returns that socket path. Directory names are runs of
// 'd' of at most 60 bytes (well under NAME_MAX).
func nest(t *testing.T, base string, total int, name string) string {
	t.Helper()
	fixed := len(base) + 1 + len(name)
	if total < fixed {
		t.Fatalf("nest: %d < %d", total, fixed)
	}
	remaining := total - fixed
	dir := base
	for remaining > 0 {
		// Each directory adds "<k bytes>/", so a remainder of 1 cannot be made;
		// avoid leaving one behind.
		if remaining < 2 {
			t.Fatalf("nest: cannot pad %s by exactly one byte", base)
		}
		k := min(remaining-1, 60)
		if remaining-(k+1) == 1 {
			k--
		}
		dir += "/" + strings.Repeat("d", k)
		remaining -= k + 1
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := dir + "/" + name
	if len(p) != total {
		t.Fatalf("nest: %d bytes, want %d: %s", len(p), total, p)
	}
	return p
}

// nestTail is nest with the length given for the part below dir: the socket
// path is `<dir>/<tail bytes>`.
func nestTail(t *testing.T, dir string, tail int, name string) string {
	t.Helper()
	return nest(t, dir, len(dir)+1+tail, name)
}

func isSocket(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&fs.ModeSocket != 0
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func nowPlusGuard() time.Time { return time.Now().Add(guard) }

// guard is how long a call that must return at once may take before the test
// is failed as hung. It is no estimate of a duration: such calls return
// within microseconds. It only turns a hang into a failure with a message.
const guard = 10 * time.Second

// atOnce runs f on another goroutine and returns its result, failing the test
// if it has not returned within guard.
func atOnce[T any](t *testing.T, what string, f func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- f() }()
	select {
	case v := <-done:
		return v
	case <-time.After(guard):
		t.Fatalf("%s blocked", what)
		panic("unreachable")
	}
}

// ping exchanges a message in each direction between a client and the
// connection the listener accepts for it. The deadlines only guard against a
// hang (the data is there before each read).
func ping(t *testing.T, l *net.UnixListener, c *net.UnixConn) {
	t.Helper()
	if err := l.SetDeadline(time.Now().Add(guard)); err != nil {
		t.Fatal(err)
	}
	s, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := l.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	pingConns(t, s, c)
}

// pingConns exchanges a message in each direction between an accepted
// connection s and its client c.
func pingConns(t *testing.T, s, c *net.UnixConn) {
	t.Helper()
	for _, x := range []*net.UnixConn{s, c} {
		if err := x.SetDeadline(time.Now().Add(guard)); err != nil {
			t.Fatal(err)
		}
	}
	buf := make([]byte, 4)
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(s, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("server read %q, %v", buf, err)
	}
	if _, err := s.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "pong" {
		t.Fatalf("client read %q, %v", buf, err)
	}
}

// checkStdTypes checks that a listener and a connection from sockat behave as
// the net package's own: their deadlines work, which they do only for an fd
// registered with the runtime's poller (an fd outside it answers SetDeadline
// with os.ErrNoDeadline, and a blocking fd would make Accept / Read wait
// instead of failing). A deadline in the past makes Accept and Read fail at
// once with os.ErrDeadlineExceeded; cleared again, the pair still talks. The
// addresses can be read without a panic (on a long path they are the
// spelling the kernel was given, not the caller's path; see
// TestAddressesOfALongPathCanBeRead).
func checkStdTypes(t *testing.T, l *net.UnixListener, c *net.UnixConn) {
	t.Helper()
	past := time.Now().Add(-time.Second)
	if err := l.SetDeadline(past); err != nil {
		t.Fatalf("listener SetDeadline: %v", err)
	}
	if err := atOnce(t, "Accept past its deadline", func() error { _, err := l.Accept(); return err }); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Accept past its deadline: %v", err)
	}
	if err := l.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetReadDeadline(past); err != nil {
		t.Fatalf("conn SetReadDeadline: %v", err)
	}
	if err := atOnce(t, "Read past its deadline", func() error { _, err := c.Read(make([]byte, 1)); return err }); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read past its deadline: %v", err)
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	_ = fmt.Sprint(l.Addr(), c.LocalAddr(), c.RemoteAddr())
	ping(t, l, c)
}

// untouched is the modification time markUntouched gives a directory.
var untouched = time.Unix(0, 0)

// markUntouched sets the modification time of dir to a fixed time in the past,
// so a later assertUntouched can tell whether anything was created in it or
// removed from it meanwhile (either updates a directory's mtime). Checking
// that dir is empty is not enough: a symlink made and removed again leaves it
// empty.
func markUntouched(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chtimes(dir, untouched, untouched); err != nil {
		t.Fatal(err)
	}
	assertUntouched(t, dir)
}

func assertUntouched(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(untouched) {
		t.Fatalf("something was created in or removed from %s", dir)
	}
}

// assertEmpty checks that dir has no entries.
func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 0 {
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		t.Fatalf("left behind in %s: %v", dir, names)
	}
}

// staleSocket leaves a socket file at path that nobody listens on.
//
// A child process of this test binary, whose cwd is the socket's directory,
// binds the bare file name without ever listening and exits, so path may be of
// any length and connect to it is ECONNREFUSED. A socket that once listened in
// this process and was closed would do too, except for a moment: other tests
// start child processes (runChild) at the same time, and a forked child holds
// a copy of every fd until it execs, so a listener closed in that window still
// accepts.
func staleSocket(t *testing.T, path string) {
	t.Helper()
	dir, name, err := splitPath(path)
	if err != nil {
		t.Fatal(err)
	}
	runChild(t, "TestChildStaleSocket", dir, "SOCKAT_NAME="+name)
	if !isSocket(path) {
		t.Fatalf("no socket at %s", path)
	}
}

func TestChildStaleSocket(t *testing.T) {
	asChild(t)
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: asSpelling(os.Getenv("SOCKAT_NAME"))}); err != nil {
		t.Fatal(err)
	}
}

// childEnv names the test a child process of this binary is to run in
// earnest; every other run skips it.
const childEnv = "SOCKAT_TEST_CHILD"

// runChild runs the test name of this test binary in a child process whose
// working directory is dir, with env added to its environment ($PWD is dir
// unless env sets it), and requires it to pass. It returns the child's output.
func runChild(t *testing.T, name, dir string, env ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v", "-test.count=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PWD="+dir, childEnv+"="+name)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child %s: %v\n%s", name, err, out)
	}
	// A misspelt name runs nothing and still exits 0.
	if !strings.Contains(string(out), "--- PASS: "+name) {
		t.Fatalf("child %s did not run\n%s", name, out)
	}
	return string(out)
}

// inTmpCwd runs the calling test again in a child process whose cwd is a fresh
// directory under /tmp, and reports whether this is that child: the test body
// runs only there (`if !inTmpCwd(t) { return }`). For tests that need sockets
// near the cwd (the relative spelling): the cwd belongs to the whole process,
// so it is chosen for a child rather than changed here, and the package
// directory, which may be read-only, is never written to.
func inTmpCwd(t *testing.T) bool {
	t.Helper()
	if os.Getenv(childEnv) == t.Name() {
		return true
	}
	runChild(t, t.Name(), tmpDir(t))
	return false
}

// unprivileged reports whether the calling test is to run its body here: as a
// user other than root, it is (`if !unprivileged(t) { return }`). As root, who
// ignores permissions and so breaks the premise of tests about read-only
// directories, it runs the calling test again in a child process (cwd /) that
// drops to nobody / nogroup before the body, and reports false; in that child
// it reports true once the ids are dropped. So such a test runs in the Linux
// container, which is root, rather than being skipped there.
func unprivileged(t *testing.T) bool {
	t.Helper()
	if os.Getenv(childEnv) == t.Name() {
		if os.Geteuid() == 0 {
			// syscall's (not unix's) set the ids of every thread of the
			// process.
			if err := syscall.Setgroups(nil); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Setgid(65534); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Setuid(65534); err != nil {
				t.Fatal(err)
			}
		}
		return true
	}
	if os.Geteuid() == 0 {
		runChild(t, t.Name(), "/")
		return false
	}
	return true
}

// asChild skips the test unless this process is the child runChild started
// for it.
func asChild(t *testing.T) {
	t.Helper()
	if os.Getenv(childEnv) != t.Name() {
		t.Skip("runs only in a child process started by another test")
	}
}

// childReport prints a line for the parent of a child test to read back.
func childReport(format string, args ...any) {
	fmt.Printf("sockat-child: "+format+"\n", args...)
}

// readChildReport finds the line childReport printed in out.
func readChildReport(t *testing.T, out string) string {
	t.Helper()
	for line := range strings.Lines(out) {
		if _, r, ok := strings.Cut(line, "sockat-child: "); ok {
			return strings.TrimSpace(r)
		}
	}
	t.Fatalf("no report\n%s", out)
	return ""
}

// must turns what a route returned into the net package's types, failing the
// test on an error and closing them at its end. It takes a route's results
// directly: must{t}.listener(threadCwdRoute(...)).
type must struct{ t *testing.T }

func (m must) listener(ep endpoint, err error) *net.UnixListener {
	m.t.Helper()
	if err != nil {
		m.t.Fatal(err)
	}
	l := ep.(*net.UnixListener)
	m.t.Cleanup(func() { l.Close() })
	return l
}

func (m must) conn(ep endpoint, err error) *net.UnixConn {
	m.t.Helper()
	if err != nil {
		m.t.Fatal(err)
	}
	c := ep.(*net.UnixConn)
	m.t.Cleanup(func() { c.Close() })
	return c
}

// wantErrno checks that err is errno as errors.Is sees it.
func wantErrno(t *testing.T, what string, err error, errno syscall.Errno) {
	t.Helper()
	if !errors.Is(err, errno) {
		t.Fatalf("%s: got %v, want %v", what, err, errno)
	}
}

// wantOpError checks that err is what the net package returns for a failed
// listen / dial on path: a *net.OpError for the "unix" network whose Addr is
// the caller's path, wrapping errno.
func wantOpError(t *testing.T, what string, o op, path string, err error, errno syscall.Errno) {
	t.Helper()
	wantErrno(t, what, err, errno)
	var oe *net.OpError
	if !errors.As(err, &oe) {
		t.Fatalf("%s: %T is not a *net.OpError: %v", what, err, err)
	}
	if oe.Op != o.netName() || oe.Net != "unix" {
		t.Fatalf("%s: Op %q Net %q", what, oe.Op, oe.Net)
	}
	if oe.Addr == nil || oe.Addr.String() != path {
		t.Fatalf("%s: Addr %v, want the caller's path %q", what, oe.Addr, path)
	}
}

// openTestDir opens dir as the routes open a socket's directory, closed at
// the end of the test.
func openTestDir(t *testing.T, dir string) *os.File {
	t.Helper()
	f, err := openDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// dialTraced dials p through w (w.dial) with opts and returns what it
// returned and the routes it recorded as tried and found unusable, in order.
// A connection is closed at the end of the test.
func dialTraced(t *testing.T, w *world, p string, opts Options) (*net.UnixConn, route, []skipped, error) {
	t.Helper()
	tr := &trace{}
	c, r, err := w.dial(p, opts, tr)
	if c != nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, r, tr.skipped, err
}

// dialIn is w.dial(p) with no Options, failing the test on an error; the
// connection is closed at the end of the test.
func dialIn(t *testing.T, w *world, p string) (*net.UnixConn, route) {
	t.Helper()
	c, r, _, err := dialTraced(t, w, p, Options{})
	if err != nil {
		t.Fatalf("dial %d bytes: %v", len(p), err)
	}
	return c, r
}

// namesOf are the names of the routes in sks, in order. Each must come with
// a reason (a short text, for diagnostics only: its wording is not pinned).
func namesOf(t *testing.T, sks []skipped) []route {
	t.Helper()
	var names []route
	for _, s := range sks {
		if s.reason == "" {
			t.Errorf("%s skipped without a reason", s.name)
		}
		names = append(names, s.name)
	}
	return names
}

// stepNames are the routes of steps, in order.
func stepNames(steps []dialStep) []route {
	var rs []route
	for _, s := range steps {
		rs = append(rs, s.route)
	}
	return rs
}

// assertNoDOrE checks that no B w holds has a D or anything in it: what a
// dial leaves (it makes no D, and unlinks the E it made right in B).
func assertNoDOrE(t *testing.T, w *world) {
	t.Helper()
	for _, b := range w.bases() {
		if b.d != nil {
			t.Fatalf("a D was made in %s", b.real)
		}
		assertEmpty(t, b.real)
	}
}

// listenAt makes a listener whose socket file is at p, however long p is and
// whatever routes a listen has here: bound at a short path in /tmp (the net
// package itself) and renamed to p. p must be on the same file system as
// /tmp. The listener does not remove anything on Close.
func listenAt(t *testing.T, p string) *net.UnixListener {
	t.Helper()
	short := tmpDir(t) + "/s"
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: short, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	t.Cleanup(func() { l.Close() })
	if err := os.Rename(short, p); err != nil {
		t.Fatal(err)
	}
	return l
}

// listenPublic is Listen(p), failing the test on an error; the listener is
// closed at the end of the test.
func listenPublic(t *testing.T, p string) *net.UnixListener {
	t.Helper()
	l, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

// listenRoute binds p as Listen does and returns the route taken, failing the
// test on an error; the listener is closed at the end of the test.
func listenRoute(t *testing.T, p string) (*net.UnixListener, route) {
	t.Helper()
	l, b, err := defaultWorld.listen(p, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, b.route
}

// threadName is a file name thread-cwd carries on Linux and macOS alike
// (E/<name> is 103 bytes, macOS's directMax) and link-dir does not.
var threadName = strings.Repeat("t", 92)

// directName is a file name thread-cwd-direct and link-file carry on Linux
// and macOS alike (at most 103 bytes) and proc-direct does not
// (/proc/self/fd/<n>/<name> is past Linux's 107).
var directName = strings.Repeat("n", 100)

// mkdirMode creates the directory p with exactly mode (set after creating it,
// so the umask does not interfere) and gives it back 0700 at the end of the
// test so it can be removed.
func mkdirMode(t *testing.T, p string, mode os.FileMode) string {
	t.Helper()
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o700) })
	return p
}

// bindFollowsSymlink reports whether this kernel's bind(2) follows a symlink
// in the last component to a path where nothing is yet, making the socket
// there: SPEC's premise for link-file in a bind (macOS and NetBSD do;
// Linux, FreeBSD, OpenBSD and illumos do not). Found out once, with a raw socket
// bound at a symlink in a fresh directory under /tmp, independently of the
// code under test.
var bindFollowsSymlink = sync.OnceValue(func() bool {
	d, err := os.MkdirTemp("/tmp", "sgt-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(d)
	if err := os.Symlink(d+"/target", d+"/link"); err != nil {
		panic(err)
	}
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		panic(err)
	}
	defer unix.Close(fd)
	return unix.Bind(fd, &unix.SockaddrUnix{Name: d + "/link"}) == nil && isSocket(d+"/target")
})

// routeErr is err as the *RouteError ListenEx / DialEx return.
func routeErr(t *testing.T, err error) *RouteError {
	t.Helper()
	var re *RouteError
	if !errors.As(err, &re) {
		t.Fatalf("%T is not a *RouteError: %v", err, err)
	}
	return re
}

// skippedNames are the names of the routes Skipped reports as tried and found
// unusable, in order. Each must come with a reason (a short text, for
// diagnostics only: its wording is not pinned).
func skippedNames(t *testing.T, sks []Skip) []route {
	t.Helper()
	var names []route
	for _, s := range sks {
		if s.Reason == "" {
			t.Errorf("%s skipped without a reason", s.Name)
		}
		names = append(names, route(s.Name))
	}
	return names
}
