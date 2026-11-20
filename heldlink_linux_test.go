package sockat

// Proc-held (Linux with /proc, Dial only; SPEC "proc-held"): D is made as for
// the other routes through D but without R (SPEC "D と R" steps 1, 2 and 5:
// mkdirat in its place, opened O_PATH, checked with fstat to be a directory
// of the effective uid with no group / other permission, set to 0700 through
// /proc), a symlink "s" to the socket file is made in it through its fd
// (symlinkat), and the connect goes through /proc/self/fd/<D>/s. The kernel
// follows the fd, not a name, so neither the place's safety nor its length
// matters, and the spelling is short whatever the path. Afterwards "s" is
// removed through the fd, and D with the rest of the dial (only while its
// name still names the directory held).
//
// Proc-held comes after proc-direct, thread-cwd and proc in the connect order,
// and before symlink. The socket paths here have a file name of 95 bytes: too
// long for /proc/self/fd/<n>/<name> (proc-direct) and, behind R/, for
// /proc/self/fd/<n>/ (proc), so with the thread cwd off (heldCfg) proc-held is
// the first route that can be used. They are far from the cwd too.

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// unsafePlace is a place that others can write to and that is not sticky
// (mode 0777), so not safeBase: no place for symlink-file, but one for D.
func unsafePlace(t *testing.T) string {
	t.Helper()
	p := mkdirMode(t, tmpDir(t)+"/open", 0o777)
	if safeBase(p) {
		t.Fatal("the premise: a 0777 directory is not a safe place")
	}
	return p
}

// heldCfg is the route order's inputs with the thread cwd off and the real
// /proc, D placed in place, and bases as the places for symlink-file.
func heldCfg(place string, bases ...string) *config {
	c := procCfg(bases...)
	c.dBases = []string{place}
	return c
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

// A dial of a name past proc-direct and proc, with the thread cwd off, takes
// proc-held, also with a safe place for symlink-file given: symlink-file is later in the
// connect order, and its place stays untouched. It reaches the listener at
// the path (data flows both ways, the types are the net package's in working
// order, the server sees this process as the peer), and D's place is empty
// afterwards (D was made there: its mtime moved). D's place need not be safe
// or short: an unsafe one (0777), the user's own, and one 600 bytes deep all
// serve. The same for a path whose directory part is long (600 bytes): the
// spelling does not depend on it.
func TestADialPastProcTakesProcHeld(t *testing.T) {
	t.Parallel()
	p, l := pastProc(t)
	safe := tmpDir(t)
	markUntouched(t, safe)
	deep := filepath.Dir(nest(t, tmpDir(t), 600, "x"))
	for _, place := range []string{unsafePlace(t), tmpDir(t), deep} {
		markUntouched(t, place)
		c, r := dialLong(t, p, heldCfg(place, safe))
		if r != routeProcHeld {
			t.Fatalf("D in %s: dial %s, want %s", place, r, routeProcHeld)
		}
		assertTouched(t, place)
		assertEmpty(t, place)
		checkStdTypes(t, l, c)
	}

	place := unsafePlace(t)
	c, _ := dialLong(t, p, heldCfg(place, safe))
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

	far := nest(t, tmpDir(t), 600, strings.Repeat("m", 95))
	fl := must{t}.listener(Listen(filepath.Dir(far) + "/s.sock"))
	if err := os.Rename(filepath.Dir(far)+"/s.sock", far); err != nil {
		t.Fatal(err)
	}
	c, r := dialLong(t, far, heldCfg(place, safe))
	if r != routeProcHeld {
		t.Fatalf("a 600-byte path: dial %s", r)
	}
	ping(t, fl, c)
	assertEmpty(t, place)
	assertUntouched(t, safe)
}

// The other unsafe places for D, run as root (docker): a place owned by
// another user, sticky or not (its owner can rename anything in it), where
// root may still create D. D is held by its fd, so it is used: proc-held,
// and the place is empty afterwards.
func TestAnotherUsersPlaceServesForProcHeld(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("only root can give a directory to another user")
	}
	p, l := pastProc(t)
	for _, mode := range []os.FileMode{0o755, os.ModeSticky | 0o777} {
		place := mkdirMode(t, tmpDir(t)+"/theirs"+strconv.FormatUint(uint64(mode.Perm()), 8), mode)
		if err := os.Chown(place, 65534, -1); err != nil {
			t.Fatal(err)
		}
		c, r := dialLong(t, p, heldCfg(place))
		if r != routeProcHeld {
			t.Fatalf("%v: dial %s", mode, r)
		}
		ping(t, l, c)
		assertEmpty(t, place)
	}
}

// A dial through proc-held that fails is the result: ENOENT for a missing
// socket, ECONNREFUSED for a socket nobody listens on, as they are, with D's
// place empty afterwards (cleaned up after a failure too), and symlink-file, the
// next route, not tried: the safe place for it stays untouched. A missing
// directory is ENOENT before any place is looked at.
func TestAFailureThroughProcHeldIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	place, later := unsafePlace(t), tmpDir(t)
	cfg := heldCfg(place, later)
	p, _ := pastProc(t)
	dir := filepath.Dir(p)
	stale := dir + "/z" + strings.Repeat("n", 94)
	staleSocket(t, stale)
	cases := []struct {
		what  string
		path  string
		errno syscall.Errno
		via   route
	}{
		{"a missing socket", dir + "/m" + strings.Repeat("n", 94), syscall.ENOENT, routeProcHeld},
		{"a socket nobody listens on", stale, syscall.ECONNREFUSED, routeProcHeld},
		{"a missing directory", dir + "/nope/" + strings.Repeat("n", 95), syscall.ENOENT, ""},
	}
	for _, c := range cases {
		markUntouched(t, later)
		_, r, err := runDialLong(c.path, cfg)
		wantErrno(t, "dial "+c.what, err, c.errno)
		if r != c.via {
			t.Fatalf("dial %s: failed through %q, want %q", c.what, r, c.via)
		}
		assertEmpty(t, place)
		assertUntouched(t, later)
	}
}

// Without /proc (procSelfFD pointed at an empty directory, or at nothing)
// there is no proc-held: the connect order is relative, thread-cwd, symlink-file.
// With the thread cwd off and only an unsafe place for symlink-file, the dial is
// ENAMETOOLONG, and nothing is made in either place (their mtime is
// unchanged).
func TestWithoutProcThereIsNoProcHeld(t *testing.T) {
	t.Parallel()
	empty := tmpDir(t) + "/proc/self/fd"
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	p, _ := pastProc(t)
	for _, none := range []string{empty, noProc} {
		place, base := tmpDir(t), unsafePlace(t)
		markUntouched(t, place)
		markUntouched(t, base)
		cfg := heldCfg(place, base)
		cfg.procSelfFD = none
		_, _, err := runDialLong(p, cfg)
		wantErrno(t, "dial with "+none, err, syscall.ENAMETOOLONG)
		assertUntouched(t, place)
		assertUntouched(t, base)
	}
}

// A umask that takes the owner's bits makes mkdir(0700) create a directory
// the owner cannot fully use: 0277 gives 0500 (no write), 0477 gives 0300 (no
// read, so a non-root O_RDONLY open would fail with EACCES). Such a D is still
// ours and private, so proc-held uses it (opened O_PATH, which needs no read
// permission, then chmod 0700 through /proc) rather than failing every dial
// and leaving a directory behind each time: the dial arrives through
// proc-held, D's place is empty afterwards, and the safe place for symlink-file
// after it is not reached. The umask belongs to the whole process, hence the
// child. The EACCES case only bites as non-root (root ignores the mode), so
// the uid 1000 run is the one that pins 0477.
func TestAUmaskTakingTheOwnersBitsStillLetsProcHeldMakeD(t *testing.T) {
	t.Parallel()
	for _, umask := range []string{"0277", "0477"} {
		t.Run("umask "+umask, func(t *testing.T) {
			p, l := pastProc(t)
			place := unsafePlace(t)
			safe := tmpDir(t)
			markUntouched(t, safe)
			runChild(t, "TestChildProcHeldUnderAUmask", "/", "SOCKAT_PATH="+p, "SOCKAT_PLACE="+place, "SOCKAT_SAFE="+safe, "SOCKAT_UMASK="+umask)
			assertEmpty(t, place)
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

func TestChildProcHeldUnderAUmask(t *testing.T) {
	asChild(t)
	umask, err := strconv.ParseUint(os.Getenv("SOCKAT_UMASK"), 8, 32)
	if err != nil {
		t.Fatal(err)
	}
	syscall.Umask(int(umask))
	p, place, safe := os.Getenv("SOCKAT_PATH"), os.Getenv("SOCKAT_PLACE"), os.Getenv("SOCKAT_SAFE")
	c, r := dialLong(t, p, heldCfg(place, safe))
	if r != routeProcHeld {
		t.Fatalf("dial: %s, want %s", r, routeProcHeld)
	}
	c.Close()
}

// A dial fails at once on a full backlog through proc-held too (the
// spelling goes to net.DialUnix, whose connect does not wait), and every dial
// that got in took proc-held; D's place is empty afterwards.
func TestADialThroughProcHeldFailsAtOnceOnAFullBacklog(t *testing.T) {
	t.Parallel()
	place := unsafePlace(t)
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
			ep, r, err := runDialLong(p, heldCfg(place))
			return result{ep, r, err}
		})
		if res.err != nil {
			full = res.err
			break
		}
		defer res.ep.Close()
		if res.r != routeProcHeld {
			t.Fatalf("dial: %s", res.r)
		}
	}
	wantErrno(t, "dial to a full backlog", full, fullBacklog)
	assertEmpty(t, place)
}

// The case that brought proc-held in (hyoui): under a seccomp filter (no
// thread-cwd), with no place for symlink-file and a file name too long for
// proc-direct and proc, a dial reaches the socket through proc-held, after
// recording the routes before it as unusable, thread-cwd among them (not even
// tried: unshare(2) is not called under a filter). A child process puts a
// filter on itself that kills on unshare(2), so this runs the same whatever
// the filter the tests run under (Docker's default, or none).
func TestUnderASeccompFilterADialReachesThroughProcHeld(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildProcHeldUnderAKillingSeccompFilter", tmpDir(t), "SOCKAT_PLACE="+unsafePlace(t))
}

func TestChildProcHeldUnderAKillingSeccompFilter(t *testing.T) {
	asChild(t)
	installKillOnUnshareFilter(t)
	place := os.Getenv("SOCKAT_PLACE")
	p, l := pastProc(t)
	cfg := heldCfg(place)
	cfg.threadCwd = true
	ep, r, skipped, err := dialTraced(t, p, cfg)
	c := must{t}.conn(ep, err)
	if want := []route{routeStandard, routeProcDirect, routeThreadCwd, routeProc}; r != routeProcHeld || !slices.Equal(namesOf(t, skipped), want) {
		t.Fatalf("dial: %s after %v, want %s after %v", r, skipped, routeProcHeld, want)
	}
	ping(t, l, c)
	assertEmpty(t, place)
}
