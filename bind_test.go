package sockat

// The bind order of a long path (bind.go) through a test world (testWorld):
// link-dir, link-file, thread-cwd / at, the Spelling and base each gives, and
// SocketFile's Remove and Cleanup of their E.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// procHere reports whether procB can be used here: a system with /proc (Linux)
// where /proc is mounted.
func procHere() bool {
	return hasProc && exists("/proc/self/fd")
}

// listenExIn is w.listenEx(p, opts), failing the test on an error; the
// listener is closed at the end of the test.
func listenExIn(t *testing.T, w *world, p string, opts Options) (*net.UnixListener, *Bound) {
	t.Helper()
	l, b, err := w.listenEx(p, opts)
	if err != nil {
		t.Fatalf("%d bytes: %v", len(p), err)
	}
	t.Cleanup(func() { l.Close() })
	if !isSocket(p) {
		t.Fatalf("%d bytes: no socket at the path (route %s)", len(p), b.Route)
	}
	return l, b
}

// dEntries are the names in the D of w's B b (none when D was not made).
func dEntries(t *testing.T, b *base) []string {
	t.Helper()
	if b == nil || b.d == nil {
		return nil
	}
	return entries(t, b.real+"/"+b.d.name)
}

// The bind order is the system's (SPEC "bind の順"): link-dir; link-file
// where bind(2) is not measured not to follow a symlink in the last
// component; thread-cwd where a thread may have a cwd of its own, at on
// FreeBSD. Only these are tried (and so can be skipped): a route of another
// system's order never appears. The table of whether bind(2) follows agrees
// with what the kernel here does (probed with a raw socket, independently of
// the code).
func TestTheBindOrderIsTheSystems(t *testing.T) {
	t.Parallel()
	var want []route
	switch runtime.GOOS {
	case "linux", "android":
		want = []route{routeLinkDir, routeThreadCwd}
	case "darwin", "ios":
		want = []route{routeLinkDir, routeLinkFile, routeThreadCwd}
	case "freebsd":
		want = []route{routeLinkDir, routeAt}
	case "netbsd":
		want = []route{routeLinkDir, routeLinkFile}
	case "openbsd", "illumos", "solaris":
		want = []route{routeLinkDir}
	default:
		want = []route{routeLinkDir, routeLinkFile}
	}
	var got []route
	for _, s := range envWorld().bindOrder() {
		got = append(got, s.route)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the bind order: %v, want %v", got, want)
	}
	if bindFollowsHere != followsUnknown && (bindFollowsHere == followsYes) != bindFollowsSymlink() {
		t.Fatalf("the table says bind(2) follows a symlink: %v; the kernel: %v", bindFollowsHere == followsYes, bindFollowsSymlink())
	}
}

// A long path whose file name a spelling through B fits binds through
// link-dir: the socket file is at the path, a dial by the path talks to it,
// the kernel keeps <B>/<D>/<E>/<name> (B spelt by procB where /proc can be
// used, by its real path otherwise), E is a symlink to the socket's
// directory's real path, and the Spelling and base are SPEC's: procB lasts
// until the process ends, is followed by the same uid only and needs no /tmp
// ("proc"); the real path of a B in a 0700 test place under /tmp lasts until
// the cleanup, is not followed by another uid nor through a private /tmp
// ("tmp"). Without /proc (stood in for by a /proc that is not there) a Linux
// bind takes the real path too.
func TestLinkDirBindsAtThePathThroughB(t *testing.T) {
	t.Parallel()
	for _, withProc := range []bool{true, false} {
		if withProc && !procHere() {
			continue
		}
		w, _ := testWorld(t, func(c *config, _ testPlaces) {
			if !withProc {
				c.proc = "/nonexistent/proc"
			}
		})
		for _, total := range []int{directMax + 1, 600} {
			p := nest(t, tmpDir(t), total, "s.sock")
			l, b := listenExIn(t, w, p, Options{})
			bb := w.auto
			if b.Route != string(routeLinkDir) || bb == nil || bb.d == nil {
				t.Fatalf("%d bytes: route %s", total, b.Route)
			}
			spell, want, wantBase := bb.real, Spelling{Kind: Linked, Removable: true, Until: UntilCleanup}, "tmp"
			if withProc {
				spell, _ = w.procB(bb)
				want, wantBase = Spelling{Kind: Linked, Removable: true, Until: UntilProcess, UsableWithPrivateTmp: true}, "proc"
			}
			if b.Spelling != want || b.Base != wantBase {
				t.Fatalf("%d bytes (proc %v): %+v %q, want %+v %q", total, withProc, b.Spelling, b.Base, want, wantBase)
			}
			addr := l.Addr().String()
			dir, _, _ := splitPath(p)
			real, _ := realDir(dir)
			prefix := spell + "/" + bb.d.name + "/"
			if !strings.HasPrefix(addr, prefix) || !strings.HasSuffix(addr, "/s.sock") {
				t.Fatalf("the kernel keeps %q, want %s<E>/s.sock", addr, prefix)
			}
			e := strings.TrimSuffix(strings.TrimPrefix(addr, prefix), "/s.sock")
			if got, err := os.Readlink(bb.real + "/" + bb.d.name + "/" + e); err != nil || got != real {
				t.Fatalf("E %s -> %q, %v, want %s", e, got, err, real)
			}
			c, err := Dial(p)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			checkStdTypes(t, l, c)
		}
	}
}

// The places in order (/tmp, $XDG_RUNTIME_DIR, $TMPDIR, the socket's
// directory), each made unusable in turn, give the B of the next and the
// base of a real-path spelling ("tmp", "runtime", "tmpdir"); procB is "proc"
// for the first three and "sockdir" for the socket's directory, where a
// real-path spelling is never used (longer than the path): without /proc
// such a bind goes on to the routes whose spelling is Dangling. A place not
// under a private /tmp gives UsableWithPrivateTmp. One process uses one B of
// the places 1 to 3: a place unusable after B was made changes nothing.
func TestTheBOfABindIsInThePlacesInOrder(t *testing.T) {
	t.Parallel()
	for _, withProc := range []bool{false, true} {
		if withProc && !procHere() {
			continue
		}
		var gone []string
		for _, kind := range []string{kindTmp, kindRuntime, kindTmpdir, kindSockdir} {
			w, ps := testWorld(t, func(c *config, _ testPlaces) {
				c.privateTmps = []string{"/nonexistent-private-tmp"}
				if !withProc {
					c.proc = "/nonexistent/proc"
				}
			})
			all := map[string]string{kindTmp: ps.tmp, kindRuntime: ps.runtime, kindTmpdir: ps.tmpdir}
			for _, g := range gone {
				os.Remove(all[g])
			}
			p := nest(t, tmpDir(t), 300, "s.sock")
			l, b, err := w.listenEx(p, Options{})
			if kind == kindSockdir && !withProc {
				if err == nil {
					defer l.Close()
				}
				if err == nil && b.Route == string(routeLinkDir) || errors.Is(err, errUnusable) {
					t.Fatalf("no place, no /proc: %+v, %v", b, err)
				}
				if err == nil && b.Spelling.Kind != Dangling {
					t.Fatalf("no place, no /proc: %+v", b)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s (proc %v): %v", kind, withProc, err)
			}
			defer l.Close()
			wantBase := kind
			if withProc && kind != kindSockdir {
				wantBase = "proc"
			}
			if b.Route != string(routeLinkDir) || b.Base != wantBase || !b.Spelling.UsableWithPrivateTmp {
				t.Fatalf("%s (proc %v): %s %q %+v, want link-dir %q", kind, withProc, b.Route, b.Base, b.Spelling, wantBase)
			}
			if kind != kindSockdir {
				os.Remove(all[kind] + "/" + bName(kind))
				gone2 := w.auto
				if _, b2 := listenExIn(t, w, nest(t, tmpDir(t), 300, "t.sock"), Options{}); b2.Base != wantBase || w.auto != gone2 {
					t.Fatalf("%s: the next bind took another B: %q", kind, b2.Base)
				}
			}
			gone = append(gone, kind)
		}
	}
}

// The process's B is the first place in order that can have one, whatever
// the length of the path (hub's answer of 2026-11-21): where no spelling of
// link-dir (nor of link-file) fits through the B of /tmp (the test's /tmp is
// a deep directory, /proc left out), the bind does not make a B in
// $XDG_RUNTIME_DIR or $TMPDIR, where it would fit, but goes on past those
// routes, so neither place gets anything. A bind that gets as far as the
// routes whose spelling is Dangling makes its D in the B of /tmp, the
// process's B.
func TestABindWhoseSpellingDoesNotFitTheFirstPlaceTriesNoOtherPlace(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, func(c *config, ps testPlaces) {
		c.proc = "/nonexistent/proc"
		deep, _, _ := splitPath(nest(t, ps.tmp, 90, "x"))
		c.tmp = deep
	})
	p := nest(t, tmpDir(t), 300, "s.sock")
	l, b, err := w.listenEx(p, Options{})
	switch {
	case err == nil:
		defer l.Close()
		if b.Spelling.Kind != Dangling {
			t.Fatalf("%s %q %+v, want a Dangling spelling", b.Route, b.Base, b.Spelling)
		}
		if w.auto == nil || !strings.HasPrefix(w.auto.real, mustRealDir(t, w.cfg.tmp)+"/") {
			t.Fatalf("the B of the Dangling spelling: %v, want /tmp's", w.auto)
		}
	case !errors.Is(err, syscall.ENAMETOOLONG):
		t.Fatal(err)
	}
	assertEmpty(t, ps.runtime)
	assertEmpty(t, ps.tmpdir)
}

// Options.Base's B is estimated as the places are, before it is made
// (SPEC-routes "実体の置き場"): where the spelling of no route through it can
// fit (the given B's parent is over 100 bytes, the file name over 80 so that
// procB does not fit either), a bind passes link-dir and link-file over as
// too long and a dial passes link-file over, and the B is not made. The
// thread's cwd is turned off so the bind has no route left that makes D;
// both end with ENAMETOOLONG.
func TestAGivenBIsNotMadeWhereNoSpellingFitsThroughIt(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, func(c *config, _ testPlaces) { c.threadCwd = false })
	if atSupported {
		t.Skip("at needs no thread cwd: a bind makes D there")
	}
	g := nest(t, tmpDir(t), 120, "b")
	p := tmpDir(t) + "/" + strings.Repeat("s", 100)
	_, _, err := w.listenEx(p, Options{Base: g})
	re := routeErr(t, err)
	if !errors.Is(err, syscall.ENAMETOOLONG) || re.Route != "" {
		t.Fatalf("bind: %v (route %q)", err, re.Route)
	}
	for _, s := range re.Skipped {
		if (s.Name == string(routeLinkDir) || s.Name == string(routeLinkFile)) && s.Reason != "spelling too long" {
			t.Errorf("bind: %s skipped for %q", s.Name, s.Reason)
		}
	}
	tr := &trace{}
	_, r, err := w.dial(p, Options{Base: g}, tr)
	if !errors.Is(err, syscall.ENAMETOOLONG) || r != "" {
		t.Fatalf("dial: %v (route %q)", err, r)
	}
	if got := namesOf(t, tr.skipped); got[len(got)-1] != routeLinkFile || tr.skipped[len(got)-1].reason != "spelling too long" {
		t.Fatalf("dial: skipped %v", tr.skipped)
	}
	if exists(g) {
		t.Fatal("the given B was made")
	}
}

// D that could not be made in a B is not tried again in that B by the later
// routes of the same call (SPEC-routes "実体の置き場"): with the names for D
// failing (cfg.random errs), a bind whose link-dir, link-file (macOS) and
// thread-cwd all need D asks for a name once and passes each of them over
// as "D not made" (thread-cwd under a seccomp filter is passed over before
// it needs D). The next call tries again.
func TestDNotMadeIsNotTriedAgainInTheSameCall(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	if autoOf(w, false) == nil {
		t.Fatal("no B")
	}
	var asked atomic.Int32
	w.cfg.random = func() (string, error) {
		asked.Add(1)
		return "", errors.New("no name")
	}
	p := nest(t, tmpDir(t), 300, "s.sock")
	_, _, err := w.listenEx(p, Options{})
	re := routeErr(t, err)
	if !errors.Is(err, syscall.ENAMETOOLONG) {
		t.Fatalf("%v, skipped %v", err, re.Skipped)
	}
	if n := asked.Load(); n != 1 {
		t.Fatalf("a name for D asked %d times in one call", n)
	}
	for _, s := range re.Skipped[1:] {
		if s.Reason != "D not made" && s.Reason != "seccomp filter" && s.Reason != "thread cwd off" {
			t.Errorf("%s skipped for %q", s.Name, s.Reason)
		}
	}
	w.listenEx(p, Options{})
	if n := asked.Load(); n != 2 {
		t.Fatalf("the next call asked %d times in all", n)
	}
}

// B is chosen in order (SPEC-routes "実体の置き場"): the places known to fail
// without making anything are passed over (a /tmp that is not there; for
// UsableCrossUID, the 0700 places others cannot search), the spelling is
// estimated in the first place left, and where it does not fit there the
// bind stops looking (the deep $XDG_RUNTIME_DIR), so the later $TMPDIR, where
// it would fit, gets nothing. The B of a UsableCrossUID bind and the
// process's own B are held apart: after a cross-uid bind made its B in the
// one place others can search ($TMPDIR), a bind without it makes and keeps
// the B of the first place (/tmp) rather than move to that one.
func TestBIsChosenInOrderAndTheCrossUIDBIsHeldApart(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, func(c *config, ps testPlaces) {
		c.tmp = ps.tmp + "/missing"
		c.proc = "/nonexistent/proc"
		deep, _, _ := splitPath(nest(t, ps.runtime, 90, "x"))
		env := map[string]string{"XDG_RUNTIME_DIR": deep, "TMPDIR": ps.tmpdir}
		c.getenv = func(k string) string { return env[k] }
	})
	p := nest(t, tmpDir(t), 300, "s.sock")
	if l, _, err := w.listenEx(p, Options{}); err == nil {
		l.Close()
	}
	assertEmpty(t, ps.tmpdir)

	w2, ps2 := testWorld(t, func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc" })
	root, _, _ := splitPath(ps2.tmpdir)
	for _, d := range []string{root, ps2.tmpdir} {
		os.Chmod(d, 0o711)
	}
	q := nest(t, tmpDir(t), 200, "s.sock")
	_, b := listenExIn(t, w2, q, Options{UsableCrossUID: true})
	if b.Base != "tmpdir" || !b.Spelling.UsableCrossUID {
		t.Fatalf("cross-uid bind: %s %q %+v", b.Route, b.Base, b.Spelling)
	}
	r := nest(t, tmpDir(t), 200, "t.sock")
	_, b = listenExIn(t, w2, r, Options{})
	if b.Base != "tmp" || w2.auto == nil || w2.auto == w2.autoCross {
		t.Fatalf("bind after it: %s %q, auto %v, cross %v", b.Route, b.Base, w2.auto, w2.autoCross)
	}
}

// mustRealDir is realDir(d), failing the test on an error.
func mustRealDir(t *testing.T, d string) string {
	t.Helper()
	r, err := realDir(d)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// UsableCrossUID: the B must be one others can follow all the way (its place
// and B have search permission for others) and procB is not used, so where
// such a B is (the test place and its parent made 0711) the spelling is its
// real path with UsableCrossUID true; where none is (the 0700 test places),
// the bind does not fall back to a spelling others cannot follow but takes
// the routes whose spelling is Dangling (here forced to fail later only by
// the system's means: Dangling where it has them, ENAMETOOLONG otherwise),
// D going in the process's B others cannot follow.
func TestUsableCrossUIDTakesABOthersCanFollowOrNoneThroughB(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 300, "s.sock")
	l, b, err := w.listenEx(p, Options{UsableCrossUID: true})
	switch {
	case err == nil:
		defer l.Close()
		if b.Spelling.Kind != Dangling || b.Base != "" {
			t.Fatalf("no B others can follow: %+v", b)
		}
	case !errors.Is(err, syscall.ENAMETOOLONG):
		t.Fatalf("no B others can follow: %v", err)
	}
	if err == nil && (w.auto == nil || w.auto.crossUID()) {
		t.Fatalf("the B of the Dangling spelling: %v", w.auto)
	}
	w2, ps2 := testWorld(t, nil)
	root, _, _ := splitPath(ps2.tmp)
	for _, d := range []string{root, ps2.tmp} {
		os.Chmod(d, 0o711)
	}
	q := nest(t, tmpDir(t), 300, "t.sock")
	_, b = listenExIn(t, w2, q, Options{UsableCrossUID: true})
	want := Spelling{Kind: Linked, Removable: true, Until: UntilCleanup, UsableCrossUID: true}
	if b.Route != string(routeLinkDir) || b.Base != "tmp" || b.Spelling != want {
		t.Fatalf("a B others can follow: %s %q %+v", b.Route, b.Base, b.Spelling)
	}
}

// With the B of every place 1 to 3 occupied (SPEC-routes "B の作り方" step
// 5), a default bind makes nothing in those places and holds no B of them;
// what it can use is the B of the socket's directory (the place 4): procB's
// link-dir where /proc can be used, otherwise the routes whose spelling is
// Dangling (thread-cwd, at) where the system has them, otherwise none
// (ENAMETOOLONG). Every route through B the bind found unusable (link-dir,
// link-file) tells the occupied B in its reason, so that whoever reads the
// diagnostics learns why the places were not used.
func TestADefaultBindWithEveryPlaceOccupiedUsesTheSocketsDirectory(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	at := occupyAll(t, ps)
	p := nest(t, tmpDir(t), 300, "s.sock")
	tr := &trace{}
	ep, bd, err := w.listen(p, Options{}, tr)
	if ep != nil {
		defer ep.Close()
	}
	for _, sk := range tr.skipped {
		if (sk.name == routeLinkDir || sk.name == routeLinkFile) && !strings.Contains(sk.reason, occupiedReason(at)) {
			t.Errorf("%s skipped for %q, which does not tell the occupied B %q", sk.name, sk.reason, at)
		}
	}
	switch {
	case err != nil:
		wantErrno(t, "every place occupied", err, syscall.ENAMETOOLONG)
		if names := namesOf(t, tr.skipped); !slices.Contains(names, routeLinkDir) {
			t.Fatalf("skipped %v, want link-dir in it", names)
		}
	case bd.route == routeLinkDir:
		if !procHere() || bd.base != kindSockdir {
			t.Fatalf("link-dir through %q", bd.base)
		}
	case bd.route == routeThreadCwd || bd.route == routeAt:
		if names := namesOf(t, tr.skipped); !slices.Contains(names, routeLinkDir) {
			t.Fatalf("skipped %v, want link-dir in it", names)
		}
	default:
		t.Fatalf("route %s", bd.route)
	}
	if err == nil && len(w.sockdir) != 1 {
		t.Fatalf("the B of the socket's directory: %v", w.sockdir)
	}
	if w.auto != nil || w.autoCross != nil {
		t.Fatalf("a B of the places 1 to 3 is held: %v %v", w.auto, w.autoCross)
	}
	for _, pl := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertUntouched(t, pl)
	}
}

// With UsableCrossUID, a B others can follow is looked for in the places
// whose directories all have search permission for others (here /tmp's,
// made 0711 with its parent); occupied there, it is passed over with nothing
// made in it, and there is no other such place, so link-dir is unusable and
// its reason tells the occupied B. The bind does not fall back to a spelling
// others cannot follow (thread-cwd / at, Dangling, where the system has
// them; ENAMETOOLONG otherwise).
func TestAnOccupiedBShowsInTheReasonOfAUsableCrossUIDBind(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	root, _, _ := splitPath(ps.tmp)
	for _, d := range []string{root, ps.tmp} {
		if err := os.Chmod(d, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	occupiers[0].make(t, ps.tmp+"/"+uidName())
	markUntouched(t, ps.tmp)
	at := []string{mustRealDir(t, ps.tmp) + "/" + uidName()}
	p := nest(t, tmpDir(t), 300, "s.sock")
	l, b, err := w.listenEx(p, Options{UsableCrossUID: true})
	var sks []Skip
	if err != nil {
		wantErrno(t, "no B others can follow", err, syscall.ENAMETOOLONG)
		sks = routeErr(t, err).Skipped
	} else {
		defer l.Close()
		if b.Spelling.Kind != Dangling {
			t.Fatalf("%+v", b)
		}
		sks = b.Skipped
	}
	i := slices.IndexFunc(sks, func(s Skip) bool { return s.Name == string(routeLinkDir) })
	if i < 0 || !strings.Contains(sks[i].Reason, occupiedReason(at)) {
		t.Fatalf("skipped %+v, want link-dir telling %q", sks, at)
	}
	assertUntouched(t, ps.tmp)
}

// Forced past link-dir (a file name no spelling through B fits, and on
// macOS link-file turned off by the table), a bind takes thread-cwd (Linux,
// macOS) or at (FreeBSD): the socket file is at the path and talks, the
// spelling is Dangling and so is no base, E is gone right after the call (D
// is left empty), and neither the cwd of the process nor that of the calling
// thread changed.
func TestThreadCwdAndAtBindAtThePathAndLeaveNoE(t *testing.T) {
	t.Parallel()
	want := routeThreadCwd
	switch {
	case atSupported:
		want = routeAt
	case !hasThreadCwd:
		t.Skip("no thread cwd nor bindat here")
	case !threadCwdAvailable():
		t.Skip("this system refuses a thread a cwd of its own")
	}
	w, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsNo })
	cwd, _ := syscall.Getwd()
	cwdID, _ := statID(".")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p := nest(t, tmpDir(t), 600, threadName)
	l, b := listenExIn(t, w, p, Options{})
	threadCwd, _ := syscall.Getwd()
	if now, _ := statID("."); threadCwd != cwd || now != cwdID {
		t.Fatalf("the cwd changed to %s", threadCwd)
	}
	if b.Route != string(want) || b.Spelling != (Spelling{Kind: Dangling}) || b.Base != "" {
		t.Fatalf("%+v, want %s, Dangling", b, want)
	}
	if es := dEntries(t, w.auto); len(es) != 0 {
		t.Fatalf("D holds %v after the call", es)
	}
	if err := b.File.Cleanup(); err != nil {
		t.Fatal(err)
	}
	c, err := Dial(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ping(t, l, c)
}

// link-file binds by <B>/<D>/<E>, E a symlink to the socket file, where
// bind(2) follows a symlink in the last component: a file name too long for
// any other spelling (100 bytes) is bound at the path, the spelling is Linked
// but not Removable (unlinking it removes E, not the file), and the base is
// the place's. Where it does not follow (stood in for on the other systems by
// trying link-file regardless: followsUnknown), the bind with nothing at the
// path is taken as not followed: link-file is skipped and its E is not left;
// with something at the path, its error (EADDRINUSE) is returned through
// link-file, and the later routes are not tried.
func TestLinkFileBindsWhereBindFollowsASymlinkAndIsSkippedWhereItDoesNot(t *testing.T) {
	t.Parallel()
	follows := bindFollowsSymlink()
	w, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsUnknown })
	d := tmpDir(t)
	p := d + "/" + strings.Repeat("n", 100)
	if follows {
		l, b := listenExIn(t, w, p, Options{})
		want := Spelling{Kind: Linked, Removable: false, Until: UntilCleanup}
		if b.Route != string(routeLinkFile) || b.Spelling != want || b.Base != "tmp" {
			t.Fatalf("%+v", b)
		}
		if got, err := os.Readlink(l.Addr().String()); err != nil || got != p {
			t.Fatalf("E %s -> %q, %v", l.Addr(), got, err)
		}
		return
	}
	tr := &trace{}
	_, _, err := w.listen(p, Options{}, tr)
	wantErrno(t, "a 100-byte name", err, syscall.ENAMETOOLONG)
	if got := namesOf(t, tr.skipped); len(got) < 3 || got[2] != routeLinkFile {
		t.Fatalf("skipped %v", got)
	}
	if es := dEntries(t, w.auto); len(es) != 0 {
		t.Fatalf("D holds %v", es)
	}
	if exists(p) {
		t.Fatal("something was made at the path")
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, b, err := w.listen(p, Options{}, nil)
	wantErrno(t, "a file at the path", err, syscall.EADDRINUSE)
	if b.route != routeLinkFile {
		t.Fatalf("the failure came through %s", b.route)
	}
	if es := dEntries(t, w.auto); len(es) != 0 {
		t.Fatalf("D holds %v", es)
	}
}

// A real failure of the bind is returned and the next route is not tried
// (SPEC-routes "経路の失敗の扱い"): with a file at the path, link-dir answers
// EADDRINUSE through link-dir and removes its E; forced to thread-cwd / at,
// EADDRINUSE through it, no E left either. Only standard is in the trace
// before. A missing directory is ENOENT before any route (route "", nothing
// skipped) and makes no B.
func TestAFailureOfABindIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsNo })
	d := tmpDir(t)
	missing := d + "/nope/" + strings.Repeat("n", 200)
	l, b, err := w.listenEx(missing, Options{})
	re := routeErr(t, err)
	if !errors.Is(err, syscall.ENOENT) || l != nil || b != nil || re.Route != "" || len(re.Skipped) != 0 {
		t.Fatalf("a missing directory: %+v %v", re, err)
	}
	assertEmpty(t, ps.tmp)
	names := []string{"s.sock"}
	if atSupported || hasThreadCwd && threadCwdAvailable() {
		names = append(names, threadName)
	}
	for _, name := range names {
		p := nest(t, d, 300, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := w.listenEx(p, Options{})
		re := routeErr(t, err)
		if !errors.Is(err, syscall.EADDRINUSE) || re.Route == "" || !slices.Equal(skippedNames(t, re.Skipped)[:1], []route{routeStandard}) {
			t.Fatalf("%s: %+v %v", name, re, err)
		}
		if name == "s.sock" && (re.Route != string(routeLinkDir) || len(re.Skipped) != 1) {
			t.Fatalf("%s: %+v", name, re)
		}
		if es := dEntries(t, w.auto); len(es) != 0 {
			t.Fatalf("%s: D holds %v after the failure", name, es)
		}
		if b, _ := os.ReadFile(p); string(b) != "x" {
			t.Fatalf("%s: the file at the path changed", name)
		}
	}
}

// Cleanup removes the E of a link-dir spelling: the spelling then names
// nothing (it can no longer be looked up), a second Cleanup does nothing, and
// D and B stay; the socket file is not touched. Remove after it still removes
// the file (and does not fail for the E already gone). Of a spelling with no
// E (standard), Cleanup does nothing.
func TestCleanupRemovesTheEOnce(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 300, "s.sock")
	l, b := listenExIn(t, w, p, Options{})
	addr := l.Addr().String()
	if _, err := os.Stat(addr); err != nil {
		t.Fatalf("the spelling cannot be followed before the cleanup: %v", err)
	}
	for range 2 {
		if err := b.File.Cleanup(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(addr); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the spelling after the cleanup: %v", err)
	}
	if es := dEntries(t, w.auto); len(es) != 0 || !isSocket(p) {
		t.Fatalf("D holds %v, socket there %v", es, isSocket(p))
	}
	if err := b.File.Remove(); err != nil || exists(p) {
		t.Fatalf("Remove after Cleanup: %v, file there %v", err, exists(p))
	}
	short := tmpDir(t) + "/s.sock"
	_, bs := listenExIn(t, w, short, Options{})
	if bs.Spelling != (Spelling{Kind: Original}) || bs.File.Cleanup() != nil || !isSocket(short) {
		t.Fatalf("short: %+v", bs)
	}
}

// Remove removes the socket file and the E, leaves the listener open (it
// still answers Accept, with a deadline), and a second Remove does nothing.
func TestRemoveRemovesTheFileAndTheE(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 300, "s.sock")
	l, b := listenExIn(t, w, p, Options{})
	for range 2 {
		if err := b.File.Remove(); err != nil {
			t.Fatal(err)
		}
	}
	if exists(p) || len(dEntries(t, w.auto)) != 0 {
		t.Fatalf("file there %v, D holds %v", exists(p), dEntries(t, w.auto))
	}
	if err := l.SetDeadline(nowPlusGuard().Add(-2 * guard)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Accept(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the listener: %v, want it open", err)
	}
}

// The listener of Listen unlinks the address the kernel keeps on Close, as
// the net package's does (SetUnlinkOnClose left alone): through link-dir's
// spelling the socket file goes; through link-file's (where bind(2) follows a
// symlink) E goes and the file stays; a Dangling spelling is not unlinked
// (it would be resolved against the cwd), so the file stays. ListenEx's
// listener unlinks nothing, whatever the route: the removal is Remove's.
func TestTheListenerOfListenUnlinksItsSpellingOnClose(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	noLinkFile, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsNo })
	type c struct {
		w     *world
		name  string
		route route
		gone  bool
	}
	cases := []c{{w, "s.sock", routeLinkDir, true}}
	if w.cfg.bindFollows != followsNo && bindFollowsSymlink() {
		cases = append(cases, c{w, strings.Repeat("n", 100), routeLinkFile, false})
	}
	if atSupported {
		cases = append(cases, c{noLinkFile, threadName, routeAt, false})
	} else if hasThreadCwd && threadCwdAvailable() {
		cases = append(cases, c{noLinkFile, threadName, routeThreadCwd, false})
	}
	for _, k := range cases {
		w := k.w
		p := nest(t, tmpDir(t), 300, k.name)
		l, b, err := w.listen(p, Options{}, nil)
		if err != nil || b.route != k.route {
			t.Fatalf("%s: %s, %v", k.name, b.route, err)
		}
		addr := l.Addr().String()
		l.Close()
		if exists(p) == k.gone {
			t.Fatalf("%s through %s: file there after Close %v", k.name, b.route, exists(p))
		}
		if k.route == routeLinkFile && exists(addr) {
			t.Fatalf("link-file: E %s is still there after Close", addr)
		}
		os.Remove(p)
		lx, _ := listenExIn(t, w, p, Options{})
		lx.Close()
		if !isSocket(p) {
			t.Fatalf("%s: ListenEx's listener removed the file on Close", k.name)
		}
	}
}

// Through Options.Base the B is the one given: made there (mode 0711) when
// it is not yet, the spelling through it and the base "given" (procB too),
// its D holding the E; the automatic places are not touched. A base that
// fails its check is the error, before any route (route "", nothing
// skipped), with nothing made anywhere: EINVAL for a relative one, ENOENT
// for a missing parent, ENOTDIR for a file, EPERM for one others can write
// to, and with UsableCrossUID EPERM for a parent others cannot search. The
// form of the base is checked whatever the length of the path: a short path
// with a relative base is EINVAL too, and makes nothing.
func TestOptionsBaseIsTheBOfTheBind(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	for _, pl := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		markUntouched(t, pl)
	}
	d := tmpDir(t)
	given := d + "/b"
	p := nest(t, tmpDir(t), 300, "s.sock")
	_, b := listenExIn(t, w, p, Options{Base: given})
	if b.Route != string(routeLinkDir) || b.Base != kindGiven || modeOf(t, given) != 0o711 {
		t.Fatalf("%+v, B's mode %o", b, modeOf(t, given))
	}
	gb := w.given[mustStatID(t, given)]
	if len(dEntries(t, gb)) != 1 {
		t.Fatalf("the given B's D holds %v", dEntries(t, gb))
	}
	if err := os.WriteFile(d+"/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(d+"/open", 0o700)
	os.Chmod(d+"/open", 0o777)
	for _, c := range []struct {
		base  string
		cross bool
		want  syscall.Errno
	}{
		{"rel/b", false, syscall.EINVAL},
		{d + "/missing/b", false, syscall.ENOENT},
		{d + "/file", false, syscall.ENOTDIR},
		{d + "/open/b", false, syscall.EPERM},
		{d + "/c", true, syscall.EPERM},
	} {
		q := nest(t, tmpDir(t), 300, "s.sock")
		_, _, err := w.listenEx(q, Options{Base: c.base, UsableCrossUID: c.cross})
		re := routeErr(t, err)
		if !errors.Is(err, c.want) || re.Route != "" || len(re.Skipped) != 0 || exists(q) {
			t.Fatalf("base %q: %+v %v, want %v", c.base, re, err, c.want)
		}
	}
	if exists(d+"/missing") || exists(d+"/c") || exists(d+"/open/b") {
		t.Fatal("a base that failed was made")
	}
	for _, pl := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertUntouched(t, pl)
	}
	short := tmpDir(t) + "/s.sock"
	for _, bad := range []string{"rel/b", d + "/b/", "/"} {
		_, _, err := w.listenEx(short, Options{Base: bad})
		if re := routeErr(t, err); !errors.Is(err, syscall.EINVAL) || re.Route != "" || exists(short) {
			t.Fatalf("a short path with the base %q: %+v %v", bad, re, err)
		}
	}
	if _, b := listenExIn(t, w, short, Options{Base: d + "/missing/b"}); b.Route != string(routeStandard) {
		t.Fatalf("a short path with a base of the right form: %+v", b)
	}
}

func mustStatID(t *testing.T, p string) fileID {
	t.Helper()
	id, ok := statID(p)
	if !ok {
		t.Fatalf("cannot stat %s", p)
	}
	return id
}

// Where no route can be used, the bind is ENAMETOOLONG with no route that
// bound (route "") and every route of the order skipped with its reason, and
// nothing is left at the path or in D. A 254-byte file name fits no spelling
// but link-file's, so where bind(2) does not follow a symlink every route is
// skipped.
func TestABindNoRouteReachesIsENAMETOOLONGWithEveryRouteSkipped(t *testing.T) {
	t.Parallel()
	if bindFollowsSymlink() {
		t.Skip("link-file reaches any file name here")
	}
	w, _ := testWorld(t, nil)
	p := tmpDir(t) + "/" + strings.Repeat("n", 254)
	_, _, err := w.listenEx(p, Options{})
	re := routeErr(t, err)
	want := []route{routeStandard}
	for _, s := range w.bindOrder() {
		want = append(want, s.route)
	}
	if !errors.Is(err, syscall.ENAMETOOLONG) || re.Route != "" || !slices.Equal(skippedNames(t, re.Skipped), want) {
		t.Fatalf("%+v %v, want skipped %v", re, err, want)
	}
	if exists(p) || len(dEntries(t, w.auto)) != 0 {
		t.Fatal("something was left")
	}
}

// The spelling the kernel keeps, used by what later unlinks or looks up the
// listener's address, reaches the socket file or nothing, never an unrelated
// file: with a file of the socket's name (and one of the spelling's last
// components) in the cwd, unlinking link-dir's spelling removes the socket
// file and leaves the cwd's; unlinking link-file's removes its E and leaves
// both files; a Dangling spelling names nothing, its unlink fails and both
// files stay. Runs in a child whose cwd is a fresh directory.
func TestTheSpellingReachesTheSocketOrNothing(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	w, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsUnknown })
	type c struct {
		name      string
		removable bool
	}
	cases := []c{{"s.sock", true}}
	if bindFollowsSymlink() {
		cases = append(cases, c{strings.Repeat("n", 100), false})
	}
	if atSupported || hasThreadCwd && threadCwdAvailable() && !bindFollowsSymlink() {
		cases = append(cases, c{threadName, false})
	}
	for _, k := range cases {
		p := nest(t, tmpDir(t), 300, k.name)
		l, b := listenExIn(t, w, p, Options{})
		addr := l.Addr().String()
		decoys := []string{k.name, filepath.Base(addr)}
		if b.Spelling.Kind == Dangling {
			e, _, _ := splitPath(addr)
			if err := os.MkdirAll(e, 0o700); err == nil {
				t.Cleanup(func() { os.RemoveAll(e) })
			}
		}
		for _, n := range decoys {
			os.WriteFile(n, []byte("decoy"), 0o600)
			t.Cleanup(func() { os.Remove(n) })
		}
		err := os.Remove(addr)
		switch {
		case b.Spelling.Kind == Dangling:
			if err == nil {
				t.Fatalf("%s: a Dangling spelling %q named something", b.Route, addr)
			}
		case err != nil:
			t.Fatalf("%s: unlink %q: %v", b.Route, addr, err)
		}
		if isSocket(p) == k.removable {
			t.Fatalf("%s: socket there after the unlink: %v", b.Route, isSocket(p))
		}
		for _, n := range decoys {
			if bs, _ := os.ReadFile(n); string(bs) != "decoy" {
				t.Fatalf("%s: the cwd's %s was touched", b.Route, n)
			}
		}
	}
}

// Many goroutines binding long paths at once through one world all get their
// socket, through one B and one D (the E of each is its own).
func TestConcurrentBindsShareOneBAndOneD(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	d := tmpDir(t)
	const n = 32
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			p := d + "/" + strings.Repeat("d", 100) + "/" + string(rune('a'+i%26)) + strings.Repeat("x", i) + ".sock"
			os.MkdirAll(filepath.Dir(p), 0o700)
			l, _, err := w.listenEx(p, Options{})
			if err == nil {
				defer l.Close()
				if !isSocket(p) {
					err = errors.New("no socket at " + p)
				}
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if es := entries(t, ps.tmp); len(es) != 1 {
		t.Fatalf("the place holds %v", es)
	}
	if es := entries(t, w.auto.real); len(es) != 1 {
		t.Fatalf("B holds %v", es)
	}
	if es := dEntries(t, w.auto); len(es) != n {
		t.Fatalf("D holds %d E, want %d", len(es), n)
	}
}

// Skipped (the trace as reported) is never nil, and keeps each route's
// reason, so a caller can range over it and log it without a nil check.
func TestSkipsIsNeverNil(t *testing.T) {
	t.Parallel()
	var tr trace
	if s := tr.skips(); s == nil || len(s) != 0 {
		t.Fatalf("%v", s)
	}
	tr.add(routeStandard, "path too long")
	if s := tr.skips(); len(s) != 1 || s[0] != (Skip{"standard", "path too long"}) {
		t.Fatalf("%v", s)
	}
}
