//go:build !freebsd

package sockat

// The routes of a dial that spell the path without D (every system but
// FreeBSD): relative (the path relative to the cwd), first in the connect
// order where /proc is not used, and symlink-file (a short symlink to the socket
// file under the first safe place of TMPDIR, then /tmp), last in it. A
// connect leaves no spelling behind with the peer (the connecting socket is
// not bound), so these spellings need no random part; a listen never takes
// relative, and takes symlink-file only where a bind follows a symlink
// (symlinkfile_test.go). With /proc, Linux has proc-direct and proc-held instead
// (os_linux_test.go, heldlink_linux_test.go).
//
// The tests reach them with fallbackCfg: no thread cwd and no /proc, so the
// routes through D have no means and are not even attempted (and Linux takes
// its order without /proc). The listener at a long path is made by listenAt
// (bound at a short path and renamed), so these tests need no route of a
// listen and run on every system.

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

// fallbackCfg is the route order's inputs with the routes through D out of
// reach (no thread cwd, /proc made to look absent, so no means at all) and
// bases as the places for symlink-file: a dial takes relative or symlink-file.
func fallbackCfg(bases ...string) *config {
	return &config{tmpBases: bases, procSelfFD: noProc, threadCwd: false}
}

// centralCfg is the route order's inputs for the routes through D: D placed in
// dBases (then the socket's directory), thread-cwd on, /proc looked
// for where it is (Linux), and no place for symlink-file.
func centralCfg(dBases ...string) *config {
	return &config{dBases: dBases, procSelfFD: "/proc/self/fd", threadCwd: true}
}

// dialLong runs the long-path connect order with cfg and returns the net
// package's type, failing the test on an error.
func dialLong(t *testing.T, p string, cfg *config) (*net.UnixConn, route) {
	t.Helper()
	ep, r, err := runDialLong(p, cfg)
	return must{t}.conn(ep, err), r
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

// A safe place for the symlink must be a directory. A file of ours with no
// group / other write bits (or a symlink to one) passes the mode and owner
// test of onlyWeCanReplace, but nothing can be created in it; it is not a
// place at all, so safeBase says no and the order moves on without trying it.
func TestASafePlaceMustBeADirectory(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	if !safeBase(d) {
		t.Fatalf("%s (our 0700 directory): want safe", d)
	}
	f := d + "/file"
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if safeBase(f) {
		t.Errorf("%s (our 0600 file): want not safe", f)
	}
	link := d + "/link"
	if err := os.Symlink(f, link); err != nil {
		t.Fatal(err)
	}
	if safeBase(link) {
		t.Errorf("%s (a symlink to our file): want not safe", link)
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
// that refuses every name; symlink then counts that place as
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

// symlinkFile (a connect, and a bind where the system tries it) checks,
// before creating anything, that the spelling through the link fits and that
// the place is safe; and a place where the link cannot be created makes the
// route unusable there (errUnusable: the order moves on) rather than failing
// the call. Checked on places that are missing, a file,
// too long (250 bytes, more than the sockaddr carries even for the link
// alone), writable by others, and, unless running as root (who may write
// anywhere), read-only. No socket appears, and the directories involved keep
// their mtime.
func TestSymlinkFileIsUnusableWhereItCannotBeUsedAndCreatesNothing(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	file := d + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	long := nest(t, tmpDir(t), 250, "base")
	mkdirMode(t, long, 0o700)
	open := mkdirMode(t, d+"/open", 0o777)
	places := []string{long, open}
	if os.Geteuid() != 0 {
		places = append(places, mkdirMode(t, d+"/readonly", 0o500))
	}
	for _, pl := range places {
		markUntouched(t, pl)
	}
	for _, pl := range append([]string{"/nonexistent", file}, places...) {
		if _, err := symlinkFile(p, []string{pl}); !errors.Is(err, errUnusable) {
			t.Errorf("dial under %s: %v, want errUnusable", pl, err)
		}
	}
	for _, pl := range places {
		assertUntouched(t, pl)
	}
	if exists(p) {
		t.Fatal("something was created at the path")
	}
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

// A path far from the cwd (600 bytes under /tmp, so no relative spelling
// fits) is dialed through a symlink <place>/sockat.XXXXXXXXXX to the socket
// file: it reaches the listener there, the connection is the net package's
// type in working order, and the place is empty afterwards (the link is
// removed once the call returns).
func TestADialFarFromTheCwdGoesThroughATemporarySymlink(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 600, "s.sock")
	l := listenAt(t, p)
	base := tmpDir(t)
	c, r := dialLong(t, p, fallbackCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	assertEmpty(t, base)
	checkStdTypes(t, l, c)
}

// The link to the socket file is spelled <place>/sockat.XXXXXXXXXX whatever
// the file name, so a dial reaches a file name of any length (here 200 bytes,
// past every sockaddr and past what the routes through D carry).
func TestADialThroughTheSymlinkReachesAnyFileName(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	p := nest(t, tmpDir(t), 600, strings.Repeat("n", 200))
	l := listenAt(t, p)
	c, r := dialLong(t, p, fallbackCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertEmpty(t, base)
}

// The link holds the path as the caller wrote it, so the kernel resolves it
// as it resolves the path: a ".." right after a symlink climbs out of the
// symlink's target, not out of the directory the symlink sits in. With
// <t>/link -> <t>/real/sub, <t>/link/../x/... names <t>/real/x/...; a listener
// at <t>/x/... (where a lexical ".." would lead) is a decoy that must not be
// reached.
func TestTheLinkResolvesThePathAsTheKernelDoes(t *testing.T) {
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
	l := listenAt(t, atReal)
	decoyL := listenAt(t, decoy)
	c, r := dialLong(t, spelled, fallbackCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertNothingAccepted(t, decoyL)
	assertEmpty(t, base)
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

// A dial that actually ran through the symlink under the first place is the
// result: its error comes back as it is and the call is not made again under
// the next place (which would repeat it, or worse, succeed somewhere the
// caller did not mean). The second place stays untouched (no link was even
// created there) and the first is left empty: the link is removed after a
// failure too. Covered: a missing socket (ENOENT), a socket nobody listens on
// (ECONNREFUSED), a missing directory (ENOENT, from opening it first).
func TestAFailureThroughTheSymlinkIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := filepath.Dir(p)
	first, second := tmpDir(t), tmpDir(t)
	cfg := fallbackCfg(first, second)
	staleSocket(t, dir+"/stale.sock")
	markUntouched(t, second)
	cases := []struct {
		what  string
		path  string
		errno syscall.Errno
	}{
		{"a missing socket", p, syscall.ENOENT},
		{"a socket nobody listens on", dir + "/stale.sock", syscall.ECONNREFUSED},
		{"a missing directory", dir + "/nope/s.sock", syscall.ENOENT},
	}
	for _, c := range cases {
		_, _, err := runDialLong(c.path, cfg)
		wantErrno(t, "dial "+c.what, err, c.errno)
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
// own (0700, 0755) are used. With only unsafe places there is no route:
// ENAMETOOLONG. Run as root (docker), a place owned by another user is made
// too (chown) and must be passed over, sticky or not.
func TestAnUnsafePlaceForTheSymlinkIsPassedOver(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	dir := filepath.Dir(nest(t, d, 600, "x"))
	places := tmpDir(t)
	good := tmpDir(t)
	var bad []string
	for _, mode := range []os.FileMode{0o777, 0o775, 0o757} {
		pl := mkdirMode(t, fmt.Sprintf("%s/bad%o", places, mode), mode)
		bad = append(bad, pl)
		markUntouched(t, pl)
		q := fmt.Sprintf("%s/bad%o.sock", dir, mode)
		l := listenAt(t, q)
		c, r := dialLong(t, q, fallbackCfg(pl, good))
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
		l := listenAt(t, q)
		c, r := dialLong(t, q, fallbackCfg(pl))
		if r != routeSymlinkFile {
			t.Fatalf("%v: dial %s", mode, r)
		}
		ping(t, l, c)
		assertEmpty(t, pl)
	}

	q := dir + "/onlybad.sock"
	listenAt(t, q)
	_, _, err := runDialLong(q, fallbackCfg(bad...))
	wantErrno(t, "dial with only unsafe places", err, syscall.ENAMETOOLONG)
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
			listenAt(t, q)
			_, r := dialLong(t, q, fallbackCfg(pl, good))
			if r != routeSymlinkFile {
				t.Fatalf("%v: dial %s", mode, r)
			}
			assertUntouched(t, pl)
		}
	})
}

// A place where the symlink cannot be created (missing, or a file rather than
// a directory) makes that place unusable, not the whole route: the next place
// is used.
func TestAPlaceWhereTheSymlinkCannotBeCreatedMovesOnToTheNext(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	l := listenAt(t, p)
	file := d + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	good := tmpDir(t)
	for _, bad := range []string{"/nonexistent", file} {
		c, r := dialLong(t, p, fallbackCfg(bad, good))
		if r != routeSymlinkFile {
			t.Fatalf("%s: dial %s", bad, r)
		}
		ping(t, l, c)
	}
	assertEmpty(t, good)
}

// The symlink goes under the first place where the link fits. A place so long
// that the sockaddr cannot carry even the link (the situation of a deeply
// nested TMPDIR) is passed over without creating anything in it, and the next
// place is used. With two short places the first is used and the second never
// touched.
func TestTheSymlinkGoesUnderTheFirstPlaceWhereItFits(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 600, "s.sock")
	l := listenAt(t, p)
	long := nest(t, tmpDir(t), 250, "base")
	mkdirMode(t, long, 0o700)
	first, second := tmpDir(t), tmpDir(t)
	markUntouched(t, long)
	c, r := dialLong(t, p, fallbackCfg(long, first))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertUntouched(t, long)
	assertEmpty(t, first)

	markUntouched(t, second)
	c, _ = dialLong(t, p, fallbackCfg(first, second))
	ping(t, l, c)
	assertUntouched(t, second)
	assertEmpty(t, first)
}

// A TMPDIR too long for the symlink does not leave a long path without it:
// configFromTmpdir puts /tmp after TMPDIR, so a 600-byte path is still dialed
// through a symlink, under /tmp, and nothing is created in TMPDIR. No link of
// sockat's pointing at the socket's directory is left in /tmp either.
func TestADeepTmpdirFallsBackToTmp(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	l := listenAt(t, p)
	long := nest(t, tmpDir(t), 250, "tmpdir")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	cfg := configFromTmpdir(long)
	cfg.procSelfFD, cfg.threadCwd = noProc, false
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

// The same through the environment, as Dial reads it (configFromEnv): with
// TMPDIR set to a directory too long for the symlink, a 600-byte path is
// dialed through a symlink under /tmp and nothing is created in TMPDIR. Runs
// in a child process because it sets TMPDIR; the child's cwd is / so the
// relative spelling is long too.
func TestADeepTmpdirInTheEnvironmentFallsBackToTmp(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	l := listenAt(t, p)
	long := nest(t, tmpDir(t), 250, "tmpdir")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	out := runChild(t, "TestChildDialFromEnv", "/", "TMPDIR="+long, "SOCKAT_PATH="+p)
	if got := readChildReport(t, out); got != string(routeSymlinkFile) {
		t.Fatalf("route %s\n%s", got, out)
	}
	l.SetDeadline(nowPlusGuard())
	if _, err := l.Accept(); err != nil {
		t.Fatalf("the child's dial did not arrive: %v", err)
	}
	assertUntouched(t, long)
	assertNoLinkInTmpTo(t, d)
}

func TestChildDialFromEnv(t *testing.T) {
	asChild(t)
	cfg := configFromEnv()
	cfg.procSelfFD, cfg.threadCwd = noProc, false
	_, r := dialLong(t, os.Getenv("SOCKAT_PATH"), cfg)
	childReport("%s", r)
}

// With no route left for a dial (no routes through D, no relative spelling, the
// only place for the symlink too long), a path is ENAMETOOLONG, the error of a
// connect to a path too long for sun_path, and nothing happens: no connection
// reaches the listener, nothing is created in the place or next to the
// socket. A real failure stays what it is: a missing directory is ENOENT. A
// listen with the routes through D out of reach is ENAMETOOLONG too: a listen
// never takes relative, and symlink-file's only place is too long.
func TestWithNoRouteLeftAPathIsENAMETOOLONGAndNothingIsMade(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := filepath.Dir(p)
	l := listenAt(t, p)
	long := nest(t, tmpDir(t), 250, "base")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	markUntouched(t, dir)
	none := fallbackCfg(long)
	_, _, err := runDialLong(p, none)
	wantErrno(t, "dial", err, syscall.ENAMETOOLONG)
	_, _, err = runDialLong(dir+"/nope/s.sock", none)
	wantErrno(t, "dial in a missing directory", err, syscall.ENOENT)
	assertUntouched(t, long)
	assertUntouched(t, dir)
	assertNothingAccepted(t, l)
}

// ----- the relative spelling -----

// A path over directMax bytes whose spelling relative to the cwd is at most
// directMax bytes is dialed through that spelling (through the net package),
// before any place for the symlink is considered, which stays untouched. Runs
// in a child process whose cwd is a temporary directory, to have a socket near
// the cwd.
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
	base := tmpDir(t)
	markUntouched(t, base)
	c, r := dialLong(t, p, fallbackCfg(base))
	if r != routeRelative {
		t.Fatalf("dial: %s", r)
	}
	checkStdTypes(t, l, c)
	assertUntouched(t, base)
}

// A failure of a dial made through the relative spelling is the result: it is
// not retried through a symlink (the place stays untouched). The other paths
// have names of the same length, so their relative spellings fit as well.
// Runs in a child process whose cwd is a temporary directory.
func TestAFailureThroughTheRelativeSpellingIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	p := belowCwdPath(t, directMax, "s.sock")
	dir := filepath.Dir(p)
	base := tmpDir(t)
	cfg := fallbackCfg(base)
	staleSocket(t, dir+"/z.sock")
	markUntouched(t, base)
	_, _, err := runDialLong(p, cfg)
	wantErrno(t, "dial a missing socket", err, syscall.ENOENT)
	_, _, err = runDialLong(dir+"/z.sock", cfg)
	wantErrno(t, "dial a socket nobody listens on", err, syscall.ECONNREFUSED)
	assertUntouched(t, base)
}

// The tests below run in a child process so the cwd and $PWD can be chosen.
// The parent listens at the path; the child spells the path relative to its
// cwd (newRelative), dials through the connect order with no place for a
// symlink, and reports the route and the spelling; the parent checks that the
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
	c, r := dialLong(t, p, fallbackCfg())
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
