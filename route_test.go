package sockat

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// directMax is sun_path less the NUL: the longest path the net package
// accepts. Pinned per system, because every path at most this long goes to the
// net package untouched and every longer one to the routes.
func TestDirectMaxIsSunPathLessTheNUL(t *testing.T) {
	t.Parallel()
	want := map[string]int{
		"linux": 107, "android": 107, "illumos": 107, "solaris": 107,
		"darwin": 103, "ios": 103, "freebsd": 103, "netbsd": 103, "openbsd": 103, "dragonfly": 103,
	}[runtime.GOOS]
	if want == 0 {
		t.Skipf("no expectation for %s", runtime.GOOS)
	}
	if directMax != want {
		t.Fatalf("directMax = %d, want %d", directMax, want)
	}
}

// The first principle: every path the net package accepts behaves exactly as
// with the net package, and no path it refuses is handed to it. The boundary
// is checked against net.ListenUnix itself: it binds a path of directMax
// bytes and refuses one byte more. Then run sends the directMax-byte path to
// the net package unchanged (routeDirect) and the one a byte longer down the
// routes (anything else), with the socket at the given path either way.
func TestAPathTheNetPackageAcceptsGoesToItAndOneByteMoreDoesNot(t *testing.T) {
	t.Parallel()
	std := tmpDir(t)
	at := nest(t, std, directMax, "a.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: at, Net: "unix"})
	if err != nil {
		t.Fatalf("the premise: net.ListenUnix takes %d bytes: %v", directMax, err)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: at, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ping(t, l, c)
	l.Close()
	c.Close()
	over := nest(t, std, directMax+1, "o.sock")
	if _, err := net.ListenUnix("unix", &net.UnixAddr{Name: over, Net: "unix"}); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("the premise: net.ListenUnix refuses %d bytes with EINVAL: %v", directMax+1, err)
	}
	if exists(over) {
		t.Fatal("net.ListenUnix created something at a path it refused")
	}

	d := tmpDir(t)
	at = nest(t, d, directMax, "s.sock")
	ep, r, err := run(opListen, at)
	ll := must{t}.listener(ep, err)
	if r != routeDirect {
		t.Fatalf("%d bytes: %s, want %s", directMax, r, routeDirect)
	}
	ep, r, err = run(opDial, at)
	cc := must{t}.conn(ep, err)
	if r != routeDirect {
		t.Fatalf("%d bytes: %s, want %s", directMax, r, routeDirect)
	}
	ping(t, ll, cc)

	over = nest(t, d, directMax+1, "o.sock")
	ep, r, err = run(opListen, over)
	ll = must{t}.listener(ep, err)
	if r == routeDirect {
		t.Fatalf("%d bytes went to the net package", directMax+1)
	}
	if !isSocket(over) {
		t.Fatalf("no socket at %s (route %s)", over, r)
	}
	ep, r, err = run(opDial, over)
	cc = must{t}.conn(ep, err)
	if r == routeDirect {
		t.Fatalf("%d bytes went to the net package", directMax+1)
	}
	ping(t, ll, cc)
}

// Closing a listener: a short path behaves as the net package does, which
// removes the socket file (SetUnlinkOnClose defaults to true there). A long
// path's listener does not remove it. The address the listener holds is the
// spelling the kernel was given (/proc/self/fd/N/name, a relative path, a
// symlink that is gone, a bare file name); unlinking that spelling at Close
// would resolve it against whatever the fd number, the cwd or the symlink's
// name means by then, and could remove some other file. So the long path's
// socket file stays, for the caller to remove by the path they hold.
func TestOnlyAShortPathsListenerRemovesItsSocketFileOnClose(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	short := d + "/s.sock"
	l, err := Listen(short)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if exists(short) {
		t.Fatal("a short path's listener left its socket file on Close")
	}

	for _, total := range []int{directMax + 1, 300} {
		long := nest(t, d, total, "s.sock")
		l, err := Listen(long)
		if err != nil {
			t.Fatal(err)
		}
		l.Close()
		if !isSocket(long) {
			t.Fatalf("%d bytes: a long path's listener removed its socket file on Close", total)
		}
	}
}

// Long paths of several sizes, through the public API with the real
// environment: the socket is at the given path, a client reaching it by the
// same path talks to that listener, and both are the net package's types in
// full working order. The sizes run from directMax + 1 through 150 (past
// sun_path on every system) and 253 / 254 (where SPEC's widened sockaddr on
// macOS would end; sockat-go does not use it, so nothing may change there) to
// 600 (past everything but the routes that never put the directory in the
// sockaddr).
func TestLongPathsListenAtTheGivenPathAndAcceptConnections(t *testing.T) {
	t.Parallel()
	for _, total := range []int{directMax + 1, 150, 253, 254, 300, 600} {
		d := tmpDir(t)
		p := nest(t, d, total, "s.sock")
		l, err := Listen(p)
		if err != nil {
			t.Fatalf("%d: %v", total, err)
		}
		defer l.Close()
		if !isSocket(p) {
			t.Fatalf("%d: no socket at the path", total)
		}
		c, err := Dial(p)
		if err != nil {
			t.Fatalf("%d: %v", total, err)
		}
		defer c.Close()
		checkStdTypes(t, l, c)
	}
}

// Failures come out as the kernel's errors for the path the caller gave,
// whatever the length and route, in the shape the net package uses: a
// *net.OpError (Op "listen" / "dial", Net "unix", Addr the caller's path, not
// a spelling) that errors.Is matches against the errno. A missing directory
// is ENOENT for both, a missing socket ENOENT, an existing file EADDRINUSE, a
// socket nobody listens on ECONNREFUSED. A route must not turn these into
// "try the next route" and end up reporting something else, or creating the
// socket elsewhere. The short path is the net package's own answer, for
// comparison.
func TestFailuresAreTheKernelsErrorsAsNetOpErrors(t *testing.T) {
	t.Parallel()
	for _, total := range []int{60, 150, 300, 600} {
		d := tmpDir(t)
		p := nest(t, d, total, "s.sock")
		what := func(s string) string { return fmt.Sprintf("%d bytes: %s", total, s) }

		missingDir := filepath.Dir(p) + "/nope/s.sock"
		_, err := Listen(missingDir)
		wantOpError(t, what("listen in a missing directory"), opListen, missingDir, err, syscall.ENOENT)
		_, err = Dial(missingDir)
		wantOpError(t, what("dial in a missing directory"), opDial, missingDir, err, syscall.ENOENT)
		if exists(filepath.Dir(missingDir)) {
			t.Fatal(what("the missing directory was created"))
		}

		_, err = Dial(p)
		wantOpError(t, what("dial a missing socket"), opDial, p, err, syscall.ENOENT)

		l, err := Listen(p)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		_, err = Listen(p)
		wantOpError(t, what("listen on an existing socket"), opListen, p, err, syscall.EADDRINUSE)

		stale := filepath.Dir(p) + "/stale.sock"
		staleSocket(t, stale)
		_, err = Dial(stale)
		wantOpError(t, what("dial a socket nobody listens on"), opDial, stale, err, syscall.ECONNREFUSED)
	}
}

// A missing directory is ENOENT even when the file name (254 bytes) fits no
// sockaddr: the directory is looked up before the name is put in one, so the
// caller learns that the directory is missing, not that the path is too long.
func TestAMissingDirectoryIsENOENTEvenWhenTheNameFitsNoSockaddr(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := d + "/nope/" + strings.Repeat("n", 254)
	_, err := Listen(p)
	wantOpError(t, "listen", opListen, p, err, syscall.ENOENT)
	_, err = Dial(p)
	wantOpError(t, "dial", opDial, p, err, syscall.ENOENT)
	if exists(d + "/nope") {
		t.Fatal("the missing directory was created")
	}
}

// A file name of 254 bytes (NAME_MAX is 255) directly under a short directory.
//
//   - Listen: every route that does not hand the whole path to the kernel puts
//     the file name in a sockaddr (behind /proc/self/fd/N/, behind the symlink
//     to the directory, alone from the thread-cwd route, behind bindat), and
//     254 bytes fits none of them: ENAMETOOLONG, the error of a bind to a path
//     too long for sun_path, and nothing is created.
//   - Dial (except FreeBSD, whose connectat takes the name as bindat does): the
//     symlink to the socket file is spelled without the file name, so the
//     name's length does not matter. With nothing there the error is the
//     kernel's ENOENT, not ENAMETOOLONG; a socket bound under a short name and
//     renamed to the long one is reached and talks.
func TestAFileNameOf254BytesCannotBeListenedOnButCanBeDialed(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := d + "/" + strings.Repeat("n", 254)
	_, err := Listen(p)
	wantOpError(t, "listen", opListen, p, err, syscall.ENAMETOOLONG)
	if exists(p) {
		t.Fatal("something was created at the path")
	}
	if runtime.GOOS == "freebsd" {
		_, err = Dial(p)
		wantOpError(t, "dial", opDial, p, err, syscall.ENAMETOOLONG)
		return
	}
	_, err = Dial(p)
	wantOpError(t, "dial with nothing there", opDial, p, err, syscall.ENOENT)

	l, err := Listen(d + "/s.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Rename(d+"/s.sock", p); err != nil {
		t.Fatal(err)
	}
	c, err := Dial(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ping(t, l, c)
}

// A long path that does not end with a file name ("<dir>/", "<dir>/.",
// "<dir>/..") names a directory; binding "the last component" would create a
// socket named after a directory, which is not what the path says. It is
// refused with EINVAL before anything is looked up or created (the directory
// and its parent keep their mtime).
//
// A NUL anywhere but first is EINVAL whatever the length: the net package
// would silently cut the path at the NUL and bind / connect the shorter path
// (/d/a for /d/a\x00b). The short case is the one that differs from the net
// package; nothing may appear at the cut path.
func TestPathsWithoutAFileNameOrWithANULAreEINVALAndCreateNothing(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	dir := filepath.Dir(nest(t, d, 150, "s.sock"))
	parent := filepath.Dir(dir)
	shortDir := d + "/short"
	if err := os.Mkdir(shortDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		dir + "/",
		dir + "/.",
		dir + "/..",
		dir + "/a\x00b",
		shortDir + "/a\x00b",
		shortDir + "/a\x00",
	}
	for _, dd := range []string{dir, parent, shortDir} {
		markUntouched(t, dd)
	}
	for _, p := range bad {
		_, err := Listen(p)
		wantOpError(t, fmt.Sprintf("listen %q", p), opListen, p, err, syscall.EINVAL)
		_, err = Dial(p)
		wantOpError(t, fmt.Sprintf("dial %q", p), opDial, p, err, syscall.EINVAL)
	}
	for _, dd := range []string{dir, parent, shortDir} {
		assertUntouched(t, dd)
	}
	if exists(shortDir+"/a") || exists(dir+"/a") {
		t.Fatal("the path cut at the NUL was created")
	}
}

// The addresses the kernel reports for a long path (getsockname /
// getpeername) are the spelling it was given (a /proc/self/fd path, a relative
// path, a symlink that is gone, a bare file name, or a path cut short), not the
// caller's path, which the caller keeps. Reading them must not fail or panic,
// for the listener, the dialed connection and the accepted one, at lengths
// that take each system's routes.
func TestAddressesOfALongPathCanBeRead(t *testing.T) {
	t.Parallel()
	for _, total := range []int{directMax + 1, 200, 600} {
		p := nest(t, tmpDir(t), total, "s.sock")
		l, err := Listen(p)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		c, err := Dial(p)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		s, err := l.AcceptUnix()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if l.Addr() == nil || l.Addr().String() == "" {
			t.Fatalf("%d: the listener has no address", total)
		}
		t.Logf("%d bytes: listener %v; conn local %v remote %v; accepted local %v remote %v",
			total, l.Addr(), c.LocalAddr(), c.RemoteAddr(), s.LocalAddr(), s.RemoteAddr())
	}
}

// Where the net package has no abstract socket names (macOS and the BSDs),
// a leading NUL is a NUL like any other: EINVAL whatever the length, and
// nothing is created (the net package would cut the path at the NUL, here to
// the empty path, or to the bytes before a later NUL). A leading '@' is an
// ordinary byte there and names a file.
func TestALeadingNULIsEINVALWhereThereAreNoAbstractNames(t *testing.T) {
	t.Parallel()
	if abstractNames {
		t.Skip("the net package reads a leading NUL as an abstract name here (TestAbstractNamesGoToTheNetPackageAsTheyAre)")
	}
	d := tmpDir(t)
	markUntouched(t, d)
	for _, p := range []string{"\x00", "\x00s.sock", "\x00" + d + "/s.sock", "\x00" + strings.Repeat("a", 200)} {
		_, err := Listen(p)
		wantOpError(t, fmt.Sprintf("listen %q", p), opListen, p, err, syscall.EINVAL)
		_, err = Dial(p)
		wantOpError(t, fmt.Sprintf("dial %q", p), opDial, p, err, syscall.EINVAL)
	}
	assertUntouched(t, d)

	at := d + "/@s.sock"
	l, err := Listen(at)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !isSocket(at) {
		t.Fatal("a leading '@' in a file name did not name a file")
	}
}
