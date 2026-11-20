//go:build !freebsd

package sockat

// relative, the route of a dial that spells the path relative to the cwd
// (every system but FreeBSD), first in the connect order where /proc is not
// used. A connect leaves no spelling behind with the peer (the connecting
// socket is not bound); a listen never takes relative.
//
// The tests reach it with relWorld: no thread cwd and no /proc, so the routes
// before link-file are relative alone (and Linux takes its order without
// /proc). The listener at a long path is made by listenAt (bound at a short
// path and renamed), so these tests need no route of a listen and run on
// every system.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// relWorld is a test world with no thread cwd and /proc said to be at a path
// that does not exist: a dial takes relative where it fits, and link-file
// otherwise.
func relWorld(t *testing.T) (*world, testPlaces) {
	t.Helper()
	return testWorld(t, func(c *config, _ testPlaces) {
		c.proc = "/nonexistent/proc"
		c.threadCwd = false
	})
}

// assertNothingAccepted checks that no connection is waiting at l.
func assertNothingAccepted(t *testing.T, l *net.UnixListener) {
	t.Helper()
	l.SetDeadline(time.Now().Add(-time.Second))
	if _, err := l.Accept(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("a connection got in: %v", err)
	}
	l.SetDeadline(time.Time{})
}

// ----- the relative spelling -----

// A path over directMax bytes whose spelling relative to the cwd is at most
// directMax bytes is dialed through that spelling (through the net package),
// before link-file: the places stay untouched (no B is made). Runs in a child
// process whose cwd is a temporary directory, to have a socket near the cwd.
func TestADialShortRelativeToTheCwdTakesTheRelativeRoute(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	p := belowCwdPath(t, directMax, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	l := listenAt(t, p)
	w, ps := relWorld(t)
	markUntouched(t, ps.tmp)
	c, r := dialIn(t, w, p)
	if r != routeRelative {
		t.Fatalf("dial: %s", r)
	}
	checkStdTypes(t, l, c)
	assertUntouched(t, ps.tmp)
}

// A failure of a dial made through the relative spelling is the result, with
// relative as its route: it is not retried through link-file (the places stay
// untouched). The other paths have names of the same length, so their
// relative spellings fit as well. Runs in a child process whose cwd is a
// temporary directory.
func TestAFailureThroughTheRelativeSpellingIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	p := belowCwdPath(t, directMax, "s.sock")
	dir := filepath.Dir(p)
	w, ps := relWorld(t)
	staleSocket(t, dir+"/z.sock")
	markUntouched(t, ps.tmp)
	_, r, _, err := dialTraced(t, w, p, Options{})
	wantErrno(t, "dial a missing socket", err, syscall.ENOENT)
	if r != routeRelative {
		t.Fatalf("dial a missing socket: route %q", r)
	}
	_, r, _, err = dialTraced(t, w, dir+"/z.sock", Options{})
	wantErrno(t, "dial a socket nobody listens on", err, syscall.ECONNREFUSED)
	if r != routeRelative {
		t.Fatalf("dial a socket nobody listens on: route %q", r)
	}
	assertUntouched(t, ps.tmp)
}

// The tests below run in a child process so the cwd and $PWD can be chosen.
// The parent listens at the path; the child spells the path relative to its
// cwd (newRelative), dials through the connect order with no thread cwd and
// no /proc (relWorld), and reports the route and the spelling; the parent checks that the
// connection reached its listener (and not a decoy).

// dialInChild dials path in a child whose cwd is cwd and whose $PWD is pwd,
// and returns the route and the relative spelling ("-" for none) the child
// reported.
func dialInChild(t *testing.T, cwd, pwd, path string) (route, string) {
	t.Helper()
	out := runChild(t, "TestChildDialRelative", cwd, "PWD="+pwd, "SOCKAT_PATH="+path)
	r, rel, _ := strings.Cut(readChildReport(t, out), " ")
	return route(r), rel
}

func TestChildDialRelative(t *testing.T) {
	asChild(t)
	p := os.Getenv("SOCKAT_PATH")
	dir, _, _ := splitPath(p)
	rel := "-"
	if r := newRelative(p, dir); r != nil {
		rel = r.spelling
	}
	w, _ := relWorld(t)
	c, r := dialIn(t, w, p)
	c.Write([]byte("x"))
	childReport("%s %s", r, rel)
}

// assertReached checks that a connection from a child reached l, and wrote the
// byte the child sends.
func assertReached(t *testing.T, l *net.UnixListener) {
	t.Helper()
	l.SetDeadline(nowPlusGuard())
	s, err := l.Accept()
	if err != nil {
		t.Fatalf("the dial did not reach the listener: %v", err)
	}
	defer s.Close()
	s.SetDeadline(nowPlusGuard())
	if _, err := s.Read(make([]byte, 1)); err != nil {
		t.Fatalf("nothing arrived: %v", err)
	}
}

// realAndLink makes <d>/real and <d>/link -> real.
func realAndLink(t *testing.T, d string) (real, link string) {
	t.Helper()
	real, link = d+"/real", d+"/link"
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	return real, link
}

// $PWD reached through a symlink, and the path written through the same
// symlink: the path starts with $PWD, so that prefix is cut off and the
// spelling only descends (no "..", no realpath), resolving through the same
// symlinks the path does. getcwd would not have matched: it is the real path
// (on macOS also through /tmp -> /private/tmp).
func TestAPathBelowPWDThroughASymlinkIsCutAtPWD(t *testing.T) {
	t.Parallel()
	real, link := realAndLink(t, tmpDir(t))
	p := nestTail(t, link, 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	want := strings.TrimPrefix(p, link+"/")
	l := listenAt(t, real+"/"+want)
	r, rel := dialInChild(t, link, link, p)
	if r != routeRelative || rel != want {
		t.Fatalf("%s %q, want %s %q", r, rel, routeRelative, want)
	}
	assertReached(t, l)
}

// The cwd is reached through a symlink but $PWD names another directory. The
// path is written through the symlink, so it starts with neither getcwd nor a
// usable $PWD; both sides are resolved to real paths instead and the spelling
// descends from the real cwd.
func TestACwdThroughASymlinkIsMatchedByRealPaths(t *testing.T) {
	t.Parallel()
	real, link := realAndLink(t, tmpDir(t))
	p := nestTail(t, link, 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	want := strings.TrimPrefix(p, link+"/")
	l := listenAt(t, real+"/"+want)
	r, rel := dialInChild(t, link, "/", p)
	if r != routeRelative || rel != want {
		t.Fatalf("%s %q, want %s %q", r, rel, routeRelative, want)
	}
	assertReached(t, l)
}

// A symlink in the middle of the path, below the cwd: <cwd>/link/... where
// link -> real. Cutting the cwd off gives "link/..." (descending through the
// link, as the path does); the spelling must not climb, and the dial reaches
// the socket in the real directory.
func TestASymlinkInTheMiddleOfThePathStillReachesTheRealDirectory(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	real, link := realAndLink(t, d)
	p := nestTail(t, link, 90, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	l := listenAt(t, real+strings.TrimPrefix(p, link))
	r, rel := dialInChild(t, d, d, p)
	if r != routeRelative || strings.Contains(rel, "..") {
		t.Fatalf("%s %q", r, rel)
	}
	assertReached(t, l)
}

// The path is in a sibling of the cwd, so the spelling climbs with "..": cwd
// <t>/a/b, path <t>/c/... gives "../../c/...". Both sides are real paths, so
// the climb goes out of the real cwd.
func TestAPathBesideTheCwdIsSpelledWithDotDot(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	cwd := d + "/a/b"
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	p := nestTail(t, d+"/c", 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	l := listenAt(t, p)
	r, rel := dialInChild(t, cwd, cwd, p)
	if r != routeRelative || !strings.HasPrefix(rel, "../../c/") {
		t.Fatalf("%s %q", r, rel)
	}
	assertReached(t, l)
}

// A $PWD that names a directory other than the cwd must not be used: cut off
// the path, it would spell the right subpath relative to the wrong directory.
// Both directories have the same subtree, with a decoy listener in the cwd's
// copy: using $PWD would silently reach the decoy. The dial must reach the
// listener where the path points, by climbing out of the real cwd.
func TestAPWDThatIsNotTheCwdIsNotUsed(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	cwd, pwd := d+"/one", d+"/two"
	p := nestTail(t, pwd, 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	sub := strings.TrimPrefix(p, pwd)
	if err := os.MkdirAll(filepath.Dir(cwd+sub), 0o700); err != nil {
		t.Fatal(err)
	}
	l := listenAt(t, p)
	decoy := listenAt(t, cwd+sub)
	r, rel := dialInChild(t, cwd, pwd, p)
	if r != routeRelative || !strings.HasPrefix(rel, "../two/") {
		t.Fatalf("%s %q", r, rel)
	}
	assertReached(t, l)
	assertNothingAccepted(t, decoy)
}

// A cwd whose name is a byte prefix of the path's next component (cwd
// <t>/ab, path <t>/abc/...) is not a prefix of the path: cutting bytes would
// spell "c/..." and reach a decoy at <t>/ab/c/.... The spelling climbs instead.
func TestACwdThatIsOnlyABytePrefixIsNotCutOff(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	cwd := d + "/ab"
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	p := nestTail(t, d+"/abc", 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	l := listenAt(t, p)
	wrong := cwd + "/c" + strings.TrimPrefix(p, d+"/abc")
	if err := os.MkdirAll(filepath.Dir(wrong), 0o700); err != nil {
		t.Fatal(err)
	}
	decoy := listenAt(t, wrong)
	r, rel := dialInChild(t, cwd, cwd, p)
	if r != routeRelative || !strings.HasPrefix(rel, "../abc/") {
		t.Fatalf("%s %q", r, rel)
	}
	assertReached(t, l)
	assertNothingAccepted(t, decoy)
}

// A relative spelling that starts with '@' (the cwd's subdirectory is named
// "@d") gets "./" in front. Go's net package reads a leading '@' as an
// abstract socket name on Linux: without the "./" the dial would go to the
// abstract name and fail (or reach whatever listens there). Checked on every
// system, since the spelling is made the same way everywhere.
func TestARelativeSpellingStartingWithAtIsNotAbstract(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nestTail(t, d+"/@d", 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	l := listenAt(t, p)
	r, rel := dialInChild(t, d, d, p)
	if r != routeRelative || !strings.HasPrefix(rel, "./@d/") {
		t.Fatalf("%s %q", r, rel)
	}
	assertReached(t, l)
}

// The relative spelling is resolved against the cwd at the moment of the call,
// and the cwd is shared by every thread, so another thread that changes it in
// between sends the call to another directory. When the cwd is still changed
// afterwards, relative.run must turn the result into errCwdChanged even though
// the call itself succeeded: a connection to the wrong server must not reach
// the caller as if it were to their path. The change is made from inside the
// call (the function chdirs before it dials), standing in for another thread,
// and a listener at the same spelling in the other directory makes the wrong
// call succeed. The connection that did reach it is closed, which that
// listener sees as end of file. Runs in a child process because it changes the
// cwd.
func TestACwdChangedDuringARelativeDialDiscardsTheResult(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	one, two := d+"/one", d+"/two"
	p := nestTail(t, one, 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	sub := strings.TrimPrefix(p, one)
	if err := os.MkdirAll(filepath.Dir(two+sub), 0o700); err != nil {
		t.Fatal(err)
	}
	l := listenAt(t, two+sub)
	runChild(t, "TestChildRelativeWithTheCwdChangedMidway", one, "SOCKAT_PATH="+p, "SOCKAT_OTHER="+two)
	l.SetDeadline(nowPlusGuard())
	s, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetDeadline(nowPlusGuard())
	if n, err := s.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("the discarded connection is open: %d, %v", n, err)
	}
}

func TestChildRelativeWithTheCwdChangedMidway(t *testing.T) {
	asChild(t)
	p, two := os.Getenv("SOCKAT_PATH"), os.Getenv("SOCKAT_OTHER")
	dir, _, _ := splitPath(p)
	rel := newRelative(p, dir)
	if rel == nil || strings.Contains(rel.spelling, "..") {
		t.Fatalf("the premise: a spelling below the cwd: %+v", rel)
	}
	_, err := rel.run(func(s string) (endpoint, error) {
		if err := os.Chdir(two); err != nil {
			t.Fatal(err)
		}
		return viaStd(opDial, s, false)
	})
	if !errors.Is(err, errCwdChanged) {
		t.Fatalf("dial: %v, want errCwdChanged", err)
	}
}
