package sockat

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/kawaz/sockat-go/internal/argerr"
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
// the net package unchanged (routeStandard) and the one a byte longer down the
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
	ll, r := listenRoute(t, at)
	if r != routeStandard {
		t.Fatalf("%d bytes: %s, want %s", directMax, r, routeStandard)
	}
	cc, r := dialIn(t, defaultWorld, at)
	if r != routeStandard {
		t.Fatalf("%d bytes: %s, want %s", directMax, r, routeStandard)
	}
	ping(t, ll, cc)

	over = nest(t, d, directMax+1, "o.sock")
	ll, r = listenRoute(t, over)
	if r == routeStandard {
		t.Fatalf("%d bytes went to the net package", directMax+1)
	}
	if !isSocket(over) {
		t.Fatalf("no socket at %s (route %s)", over, r)
	}
	cc, r = dialIn(t, defaultWorld, over)
	if r == routeStandard {
		t.Fatalf("%d bytes went to the net package", directMax+1)
	}
	ping(t, ll, cc)
}

// Closing a listener of Listen unlinks the address it holds, as the net
// package's does: a short path's socket file is removed, and so is a long
// path's bound through link-dir (its spelling B/D/E/<name> goes through E to
// the socket's directory, so the unlink reaches the file). The other
// spellings are TestTheListenerOfListenUnlinksItsSpellingOnClose's.
func TestAListenersCloseRemovesTheSocketFileThroughTheSpelling(t *testing.T) {
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
		l, r := listenRoute(t, long)
		if r != routeLinkDir {
			t.Fatalf("%d bytes: %s, want %s", total, r, routeLinkDir)
		}
		l.Close()
		if exists(long) {
			t.Fatalf("%d bytes: a link-dir listener left its socket file on Close", total)
		}
	}
}

// Long paths of several sizes, through the public API with the real
// environment: the socket is at the given path, a client reaching it by the
// same path talks to that listener, and both are the net package's types in
// full working order. The sizes run from directMax + 1 through 150 (past
// sun_path on every system) and 253 / 254 (where SPEC's widened sockaddr on
// macOS would end; sockat-go does not use it, so nothing may change there) to
// 600: no route puts the directory in the sockaddr.
func TestLongPathsListenAtTheGivenPathAndAcceptConnections(t *testing.T) {
	t.Parallel()
	for _, total := range []int{directMax + 1, 150, 253, 254, 300, 600} {
		d := tmpDir(t)
		p := nest(t, d, total, "s.sock")
		l := listenPublic(t, p)
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

		listenPublic(t, p)
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
//   - Listen: link-dir, thread-cwd and at put the file name in the sockaddr
//     behind B/D/E/ or E/, and 254 bytes fits none: ENAMETOOLONG, the error
//     of a bind to a path too long for sun_path, and nothing is created.
//     Where a bind follows a symlink (macOS, NetBSD), link-file is spelled
//     without the file name and binds it at the path; Linux does not try
//     it.
//   - Dial: link-file, last in every system's connect order, is spelled
//     <B>/<E> without the file name, so the name's length does not matter.
//     With nothing there the error is the kernel's ENOENT, not ENAMETOOLONG;
//     a socket bound under a short name and renamed to the long one is
//     reached and talks.
func TestAFileNameOf254BytesIsListenedOnOnlyThroughLinkFileAndDialed(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := d + "/" + strings.Repeat("n", 254)
	l, err := Listen(p)
	if bindFollowsHere != followsNo && bindFollowsSymlink() {
		if err != nil || !isSocket(p) {
			t.Fatalf("listen through symlink-file: %v, socket there %v", err, isSocket(p))
		}
		l.Close()
		os.Remove(p)
	} else {
		wantOpError(t, "listen", opListen, p, err, syscall.ENAMETOOLONG)
		if exists(p) {
			t.Fatal("something was created at the path")
		}
	}
	_, err = Dial(p)
	wantOpError(t, "dial with nothing there", opDial, p, err, syscall.ENOENT)

	l, err = Listen(d + "/s.sock")
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

// An Options.Base with a NUL in it is of the wrong form (SPEC "エラー"), as a
// relative one is: the base is a path the system calls would cut short at
// the NUL, so it names some other directory than the one written. The form
// of the base is checked whatever the length of the path and before the
// abstract name is told apart, so a short path, a long one and (where the
// kernel has them) an abstract name with such a base are all EINVAL with no
// route and nothing skipped, for a bind, a connect and both resolves, and
// nothing appears at the path, at the base or at the base cut at the NUL.
// SweepBase of such a base is EINVAL as well.
func TestAnOptionsBaseWithANULIsEINVALWhateverThePath(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	for _, pl := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		markUntouched(t, pl)
	}
	d := tmpDir(t)
	markUntouched(t, d)
	bad := d + "/b\x00c"
	paths := []string{tmpDir(t) + "/s.sock", nest(t, tmpDir(t), 300, "s.sock")}
	if abstractNames {
		paths = append(paths, "@sgt-base-nul-"+strconv.Itoa(os.Getpid()))
	}
	for _, p := range paths {
		opts := Options{Base: bad}
		_, _, err := w.listenEx(p, opts)
		if re := routeErr(t, err); !errors.Is(err, syscall.EINVAL) || re.Route != "" || len(re.Skipped) != 0 {
			t.Fatalf("listen %q: %+v %v", p, re, err)
		}
		_, r, sks, err := dialTraced(t, w, p, opts)
		if !errors.Is(err, syscall.EINVAL) || r != "" || len(sks) != 0 {
			t.Fatalf("dial %q: route %q, skipped %v, %v", p, r, sks, err)
		}
		_, err = w.resolveForBind(p, opts)
		wantResolveError(t, "resolve for bind "+p, p, err, syscall.EINVAL)
		_, err = w.resolveForConnect(p, opts)
		wantResolveError(t, "resolve for connect "+p, p, err, syscall.EINVAL)
		if !abstractName(p) && exists(p) {
			t.Fatalf("%q was made", p)
		}
	}
	if err := SweepBase(bad); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("SweepBase: %v, want EINVAL", err)
	}
	assertUntouched(t, d)
	for _, pl := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertUntouched(t, pl)
	}
}

// The argument errors sockat finds itself (SPEC "エラー": an empty path, a
// NUL other than an abstract name's leading one, a long path not ending with
// a file name, an Options.Base of the wrong form) carry the internal mark
// (internal/argerr), by which the conformance CLI tells them,
// "invalid-argument", from an EINVAL a system call or the standard library
// returned, which it reports as "EINVAL". The mark must reach every public
// function's error and change nothing a caller sees: errors.Is(err,
// syscall.EINVAL) still holds, and the error is the same type in the same
// shape as before (*net.OpError, *RouteError, *os.PathError). An error that
// is not one of them carries no mark: a long path whose directory is missing
// (ENOENT), and, where the kernel has abstract names, an abstract name too
// long for sun_path, which the standard library refuses with an EINVAL of its
// own.
func TestSockatsOwnArgumentErrorsAndOnlyThoseAreMarked(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	long := nest(t, tmpDir(t), 300, "s.sock")
	type call struct {
		name string
		do   func(p string, o Options) error
	}
	calls := []call{
		{"Listen", func(p string, o Options) error { _, err := Listen(p); return wantType[*net.OpError](t, err) }},
		{"Dial", func(p string, o Options) error { _, err := Dial(p); return wantType[*net.OpError](t, err) }},
		{"ListenUnixgram", func(p string, o Options) error { _, err := ListenUnixgram(p); return wantType[*net.OpError](t, err) }},
		{"DialUnixgram", func(p string, o Options) error { _, err := DialUnixgram(p); return wantType[*net.OpError](t, err) }},
		{"ListenEx", func(p string, o Options) error {
			_, _, err := ListenEx(p, o)
			return wantRouteError[*net.OpError](t, err)
		}},
		{"DialEx", func(p string, o Options) error {
			_, _, err := DialEx(p, o)
			return wantRouteError[*net.OpError](t, err)
		}},
		{"ListenUnixgramEx", func(p string, o Options) error {
			_, _, err := ListenUnixgramEx(p, o)
			return wantRouteError[*net.OpError](t, err)
		}},
		{"DialUnixgramEx", func(p string, o Options) error {
			_, _, err := DialUnixgramEx(p, o)
			return wantRouteError[*net.OpError](t, err)
		}},
		{"ResolveForBind", func(p string, o Options) error {
			_, err := ResolveForBind(p, o)
			return wantRouteError[*os.PathError](t, err)
		}},
		{"ResolveForConnect", func(p string, o Options) error {
			_, err := ResolveForConnect(p, o)
			return wantRouteError[*os.PathError](t, err)
		}},
	}
	args := []struct {
		what string
		path string
		opts Options
	}{
		{"an empty path", "", Options{}},
		{"a NUL in a short path", d + "/a\x00b", Options{}},
		{"a NUL in a long path", long + "\x00b", Options{}},
		{"a long path ending with /", filepath.Dir(long) + "/", Options{}},
		{"a long path ending with /..", filepath.Dir(long) + "/..", Options{}},
		{"a relative Options.Base", d + "/s.sock", Options{Base: "rel"}},
		{"an Options.Base ending with /", long, Options{Base: d + "/b/"}},
		{"an Options.Base with a NUL", long, Options{Base: d + "/b\x00c"}},
	}
	for _, a := range args {
		for _, c := range calls {
			if a.opts.Base != "" && !strings.HasSuffix(c.name, "Ex") && !strings.HasPrefix(c.name, "Resolve") {
				continue // no Options to give
			}
			err := c.do(a.path, a.opts)
			if !errors.Is(err, syscall.EINVAL) || !argerr.Is(err) {
				t.Errorf("%s, %s: %v (EINVAL %v, marked %v)", a.what, c.name, err, errors.Is(err, syscall.EINVAL), argerr.Is(err))
			}
		}
	}
	if err := SweepBase("rel"); !errors.Is(err, syscall.EINVAL) || !argerr.Is(err) {
		t.Errorf("SweepBase of a relative base: %v, marked %v", err, argerr.Is(err))
	}
	if _, ok := SweepBase("rel").(*os.PathError); !ok {
		t.Error("SweepBase's error is not an *os.PathError")
	}

	missing := filepath.Dir(long) + "/missing/s.sock"
	for _, c := range calls {
		if err := c.do(missing, Options{}); !errors.Is(err, syscall.ENOENT) || argerr.Is(err) {
			t.Errorf("a missing directory, %s: %v, marked %v", c.name, err, argerr.Is(err))
		}
	}
	if abstractNames {
		tooLong := "@" + strings.Repeat("a", directMax+10)
		for _, c := range calls[:8] {
			if err := c.do(tooLong, Options{}); !errors.Is(err, syscall.EINVAL) || argerr.Is(err) {
				t.Errorf("an abstract name too long, %s: %v, marked %v", c.name, err, argerr.Is(err))
			}
		}
	}
}

// wantType checks that err is a T itself (the type a function returns its
// errors as) and returns it.
func wantType[T error](t *testing.T, err error) error {
	t.Helper()
	if _, ok := err.(T); !ok {
		var zero T
		t.Errorf("%T is not a %T: %v", err, zero, err)
	}
	return err
}

// wantRouteError checks that err is a *RouteError whose Err is a T, and
// returns err.
func wantRouteError[T error](t *testing.T, err error) error {
	t.Helper()
	re, ok := err.(*RouteError)
	if !ok {
		t.Errorf("%T is not a *RouteError: %v", err, err)
		return err
	}
	if _, ok := re.Err.(T); !ok {
		var zero T
		t.Errorf("RouteError.Err %T is not a %T: %v", re.Err, zero, err)
	}
	return err
}

// An empty path is an invalid argument (SPEC "短いパス"): every function
// refuses it with EINVAL in its own error type, and gives no socket. The net
// package would otherwise bind an address of the kernel's choosing (an
// abstract name on Linux, where ListenEx could then record no file) or fail
// a dial with an error of no errno, and a resolve would spell the empty
// path. Listen, Dial, ListenUnixgram and DialUnixgram return the
// *net.OpError of the net package's shape (Op, Net, the caller's path as
// Addr); each Ex a *RouteError with no route and nothing skipped that
// unwraps to that *net.OpError; ResolveForBind and ResolveForConnect a
// *RouteError around an *os.PathError of Op "resolve" for the path.
func TestAnEmptyPathIsEINVALEverywhere(t *testing.T) {
	t.Parallel()
	wantNetError := func(what string, o op, err error) {
		t.Helper()
		var oe *net.OpError
		if !errors.Is(err, syscall.EINVAL) || !errors.As(err, &oe) {
			t.Fatalf("%s: %T %v, want a *net.OpError of EINVAL", what, err, err)
		}
		if oe.Op != o.netName() || oe.Net != o.network() || oe.Addr == nil || oe.Addr.String() != "" {
			t.Fatalf("%s: Op %q Net %q Addr %v", what, oe.Op, oe.Net, oe.Addr)
		}
	}
	wantRouteError := func(what string, o op, err error) {
		t.Helper()
		re := routeErr(t, err)
		if re.Route != "" || len(re.Skipped) != 0 {
			t.Fatalf("%s: route %q, skipped %v", what, re.Route, re.Skipped)
		}
		wantNetError(what, o, err)
	}

	l, err := Listen("")
	wantNetError("Listen", opListen, err)
	c, err := Dial("")
	wantNetError("Dial", opDial, err)
	g, err := ListenUnixgram("")
	wantNetError("ListenUnixgram", opListenGram, err)
	gc, err := DialUnixgram("")
	wantNetError("DialUnixgram", opDialGram, err)
	if l != nil || c != nil || g != nil || gc != nil {
		t.Fatalf("a socket for the empty path: %v %v %v %v", l, c, g, gc)
	}

	le, lb, err := ListenEx("", Options{})
	wantRouteError("ListenEx", opListen, err)
	ce, _, err := DialEx("", Options{})
	wantRouteError("DialEx", opDial, err)
	ge, gb, err := ListenUnixgramEx("", Options{})
	wantRouteError("ListenUnixgramEx", opListenGram, err)
	gce, _, err := DialUnixgramEx("", Options{})
	wantRouteError("DialUnixgramEx", opDialGram, err)
	if le != nil || lb != nil || ce != nil || ge != nil || gb != nil || gce != nil {
		t.Fatal("a socket or a Bound for the empty path")
	}

	fb, err := ResolveForBind("", Options{})
	wantResolveError(t, "ResolveForBind", "", err, syscall.EINVAL)
	fc, err := ResolveForConnect("", Options{})
	wantResolveError(t, "ResolveForConnect", "", err, syscall.EINVAL)
	if fb != nil || fc != nil {
		t.Fatalf("a spelling for the empty path: %+v %+v", fb, fc)
	}
}

// The addresses the kernel reports for a long path (getsockname /
// getpeername) are the spelling it was given (B/D/E/<name>, E/<name> with E
// gone, a /proc/self/fd path, a relative path), not the caller's path, which the
// caller keeps. Reading them must not fail or panic,
// for the listener, the dialed connection and the accepted one, at lengths
// that take each system's routes.
func TestAddressesOfALongPathCanBeRead(t *testing.T) {
	t.Parallel()
	for _, total := range []int{directMax + 1, 200, 600} {
		p := nest(t, tmpDir(t), total, "s.sock")
		l := listenPublic(t, p)
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
		// Each address must be a non-nil net.Addr whose String() works (a nil
		// one panics there). The bound side (the listener, the accepted
		// connection's local end, the dialed connection's remote end) must
		// name something; the dialing side is unbound and may be empty.
		addrs := []struct {
			what  string
			a     net.Addr
			named bool
		}{
			{"listener", l.Addr(), true},
			{"accepted local", s.LocalAddr(), true},
			{"accepted remote", s.RemoteAddr(), false},
			{"dialed local", c.LocalAddr(), false},
			{"dialed remote", c.RemoteAddr(), true},
		}
		for _, x := range addrs {
			if x.a == nil {
				t.Fatalf("%d: %s address is nil", total, x.what)
			}
			str := x.a.String()
			if x.named && (str == "" || str == "@") {
				t.Fatalf("%d: %s address %q names nothing", total, x.what, str)
			}
			t.Logf("%d bytes: %s %q", total, x.what, str)
		}
	}
}

// Where the kernel has no abstract socket names (all but Linux and Android,
// SPEC "短いパス"), a leading NUL is a NUL like any other: EINVAL whatever the
// length, and nothing is created (the net package would cut the path at the
// NUL, here to the empty path, or to the bytes before a later NUL, and on
// illumos read it as an abstract name the kernel does not have). A leading
// '@' is an ordinary byte there and names a file.
func TestALeadingNULIsEINVALWhereThereAreNoAbstractNames(t *testing.T) {
	t.Parallel()
	if abstractNames {
		t.Skip("a leading NUL is an abstract name here (TestAbstractNamesGoToTheNetPackageAsTheyAre)")
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

// Where the kernel has no abstract socket names (all but Linux and Android),
// a path that starts with '@' is a relative path like any other (SPEC "短いパス"):
// a short one is bound through the standard library as the file "@..." in the
// cwd, CwdRelative as every relative path taken as it is, and reached by a dial of the same path. On illumos the net package would
// read the '@' as an abstract name, which that kernel lacks (ENOENT);
// sockat-go hands it "./@..." there instead (DR-0002), so the kernel keeps
// that spelling, which still names the same file. Close leaves the file on
// every system: elsewhere the listener is the net package's own for "@...",
// whose Close skips the removal of any address that starts with '@'; on
// illumos sockat-go's listener for "./@..." does the same, so that Close does
// not unlink a relative spelling after the cwd may have changed. Where "./" makes the spelling longer than the
// standard library takes (a path of directMax bytes), the path goes through
// the routes as a long one, and a dial still reaches the file; elsewhere such
// a path is still the standard library's. Runs in a child whose cwd is a fresh
// directory, since the paths are relative to it.
func TestALeadingAtIsAPathWhereThereAreNoAbstractNames(t *testing.T) {
	t.Parallel()
	if abstractNames {
		t.Skip("a leading '@' is an abstract name here (TestAbstractNamesGoToTheNetPackageAsTheyAre)")
	}
	if !inTmpCwd(t) {
		return
	}
	const p = "@s.sock"
	l, res, err := ListenEx(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if res.Route != string(routeStandard) || res.Spelling != (Spelling{Kind: CwdRelative}) {
		t.Fatalf("%q: %+v, want CwdRelative through standard", p, res)
	}
	if !isSocket(p) {
		t.Fatalf("%q did not name the file %q in the cwd", p, p)
	}
	if a := l.Addr().String(); !isSocket(a) {
		t.Fatalf("the listener's address %q does not name the socket file", a)
	}
	c, err := Dial(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ping(t, l, c)

	const q = "@t.sock"
	lq, err := Listen(q)
	if err != nil {
		t.Fatal(err)
	}
	if !isSocket(q) {
		t.Fatalf("%q did not name the file %q in the cwd", q, q)
	}
	lq.Close()
	if _, err := os.Lstat(q); err != nil {
		t.Fatalf("Close removed %q, which a path starting with '@' keeps: %v", q, err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	edge := "@" + strings.Repeat("e", directMax-1)
	le := listenAt(t, filepath.Join(wd, edge))
	ce, r := dialIn(t, defaultWorld, edge)
	ping(t, le, ce)
	if stdReadsAtAsAbstract && r == routeStandard {
		t.Fatalf("%d bytes from '@': went through standard, whose spelling \"./\" + path does not fit", len(edge))
	}
	if !stdReadsAtAsAbstract && r != routeStandard {
		t.Fatalf("%d bytes from '@': %s, want standard (the path as it is)", len(edge), r)
	}
}

// A long path whose directory part is a file, not a directory, is ENOTDIR,
// decided by looking the directory up first: not ENAMETOOLONG (which a failure
// later in the order would give), and nothing is made in the places.
// (The other early error, a directory whose real path cannot be resolved
// although stat finds it, is TestADirectoryWhoseRealPathCannotBeResolvedIsThatError.)
func TestADirectoryPartThatIsAFileIsENOTDIR(t *testing.T) {
	t.Parallel()
	f := nest(t, tmpDir(t), 300, "f")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := f + "/s.sock"
	w, ps := testWorld(t, nil)
	markUntouched(t, ps.tmp)
	if _, _, err := w.dial(p, Options{}, nil); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("dial: %v, want ENOTDIR", err)
	}
	if _, _, err := w.listen(p, nil); !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("listen: %v, want ENOTDIR", err)
	}
	assertUntouched(t, ps.tmp)
}

// The socket's directory of a long relative path ("./" + a long file name)
// may be a cwd that was removed: the process still has it, but it has no path
// any more. The lookup of the directory comes before anything else (SPEC
// "長いパスの経路と試す順番"), so the listen and the dial fail with the
// lookup's error, not ENAMETOOLONG, and nothing is made in the places.
// Which step fails depends on the system: stat(".") still finds the removed
// cwd on Linux, macOS, FreeBSD and NetBSD, and then its real path cannot be
// resolved (ENOENT); on OpenBSD stat(".") itself fails (ENOENT), and then
// that is the error. The child checks whichever its system gives. The cwd
// belongs to the whole process, hence a child; and since illumos refuses to
// remove the caller's own cwd (rmdir(2) EINVAL), the child has it removed by
// another process (a grandchild whose cwd is /), the same on every system.
func TestADirectoryWhoseRealPathCannotBeResolvedIsThatError(t *testing.T) {
	t.Parallel()
	cwd := tmpDir(t) + "/gone"
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	runChild(t, "TestChildRealPathOfARemovedCwd", cwd)
}

func TestChildRealPathOfARemovedCwd(t *testing.T) {
	asChild(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runChild(t, "TestChildRemoveDir", "/", "SOCKAT_DIR="+cwd)
	if exists(cwd) {
		t.Fatalf("%s is still there", cwd)
	}
	os.Unsetenv("PWD")
	want := syscall.ENOENT
	if _, err := os.Stat("."); err != nil {
		if !errors.As(err, &want) {
			t.Fatalf("stat of the removed cwd: %v, not an errno", err)
		}
		t.Logf("stat of the removed cwd fails here (%v): that is the error to come", err)
	}
	p := "./" + strings.Repeat("n", directMax)
	w, ps := testWorld(t, nil)
	_, _, err = w.dial(p, Options{}, nil)
	if !errors.Is(err, want) || errors.Is(err, syscall.ENAMETOOLONG) {
		t.Fatalf("dial: %v, want %v", err, want)
	}
	_, _, err = w.listen(p, nil)
	if !errors.Is(err, want) || errors.Is(err, syscall.ENAMETOOLONG) {
		t.Fatalf("listen: %v, want %v", err, want)
	}
	assertEmpty(t, ps.tmp)
}

// TestChildRemoveDir removes the empty directory $SOCKAT_DIR, from a process
// other than the one whose cwd it is.
func TestChildRemoveDir(t *testing.T) {
	asChild(t)
	if err := os.Remove(os.Getenv("SOCKAT_DIR")); err != nil {
		t.Fatal(err)
	}
}
