//go:build !freebsd

package sockat

// ListenAny (listenAny, the routes of package atanycost; SPEC's bind_any):
// Listen, and where that ends in ENAMETOOLONG, a bind through symlink: a
// symlink to the socket's directory (its real path) in a place, the bind by
// <place>/sockat.XXXXXXXXXX/<name>, the symlink removed right after. The places
// (anyBases: XDG_RUNTIME_DIR, TMPDIR, /tmp) that only the user can write to are
// tried first, in order; then the shared ones (sticky, owned by root or the
// user). What the route gave up comes back as the loss: none for the path
// itself, respelled for any other route (the kernel keeps a spelling, not the
// path) including symlink in a place of the user's own, shared-symlink for
// symlink in a shared place (others can make the spelling name a directory
// of theirs).
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

// noMeansSkipped is what noMeansCfg makes the routes through D report as
// unusable before symlink, in the order tried: thread-cwd (turned off) where
// the system has it, then proc (no /proc) where it has that.
func noMeansSkipped() []route {
	var rs []route
	if hasThreadCwd {
		rs = append(rs, routeThreadCwd)
	}
	if hasProc {
		rs = append(rs, routeProc)
	}
	return rs
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
// own: the socket is at the path and reachable, the loss is respelled, the
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
	if got.loss != lossRespelled {
		t.Fatalf("loss %d, want respelled (%d)", got.loss, lossRespelled)
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
// binds through symlink there, and the loss is shared-symlink. The place is
// empty afterwards.
func TestListenAnyInASharedPlaceIsSharedSymlink(t *testing.T) {
	t.Parallel()
	shared := sharedPlace(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	l, got := listenAnyAt(t, p, noMeansCfg(shared))
	if got.loss != lossSharedSymlink {
		t.Fatalf("loss %d, want shared-symlink (%d)", got.loss, lossSharedSymlink)
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
		want      loss
	}{
		{[]string{shared, own1}, own1, []string{shared}, lossRespelled},
		{[]string{own1, own2, shared}, own1, []string{own2, shared}, lossRespelled},
		{[]string{own2, own1}, own2, []string{own1}, lossRespelled},
	} {
		p := fmt.Sprintf("%s/%d.sock", dir, i)
		for _, b := range append([]string{c.used}, c.untouched...) {
			markUntouched(t, b)
		}
		_, got := listenAnyAt(t, p, noMeansCfg(c.bases...))
		if got.loss != c.want {
			t.Fatalf("%d: loss %d, want %d", i, got.loss, c.want)
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
	if got.loss != lossSharedSymlink {
		t.Fatalf("loss %d, want shared-symlink", got.loss)
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
	_, _, err := listenAny(p, noMeansCfg(append([]string{"/nonexistent", file}, places...)...))
	wantOpError(t, "listen any", opListen, p, err, syscall.ENAMETOOLONG)
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
	_, _, err := listenAny(p, noMeansCfg(first, second))
	wantOpError(t, "listen any on an existing file", opListen, p, err, syscall.EADDRINUSE)
	assertEmpty(t, first)
	assertUntouched(t, second)

	// Where Listen itself fails for real (a route through D: EADDRINUSE),
	// that is the result: symlink is not tried, no place is touched.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		cfg := centralCfg(tmpDir(t))
		cfg.anyBases = []string{first}
		markUntouched(t, first)
		_, _, err := listenAny(p, cfg)
		wantOpError(t, "listen any through D on an existing file", opListen, p, err, syscall.EADDRINUSE)
		assertUntouched(t, first)
	}

	markUntouched(t, first)
	missing := filepath.Dir(p) + "/nope/s.sock"
	_, _, err = listenAny(missing, noMeansCfg(first, second))
	wantOpError(t, "listen any in a missing directory", opListen, missing, err, syscall.ENOENT)
	assertUntouched(t, first)
	assertUntouched(t, second)
}

// Where Listen itself reaches the path, ListenAny is Listen and reports that
// route's loss: none for a short path (the net package itself) and for an
// abstract name, respelled for a long path through D. ListenAny's places are
// not touched then.
func TestListenAnyWhereListenReachesReportsThatRoutesLoss(t *testing.T) {
	t.Parallel()
	own := ownPlace(t)
	markUntouched(t, own)
	short := tmpDir(t) + "/s.sock"
	l, got := listenAnyAt(t, short, noMeansCfg(own))
	if got.loss != lossNone || got.route != routeStandard || len(got.skipped) != 0 {
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
		wantRoute, wantSkipped := routeThreadCwd, []route(nil)
		if !threadCwdAvailable() {
			wantRoute, wantSkipped = routeProc, []route{routeThreadCwd}
		}
		if got.loss != lossRespelled || got.route != wantRoute || !slices.Equal(skippedNames(t, got), wantSkipped) {
			t.Fatalf("a long path through D: %+v, want respelled through %s after %v", got, wantRoute, wantSkipped)
		}
		assertReachable(t, l, p)

		// With the thread cwd off, Linux takes proc, after thread-cwd.
		if runtime.GOOS == "linux" {
			q := filepath.Dir(p) + "/q.sock"
			cfg.threadCwd = false
			l, got := listenAnyAt(t, q, cfg)
			if got.route != routeProc || !slices.Equal(skippedNames(t, got), []route{routeThreadCwd}) {
				t.Fatalf("thread cwd off: %+v", got)
			}
			assertReachable(t, l, q)
		}
	}
	if abstractNames {
		name := "@sockat-any-" + t.Name()
		l, got := listenAnyAt(t, name, noMeansCfg(own))
		if got.loss != lossNone || got.route != routeStandard {
			t.Fatalf("an abstract name: %+v, want none through standard", got)
		}
		assertReachable(t, l, name)
	}
	assertUntouched(t, own)
}

// The losses of the routes: the path itself gives up nothing; every other
// route of Listen gives up the path as the address the kernel keeps.
func TestTheLossOfEachRoute(t *testing.T) {
	t.Parallel()
	if lossOf(routeStandard) != lossNone {
		t.Error("standard: want none")
	}
	for _, r := range []route{routeThreadCwd, routeProc, routeAt, routeThreadCwdSockdir, routeProcSockdir, routeAtSockdir} {
		if lossOf(r) != lossRespelled {
			t.Errorf("%s: want respelled", r)
		}
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
