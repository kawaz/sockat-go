//go:build !freebsd

package sockat

// ListenAny (listenAny, the routes of package atanycost; SPEC's bind_any):
// Listen, and where that ends in ENAMETOOLONG, a bind through symlink: a
// symlink to the socket's directory (its real path) in a place, the bind by
// <place>/sockat.XXXXXXXXXX/<name>, the symlink removed right after. The places
// (anyBases: XDG_RUNTIME_DIR, TMPDIR, /tmp) that only the user can write to are
// tried first, in order; then the shared ones (sticky, owned by root or the
// user). What the route gave up comes back as the losses, by SPEC's names:
// none for the path itself; [respelled] for any other route (the kernel keeps
// a spelling, not the path), symlink in a place of the user's own included;
// [respelled, recreatable-spelling] for symlink in a shared place (others can
// make the spelling name a directory of theirs). With them come the route
// taken and the routes skipped before it, in the order tried; on a failure,
// the route that bound and failed (none when every route was unusable) and
// the routes skipped.
//
// The tests force "Listen ends in ENAMETOOLONG" with noMeansCfg: no thread cwd
// and no /proc, so no route through D has a means. (Not on FreeBSD, where at
// always has one; there only a name too long for every route would do, and
// then symlink does not fit either.)

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// noMeansCfg is the route order's inputs with no means for the routes through
// D, and anyBases as ListenAny's places.
func noMeansCfg(anyBases ...string) *config {
	return &config{anyBases: anyBases, procSelfFD: noProc, threadCwd: false}
}

// ownPlace is a place only the user can write to (the user's, 0700).
func ownPlace(t *testing.T) string {
	t.Helper()
	return tmpDir(t)
}

// sharedPlace is a shared place: sticky and writable by all, owned by the
// user (root as root), like /tmp.
func sharedPlace(t *testing.T) string {
	t.Helper()
	return mkdirMode(t, tmpDir(t)+"/shared", os.ModeSticky|0o777)
}

// listenAnyAt runs listenAny and returns the listener (closed at the end of
// the test) and the outcome, failing the test on an error.
func listenAnyAt(t *testing.T, p string, cfg *config) (*net.UnixListener, anyOutcome) {
	t.Helper()
	ep, out, err := listenAny(p, cfg)
	return must{t}.listener(ep, err), out
}

// skippedNames are the names of the routes an outcome reports as tried and
// found unusable, in order. Each must come with a reason (a short text, for
// diagnostics only: its wording is not pinned).
func skippedNames(t *testing.T, out anyOutcome) []route {
	t.Helper()
	var names []route
	for _, s := range out.skipped {
		if s.reason == "" {
			t.Errorf("%s skipped without a reason", s.name)
		}
		names = append(names, s.name)
	}
	return names
}

// respelled and recreatable are the losses of symlink in a place of the
// user's own and in a shared place, in SPEC's order.
var (
	respelled   = []string{lossRespelled}
	recreatable = []string{lossRespelled, lossRecreatableSpelling}
)

// noMeansSkipped is what noMeansCfg makes the routes through D report as
// unusable before symlink, in the order tried: thread-cwd (turned off) where
// the system has it, then proc (no /proc) where it has that, then fork.
func noMeansSkipped() []route {
	rs := []route{routeStandard}
	if hasThreadCwd {
		rs = append(rs, routeThreadCwd)
	}
	if hasProc {
		rs = append(rs, routeProc)
	}
	// SPEC's next route for a bind, which Go cannot make: skipped as such.
	return append(rs, routeFork)
}

// assertReachable dials p through the public order and exchanges data with
// the listener there.
func assertReachable(t *testing.T, l *net.UnixListener, p string) {
	t.Helper()
	c, err := Dial(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ping(t, l, c)
}

// Where Listen ends in ENAMETOOLONG (the premise, checked through runWith with
// the same inputs), ListenAny binds through symlink in a place of the user's
// own: the socket is at the path and reachable, the losses are [respelled], the
// kernel keeps <place>/sockat.XXXXXXXXXX/<name> (gone by then), and the place
// is empty afterwards.
func TestListenAnyGoesThroughSymlinkWhereListenCannot(t *testing.T) {
	t.Parallel()
	own := ownPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	cfg := noMeansCfg(own)
	_, _, err := runWith(opListen, p, cfg)
	wantOpError(t, "the premise: Listen", opListen, p, err, syscall.ENAMETOOLONG)

	markUntouched(t, own)
	l, got := listenAnyAt(t, p, cfg)
	if !slices.Equal(got.losses, respelled) {
		t.Fatalf("losses %q, want %q", got.losses, respelled)
	}
	if got.route != routeSymlink || !slices.Equal(skippedNames(t, got), noMeansSkipped()) {
		t.Fatalf("route %s after %v, want %s after %v", got.route, got.skipped, routeSymlink, noMeansSkipped())
	}
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	assertTouched(t, own)
	assertEmpty(t, own)
	spelling := regexp.MustCompile(`^` + regexp.QuoteMeta(own) + `/sockat\.[A-Za-z0-9]{10}/s\.sock$`)
	if a := l.Addr().String(); !spelling.MatchString(a) || exists(a) {
		t.Fatalf("the kernel keeps %q (exists %v)", a, exists(a))
	}
	assertReachable(t, l, p)
}

// With only a shared place (sticky, owned by the user or root), ListenAny
// binds through symlink there, and the losses are [respelled,
// recreatable-spelling]. The place is empty afterwards.
func TestListenAnyInASharedPlaceIsRecreatableSpelling(t *testing.T) {
	t.Parallel()
	shared := sharedPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	l, got := listenAnyAt(t, p, noMeansCfg(shared))
	if !slices.Equal(got.losses, recreatable) {
		t.Fatalf("losses %q, want %q", got.losses, recreatable)
	}
	if got.route != routeSymlink {
		t.Fatalf("route %s", got.route)
	}
	assertEmpty(t, shared)
	assertReachable(t, l, p)
}

// The places of the user's own come first, whatever the order of anyBases: a
// shared place listed first is not touched while a place of the user's own
// is usable. Among places of one kind, the first listed is used and the next
// not touched.
func TestListenAnyTriesThePlacesOfTheUsersOwnFirstThenInOrder(t *testing.T) {
	t.Parallel()
	shared, own1, own2 := sharedPlace(t), ownPlace(t), ownPlace(t)
	dir := filepath.Dir(nest(t, tmpDir(t), 600, "x"))
	for i, c := range []struct {
		bases     []string
		used      string
		untouched []string
		want      []string
	}{
		{[]string{shared, own1}, own1, []string{shared}, respelled},
		{[]string{own1, own2, shared}, own1, []string{own2, shared}, respelled},
		{[]string{own2, own1}, own2, []string{own1}, respelled},
	} {
		p := fmt.Sprintf("%s/%d.sock", dir, i)
		for _, b := range append([]string{c.used}, c.untouched...) {
			markUntouched(t, b)
		}
		_, got := listenAnyAt(t, p, noMeansCfg(c.bases...))
		if !slices.Equal(got.losses, c.want) {
			t.Fatalf("%d: losses %q, want %q", i, got.losses, c.want)
		}
		assertTouched(t, c.used)
		assertEmpty(t, c.used)
		for _, b := range c.untouched {
			assertUntouched(t, b)
		}
	}
}

// A place the symlink's spelling does not fit in (<place>/sockat.XXXXXXXXXX/
// <name> over the sockaddr; here a 250-byte place of the user's own) is passed
// over before anything is made in it, and the next one is used.
func TestListenAnyPassesOverAPlaceTooLongForTheSpelling(t *testing.T) {
	t.Parallel()
	long := nest(t, tmpDir(t), 250, "place")
	mkdirMode(t, long, 0o700)
	shared := sharedPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	markUntouched(t, long)
	l, got := listenAnyAt(t, p, noMeansCfg(long, shared))
	if !slices.Equal(got.losses, recreatable) {
		t.Fatalf("losses %q, want %q", got.losses, recreatable)
	}
	assertUntouched(t, long)
	assertEmpty(t, shared)
	assertReachable(t, l, p)
}

// With no usable place (missing, a file, writable by others and not sticky,
// too long for the spelling, as root a sticky place of another user),
// ListenAny returns Listen's own error, ENAMETOOLONG for the caller's path,
// and creates nothing: not in the places (their mtime is unchanged), not at
// the path. A file name too long for any place's spelling is the same.
func TestListenAnyWithNoUsablePlaceIsListensENAMETOOLONG(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	file := d + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	open := mkdirMode(t, d+"/open", 0o777)
	long := nest(t, tmpDir(t), 250, "place")
	mkdirMode(t, long, 0o700)
	places := []string{open, long}
	if os.Geteuid() == 0 {
		theirs := mkdirMode(t, d+"/theirs", os.ModeSticky|0o777)
		if err := os.Chown(theirs, 65534, -1); err != nil {
			t.Fatal(err)
		}
		places = append(places, theirs)
	}
	for _, pl := range places {
		markUntouched(t, pl)
	}
	p := nest(t, tmpDir(t), 600, "s.sock")
	_, out, err := listenAny(p, noMeansCfg(append([]string{"/nonexistent", file}, places...)...))
	wantOpError(t, "listen any", opListen, p, err, syscall.ENAMETOOLONG)
	// No route bound, so none is named; every route tried, symlink last, is
	// reported as skipped.
	if want := append(noMeansSkipped(), routeSymlink); out.route != "" || !slices.Equal(skippedNames(t, out), want) {
		t.Fatalf("outcome %+v, want no route after %v", out, want)
	}
	for _, pl := range places {
		assertUntouched(t, pl)
	}
	if exists(p) {
		t.Fatal("something was created at the path")
	}

	own := ownPlace(t)
	markUntouched(t, own)
	q := filepath.Dir(p) + "/" + strings.Repeat("n", directMax-len(own)-18)
	_, _, err = listenAny(q, noMeansCfg(own))
	wantOpError(t, "a name too long for the place", opListen, q, err, syscall.ENAMETOOLONG)
	assertUntouched(t, own)
}

// A bind through symlink that fails is the result: EADDRINUSE for a file
// already at the path comes back as it is (in a *net.OpError for the path),
// the symlink is removed after the failure too, and the next place is not
// touched. A failure of Listen itself other than ENAMETOOLONG is the result
// too, before any place is looked at: EADDRINUSE through D, and ENOENT for a
// missing socket directory.
func TestAListenAnyFailureThroughSymlinkIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	first, second := ownPlace(t), ownPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	markUntouched(t, second)
	_, out, err := listenAny(p, noMeansCfg(first, second))
	wantOpError(t, "listen any on an existing file", opListen, p, err, syscall.EADDRINUSE)
	if out.route != routeSymlink || !slices.Equal(skippedNames(t, out), noMeansSkipped()) {
		t.Fatalf("outcome %+v, want symlink (the route that bound and failed) after %v", out, noMeansSkipped())
	}
	assertEmpty(t, first)
	assertUntouched(t, second)

	// Where Listen itself fails for real (a route through D: EADDRINUSE),
	// that is the result: symlink is not tried, no place is touched.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		cfg := centralCfg(tmpDir(t))
		cfg.anyBases = []string{first}
		markUntouched(t, first)
		_, out, err := listenAny(p, cfg)
		wantOpError(t, "listen any through D on an existing file", opListen, p, err, syscall.EADDRINUSE)
		want := routeThreadCwd
		if !threadCwdAvailable() {
			want = routeProc
		}
		if out.route != want {
			t.Fatalf("outcome %+v, want %s (the route that bound and failed)", out, want)
		}
		assertUntouched(t, first)
	}

	markUntouched(t, first)
	missing := filepath.Dir(p) + "/nope/s.sock"
	_, out, err = listenAny(missing, noMeansCfg(first, second))
	wantOpError(t, "listen any in a missing directory", opListen, missing, err, syscall.ENOENT)
	if out.route != "" || len(out.skipped) != 0 {
		t.Fatalf("outcome %+v, want nothing: the lookup fails before any route", out)
	}
	assertUntouched(t, first)
	assertUntouched(t, second)
}

// Where Listen itself reaches the path, ListenAny is Listen and reports that
// route's loss: none for a short path (the net package itself) and for an
// abstract name, respelled for a long path through D. ListenAny's places are
// not touched then.
func TestListenAnyWhereListenReachesReportsThatRoutesLosses(t *testing.T) {
	t.Parallel()
	own := ownPlace(t)
	markUntouched(t, own)
	short := tmpDir(t) + "/s.sock"
	l, got := listenAnyAt(t, short, noMeansCfg(own))
	if len(got.losses) != 0 || got.route != routeStandard || len(got.skipped) != 0 {
		t.Fatalf("a short path: %+v, want none through standard, nothing skipped", got)
	}
	assertReachable(t, l, short)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		p := nest(t, tmpDir(t), 600, "s.sock")
		cfg := centralCfg(tmpDir(t))
		cfg.anyBases = []string{own}
		l, got := listenAnyAt(t, p, cfg)
		// The first means here: thread-cwd where the system allows it, with
		// nothing skipped; proc otherwise, after thread-cwd.
		wantRoute, wantSkipped := routeThreadCwd, []route{routeStandard}
		if !threadCwdAvailable() {
			wantRoute, wantSkipped = routeProc, []route{routeStandard, routeThreadCwd}
		}
		if !slices.Equal(got.losses, respelled) || got.route != wantRoute || !slices.Equal(skippedNames(t, got), wantSkipped) {
			t.Fatalf("a long path through D: %+v, want respelled through %s after %v", got, wantRoute, wantSkipped)
		}
		assertReachable(t, l, p)

		// With the thread cwd off, Linux takes proc, after standard (too long) and thread-cwd.
		if runtime.GOOS == "linux" {
			q := filepath.Dir(p) + "/q.sock"
			cfg.threadCwd = false
			l, got := listenAnyAt(t, q, cfg)
			if got.route != routeProc || !slices.Equal(skippedNames(t, got), []route{routeStandard, routeThreadCwd}) {
				t.Fatalf("thread cwd off: %+v", got)
			}
			assertReachable(t, l, q)
		}
	}
	if abstractNames {
		name := "@sockat-any-" + t.Name()
		l, got := listenAnyAt(t, name, noMeansCfg(own))
		if len(got.losses) != 0 || got.route != routeStandard {
			t.Fatalf("an abstract name: %+v, want none through standard", got)
		}
		assertReachable(t, l, name)
	}
	assertUntouched(t, own)
}

// What each route of Listen gives up (SPEC "失うもの", by its names):
//
//   - standard: nothing for an absolute path; cwd-relative for a relative
//     one (the kernel keeps it as given, to be resolved against whatever the
//     cwd is later); nothing for an abstract name, which is no path (on
//     Linux; elsewhere "@x" is a relative path like any other);
//   - every other route: respelled (the kernel keeps a spelling, not the
//     path), whether the path was absolute or relative.
func TestTheLossesOfEachRoute(t *testing.T) {
	t.Parallel()
	if got := lossesOf(routeStandard, "/tmp/s.sock"); len(got) != 0 {
		t.Errorf("standard, an absolute path: %q, want none", got)
	}
	for _, rel := range []string{"s.sock", "d/s.sock", "./s.sock", "../s.sock"} {
		if got := lossesOf(routeStandard, rel); !slices.Equal(got, []string{lossCwdRelative}) {
			t.Errorf("standard, %q: %q, want [cwd-relative]", rel, got)
		}
	}
	at := lossesOf(routeStandard, "@sockat")
	if abstractNames && len(at) != 0 {
		t.Errorf("standard, an abstract name: %q, want none", at)
	}
	if !abstractNames && !slices.Equal(at, []string{lossCwdRelative}) {
		t.Errorf("standard, \"@sockat\" (a relative path here): %q, want [cwd-relative]", at)
	}
	for _, r := range []route{routeThreadCwd, routeProc, routeAt, routeThreadCwdSockdir, routeProcSockdir, routeAtSockdir} {
		for _, p := range []string{"/tmp/s.sock", "d/s.sock"} {
			if got := lossesOf(r, p); !slices.Equal(got, respelled) {
				t.Errorf("%s, %q: %q, want %q", r, p, got, respelled)
			}
		}
	}
	if lossRespelled != "respelled" || lossRecreatableSpelling != "recreatable-spelling" || lossCwdRelative != "cwd-relative" {
		t.Errorf("names %q, %q, %q: want SPEC's", lossRespelled, lossRecreatableSpelling, lossCwdRelative)
	}
}

// ListenAny's places, from XDG_RUNTIME_DIR and TMPDIR: each when it is an
// absolute path (a relative one would make the spelling depend on the cwd),
// then /tmp, in that order, a spelling given twice (component by component)
// tried once.
func TestAnyBasesFrom(t *testing.T) {
	t.Parallel()
	cases := []struct {
		runtimeDir, tmpdir string
		want               []string
	}{
		{"", "", []string{"/tmp"}},
		{"/run/user/1000", "", []string{"/run/user/1000", "/tmp"}},
		{"", "/var/t", []string{"/var/t", "/tmp"}},
		{"/run/user/1000", "/var/t", []string{"/run/user/1000", "/var/t", "/tmp"}},
		{"rel", "./t", []string{"/tmp"}},
		{"/tmp", "/tmp/", []string{"/tmp"}},
		{"/x", "/x/", []string{"/x", "/tmp"}},
		{"/private/tmp", "", []string{"/private/tmp", "/tmp"}},
	}
	for _, c := range cases {
		if got := anyBasesFrom(c.runtimeDir, c.tmpdir); !slices.Equal(got, c.want) {
			t.Errorf("anyBasesFrom(%q, %q) = %q, want %q", c.runtimeDir, c.tmpdir, got, c.want)
		}
	}
}
