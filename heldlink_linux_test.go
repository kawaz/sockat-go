package sockat

// The fd-held form of the symlink route (Linux with /proc, Dial only): in a
// place for the symlink that others can write to (not safeBase), a symlink
// reached by its name could be renamed away and replaced, so the route makes
// a directory of its own there (mode 0700, sockat.XXXXXXXXXX), opens it
// O_DIRECTORY | O_NOFOLLOW, checks with fstat that it is a directory of the
// effective uid with mode 0700, creates the symlink "s" to the socket file in
// it through the fd (symlinkat), and connects through /proc/self/fd/<fd>/s.
// The kernel follows the fd, not the name, so a rename in the place does not
// matter, and the spelling is short whatever the path. Afterwards "s" is
// removed through the fd and the directory only while its name still names
// the directory held.
//
// The order per place: a safe place gets the symlink, an unsafe one the
// fd-held form; TMPDIR before /tmp; then the thread-cwd route. The form needs
// /proc: without it an unsafe place is passed over untouched, as on every
// other system (TestAnUnsafePlaceForTheSymlinkIsPassedOver runs there and on
// Linux with /proc made to look absent).
//
// The socket paths here have a file name of 95 bytes directly under a short
// directory: too long to fit behind /proc/self/fd/<n>/ (so the /proc route
// cannot be used although /proc is there, and the POSIX order runs), far from
// the cwd (no relative spelling).

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
	"time"

	"golang.org/x/sys/unix"
)

// unsafePlace is a place for the symlink that others can write to and that is
// not sticky (mode 0777), so not safeBase.
func unsafePlace(t *testing.T) string {
	t.Helper()
	p := mkdirMode(t, tmpDir(t)+"/open", 0o777)
	if safeBase(p) {
		t.Fatal("the premise: a 0777 directory is not a safe place")
	}
	return p
}

// pastProc is a socket path whose 95-byte file name does not fit behind
// /proc/self/fd/<n>/, with a listener at it (bound under a short name and
// renamed, since no listen reaches the long name where the thread-cwd route
// is refused).
func pastProc(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	d := tmpDir(t)
	p := d + "/" + strings.Repeat("n", 95)
	if len("/proc/self/fd/0/")+95 <= directMax {
		t.Fatal("the premise: the name does not fit behind /proc")
	}
	if rel := newRelative(p, d); rel != nil {
		t.Fatalf("the premise: %s is far from the cwd", p)
	}
	l, err := Listen(d + "/s.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if err := os.Rename(d+"/s.sock", p); err != nil {
		t.Fatal(err)
	}
	return p, l
}

// In an unsafe place, a dial takes the fd-held form: it reaches the listener
// at the path (data flows both ways, the types are the net package's in
// working order, the server sees this process as the peer), and the place is
// empty afterwards (the symlink and the directory are removed). The same for
// a path whose directory part is long (600 bytes): the spelling does not
// depend on it. A name that fits behind /proc/self/fd/<n>/ (85 bytes: too
// long for the central route's /proc form, which also holds R/) takes the
// /proc route and the place is not touched at all.
func TestADialThroughAnUnsafePlaceTakesTheFdHeldForm(t *testing.T) {
	t.Parallel()
	base := unsafePlace(t)
	p, l := pastProc(t)
	c, r := dialLong(t, p, procCfg(base))
	if r != routeHeldLink {
		t.Fatalf("dial: %s, want %s", r, routeHeldLink)
	}
	assertEmpty(t, base)
	checkStdTypes(t, l, c)

	c, _ = dialLong(t, p, procCfg(base))
	l.SetDeadline(nowPlusGuard())
	s, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if pid := peerPid(t, s); pid != os.Getpid() {
		t.Fatalf("the peer is pid %d, not this process", pid)
	}
	pingConns(t, s, c)

	deep := nest(t, tmpDir(t), 600, strings.Repeat("m", 95))
	dl := must{t}.listener(Listen(filepath.Dir(deep) + "/s.sock"))
	if err := os.Rename(filepath.Dir(deep)+"/s.sock", deep); err != nil {
		t.Fatal(err)
	}
	c, r = dialLong(t, deep, procCfg(base))
	if r != routeHeldLink {
		t.Fatalf("a 600-byte path: dial %s", r)
	}
	ping(t, dl, c)
	assertEmpty(t, base)

	fits := nest(t, tmpDir(t), 300, strings.Repeat("f", 85))
	fl := listenAt(t, fits)
	markUntouched(t, base)
	c, r = dialLong(t, fits, procCfg(base))
	if r != routeProc {
		t.Fatalf("a name that fits behind /proc: dial %s", r)
	}
	ping(t, fl, c)
	assertUntouched(t, base)
}

// The other unsafe places, run as root (docker): a place owned by another user
// is unsafe whether sticky or not (its owner can rename anything in it), and
// root may still create the directory there. Both take the fd-held form and
// are empty afterwards.
func TestAnotherUsersPlaceTakesTheFdHeldForm(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("only root can give a directory to another user")
	}
	p, l := pastProc(t)
	for _, mode := range []os.FileMode{0o755, os.ModeSticky | 0o777} {
		base := mkdirMode(t, fmt.Sprintf("%s/theirs%o", tmpDir(t), mode.Perm()), mode)
		if err := os.Chown(base, 65534, -1); err != nil {
			t.Fatal(err)
		}
		if safeBase(base) {
			t.Fatalf("the premise: %v owned by another user is not safe", mode)
		}
		c, r := dialLong(t, p, procCfg(base))
		if r != routeHeldLink {
			t.Fatalf("%v: dial %s", mode, r)
		}
		ping(t, l, c)
		assertEmpty(t, base)
	}
}

// A dial through the fd-held form that fails is the result: ENOENT for a
// missing socket, ECONNREFUSED for a socket nobody listens on, as they are,
// with the place empty afterwards (cleaned up after a failure too), and the
// later places, unsafe or safe, untouched: no directory or symlink was even
// created there, and the thread-cwd route was not tried. A missing directory
// is ENOENT before any place is looked at.
func TestAFailureThroughTheFdHeldFormIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	first, laterUnsafe, laterSafe := unsafePlace(t), unsafePlace(t), tmpDir(t)
	cfg := procCfg(first, laterUnsafe, laterSafe)
	p, _ := pastProc(t)
	dir := filepath.Dir(p)
	stale := dir + "/z" + strings.Repeat("n", 94)
	staleSocket(t, stale)
	cases := []struct {
		what  string
		path  string
		errno syscall.Errno
	}{
		{"a missing socket", dir + "/m" + strings.Repeat("n", 94), syscall.ENOENT},
		{"a socket nobody listens on", stale, syscall.ECONNREFUSED},
		{"a missing directory", dir + "/nope/" + strings.Repeat("n", 95), syscall.ENOENT},
	}
	for _, c := range cases {
		markUntouched(t, laterUnsafe)
		markUntouched(t, laterSafe)
		_, _, err := runLong(opDial, c.path, cfg)
		wantErrno(t, "dial "+c.what, err, c.errno)
		assertEmpty(t, first)
		assertUntouched(t, laterUnsafe)
		assertUntouched(t, laterSafe)
	}
}

// TMPDIR is used up before /tmp: the places are tried in order, each in the
// form its safety calls for, and the first that works is the result. With two
// unsafe places the first takes the fd-held form and the second is untouched;
// an unsafe TMPDIR is used (fd-held) before a safe /tmp, and a safe TMPDIR
// (the symlink) before an unsafe /tmp, the later one untouched either way.
// configFromTmpdir puts TMPDIR first (TestConfigFromTmpdirTriesTmpdirThenTmp).
func TestThePlacesAreTriedInOrderEachInItsForm(t *testing.T) {
	t.Parallel()
	p, l := pastProc(t)
	cases := []struct {
		first, second string
		want          route
	}{
		{unsafePlace(t), unsafePlace(t), routeHeldLink},
		{unsafePlace(t), tmpDir(t), routeHeldLink},
		{tmpDir(t), unsafePlace(t), routeSymlinkFile},
	}
	for _, c := range cases {
		markUntouched(t, c.second)
		cc, r := dialLong(t, p, procCfg(c.first, c.second))
		if r != c.want {
			t.Fatalf("%s then %s: %s, want %s", c.first, c.second, r, c.want)
		}
		ping(t, l, cc)
		assertEmpty(t, c.first)
		assertUntouched(t, c.second)
	}
}

// Without /proc (procSelfFD pointed at an empty directory, or at nothing)
// there is no fd-held form: an unsafe place is passed over without anything
// created in it (its mtime is unchanged), as on the systems without /proc;
// with no other way left the dial is ENAMETOOLONG.
func TestWithoutProcAnUnsafePlaceIsPassedOverUntouched(t *testing.T) {
	t.Parallel()
	empty := tmpDir(t) + "/proc/self/fd"
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	p, _ := pastProc(t)
	for _, none := range []string{empty, noProc} {
		base := unsafePlace(t)
		markUntouched(t, base)
		cfg := procCfg(base)
		cfg.procSelfFD = none
		_, _, err := runLong(opDial, p, cfg)
		wantErrno(t, "dial with "+none, err, syscall.ENAMETOOLONG)
		assertUntouched(t, base)
	}
}

// A listen never takes the fd-held form, or any of a dial's fallbacks: it
// creates nothing in a place for a dial's symlink, unsafe or not. With the
// thread cwd off, a 95-byte name is too long for the central route's /proc
// form, so the listen is ENAMETOOLONG; a short name takes that form (D goes in
// the socket's directory here, procCfg giving no place for D).
func TestAListenCreatesNothingInAnUnsafePlace(t *testing.T) {
	t.Parallel()
	base := unsafePlace(t)
	markUntouched(t, base)
	d := tmpDir(t)
	_, _, err := runLong(opListen, d+"/"+strings.Repeat("n", 95), procCfg(base))
	wantErrno(t, "listen on a 95-byte name", err, syscall.ENAMETOOLONG)
	_, r := listenLong(t, d+"/s"+strings.Repeat("x", 60), procCfg(base))
	if r != routeProcD {
		t.Fatalf("listen on a short name: %s", r)
	}
	assertUntouched(t, base)
}

// The directory of the fd-held form is named as the symlink is: a name
// already taken (by anything, here a file) is skipped for the next, the entry
// left as it was; after 16 taken names (linkAttempts) it gives up with
// errNoFreeName; any other failure of mkdir(2) (the place missing: ENOENT),
// or of drawing a name, ends the attempt at once. What it creates is a
// directory of mode 0700.
func TestCreateHeldDirSkipsTakenNamesAndGivesUpAfter16(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	taken, free := linkPrefix+"AAAAAAAAAA", linkPrefix+"BBBBBBBBBB"
	if err := os.WriteFile(base+"/"+taken, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{taken, free}
	calls := 0
	got, err := createHeldDir(base, func() (string, error) { calls++; return names[calls-1], nil })
	if err != nil || calls != 2 || got != base+"/"+free {
		t.Fatalf("%q, %v after %d tries", got, err, calls)
	}
	if fi, err := os.Lstat(got); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%v, %v: want a directory of mode 0700", fi.Mode(), err)
	}
	if b, _ := os.ReadFile(base + "/" + taken); string(b) != "keep" {
		t.Fatalf("the taken entry was changed: %q", b)
	}

	calls = 0
	_, err = createHeldDir(base, func() (string, error) { calls++; return taken, nil })
	if !errors.Is(err, errNoFreeName) || calls != linkAttempts || linkAttempts != 16 {
		t.Fatalf("all taken: %v after %d tries, want errNoFreeName after 16", err, calls)
	}

	calls = 0
	_, err = createHeldDir("/nonexistent", func() (string, error) { calls++; return randomName() })
	if !errors.Is(err, syscall.ENOENT) || calls != 1 {
		t.Fatalf("missing place: %v after %d tries", err, calls)
	}
	drawErr := errors.New("no randomness")
	calls = 0
	_, err = createHeldDir(base, func() (string, error) { calls++; return "", drawErr })
	if !errors.Is(err, drawErr) || calls != 1 {
		t.Fatalf("failed draw: %v after %d tries", err, calls)
	}
}

// isOurPrivateDir, the check made on the held directory as on D, is pinned by
// TestIsOurPrivateDirTakesOnlyOurOwnPrivateDirectory (central_test.go).

// A umask that takes the owner's bits makes mkdir(0700) create a directory
// the owner cannot fully use: 0277 gives 0500 (no write), 0477 gives 0300 (no
// read, so a non-root O_RDONLY open would fail with EACCES). Such a directory
// is still ours and private, so the route uses it (opened O_PATH, which needs
// no read permission, then chmod 0700 through /proc) rather than failing every
// dial and leaving a directory behind each time: the dial arrives through the
// fd-held form and the place is empty afterwards. The umask belongs to the
// whole process, hence the child. The EACCES case only bites as non-root (root
// ignores the mode), so the uid 1000 run is the one that pins 0477.
func TestAUmaskTakingTheOwnersBitsStillUsesTheHeldDirectory(t *testing.T) {
	t.Parallel()
	for _, umask := range []string{"0277", "0477"} {
		t.Run("umask "+umask, func(t *testing.T) {
			p, l := pastProc(t)
			base := unsafePlace(t)
			safe := tmpDir(t)
			markUntouched(t, safe)
			runChild(t, "TestChildHeldDirUnderAUmask", "/", "SOCKAT_PATH="+p, "SOCKAT_BASE="+base, "SOCKAT_SAFE="+safe, "SOCKAT_UMASK="+umask)
			assertEmpty(t, base)
			assertUntouched(t, safe)
			l.SetDeadline(nowPlusGuard())
			s, err := l.Accept()
			if err != nil {
				t.Fatalf("the child's dial did not arrive: %v", err)
			}
			s.Close()
		})
	}
}

// The check is made on what the route actually opened. A directory swapped in
// between the mkdir and the open differs in its owner (or has group / other
// bits); such a directory is stood in for by making the route's own mkdir
// produce one (root only): the route runs on a thread whose filesystem uid is
// 65534 (setfsuid(2) is per thread), so the directory belongs to 65534 while
// the effective uid stays 0. The route must not use it: errUnusable, no
// symlink created in it, and it is left alone, not being known to be ours.
func TestADirectoryThatIsNotOurOwnIsNotUsed(t *testing.T) {
	t.Parallel()
	p, _ := pastProc(t)

	t.Run("owner", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("only root can create a directory as another user")
		}
		base := unsafePlace(t)
		// The thread with filesystem uid 65534 must be able to reach the place
		// (tmpDir is 0700).
		if err := os.Chmod(filepath.Dir(base), 0o755); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			// Never unlocked: the thread's filesystem uid is changed, so the
			// thread is discarded with the goroutine.
			runtime.LockOSThread()
			if err := unix.Setfsuid(65534); err != nil {
				done <- err
				return
			}
			_, err := heldLinkRoute(base, p, "/proc/self/fd")
			done <- err
		}()
		if err := <-done; !errors.Is(err, errUnusable) {
			t.Fatalf("%v, want errUnusable", err)
		}
		assertOnlyAnUnusedHeldDir(t, base, 0o700, 65534)
	})
}

func TestChildHeldDirUnderAUmask(t *testing.T) {
	asChild(t)
	umask, err := strconv.ParseUint(os.Getenv("SOCKAT_UMASK"), 8, 32)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Umask(int(umask))
	p, base, safe := os.Getenv("SOCKAT_PATH"), os.Getenv("SOCKAT_BASE"), os.Getenv("SOCKAT_SAFE")
	// The unsafe place comes first and its fd-held form is used despite the
	// umask; the safe place after it is not reached.
	c, r := dialLong(t, p, procCfg(base, safe))
	if r != routeHeldLink {
		t.Fatalf("dial: %s, want %s", r, routeHeldLink)
	}
	c.Close()
}

// assertOnlyAnUnusedHeldDir checks that base holds exactly one directory of
// the route's (sockat.*, mode perm, owned by owner), with nothing in it: no symlink was
// created through a directory that failed the check.
func assertOnlyAnUnusedHeldDir(t *testing.T, base string, perm os.FileMode, owner int) {
	t.Helper()
	es, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || !strings.HasPrefix(es[0].Name(), linkPrefix) {
		t.Fatalf("entries of %s: %v", base, es)
	}
	dir := base + "/" + es[0].Name()
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != perm {
		t.Fatalf("%s: %v, %v", dir, fi.Mode(), err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && owner >= 0 && int(st.Uid) != owner {
		t.Fatalf("%s: owner %d, want %d", dir, st.Uid, owner)
	}
	os.Chmod(dir, 0o700)
	in, err := os.ReadDir(dir)
	if err != nil || len(in) != 0 {
		t.Fatalf("in %s: %v, %v (a symlink was created through it)", dir, in, err)
	}
}

// A dial fails at once on a full backlog through the fd-held form too (the
// spelling goes to net.DialUnix, whose connect does not wait), and every dial
// that got in took the form; the place is empty afterwards.
func TestADialThroughTheFdHeldFormFailsAtOnceOnAFullBacklog(t *testing.T) {
	t.Parallel()
	base := unsafePlace(t)
	p, l := pastProc(t)
	relisten(t, l, 1)
	var full error
	for queued := 0; full == nil; queued++ {
		if queued == 16 {
			t.Fatal("the backlog never filled")
		}
		type result struct {
			ep  endpoint
			r   route
			err error
		}
		res := atOnce(t, "a dial to a full backlog", func() result {
			ep, r, err := runLong(opDial, p, procCfg(base))
			return result{ep, r, err}
		})
		if res.err != nil {
			full = res.err
			break
		}
		defer res.ep.Close()
		if res.r != routeHeldLink {
			t.Fatalf("dial: %s", res.r)
		}
	}
	wantErrno(t, "dial to a full backlog", full, fullBacklog)
	assertEmpty(t, base)
}

// The case that brought the fd-held form in (hyoui): under a seccomp filter
// (no thread-cwd route), with no safe place for the symlink and a file name
// too long for the /proc route, a dial reaches the socket. A child process
// puts a filter on itself that kills on unshare(2), so this runs the same
// whatever the filter the tests run under (Docker's default, or none).
func TestUnderASeccompFilterADialReachesThroughAnUnsafePlace(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildHeldLinkUnderAKillingSeccompFilter", tmpDir(t), "SOCKAT_BASE="+unsafePlace(t))
}

func TestChildHeldLinkUnderAKillingSeccompFilter(t *testing.T) {
	asChild(t)
	installKillOnUnshareFilter(t)
	base := os.Getenv("SOCKAT_BASE")
	p, l := pastProc(t)
	_, _, err := runLong(opDial, p, procCfg())
	wantErrno(t, "dial with no place (the premise: nothing else reaches it)", err, syscall.ENAMETOOLONG)
	c, r := dialLong(t, p, procCfg(base))
	if r != routeHeldLink {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertEmpty(t, base)
}

// A directory swapped in for the route's between its mkdir and its open
// (someone who can write to the place renames ours away and puts theirs, with
// their own "s" in it, at the name) must neither be used nor be touched: the
// route returns errUnusable, connects nowhere, and leaves that directory and
// everything in it as it was. The swap is stood in for by handing heldLinkVia
// a directory that fails the check: one of ours with group / other bits (any
// user), and one of another user (root only, chown).
func TestASwappedInDirectoryAndWhatIsInItAreLeftAlone(t *testing.T) {
	t.Parallel()
	p, l := pastProc(t)
	cases := []struct {
		name  string
		mode  os.FileMode
		owner int
	}{
		{"group and other bits", 0o777, -1},
		{"another user", 0o700, 65534},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.owner >= 0 && os.Geteuid() != 0 {
				t.Skip("only root can create a directory of another user")
			}
			base := unsafePlace(t)
			swapped := base + "/" + linkPrefix + "swappedin0"
			if err := os.Mkdir(swapped, 0o700); err != nil {
				t.Fatal(err)
			}
			theirs := swapped + "/" + heldLinkName
			if err := os.Symlink("/their/target", theirs); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(swapped+"/other", []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.owner >= 0 {
				if err := os.Lchown(theirs, c.owner, -1); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(swapped, c.owner, -1); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(swapped, c.mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(swapped, 0o700) })

			_, err := heldLinkVia(base, p, "/proc/self/fd", func(string) (string, error) { return swapped, nil })
			if !errors.Is(err, errUnusable) {
				t.Fatalf("%v, want errUnusable", err)
			}
			if got, err := os.Readlink(theirs); err != nil || got != "/their/target" {
				t.Fatalf("their %s: %q, %v (removed or replaced)", heldLinkName, got, err)
			}
			if b, err := os.ReadFile(swapped + "/other"); err != nil || string(b) != "x" {
				t.Fatalf("their other file: %q, %v", b, err)
			}
			fi, err := os.Lstat(swapped)
			if err != nil || !fi.IsDir() || fi.Mode().Perm() != c.mode {
				t.Fatalf("the swapped-in directory: %v, %v", fi, err)
			}
			l.SetDeadline(time.Now())
			if s, err := l.Accept(); err == nil {
				s.Close()
				t.Fatal("something connected through the swapped-in directory")
			}
		})
	}
}
