//go:build !freebsd

package sockat

// The POSIX route order (posixRun): every system but FreeBSD, and Linux when
// the /proc route cannot be used. In order: the path relative to the cwd, on
// macOS the widened sockaddr (Dial only), a short symlink under the first
// usable place (TMPDIR, then /tmp), and where the system has per-thread
// working directories, a thread whose own cwd is the socket's directory.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// noProc is where /proc/self/fd is said to be for the route order to run as on
// a Linux system without /proc: a path that does not exist. Elsewhere it is
// never read.
const noProc = "/nonexistent/proc/self/fd"

// posixCfg is the route order's inputs with bases as the places for the
// symlink, /proc made to look absent (Linux), the widened sockaddr on (macOS)
// and the thread-cwd route on.
func posixCfg(bases ...string) *config {
	return &config{tmpBases: bases, wide: true, procSelfFD: noProc, threadCwd: true}
}

// plainCfg is posixCfg with the widened sockaddr off (on macOS standing in for
// a kernel that refuses it; elsewhere the same).
func plainCfg(bases ...string) *config {
	c := posixCfg(bases...)
	c.wide = false
	return c
}

// listenLong and dialLong run the long-path route order with cfg and return
// the net package's types, failing the test on an error.
func listenLong(t *testing.T, p string, cfg *config) (*net.UnixListener, route) {
	t.Helper()
	ep, r, err := runLong(opListen, p, cfg)
	return must{t}.listener(ep, err), r
}

func dialLong(t *testing.T, p string, cfg *config) (*net.UnixConn, route) {
	t.Helper()
	ep, r, err := runLong(opDial, p, cfg)
	return must{t}.conn(ep, err), r
}

// wantLastRoute checks the outcome of a request that nothing but the last
// route (the thread cwd) can carry: that route where the system has it and
// allows it (not on NetBSD / OpenBSD / illumos, nor on Linux under a seccomp
// filter that refuses unshare(2)), ENAMETOOLONG otherwise. It reports whether
// the route was taken.
func wantLastRoute(t *testing.T, what string, r route, err error) bool {
	t.Helper()
	if threadCwdAvailable() {
		if err != nil || r != routeThreadCwd {
			t.Fatalf("%s: %s, %v; want %s", what, r, err, routeThreadCwd)
		}
		return true
	}
	wantErrno(t, what, err, syscall.ENAMETOOLONG)
	return false
}

// A place for the symlink is used only where no other user can replace the
// symlink between its creation and the bind / connect through it (an attacker
// who could would redirect the call to a socket of their choosing). Row by row
// (owner and user as uids; 0 is root):
//
//   - sticky and owned by root (as /tmp is) or by the user: yes. Sticky and
//     owned by another user: no, the directory's owner can remove or rename any
//     entry in it;
//   - the user's own without group / other write (0700, 0755, 0500): yes. With
//     either and not sticky (0775, 0757, 0777): no, anyone with write can
//     rename the link away and put another in its place;
//   - another user's without group / other write: no, it is theirs to change;
//     root's and not sticky: no (only for root itself, who can replace
//     anything anyway).
func TestAPlaceForTheSymlinkMustBeStickyOrTheUsersOwn(t *testing.T) {
	t.Parallel()
	const me = 1000
	cases := []struct {
		mode, owner uint32
		want        bool
	}{
		{0o1777, 0, true},
		{0o1777, me, true},
		{0o1700, me, true},
		{0o1777, 2000, false},
		{0o700, me, true},
		{0o755, me, true},
		{0o500, me, true},
		{0o775, me, false},
		{0o757, me, false},
		{0o777, me, false},
		{0o700, 2000, false},
		{0o700, 0, false},
		{0o777, 0, false},
	}
	for _, c := range cases {
		if got := onlyWeCanReplace(c.mode, c.owner, me); got != c.want {
			t.Errorf("mode %o owned by %d: %v, want %v", c.mode, c.owner, got, c.want)
		}
	}
	if !onlyWeCanReplace(0o700, 0, 0) {
		t.Error("root's own directory, for root: want true")
	}
	// Only the permission bits and the sticky bit count, not the file type
	// bits that st_mode carries too.
	if !onlyWeCanReplace(syscall.S_IFDIR|0o1777, 0, me) {
		t.Error("S_IFDIR|1777 owned by root: want true")
	}
}

// underBase spells an entry of the place for the symlink. It drops trailing
// slashes but does not clean the place's spelling otherwise: a lexical clean
// would resolve ".." differently from the kernel where the place goes through
// a symlink.
func TestUnderBaseJoinsWithoutCleaning(t *testing.T) {
	t.Parallel()
	cases := []struct{ base, want string }{
		{"/tmp", "/tmp/x"},
		{"/tmp/", "/tmp/x"},
		{"/tmp//", "/tmp/x"},
		{"/", "/x"},
		{"//", "/x"},
		{"/a/../b", "/a/../b/x"},
	}
	for _, c := range cases {
		if got := underBase(c.base, "x"); got != c.want {
			t.Errorf("underBase(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// The symlink's name is "sockat." and 10 characters out of the 62 letters and
// digits, so a link left behind by a process that died is recognisable as
// sockat's, and two draws practically never collide. A generator that
// returned the same name each time would make every place look full after 16
// tries.
func TestTheSymlinkNameIsSockatDotAndTenRandomLettersOrDigits(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 100 {
		n, err := randomName()
		if err != nil {
			t.Fatal(err)
		}
		rest, ok := strings.CutPrefix(n, linkPrefix)
		if !ok || len(rest) != linkRandomLen {
			t.Fatalf("%q", n)
		}
		for _, b := range []byte(rest) {
			if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9') {
				t.Fatalf("%q: %q is not a letter or digit", n, b)
			}
		}
		if seen[n] {
			t.Fatalf("%q drawn twice", n)
		}
		seen[n] = true
	}
}

// symlink(2) fails with EEXIST rather than replacing an entry, which is the
// O_EXCL of symlinks: a name already taken (by anything, here a file) is
// skipped for the next name, and the existing entry is left as it was. The
// names come from a stand-in generator so the collision is certain.
func TestATakenSymlinkNameIsSkippedForTheNextOne(t *testing.T) {
	t.Parallel()
	base, target := tmpDir(t), tmpDir(t)
	taken, free := linkPrefix+"AAAAAAAAAA", linkPrefix+"BBBBBBBBBB"
	if err := os.WriteFile(base+"/"+taken, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{taken, free}
	calls := 0
	link, err := createLink(base, target, func() (string, error) {
		calls++
		return names[calls-1], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || link != base+"/"+free {
		t.Fatalf("calls %d, link %s", calls, link)
	}
	if got, _ := os.Readlink(link); got != target {
		t.Fatalf("link points to %q, want %q", got, target)
	}
	if got, _ := os.ReadFile(base + "/" + taken); string(got) != "keep" {
		t.Fatalf("the taken entry was changed: %q", got)
	}
}

// When every name tried is taken, createLink gives up after 16 tries
// (linkAttempts) with errNoFreeName, rather than looping forever on a place
// that refuses every name; the symlink route then counts that place as
// unusable. Nothing is touched.
func TestRunningOutOfSymlinkNamesEndsAfter16Tries(t *testing.T) {
	t.Parallel()
	if linkAttempts != 16 {
		t.Fatalf("linkAttempts = %d, want 16", linkAttempts)
	}
	base, target := tmpDir(t), tmpDir(t)
	taken := linkPrefix + "AAAAAAAAAA"
	if err := os.WriteFile(base+"/"+taken, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := createLink(base, target, func() (string, error) { calls++; return taken, nil })
	if !errors.Is(err, errNoFreeName) {
		t.Fatalf("%v, want errNoFreeName", err)
	}
	if calls != 16 {
		t.Fatalf("%d tries, want 16", calls)
	}
	if got, _ := os.ReadFile(base + "/" + taken); string(got) != "keep" {
		t.Fatalf("the taken entry was changed: %q", got)
	}
}

// Only EEXIST is a reason to draw another name. Any other failure of
// symlink(2) (here ENOENT: the place does not exist) ends the attempt at once,
// and so does a failure to draw a name (the system's random generator
// failing).
func TestCreateLinkStopsAtAnyOtherFailure(t *testing.T) {
	t.Parallel()
	target := tmpDir(t)
	calls := 0
	_, err := createLink("/nonexistent", target, func() (string, error) { calls++; return randomName() })
	if !errors.Is(err, syscall.ENOENT) || calls != 1 {
		t.Fatalf("missing place: %v after %d tries; want ENOENT after 1", err, calls)
	}
	drawErr := errors.New("no randomness")
	calls = 0
	_, err = createLink(tmpDir(t), target, func() (string, error) { calls++; return "", drawErr })
	if !errors.Is(err, drawErr) || calls != 1 {
		t.Fatalf("failed draw: %v after %d tries", err, calls)
	}
}

// symlinkRoute checks, before creating anything, that the spelling through
// the link fits and that the place is safe; and a place where the link cannot
// be created makes the route unusable there (errUnusable: the order moves on)
// rather than failing the call. Checked for the link to the directory (listen)
// and to the file (dial), on places that are missing, a file, too long (250
// bytes, more than any sockaddr carries even without the file name), writable
// by others, and, unless running as root (who may write anywhere), read-only.
// No socket appears, and the directories involved keep their mtime.
func TestSymlinkRouteIsUnusableWhereItCannotBeUsedAndCreatesNothing(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir, name, _ := splitPath(p)
	file := d + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	long := nest(t, tmpDir(t), 250, "base")
	mkdirMode(t, long, 0o700)
	open := mkdirMode(t, d+"/open", 0o777)
	places := []string{"/nonexistent", file, long, open}
	if os.Geteuid() != 0 {
		places = append(places, mkdirMode(t, d+"/readonly", 0o500))
	}
	for _, pl := range places {
		if pl != "/nonexistent" && pl != file {
			markUntouched(t, pl)
		}
	}
	markUntouched(t, dir)
	for _, pl := range places {
		for _, o := range []op{opListen, opDial} {
			_, _, err := symlinkRoute(o, pl, p, dir, name, wideMax)
			if !errors.Is(err, errUnusable) {
				t.Errorf("%s under %s: %v, want errUnusable", o.sysName(), pl, err)
			}
		}
	}
	for _, pl := range places {
		if pl != "/nonexistent" && pl != file {
			assertUntouched(t, pl)
		}
	}
	assertUntouched(t, dir)
}

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

// A path far from the cwd (600 bytes under /tmp, so no relative spelling fits
// and on macOS the widened sockaddr does not either) goes through a symlink
// <place>/sockat.XXXXXXXXXX: a listen through one to the socket's directory,
// so the socket is created in the real directory; a dial through one to the
// socket file. Under a short place both spellings fit the plain sockaddr. The
// place is empty afterwards: the link is removed once the call returns.
func TestAPathFarFromTheCwdGoesThroughATemporarySymlink(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 600, "s.sock")
	base := tmpDir(t)
	l, r := listenLong(t, p, posixCfg(base))
	if r != routeSymlinkDir {
		t.Fatalf("listen: %s", r)
	}
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	assertEmpty(t, base)
	c, r := dialLong(t, p, posixCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	assertEmpty(t, base)
	checkStdTypes(t, l, c)
}

// The link to the socket file is spelled <place>/sockat.XXXXXXXXXX whatever
// the file name, so a dial reaches a name too long for the link to the
// directory (<place>/sockat.XXXXXXXXXX/<name> over the sockaddr), which a
// listen cannot use. The socket is bound under a short name and renamed so the
// test does not depend on the routes a listen has for the long name.
func TestADialReachesAFileNameTooLongForTheLinkToTheDirectory(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	base := tmpDir(t)
	name := strings.Repeat("n", 90)
	p := nest(t, d, 600, name)
	dir := filepath.Dir(p)
	if len(underBase(base, linkPrefix+strings.Repeat("X", linkRandomLen)))+1+len(name) <= directMax {
		t.Fatal("the premise: the link to the directory does not fit")
	}
	l, _ := listenLong(t, dir+"/s.sock", posixCfg(base))
	if err := os.Rename(dir+"/s.sock", p); err != nil {
		t.Fatal(err)
	}
	c, r := dialLong(t, p, plainCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertEmpty(t, base)
}

// Both links hold the path as the caller wrote it, so the kernel resolves them
// as it resolves the path: a ".." right after a symlink climbs out of the
// symlink's target, not out of the directory the symlink sits in. With
// <t>/link -> <t>/real/sub, <t>/link/../x/... names <t>/real/x/...; a socket at
// <t>/x/... (where a lexical ".." would lead) is a decoy that must not be
// reached.
func TestTheLinksResolveThePathAsTheKernelDoes(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	base := tmpDir(t)
	real := d + "/real"
	if err := os.MkdirAll(real+"/sub", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real+"/sub", d+"/link"); err != nil {
		t.Fatal(err)
	}
	atReal := nestTail(t, real+"/x", 500, "s.sock")
	rest := strings.TrimPrefix(atReal, real)
	spelled := d + "/link/.." + rest
	decoy := d + rest
	if err := os.MkdirAll(filepath.Dir(decoy), 0o700); err != nil {
		t.Fatal(err)
	}

	l, r := listenLong(t, spelled, posixCfg(base))
	if r != routeSymlinkDir {
		t.Fatalf("listen: %s", r)
	}
	if !isSocket(atReal) || isSocket(decoy) {
		t.Fatalf("socket at the real path %v, at the decoy %v", isSocket(atReal), isSocket(decoy))
	}
	decoyL, err := Listen(decoy)
	if err != nil {
		t.Fatal(err)
	}
	defer decoyL.Close()

	c, r := dialLong(t, spelled, posixCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	decoyL.SetDeadline(time.Now().Add(-time.Second))
	if _, err := decoyL.Accept(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the decoy got a connection: %v", err)
	}
	assertEmpty(t, base)
}

// A listen / dial that actually ran through the symlink under the first place
// is the result: its error is returned and the call is not made again under
// the next place (which would repeat it, or worse, succeed somewhere the caller
// did not mean) nor on the thread-cwd route. The second place stays untouched
// (no link was even created there) and the first is left empty: the link is
// removed after a failure too. Covered: EADDRINUSE, a missing directory
// (ENOENT through a dangling link), a missing socket (ENOENT) and a socket
// nobody listens on (ECONNREFUSED).
func TestAFailureThroughTheSymlinkIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir := filepath.Dir(p)
	first, second := tmpDir(t), tmpDir(t)
	cfg := posixCfg(first, second)
	_, r := listenLong(t, p, cfg)
	if r != routeSymlinkDir {
		t.Fatalf("the premise: %s", r)
	}
	staleSocket(t, dir+"/stale.sock")
	markUntouched(t, second)

	cases := []struct {
		what  string
		o     op
		path  string
		errno syscall.Errno
	}{
		{"listen on an existing socket", opListen, p, syscall.EADDRINUSE},
		{"listen in a missing directory", opListen, dir + "/nope/s.sock", syscall.ENOENT},
		{"dial a missing socket", opDial, dir + "/x.sock", syscall.ENOENT},
		{"dial a socket nobody listens on", opDial, dir + "/stale.sock", syscall.ECONNREFUSED},
		{"dial in a missing directory", opDial, dir + "/nope/s.sock", syscall.ENOENT},
	}
	for _, c := range cases {
		_, _, err := runLong(c.o, c.path, cfg)
		wantErrno(t, c.what, err, c.errno)
		assertEmpty(t, first)
	}
	assertUntouched(t, second)
	if exists(dir + "/nope") {
		t.Fatal("the missing directory was created")
	}
}

// A place that is not safe (writable by others and not sticky: 0777, 0775,
// 0757) is passed over without creating anything in it (its mtime is
// unchanged) and the next place is used. A sticky place (1777) and the user's
// own (0700, 0755) are used. With only unsafe places there is no symlink
// route. Run as root (docker), a place owned by another user is made too
// (chown) and must be passed over, sticky or not.
func TestAnUnsafePlaceForTheSymlinkIsPassedOver(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir := filepath.Dir(p)
	places := tmpDir(t)
	good := tmpDir(t)
	var bad []string
	for _, mode := range []os.FileMode{0o777, 0o775, 0o757} {
		pl := mkdirMode(t, fmt.Sprintf("%s/bad%o", places, mode), mode)
		bad = append(bad, pl)
		markUntouched(t, pl)
		q := fmt.Sprintf("%s/bad%o.sock", dir, mode)
		l, r := listenLong(t, q, posixCfg(pl, good))
		if r != routeSymlinkDir || !isSocket(q) {
			t.Fatalf("%o: listen %s", mode, r)
		}
		c, r := dialLong(t, q, posixCfg(pl, good))
		if r != routeSymlinkFile {
			t.Fatalf("%o: dial %s", mode, r)
		}
		ping(t, l, c)
		assertUntouched(t, pl)
		assertEmpty(t, good)
	}
	for _, mode := range []os.FileMode{os.ModeSticky | 0o777, 0o700, 0o755} {
		pl := mkdirMode(t, fmt.Sprintf("%s/ok%o", places, mode.Perm()), mode)
		q := fmt.Sprintf("%s/ok%o.sock", dir, mode.Perm())
		l, r := listenLong(t, q, posixCfg(pl))
		if r != routeSymlinkDir {
			t.Fatalf("%v: listen %s", mode, r)
		}
		c, r := dialLong(t, q, posixCfg(pl))
		if r != routeSymlinkFile {
			t.Fatalf("%v: dial %s", mode, r)
		}
		ping(t, l, c)
		assertEmpty(t, pl)
	}

	q := dir + "/onlybad.sock"
	cfg := plainCfg(bad...)
	ep, r, err := runLong(opListen, q, cfg)
	if wantLastRoute(t, "listen with only unsafe places", r, err) {
		ep.Close()
	}
	for _, pl := range bad {
		assertUntouched(t, pl)
	}

	t.Run("another user's place", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("only root can give a directory to another user")
		}
		for _, mode := range []os.FileMode{os.ModeSticky | 0o777, 0o700} {
			pl := mkdirMode(t, fmt.Sprintf("%s/theirs%o", places, mode.Perm()), mode)
			if err := os.Chown(pl, 65534, -1); err != nil {
				t.Fatal(err)
			}
			markUntouched(t, pl)
			q := fmt.Sprintf("%s/theirs%o.sock", dir, mode.Perm())
			_, r := listenLong(t, q, posixCfg(pl, good))
			if r != routeSymlinkDir {
				t.Fatalf("%v: listen %s", mode, r)
			}
			assertUntouched(t, pl)
		}
	})
}

// A place where the symlink cannot be created (missing, a file rather than a
// directory) makes that place unusable, not the whole route: the next place is
// tried, and the socket is at the given path.
func TestAPlaceWhereTheSymlinkCannotBeCreatedMovesOnToTheNext(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir := filepath.Dir(p)
	file := d + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	good := tmpDir(t)
	for i, bad := range []string{"/nonexistent", file} {
		q := fmt.Sprintf("%s/%d.sock", dir, i)
		l, r := listenLong(t, q, posixCfg(bad, good))
		if r != routeSymlinkDir || !isSocket(q) {
			t.Fatalf("%s: listen %s", bad, r)
		}
		c, r := dialLong(t, q, posixCfg(bad, good))
		if r != routeSymlinkFile {
			t.Fatalf("%s: dial %s", bad, r)
		}
		ping(t, l, c)
	}
	assertEmpty(t, good)
}

// The symlink goes under the first place where the spelling through it fits.
// A place so long that no sockaddr carries even the link (the situation of a
// deeply nested TMPDIR) is passed over without creating anything in it, and
// the next place is used. With two short places the first is used and the
// second never touched.
func TestTheSymlinkGoesUnderTheFirstPlaceWhereThePathFits(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 600, "s.sock")
	long := nest(t, tmpDir(t), 250, "base")
	mkdirMode(t, long, 0o700)
	first, second := tmpDir(t), tmpDir(t)

	markUntouched(t, long)
	l, r := listenLong(t, p, posixCfg(long, first))
	if r != routeSymlinkDir || !isSocket(p) {
		t.Fatalf("listen: %s", r)
	}
	c, r := dialLong(t, p, posixCfg(long, first))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertUntouched(t, long)
	assertEmpty(t, first)

	markUntouched(t, second)
	q := filepath.Dir(p) + "/q.sock"
	l, r = listenLong(t, q, posixCfg(first, second))
	if r != routeSymlinkDir {
		t.Fatalf("listen: %s", r)
	}
	c, _ = dialLong(t, q, posixCfg(first, second))
	ping(t, l, c)
	assertUntouched(t, second)
	assertEmpty(t, first)
}

// A TMPDIR too long for the symlink route does not leave a long path without
// it: configFromTmpdir puts /tmp after TMPDIR, so a 600-byte path still goes
// through a symlink, under /tmp, and nothing is created in TMPDIR. No link of
// sockat's pointing at the socket's directory is left in /tmp either.
func TestADeepTmpdirFallsBackToTmp(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	long := nest(t, tmpDir(t), 250, "tmpdir")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	cfg := configFromTmpdir(long)
	cfg.procSelfFD = noProc
	l, r := listenLong(t, p, cfg)
	if r != routeSymlinkDir || !isSocket(p) {
		t.Fatalf("listen: %s", r)
	}
	c, r := dialLong(t, p, cfg)
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertUntouched(t, long)
	assertNoLinkInTmpTo(t, d)
}

// assertNoLinkInTmpTo checks that /tmp holds no symlink of sockat's pointing
// into dir.
func assertNoLinkInTmpTo(t *testing.T, dir string) {
	t.Helper()
	es, err := os.ReadDir("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if !strings.HasPrefix(e.Name(), linkPrefix) {
			continue
		}
		if to, err := os.Readlink("/tmp/" + e.Name()); err == nil && strings.HasPrefix(to, dir) {
			t.Fatalf("/tmp/%s -> %s left behind", e.Name(), to)
		}
	}
}

// The same through the environment, as Listen reads it (configFromEnv): with
// TMPDIR set to a directory too long for the symlink, a 600-byte path goes
// through a symlink under /tmp and nothing is created in TMPDIR. Runs in a
// child process because it sets TMPDIR; the child's cwd is / so the relative
// spelling is long too.
func TestADeepTmpdirInTheEnvironmentFallsBackToTmp(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	long := nest(t, tmpDir(t), 250, "tmpdir")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	out := runChild(t, "TestChildListenFromEnv", "/", "TMPDIR="+long, "SOCKAT_PATH="+p)
	if got := readChildReport(t, out); got != string(routeSymlinkDir) {
		t.Fatalf("route %s\n%s", got, out)
	}
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	assertUntouched(t, long)
	assertNoLinkInTmpTo(t, d)
}

func TestChildListenFromEnv(t *testing.T) {
	asChild(t)
	cfg := configFromEnv()
	cfg.procSelfFD = noProc
	_, r := listenLong(t, os.Getenv("SOCKAT_PATH"), cfg)
	childReport("%s", r)
}

// With no route left (no relative spelling, no widened sockaddr, the only place
// for the symlink too long, the thread-cwd route ruled out), a path is
// ENAMETOOLONG, the error of a bind / connect to a path too long for sun_path,
// and nothing happens: no connection reaches the listener, nothing is created
// in the place for the symlink (its mtime is unchanged) or next to the socket.
// A real failure stays what it is: a missing directory is ENOENT.
func TestWithNoRouteLeftAPathIsENAMETOOLONGAndNothingIsMade(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir := filepath.Dir(p)
	short := tmpDir(t)
	l, _ := listenLong(t, p, posixCfg(short))
	long := nest(t, tmpDir(t), 250, "base")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	markUntouched(t, dir)
	none := plainCfg(long)
	none.threadCwd = false
	for _, o := range []op{opListen, opDial} {
		_, _, err := runLong(o, p, none)
		wantErrno(t, o.sysName(), err, syscall.ENAMETOOLONG)
		_, _, err = runLong(o, dir+"/other.sock", none)
		wantErrno(t, o.sysName()+" elsewhere", err, syscall.ENAMETOOLONG)
		_, _, err = runLong(o, dir+"/nope/s.sock", none)
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
	assertUntouched(t, long)
	assertUntouched(t, dir)
	l.SetDeadline(time.Now().Add(-time.Second))
	if _, err := l.Accept(); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("a connection got in: %v", err)
	}
}

// ----- the relative spelling -----

// A path over directMax bytes whose spelling relative to the cwd is at most
// directMax bytes takes the relative route (through the net package, the plain
// sockaddr), before anything else is considered. The socket is at the given
// absolute path, and no place for the symlink is touched.
func TestAPathShortRelativeToTheCwdTakesTheRelativeRoute(t *testing.T) {
	t.Parallel()
	p := belowCwdPath(t, directMax, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	base := tmpDir(t)
	markUntouched(t, base)
	l, r := listenLong(t, p, posixCfg(base))
	if r != routeRelative || !isSocket(p) {
		t.Fatalf("listen: %s", r)
	}
	c, r := dialLong(t, p, posixCfg(base))
	if r != routeRelative {
		t.Fatalf("dial: %s", r)
	}
	checkStdTypes(t, l, c)
	assertUntouched(t, base)
}

// A failure of a listen / dial made through the relative spelling is the
// result: it is not retried through a symlink (the place stays untouched) or
// on the thread-cwd route. The other paths have names of the same length, so
// their relative spellings fit as well.
func TestAFailureThroughTheRelativeSpellingIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	p := belowCwdPath(t, directMax, "s.sock")
	dir := filepath.Dir(p)
	base := tmpDir(t)
	cfg := posixCfg(base)
	_, r := listenLong(t, p, cfg)
	if r != routeRelative {
		t.Fatalf("the premise: %s", r)
	}
	staleSocket(t, dir+"/z.sock")
	markUntouched(t, base)
	_, _, err := runLong(opListen, p, cfg)
	wantErrno(t, "listen on an existing socket", err, syscall.EADDRINUSE)
	_, _, err = runLong(opDial, dir+"/x.sock", cfg)
	wantErrno(t, "dial a missing socket", err, syscall.ENOENT)
	_, _, err = runLong(opDial, dir+"/z.sock", cfg)
	wantErrno(t, "dial a socket nobody listens on", err, syscall.ECONNREFUSED)
	assertUntouched(t, base)
}

// The tests below run in a child process so the cwd and $PWD can be chosen.
// The child spells the path relative to its cwd (newRelative), listens
// through the route order with no place for a symlink, and reports the route
// and the spelling; the parent checks where the socket appeared.

// listenInChild listens on path in a child whose cwd is cwd and whose $PWD is
// pwd, and returns the route and the relative spelling ("-" for none) the
// child reported.
func listenInChild(t *testing.T, cwd, pwd, path string) (route, string) {
	t.Helper()
	out := runChild(t, "TestChildListenRelative", cwd, "PWD="+pwd, "SOCKAT_PATH="+path)
	r, rel, _ := strings.Cut(readChildReport(t, out), " ")
	return route(r), rel
}

func TestChildListenRelative(t *testing.T) {
	asChild(t)
	p := os.Getenv("SOCKAT_PATH")
	dir, _, _ := splitPath(p)
	rel := "-"
	if r := newRelative(p, dir); r != nil {
		rel = r.spelling
	}
	_, r := listenLong(t, p, posixCfg())
	childReport("%s %s", r, rel)
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
// (on macOS also through /tmp -> /private/tmp). The socket is in the real
// directory.
func TestAPathBelowPWDThroughASymlinkIsCutAtPWD(t *testing.T) {
	t.Parallel()
	real, link := realAndLink(t, tmpDir(t))
	p := nestTail(t, link, 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	want := strings.TrimPrefix(p, link+"/")
	r, rel := listenInChild(t, link, link, p)
	if r != routeRelative || rel != want {
		t.Fatalf("%s %q, want %s %q", r, rel, routeRelative, want)
	}
	if !isSocket(real + "/" + want) {
		t.Fatal("no socket in the real directory")
	}
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
	r, rel := listenInChild(t, link, "/", p)
	if r != routeRelative || rel != want {
		t.Fatalf("%s %q, want %s %q", r, rel, routeRelative, want)
	}
	if !isSocket(real + "/" + want) {
		t.Fatal("no socket in the real directory")
	}
}

// A symlink in the middle of the path, below the cwd: <cwd>/link/... where
// link -> real. Cutting the cwd off gives "link/..." (descending through the
// link, as the path does); either way the socket must be in the real
// directory. The point is the place, not the spelling, which must not climb.
func TestASymlinkInTheMiddleOfThePathStillLandsInTheRealDirectory(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	real, link := realAndLink(t, d)
	p := nestTail(t, link, 90, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	r, rel := listenInChild(t, d, d, p)
	if r != routeRelative || strings.Contains(rel, "..") {
		t.Fatalf("%s %q", r, rel)
	}
	if !isSocket(real + strings.TrimPrefix(p, link)) {
		t.Fatal("no socket in the real directory")
	}
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
	r, rel := listenInChild(t, cwd, cwd, p)
	if r != routeRelative || !strings.HasPrefix(rel, "../../c/") {
		t.Fatalf("%s %q", r, rel)
	}
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
}

// A $PWD that names a directory other than the cwd must not be used: cut off
// the path, it would spell the right subpath relative to the wrong directory.
// Both directories have the same subtree here, so using $PWD would silently
// create the socket in the cwd's copy. The socket must be where the path
// points, reached by climbing out of the real cwd.
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
	r, rel := listenInChild(t, cwd, pwd, p)
	if r != routeRelative || !strings.HasPrefix(rel, "../two/") {
		t.Fatalf("%s %q", r, rel)
	}
	if !isSocket(p) || exists(cwd+sub) {
		t.Fatalf("socket at the path %v, in the cwd's copy %v", isSocket(p), exists(cwd+sub))
	}
}

// A cwd whose name is a byte prefix of the path's next component (cwd
// <t>/ab, path <t>/abc/...) is not a prefix of the path: cutting bytes would
// spell "c/..." and name <t>/ab/c/.... The spelling climbs instead.
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
	r, rel := listenInChild(t, cwd, cwd, p)
	if r != routeRelative || !strings.HasPrefix(rel, "../abc/") {
		t.Fatalf("%s %q", r, rel)
	}
	if !isSocket(p) || exists(cwd+"/c") {
		t.Fatal("the socket is not at the path")
	}
}

// A relative spelling that starts with '@' (the cwd's subdirectory is named
// "@d") gets "./" in front. Go's net package reads a leading '@' as an
// abstract socket name on Linux: without the "./" the listen would succeed
// with no file anywhere, and the socket would not be at the path. Checked on
// every system, since the spelling is made the same way everywhere.
func TestARelativeSpellingStartingWithAtIsNotAbstract(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nestTail(t, d+"/@d", 95, "s.sock")
	if len(p) <= directMax {
		t.Fatalf("the premise: %d bytes is not long", len(p))
	}
	r, rel := listenInChild(t, d, d, p)
	if r != routeRelative || !strings.HasPrefix(rel, "./@d/") {
		t.Fatalf("%s %q", r, rel)
	}
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
}

// The relative spelling is resolved against the cwd at the moment of the call,
// and the cwd is shared by every thread, so another thread that changes it in
// between sends the call to another directory. When the cwd is still changed
// afterwards, relative.run must turn the result into errCwdChanged even though
// the call itself succeeded: a listener in the wrong directory or a connection
// to the wrong server must not reach the caller as if it were at their path.
//
// The change is made from inside the call (the function chdirs before it
// listens / dials), standing in for another thread. Two directories with the
// same subtree make the wrong directory one where the call succeeds:
//
//   - listen: the error comes back, the socket file is in the other directory
//     and is left there (where it went is not known, so nothing is removed),
//     and nothing appears at the given path;
//   - dial: the error comes back, and the connection that did reach a listener
//     in the other directory is closed, which that listener sees as end of
//     file.
//
// Runs in a child process because it changes the cwd.
func TestACwdChangedDuringARelativeCallDiscardsTheResult(t *testing.T) {
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
	runChild(t, "TestChildRelativeWithTheCwdChangedMidway", one, "SOCKAT_PATH="+p, "SOCKAT_OTHER="+two)
	if isSocket(p) || !isSocket(two+sub) {
		t.Fatalf("socket at the path %v, in the other directory %v", isSocket(p), isSocket(two+sub))
	}
}

func TestChildRelativeWithTheCwdChangedMidway(t *testing.T) {
	asChild(t)
	p, two := os.Getenv("SOCKAT_PATH"), os.Getenv("SOCKAT_OTHER")
	one, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir, _, _ := splitPath(p)
	rel := newRelative(p, dir)
	if rel == nil || strings.Contains(rel.spelling, "..") {
		t.Fatalf("the premise: a spelling below the cwd: %+v", rel)
	}
	_, err = rel.run(func(s string) (endpoint, error) {
		if err := os.Chdir(two); err != nil {
			t.Fatal(err)
		}
		return viaStd(opListen, s, false)
	})
	if !errors.Is(err, errCwdChanged) {
		t.Fatalf("listen: %v, want errCwdChanged", err)
	}
	if !isSocket(two+"/"+rel.spelling) || isSocket(p) {
		t.Fatal("listen: the socket is not where the changed cwd sent it")
	}

	// A listener in the other directory at the spelling of c.sock.
	if err := os.Chdir(one); err != nil {
		t.Fatal(err)
	}
	c := filepath.Dir(p) + "/c.sock"
	crel := newRelative(c, dir)
	if crel == nil {
		t.Fatal("no relative spelling")
	}
	l, err := Listen(two + "/" + crel.spelling)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_, err = crel.run(func(s string) (endpoint, error) {
		if err := os.Chdir(two); err != nil {
			t.Fatal(err)
		}
		return viaStd(opDial, s, false)
	})
	if !errors.Is(err, errCwdChanged) {
		t.Fatalf("dial: %v, want errCwdChanged", err)
	}
	l.SetDeadline(time.Now().Add(guard))
	s, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(guard))
	if n, err := s.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("the discarded connection is open: %d, %v", n, err)
	}
}
