//go:build !freebsd

package sockat

// ListenEx's last resort (listenReported with UseLastResort; SPEC's bind_ex
// with the last resort): Listen, and where every route of it is unusable
// (ENAMETOOLONG), a bind through symlink-dir: a symlink to the socket's
// directory (its real path) in a place, the bind by
// <place>/sockat.XXXXXXXXXX/<name>, the symlink removed right after. The places
// (dirBases: XDG_RUNTIME_DIR, TMPDIR, /tmp) that only the user can write to are
// tried first, in order; then the shared ones (sticky, owned by root or the
// user). What the route gave up comes back as the losses, by SPEC's names:
// none for the path itself; [respelled] for any other route (the kernel keeps
// a spelling, not the path), symlink-dir in a place of the user's own
// included; [respelled, recreatable-spelling] for symlink-dir in a shared
// place (others can make the spelling name a directory of theirs). With them
// come the route taken and the routes skipped before it, in the order tried;
// on a failure, a *RouteError with the route that bound and failed (none when
// every route was unusable) and the routes skipped.
//
// The tests force "Listen ends in ENAMETOOLONG" with noMeansCfg: no place for
// symlink-file (where a listen tries it), no thread cwd and no /proc, so no
// route through D has a means. (Not on FreeBSD, where at always has one;
// there only a name too long for every route would do, and then symlink-dir
// does not fit either.)

import (
	"errors"
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
// D and no place for symlink-file, and dirBases as symlink-dir's places.
func noMeansCfg(dirBases ...string) *config {
	return &config{dirBases: dirBases, procSelfFD: noProc, threadCwd: false}
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

// listenLastResort binds p as ListenEx does with UseLastResort and returns the
// listener (closed at the end of the test) and the Result, failing the test on
// an error.
func listenLastResort(t *testing.T, p string, cfg *config) (*net.UnixListener, Result) {
	t.Helper()
	ep, res, err := listenReported(p, UseLastResort, cfg)
	return must{t}.listener(ep, err), res
}

// respelled and recreatable are the losses of symlink-dir in a place of the
// user's own and in a shared place, in SPEC's order.
var (
	respelled   = []Loss{Respelled}
	recreatable = []Loss{Respelled, RecreatableSpelling}
)

// beforeD is what a listen of a long path records before the routes through
// D: standard (the path is long), then symlink-file where a listen tries it
// (not on Linux) and it cannot be used (the configurations of these tests give
// it no place).
func beforeD() []route {
	rs := []route{routeStandard}
	if listenTriesSymlinkFile {
		rs = append(rs, routeSymlinkFile)
	}
	return rs
}

// noMeansSkipped is what noMeansCfg makes a listen report as unusable before
// symlink-dir, in the order tried: beforeD, then thread-cwd (turned off)
// where the system has it, then proc (no /proc) where it has that, then
// fork-cwd.
func noMeansSkipped() []route {
	rs := beforeD()
	if hasThreadCwd {
		rs = append(rs, routeThreadCwd)
	}
	if hasProc {
		rs = append(rs, routeProc)
	}
	// SPEC's next route for a bind, which Go cannot make: skipped as such.
	return append(rs, routeForkCwd)
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
// the same inputs), the last resort binds through symlink-dir in a place of
// the user's own: the socket is at the path and reachable, the losses are
// [respelled], the kernel keeps <place>/sockat.XXXXXXXXXX/<name> (gone by
// then), and the place is empty afterwards.
func TestTheLastResortGoesThroughSymlinkDirWhereListenCannot(t *testing.T) {
	t.Parallel()
	own := ownPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	cfg := noMeansCfg(own)
	_, _, err := runWith(opListen, p, cfg)
	wantOpError(t, "the premise: Listen", opListen, p, err, syscall.ENAMETOOLONG)

	markUntouched(t, own)
	l, got := listenLastResort(t, p, cfg)
	if !slices.Equal(got.Losses, respelled) {
		t.Fatalf("losses %q, want %q", got.Losses, respelled)
	}
	if got.Route != string(routeSymlinkDir) || !slices.Equal(skippedNames(t, got.Skipped), noMeansSkipped()) {
		t.Fatalf("route %s after %v, want %s after %v", got.Route, got.Skipped, routeSymlinkDir, noMeansSkipped())
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

// With only a shared place (sticky, owned by the user or root), the last
// resort binds through symlink-dir there, and the losses are [respelled,
// recreatable-spelling]. The place is empty afterwards.
func TestTheLastResortInASharedPlaceIsRecreatableSpelling(t *testing.T) {
	t.Parallel()
	shared := sharedPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	l, got := listenLastResort(t, p, noMeansCfg(shared))
	if !slices.Equal(got.Losses, recreatable) {
		t.Fatalf("losses %q, want %q", got.Losses, recreatable)
	}
	if got.Route != string(routeSymlinkDir) {
		t.Fatalf("route %s", got.Route)
	}
	assertEmpty(t, shared)
	assertReachable(t, l, p)
}

// The places of the user's own come first, whatever the order of dirBases: a
// shared place listed first is not touched while a place of the user's own
// is usable. Among places of one kind, the first listed is used and the next
// not touched.
func TestTheLastResortTriesThePlacesOfTheUsersOwnFirstThenInOrder(t *testing.T) {
	t.Parallel()
	shared, own1, own2 := sharedPlace(t), ownPlace(t), ownPlace(t)
	dir := filepath.Dir(nest(t, tmpDir(t), 600, "x"))
	for i, c := range []struct {
		bases     []string
		used      string
		untouched []string
		want      []Loss
	}{
		{[]string{shared, own1}, own1, []string{shared}, respelled},
		{[]string{own1, own2, shared}, own1, []string{own2, shared}, respelled},
		{[]string{own2, own1}, own2, []string{own1}, respelled},
	} {
		p := fmt.Sprintf("%s/%d.sock", dir, i)
		for _, b := range append([]string{c.used}, c.untouched...) {
			markUntouched(t, b)
		}
		_, got := listenLastResort(t, p, noMeansCfg(c.bases...))
		if !slices.Equal(got.Losses, c.want) {
			t.Fatalf("%d: losses %q, want %q", i, got.Losses, c.want)
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
func TestTheLastResortPassesOverAPlaceTooLongForTheSpelling(t *testing.T) {
	t.Parallel()
	long := nest(t, tmpDir(t), 250, "place")
	mkdirMode(t, long, 0o700)
	shared := sharedPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	markUntouched(t, long)
	l, got := listenLastResort(t, p, noMeansCfg(long, shared))
	if !slices.Equal(got.Losses, recreatable) {
		t.Fatalf("losses %q, want %q", got.Losses, recreatable)
	}
	assertUntouched(t, long)
	assertEmpty(t, shared)
	assertReachable(t, l, p)
}

// With no usable place (missing, a file, writable by others and not sticky,
// too long for the spelling, as root a sticky place of another user), the
// last resort returns Listen's own error, ENAMETOOLONG for the caller's path,
// and creates nothing: not in the places (their mtime is unchanged), not at
// the path. A file name too long for any place's spelling is the same.
func TestTheLastResortWithNoUsablePlaceIsListensENAMETOOLONG(t *testing.T) {
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
	_, _, err := listenReported(p, UseLastResort, noMeansCfg(append([]string{"/nonexistent", file}, places...)...))
	wantOpError(t, "the last resort", opListen, p, err, syscall.ENAMETOOLONG)
	// No route bound, so none is named; every route tried, symlink-dir last,
	// is reported as skipped.
	re := routeErr(t, err)
	if want := append(noMeansSkipped(), routeSymlinkDir); re.Route != "" || !slices.Equal(skippedNames(t, re.Skipped), want) {
		t.Fatalf("error %+v, want no route after %v", re, want)
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
	_, _, err = listenReported(q, UseLastResort, noMeansCfg(own))
	wantOpError(t, "a name too long for the place", opListen, q, err, syscall.ENAMETOOLONG)
	assertUntouched(t, own)
}

// A bind through symlink-dir that fails is the result: EADDRINUSE for a file
// already at the path comes back as it is (in a *net.OpError for the path),
// the symlink is removed after the failure too, and the next place is not
// touched. A failure of Listen itself other than ENAMETOOLONG is the result
// too, before any place is looked at: EADDRINUSE through D, and ENOENT for a
// missing socket directory.
func TestALastResortFailureIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	first, second := ownPlace(t), ownPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	markUntouched(t, second)
	_, _, err := listenReported(p, UseLastResort, noMeansCfg(first, second))
	wantOpError(t, "the last resort on an existing file", opListen, p, err, syscall.EADDRINUSE)
	if re := routeErr(t, err); re.Route != string(routeSymlinkDir) || !slices.Equal(skippedNames(t, re.Skipped), noMeansSkipped()) {
		t.Fatalf("error %+v, want symlink-dir (the route that bound and failed) after %v", re, noMeansSkipped())
	}
	assertEmpty(t, first)
	assertUntouched(t, second)

	// Where Listen itself fails for real (a route through D: EADDRINUSE),
	// that is the result: symlink is not tried, no place is touched.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		cfg := centralCfg(tmpDir(t))
		cfg.dirBases = []string{first}
		markUntouched(t, first)
		_, _, err := listenReported(p, UseLastResort, cfg)
		wantOpError(t, "the last resort through D on an existing file", opListen, p, err, syscall.EADDRINUSE)
		want := routeThreadCwd
		if !threadCwdAvailable() {
			want = routeProc
		}
		if re := routeErr(t, err); re.Route != string(want) {
			t.Fatalf("error %+v, want %s (the route that bound and failed)", re, want)
		}
		assertUntouched(t, first)
	}

	markUntouched(t, first)
	missing := filepath.Dir(p) + "/nope/s.sock"
	_, _, err = listenReported(missing, UseLastResort, noMeansCfg(first, second))
	wantOpError(t, "the last resort in a missing directory", opListen, missing, err, syscall.ENOENT)
	if re := routeErr(t, err); re.Route != "" || len(re.Skipped) != 0 {
		t.Fatalf("error %+v, want nothing: the lookup fails before any route", re)
	}
	assertUntouched(t, first)
	assertUntouched(t, second)
}

// Where Listen itself reaches the path, the last resort is not taken: the
// result is Listen's, with that route's loss: none for a short path (the net
// package itself) and for an abstract name (where the kernel has abstract
// names: Linux, Android), respelled for a long path through D. symlink-dir's
// places are not touched then.
func TestTheLastResortWhereListenReachesReportsThatRoutesLosses(t *testing.T) {
	t.Parallel()
	own := ownPlace(t)
	markUntouched(t, own)
	short := tmpDir(t) + "/s.sock"
	l, got := listenLastResort(t, short, noMeansCfg(own))
	if len(got.Losses) != 0 || got.Route != string(routeStandard) || len(got.Skipped) != 0 {
		t.Fatalf("a short path: %+v, want none through standard, nothing skipped", got)
	}
	assertReachable(t, l, short)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		p := nest(t, tmpDir(t), 600, "s.sock")
		cfg := centralCfg(tmpDir(t))
		cfg.dirBases = []string{own}
		l, got := listenLastResort(t, p, cfg)
		// The first means here: thread-cwd where the system allows it, after
		// what comes before D; proc otherwise, after thread-cwd too.
		wantRoute, wantSkipped := routeThreadCwd, beforeD()
		if !threadCwdAvailable() {
			wantRoute, wantSkipped = routeProc, append(beforeD(), routeThreadCwd)
		}
		if !slices.Equal(got.Losses, respelled) || got.Route != string(wantRoute) || !slices.Equal(skippedNames(t, got.Skipped), wantSkipped) {
			t.Fatalf("a long path through D: %+v, want respelled through %s after %v", got, wantRoute, wantSkipped)
		}
		assertReachable(t, l, p)

		// With the thread cwd off, Linux takes proc, after standard (too long) and thread-cwd.
		if runtime.GOOS == "linux" {
			q := filepath.Dir(p) + "/q.sock"
			cfg.threadCwd = false
			l, got := listenLastResort(t, q, cfg)
			if got.Route != string(routeProc) || !slices.Equal(skippedNames(t, got.Skipped), []route{routeStandard, routeThreadCwd}) {
				t.Fatalf("thread cwd off: %+v", got)
			}
			assertReachable(t, l, q)
		}
	}
	if abstractNames {
		name := "@sockat-any-" + t.Name()
		l, got := listenLastResort(t, name, noMeansCfg(own))
		if len(got.Losses) != 0 || got.Route != string(routeStandard) {
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
//     Linux and Android; elsewhere, illumos included, "@x" is a relative
//     path like any other);
//   - every other route: respelled (the kernel keeps a spelling, not the
//     path), whether the path was absolute or relative.
func TestTheLossesOfEachRoute(t *testing.T) {
	t.Parallel()
	if got := bindLosses(routeStandard, "/tmp/s.sock"); got == nil || len(got) != 0 {
		t.Errorf("standard, an absolute path: %q, want none (an empty list)", got)
	}
	for _, rel := range []string{"s.sock", "d/s.sock", "./s.sock", "../s.sock"} {
		if got := bindLosses(routeStandard, rel); !slices.Equal(got, []Loss{CwdRelative}) {
			t.Errorf("standard, %q: %q, want [cwd-relative]", rel, got)
		}
	}
	at := bindLosses(routeStandard, "@sockat")
	if abstractNames && len(at) != 0 {
		t.Errorf("standard, an abstract name: %q, want none", at)
	}
	if !abstractNames && !slices.Equal(at, []Loss{CwdRelative}) {
		t.Errorf("standard, \"@sockat\" (a relative path here): %q, want [cwd-relative]", at)
	}
	for _, r := range []route{routeSymlinkFile, routeThreadCwd, routeProc, routeAt, routeThreadCwdSockdir, routeProcSockdir, routeAtSockdir} {
		for _, p := range []string{"/tmp/s.sock", "d/s.sock"} {
			if got := bindLosses(r, p); !slices.Equal(got, respelled) {
				t.Errorf("%s, %q: %q, want %q", r, p, got, respelled)
			}
		}
	}
}

// symlink-dir's places, from XDG_RUNTIME_DIR and TMPDIR: each when it is an
// absolute path (a relative one would make the spelling depend on the cwd),
// then /tmp, in that order, a spelling given twice (component by component)
// tried once.
func TestDirBasesFrom(t *testing.T) {
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
		if got := dirBasesFrom(c.runtimeDir, c.tmpdir); !slices.Equal(got, c.want) {
			t.Errorf("dirBasesFrom(%q, %q) = %q, want %q", c.runtimeDir, c.tmpdir, got, c.want)
		}
	}
}

// Without the last resort, ListenEx is Listen: where every route of Listen is
// unusable, the error is the same ENAMETOOLONG, with the same routes skipped
// and nothing after them (symlink-dir not tried: its place keeps its mtime).
func TestListenExWithoutTheLastResortStopsWhereListenDoes(t *testing.T) {
	t.Parallel()
	own := ownPlace(t)
	markUntouched(t, own)
	p := nest(t, tmpDir(t), 600, "s.sock")
	_, _, err := listenReported(p, NoLastResort, noMeansCfg(own))
	wantOpError(t, "no last resort", opListen, p, err, syscall.ENAMETOOLONG)
	if re := routeErr(t, err); re.Route != "" || !slices.Equal(skippedNames(t, re.Skipped), noMeansSkipped()) {
		t.Fatalf("error %+v, want no route after %v", re, noMeansSkipped())
	}
	assertUntouched(t, own)
	if exists(p) {
		t.Fatal("something was created at the path")
	}
}

// DialEx's last resort is fork-cwd-direct, which Go cannot make. Where every
// route of the connect order is unusable (far from the cwd, no thread cwd,
// no /proc, only an unsafe place for symlink-file), DialEx without the last
// resort is Dial's ENAMETOOLONG, with standard and the connect order skipped
// in order; with it, fork-cwd-direct is named last among them, with a
// reason, and the error is the same ENAMETOOLONG with no route. Where a route
// of the order connects, the last resort is not named.
func TestDialExNamesTheForkItCannotMakeAsItsLastResort(t *testing.T) {
	t.Parallel()
	unsafePlace := mkdirMode(t, tmpDir(t)+"/open", 0o777)
	p := nest(t, tmpDir(t), 600, "s.sock")
	listenAt(t, p)
	cfg := fallbackCfg(unsafePlace)
	order := append([]route{routeStandard}, stepNames(dialSteps(false))...)
	for _, lr := range []LastResort{NoLastResort, UseLastResort} {
		ep, _, err := dialReported(p, lr, cfg)
		if ep != nil || !errors.Is(err, syscall.ENAMETOOLONG) {
			t.Fatalf("%v: %v, %v; want ENAMETOOLONG", lr, ep, err)
		}
		wantOpError(t, "dial", opDial, p, err, syscall.ENAMETOOLONG)
		want := order
		if lr == UseLastResort {
			want = append(slices.Clone(order), routeForkCwdDirect)
		}
		if re := routeErr(t, err); re.Route != "" || !slices.Equal(skippedNames(t, re.Skipped), want) {
			t.Fatalf("%v: error %+v, want no route after %v", lr, re, want)
		}
	}

	safe := tmpDir(t)
	ep, res, err := dialReported(p, UseLastResort, fallbackCfg(safe))
	if err != nil || res.Route != string(routeSymlinkFile) || slices.Contains(skippedNames(t, res.Skipped), routeForkCwdDirect) || len(res.Losses) != 0 {
		t.Fatalf("a dial the order reaches: %+v, %v", res, err)
	}
	ep.Close()
}
