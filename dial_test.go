package sockat

// The connect order of a long path (dial.go) on every system, through a test
// world (testWorld): link-file of a connect (an E right in B, unlinked after
// the connect, no D), which B it goes through, Options.Base and
// UsableCrossUID of a connect, and DialEx's Report and errors. The routes
// before link-file are each system's (os_*_test.go, ways_test.go).

import (
	"errors"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// farName is a file name no route before link-file carries on any system: it
// is past the sockaddr by itself (so proc-direct, relative, at-direct and
// thread-cwd-direct cannot be used), while link-file's spelling <B>/<E> holds
// no file name.
var farName = strings.Repeat("n", directMax+1)

// recordRandom makes w's random names go through a recorder, and returns the
// names drawn so far.
func recordRandom(w *world) func() []string {
	var mu sync.Mutex
	var drawn []string
	next := w.cfg.random
	w.cfg.random = func() (string, error) {
		n, err := next()
		mu.Lock()
		drawn = append(drawn, n)
		mu.Unlock()
		return n, err
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(drawn)
	}
}

// A dial that reaches link-file (SPEC-routes "link-file", the default
// connect's) goes through <B>/<E>, E a symlink of 10 random letters or digits
// right in the process's B, made in the first place (/tmp) since the process
// has none, and kept as the process's B (SPEC-routes "実体の置き場"): the next
// dial uses the same B, and so does a later bind, which makes its D there.
// After each dial nothing is left in B (E is unlinked right after the
// connect) and no D is made (a connect keeps no state with the peer); the
// other places are untouched.
func TestALinkFileDialLeavesNothingInBAndKeepsB(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	drawn := recordRandom(w)
	markUntouched(t, ps.runtime)
	markUntouched(t, ps.tmpdir)
	p := nest(t, tmpDir(t), 300, farName)
	l := listenAt(t, p)
	c, r, sks, err := dialTraced(t, w, p, Options{})
	if err != nil || r != routeLinkFile || slices.Contains(namesOf(t, sks), routeLinkFile) {
		t.Fatalf("dial: %s after %v, %v", r, sks, err)
	}
	ping(t, l, c)
	b := w.auto
	if b == nil || b.kind != kindTmp || !strings.HasPrefix(b.real, mustRealDir(t, ps.tmp)+"/") {
		t.Fatalf("the process's B: %+v, want /tmp's", b)
	}
	if es := entries(t, ps.tmp); !slices.Equal(es, []string{bName(kindTmp)}) {
		t.Fatalf("in /tmp: %v", es)
	}
	assertNoDOrE(t, w)
	if names := drawn(); len(names) != 1 || !regexp.MustCompile(`^`+randomRE+`$`).MatchString(names[0]) {
		t.Fatalf("E's names drawn: %q, want one of 10 letters or digits", names)
	}
	c2, r := dialIn(t, w, p)
	if r != routeLinkFile || w.auto != b {
		t.Fatalf("the next dial: %s through %+v", r, w.auto)
	}
	ping(t, l, c2)
	assertNoDOrE(t, w)
	_, bd := listenExIn(t, w, nest(t, tmpDir(t), 300, "s.sock"), Options{})
	if bd.Route != string(routeLinkDir) || w.auto != b || b.d == nil {
		t.Fatalf("a bind after the dials: %s through %+v", bd.Route, w.auto)
	}
	assertUntouched(t, ps.runtime)
	assertUntouched(t, ps.tmpdir)
}

// The B of link-file follows the places in order (/tmp, $XDG_RUNTIME_DIR,
// $TMPDIR): each place that cannot have B (missing here) is passed over.
// With none of them, link-file cannot be used and the dial is ENAMETOOLONG
// with link-file in skipped last: the socket's directory (the place 4) is no
// place for it (its real-path spelling is longer than the path), so nothing
// is made there.
func TestTheBOfALinkFileDialIsInThePlacesInOrder(t *testing.T) {
	t.Parallel()
	var gone []string
	for _, kind := range []string{kindRuntime, kindTmpdir, ""} {
		w, ps := testWorld(t, nil)
		all := map[string]string{kindTmp: ps.tmp, kindRuntime: ps.runtime, kindTmpdir: ps.tmpdir}
		gone = append(gone, map[string]string{kindRuntime: kindTmp, kindTmpdir: kindRuntime, "": kindTmpdir}[kind])
		for _, g := range gone {
			os.Remove(all[g])
		}
		p := nest(t, tmpDir(t), 300, farName)
		l := listenAt(t, p)
		dir, _, _ := splitPath(p)
		markUntouched(t, dir)
		c, r, sks, err := dialTraced(t, w, p, Options{})
		if kind == "" {
			wantErrno(t, "no place", err, syscall.ENAMETOOLONG)
			if names := namesOf(t, sks); r != "" || names[len(names)-1] != routeLinkFile {
				t.Fatalf("no place: route %q, skipped %v", r, sks)
			}
			assertUntouched(t, dir)
			continue
		}
		if err != nil || r != routeLinkFile || w.auto == nil || w.auto.kind != kind {
			t.Fatalf("%s: %s through %+v, %v", kind, r, w.auto, err)
		}
		ping(t, l, c)
		assertNoDOrE(t, w)
	}
}

// The process's B is the first place that can have one whatever the length
// of the path (hub's answer of 2026-11-21): where <B>/<E> cannot fit through
// the B of /tmp (the test's /tmp is a deep directory), link-file is unusable
// for the spelling, and no B is made there or in the later places, where it
// would fit; the dial is ENAMETOOLONG.
func TestALinkFileDialWhoseSpellingDoesNotFitTheFirstPlaceTriesNoOtherPlace(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, func(c *config, ps testPlaces) {
		deep, _, _ := splitPath(nest(t, ps.tmp, directMax, "x"))
		c.tmp = deep
	})
	p := nest(t, tmpDir(t), 300, farName)
	listenAt(t, p)
	_, _, sks, err := dialTraced(t, w, p, Options{})
	wantErrno(t, "dial", err, syscall.ENAMETOOLONG)
	if last := sks[len(sks)-1]; last.name != routeLinkFile || last.reason != "spelling too long" {
		t.Fatalf("skipped %v", sks)
	}
	assertEmpty(t, w.cfg.tmp)
	assertEmpty(t, ps.runtime)
	assertEmpty(t, ps.tmpdir)
}

// Options.Base of a connect (SPEC-routes "B の作り方"): the form is checked
// whatever the length of the path (a relative base, one not ending with a
// name: EINVAL, nothing made); for a long path the check comes after the
// socket's directory is looked up and before any route is tried (a missing
// parent: ENOENT with no route and nothing skipped), making nothing. B is
// made there only when the dial gets to link-file (a route before it that
// connects leaves no B), and then E goes right in that B and is unlinked,
// with no D, and nothing in the automatic places. UsableCrossUID is not
// looked at by a connect.
func TestOptionsBaseOfADial(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	for _, d := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		markUntouched(t, d)
	}
	d := tmpDir(t)
	short := nest(t, d, 40, "s.sock")
	for _, bad := range []string{"rel/b", d + "/b/", d + "/..", "/"} {
		for _, p := range []string{short, nest(t, tmpDir(t), 300, farName)} {
			_, r, sks, err := dialTraced(t, w, p, Options{Base: bad})
			wantErrno(t, "base "+bad, err, syscall.EINVAL)
			if r != "" || len(sks) != 0 {
				t.Fatalf("base %s: route %q, skipped %v", bad, r, sks)
			}
		}
	}
	far := nest(t, tmpDir(t), 300, farName)
	l := listenAt(t, far)
	_, r, sks, err := dialTraced(t, w, far, Options{Base: d + "/nope/b"})
	wantErrno(t, "a base whose parent is missing", err, syscall.ENOENT)
	if r != "" || len(sks) != 0 {
		t.Fatalf("a base whose parent is missing: route %q, skipped %v", r, sks)
	}

	near := nest(t, tmpDir(t), 300, "s.sock")
	ln := listenAt(t, near)
	c, r, _, err := dialTraced(t, w, near, Options{Base: d + "/b"})
	if err != nil {
		t.Fatal(err)
	}
	ping(t, ln, c)
	if r != routeLinkFile && exists(d+"/b") {
		t.Fatalf("B made at the base by a dial through %s", r)
	}

	c, r, _, err = dialTraced(t, w, far, Options{Base: d + "/b", UsableCrossUID: true})
	if err != nil || r != routeLinkFile {
		t.Fatalf("dial through the base: %s, %v", r, err)
	}
	ping(t, l, c)
	if m := modeOf(t, d+"/b"); m != 0o711 {
		t.Fatalf("the base's mode %o", m)
	}
	assertEmpty(t, d+"/b")
	assertNoDOrE(t, w)
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertUntouched(t, p)
	}
	if w.auto != nil || w.autoCross != nil {
		t.Fatal("an automatic B was made")
	}
}

// When Options.Base passes its check but B cannot be made there (its parent
// is read-only: mkdirat fails with EACCES), the dial that gets to link-file
// fails with that error, with no route (the failure is the call's, not
// link-file's) and the routes skipped before it; nothing is made in the
// automatic places. As root, who may write in a read-only directory, it runs
// in a child that has dropped to nobody (unprivileged).
func TestAnOptionsBaseThatCannotBeMadeIsTheErrorOfADial(t *testing.T) {
	t.Parallel()
	if !unprivileged(t) {
		return
	}
	w, ps := testWorld(t, nil)
	ro := mkdirMode(t, tmpDir(t)+"/ro", 0o500)
	p := nest(t, tmpDir(t), 300, farName)
	listenAt(t, p)
	_, r, sks, err := dialTraced(t, w, p, Options{Base: ro + "/b"})
	wantErrno(t, "dial", err, syscall.EACCES)
	if names := namesOf(t, sks); r != "" || len(names) == 0 || names[0] != routeStandard || slices.Contains(names, routeLinkFile) {
		t.Fatalf("route %q, skipped %v", r, sks)
	}
	assertEmpty(t, ro)
	for _, d := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertEmpty(t, d)
	}
}

// A connect does not look at UsableCrossUID (SPEC-routes "別の uid と
// PrivateTmp": its spelling stays with no peer): where no place lets others
// search (the test places are 0700), a dial with it still goes through the
// process's B by link-file, and no B for others is looked for.
func TestADialDoesNotLookAtUsableCrossUID(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 300, farName)
	l := listenAt(t, p)
	c, r, _, err := dialTraced(t, w, p, Options{UsableCrossUID: true})
	if err != nil || r != routeLinkFile || w.auto == nil || w.auto.crossUID || w.autoCross != nil {
		t.Fatalf("dial: %s through %+v, %v", r, w.auto, err)
	}
	ping(t, l, c)
}

// DialEx reports the route and the routes skipped (never nil: empty for a
// short path, standard first for a long one). Its error is a *RouteError
// whose Route is the route that connected and failed (ECONNREFUSED from a
// stale socket at a long path), wrapping the *net.OpError Dial returns for
// the caller's path; an Options.Base of the wrong form is EINVAL with no
// route, whatever the length.
func TestDialExReportsTheRouteAndTheRoutesSkipped(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	short := d + "/s.sock"
	l := listenAt(t, short)
	c, rep, err := DialEx(short, Options{})
	if err != nil || rep.Route != string(routeStandard) || rep.Skipped == nil || len(rep.Skipped) != 0 {
		t.Fatalf("short: %+v, %v", rep, err)
	}
	ping(t, l, c)
	c.Close()

	stale := nest(t, tmpDir(t), 300, "z.sock")
	staleSocket(t, stale)
	_, rep, err = DialEx(stale, Options{})
	re := routeErr(t, err)
	wantOpError(t, "a stale socket", opDial, stale, err, syscall.ECONNREFUSED)
	if re.Route == "" || re.Route == string(routeStandard) || skippedNames(t, re.Skipped)[0] != routeStandard || rep.Route != "" {
		t.Fatalf("a stale socket: route %q, skipped %v, report %+v", re.Route, re.Skipped, rep)
	}
	var oe *net.OpError
	if !errors.As(re.Unwrap(), &oe) {
		t.Fatalf("Unwrap: %T", re.Unwrap())
	}

	_, _, err = DialEx(short, Options{Base: "rel"})
	if re := routeErr(t, err); !errors.Is(err, syscall.EINVAL) || re.Route != "" || len(re.Skipped) != 0 {
		t.Fatalf("a relative base: %v, %+v", err, re)
	}
}
