package sockat

// Datagram sockets (datagram.go): the bind order and the connect order of a
// stream socket, for a socket of type SOCK_DGRAM, through a test world
// (testWorld), and the public functions.

import (
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// gramAt makes a datagram socket bound at p, however long p is: bound at a
// short path in /tmp (the net package itself) and renamed to p (on the same
// file system as /tmp). Closed at the end of the test.
func gramAt(t *testing.T, p string) *net.UnixConn {
	t.Helper()
	short := tmpDir(t) + "/g"
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: short, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := os.Rename(short, p); err != nil {
		t.Fatal(err)
	}
	return c
}

// recv reads one datagram from c (the deadline only guards against a hang:
// the datagram is there before the read) and checks it is want.
func recv(t *testing.T, c *net.UnixConn, want string) *net.UnixAddr {
	t.Helper()
	c.SetReadDeadline(nowPlusGuard())
	buf := make([]byte, 64)
	n, from, err := c.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
	return from
}

// bindGramIn binds a datagram socket at p through w as ListenUnixgramEx
// does, failing the test on an error; closed at the end of the test.
func bindGramIn(t *testing.T, w *world, p string, opts Options) (*net.UnixConn, *Bound) {
	t.Helper()
	ep, b, err := w.bindReported(opListenGram, p, opts)
	if err != nil {
		t.Fatalf("%d bytes: %v", len(p), err)
	}
	c := ep.(*net.UnixConn)
	t.Cleanup(func() { c.Close() })
	if !isSocket(p) {
		t.Fatalf("%d bytes: no socket at the path (route %s)", len(p), b.Route)
	}
	return c, b
}

// connectGramIn connects a datagram socket, not bound, to p through w as
// DialUnixgram does, failing the test on an error; closed at the end of the
// test.
func connectGramIn(t *testing.T, w *world, p string) (*net.UnixConn, route) {
	t.Helper()
	ep, r, err := w.connect(opDialGram, p, Options{}, nil)
	if err != nil {
		t.Fatalf("%d bytes: %v", len(p), err)
	}
	c := ep.(*net.UnixConn)
	t.Cleanup(func() { c.Close() })
	return c, r
}

// A datagram socket binds at a long path by the routes of the bind order,
// each forced in turn: link-dir (procB where /proc can be used, and the real
// path of B with /proc left out), link-file where bind(2) follows a symlink
// (forced with a file name no link-dir spelling fits), and thread-cwd / at
// (a file name only they carry, link-file turned off). Each time the socket
// file is at the path, the Spelling is the route's (Linked, Removable or
// not; Dangling), and a datagram sent to the path by a socket connected the
// way DialUnixgram connects arrives.
func TestADatagramSocketBindsAtALongPathByEveryBindRoute(t *testing.T) {
	t.Parallel()
	asIs, _ := testWorld(t, nil)
	noProc, _ := testWorld(t, func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc" })
	follows, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsYes })
	dangling, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsNo })
	danglingRoute := routeThreadCwd
	if atSupported {
		danglingRoute = routeAt
	}
	for _, c := range []struct {
		name  string
		w     *world
		path  string
		route route
		kind  SpellingKind
		run   bool
	}{
		{"link-dir", asIs, nest(t, tmpDir(t), 300, "s.sock"), routeLinkDir, Linked, true},
		{"link-dir-no-proc", noProc, nest(t, tmpDir(t), 300, "s.sock"), routeLinkDir, Linked, true},
		{"link-file", follows, tmpDir(t) + "/" + strings.Repeat("f", 100), routeLinkFile, Linked, bindFollowsSymlink()},
		{"dangling", dangling, nest(t, tmpDir(t), 600, threadName), danglingRoute, Dangling, atSupported || threadCwdAvailable()},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !c.run {
				t.Skip("the route cannot be used here")
			}
			s, b := bindGramIn(t, c.w, c.path, Options{})
			if b.Route != string(c.route) || b.Spelling.Kind != c.kind ||
				c.kind == Linked && b.Spelling.Removable != (c.route == routeLinkDir) {
				t.Fatalf("%s %+v, want %s %s", b.Route, b.Spelling, c.route, c.kind)
			}
			cl, _ := connectGramIn(t, c.w, c.path)
			if _, err := cl.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			recv(t, s, "x")
		})
	}
}

// The spelling the kernel keeps for a datagram socket bound at a long path
// is the sender's address its peer sees, and a reply sent there arrives
// while the spelling is Linked: a socket bound at one long path sends,
// through ResolveForConnect's spelling, to a socket bound at another; the
// receiver sees the sender's spelling (its LocalAddr) and its reply to that
// address comes back. Without the spelling's E (Cleanup), the reply cannot
// be sent.
func TestADatagramReplyReachesALinkedSpelling(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 300, "srv.sock")
	srv, _ := bindGramIn(t, w, p, Options{})
	cl, b := bindGramIn(t, w, nest(t, tmpDir(t), 300, "cl.sock"), Options{})
	if b.Spelling.Kind != Linked {
		t.Fatalf("the sender's spelling: %+v", b.Spelling)
	}
	fc := resolveConnectIn(t, w, p, Options{})
	if _, err := cl.WriteToUnix([]byte("ping"), &net.UnixAddr{Name: fc.Path, Net: "unixgram"}); err != nil {
		t.Fatal(err)
	}
	from := recv(t, srv, "ping")
	if from == nil || from.Name != cl.LocalAddr().String() {
		t.Fatalf("the sender seen as %v, its spelling is %v", from, cl.LocalAddr())
	}
	if _, err := srv.WriteToUnix([]byte("pong"), from); err != nil {
		t.Fatal(err)
	}
	recv(t, cl, "pong")
	if err := b.File.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.WriteToUnix([]byte("pong"), from); err == nil {
		t.Fatal("a reply was sent to a spelling whose E is gone")
	}
}

// The public functions: ListenUnixgram and DialUnixgram reach a long path as
// Listen and Dial do, the Ex forms report the route (Linked for the bind,
// never an empty Skipped), Bound.File's Remove removes the socket file and
// leaves the socket open. A short path goes to the net package as it is.
// Errors are the net package's for "unixgram": EADDRINUSE at an existing
// file, ENOENT for a missing directory (short or long), ECONNREFUSED for a
// datagram socket file nobody holds.
func TestListenUnixgramAndDialUnixgram(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 300, "s.sock")
	s, err := ListenUnixgram(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := DialUnixgram(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("a"))
	recv(t, s, "a")
	c2, rep, err := DialUnixgramEx(p, Options{})
	if err != nil || rep.Route == "" || rep.Skipped == nil {
		t.Fatalf("DialUnixgramEx: %+v, %v", rep, err)
	}
	defer c2.Close()
	c2.Write([]byte("b"))
	recv(t, s, "b")

	q := nest(t, tmpDir(t), 300, "t.sock")
	sx, b, err := ListenUnixgramEx(q, Options{})
	if err != nil || b.Spelling.Kind != Linked {
		t.Fatalf("ListenUnixgramEx: %+v, %v", b, err)
	}
	defer sx.Close()
	if err := b.File.Remove(); err != nil || exists(q) {
		t.Fatalf("Remove: %v, file there %v", err, exists(q))
	}
	if _, err := sx.WriteToUnix([]byte("c"), sx.LocalAddr().(*net.UnixAddr)); err == nil {
		t.Fatal("a datagram reached a removed socket file")
	}

	short := tmpDir(t) + "/s"
	ss, err := ListenUnixgram(short)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	if ss.LocalAddr().String() != short {
		t.Fatalf("short path bound as %v", ss.LocalAddr())
	}
	cs, err := DialUnixgram(short)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	cs.Write([]byte("d"))
	recv(t, ss, "d")

	_, err = ListenUnixgram(p)
	wantGramOpError(t, "bind at an existing file", "listen", p, err, syscall.EADDRINUSE)
	for _, m := range []string{tmpDir(t) + "/missing/s", tmpDir(t) + "/missing/" + strings.Repeat("m", 120)} {
		_, err = ListenUnixgram(m)
		wantGramOpError(t, "bind in a missing directory", "listen", m, err, syscall.ENOENT)
		_, err = DialUnixgram(m)
		wantGramOpError(t, "connect in a missing directory", "dial", m, err, syscall.ENOENT)
	}
	stale := nest(t, tmpDir(t), 300, "stale.sock")
	gone, err := ListenUnixgram(stale)
	if err != nil {
		t.Fatal(err)
	}
	gone.Close()
	_, err = DialUnixgram(stale)
	wantGramOpError(t, "connect to a socket file nobody holds", "dial", stale, err, staleGramErrno(t))
}

// staleGramErrno is the errno this kernel gives the net package for a connect
// of a datagram socket to a socket file nobody holds, at a short path that
// goes to the net package as it is: ECONNREFUSED on Linux and macOS,
// EDESTADDRREQ on illumos. A long path is to fail with the same errno, since
// sockat hands the kernel's error back as it is (SPEC "エラー"); the errno is
// the kernel's, not sockat's choice, so it is read here rather than fixed.
func staleGramErrno(t *testing.T) syscall.Errno {
	t.Helper()
	short := tmpDir(t) + "/g"
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: short, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	_, err = net.DialUnix("unixgram", nil, &net.UnixAddr{Name: short, Net: "unixgram"})
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		t.Fatalf("the net package's connect to a stale short path: %v, want an errno", err)
	}
	return errno
}

// wantGramOpError checks that err is what the net package returns for a
// failed listen / dial of a datagram socket on path: a *net.OpError of Op
// opName for the "unixgram" network whose Addr is path, wrapping errno.
func wantGramOpError(t *testing.T, what, opName, path string, err error, errno syscall.Errno) {
	t.Helper()
	var oe *net.OpError
	if !errors.As(err, &oe) || oe.Op != opName || oe.Net != "unixgram" || oe.Addr == nil || oe.Addr.String() != path || !errors.Is(err, errno) {
		t.Fatalf("%s: %T %v, want a unixgram %s OpError for the path with %v", what, err, err, opName, errno)
	}
}
