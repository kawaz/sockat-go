//go:build linux || darwin

package sockat

// The routes through D (centralRoute): a directory D of our own, made in a place
// (cfg.dBases, then the socket's directory as ".sockat.XXXXXXXXXX"), held by
// its fd and checked with fstat, with a symlink R of a random name in it
// pointing to the socket's directory; the bind / connect goes by R/<name>
// relative to D, from a thread whose own cwd is D (Linux, macOS) or through
// /proc/self/fd/<D>/R/<name> (Linux). R and D are removed afterwards, success
// or failure, so the spelling the kernel keeps for the bound socket names
// nothing wherever it is resolved later.
//
// Every listen of a long path takes this route (Go has no fork, SPEC's other
// way of reaching D); a dial takes it first and its fallbacks only when it
// cannot be used.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// means is one way of reaching D's fd: the route it takes and the inputs that
// force it (D placed in dBases).
type means struct {
	name  string
	want  route
	cfg   func(dBases ...string) *config
	spell *regexp.Regexp // what the kernel keeps for the bound socket
}

// rPart is R's name: ten letters or digits.
const rPart = `[A-Za-z0-9]{10}`

// allMeans are the means of the routes through D this system can use here: the
// thread cwd where a thread may have a cwd of its own (not under a seccomp
// filter on Linux), and on Linux proc (the thread cwd turned off).
func allMeans() []means {
	var ms []means
	if threadCwdAvailable() {
		ms = append(ms, means{"thread-cwd", routeThreadCwd, centralCfg,
			regexp.MustCompile(`^` + rPart + `/s\.sock$`)})
	}
	if runtime.GOOS == "linux" {
		ms = append(ms, means{"proc", routeProc, func(b ...string) *config {
			c := centralCfg(b...)
			c.threadCwd = false
			return c
		}, regexp.MustCompile(`^/proc/self/fd/[0-9]+/` + rPart + `/s\.sock$`)})
	}
	return ms
}

// assertNoSockatEntry checks that dir holds nothing of sockat's (sockat.* or
// .sockat.*): no D or R left behind.
func assertNoSockatEntry(t *testing.T, dir string) {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), linkPrefix) || strings.HasPrefix(e.Name(), "."+linkPrefix) {
			t.Fatalf("left behind in %s: %s", dir, e.Name())
		}
	}
}

// Forced by each means, the routes through D listen and dials at the path (600
// bytes, so the path itself fits no sockaddr): the socket file is at the
// path, the two talk, they are the net package's types in working order, and
// the server sees this process as the peer (no other process is involved).
// D was made in the place given (its mtime moved) and nothing is left there
// or in the socket's directory afterwards: neither D nor R, after the listen
// and after each dial. The kernel keeps R/<name> (or
// /proc/self/fd/<D>/R/<name>) as the listener's address.
func TestTheRoutesThroughDListenAndDialAtThePath(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	for _, m := range ms {
		t.Run(m.name, func(t *testing.T) {
			base := tmpDir(t)
			p := nest(t, tmpDir(t), 600, "s.sock")
			dir := filepath.Dir(p)
			markUntouched(t, base)
			l, r := listenLong(t, p, m.cfg(base))
			if r != m.want || !isSocket(p) {
				t.Fatalf("listen: %s, socket at the path %v", r, isSocket(p))
			}
			assertTouched(t, base)
			assertEmpty(t, base)
			assertNoSockatEntry(t, dir)
			if a := l.Addr().String(); !m.spell.MatchString(a) {
				t.Fatalf("the kernel keeps %q", a)
			}
			markUntouched(t, base)
			c, r := dialLong(t, p, m.cfg(base))
			if r != m.want {
				t.Fatalf("dial: %s", r)
			}
			assertTouched(t, base)
			assertEmpty(t, base)
			checkStdTypes(t, l, c)

			c2, _ := dialLong(t, p, m.cfg(base))
			l.SetDeadline(nowPlusGuard())
			s, err := l.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if pid := peerPid(t, s); pid != os.Getpid() {
				t.Fatalf("the peer is pid %d, not this process (%d)", pid, os.Getpid())
			}
			pingConns(t, s, c2)
			assertEmpty(t, base)
			assertNoSockatEntry(t, dir)
		})
	}
}

// A listen / dial through the routes through D that fails is the result: the
// kernel's error (EADDRINUSE for an existing file, ENOENT for a missing
// socket, ECONNREFUSED for a socket nobody listens on) comes back as it is,
// R and D are removed after the failure too, and a dial does not go on to
// its fallback routes (a place for a fallback symlink stays untouched).
func TestAFailureThroughDIsReturnedAndLeavesNothing(t *testing.T) {
	t.Parallel()
	for _, m := range allMeans() {
		t.Run(m.name, func(t *testing.T) {
			base, later := tmpDir(t), tmpDir(t)
			cfg := m.cfg(base)
			cfg.tmpBases = []string{later}
			p := nest(t, tmpDir(t), 600, "s.sock")
			dir := filepath.Dir(p)
			if err := os.WriteFile(dir+"/e.sock", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			staleSocket(t, dir+"/z.sock")
			markUntouched(t, later)
			for _, c := range []struct {
				what  string
				o     op
				path  string
				errno syscall.Errno
			}{
				{"listen on an existing file", opListen, dir + "/e.sock", syscall.EADDRINUSE},
				{"dial a missing socket", opDial, p, syscall.ENOENT},
				{"dial a socket nobody listens on", opDial, dir + "/z.sock", syscall.ECONNREFUSED},
			} {
				_, _, err := runLong(c.o, c.path, cfg)
				wantErrno(t, c.what, err, c.errno)
				assertEmpty(t, base)
				assertNoSockatEntry(t, dir)
			}
			assertUntouched(t, later)
		})
	}
}

// A missing socket directory is ENOENT before anything else: before the
// length is looked at (a 254-byte name fits no route) and before D is made
// (the place keeps its mtime).
func TestAMissingDirectoryIsENOENTBeforeDIsMade(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	markUntouched(t, base)
	for _, name := range []string{"s.sock", strings.Repeat("n", 254)} {
		p := tmpDir(t) + "/nope/" + name
		for _, o := range []op{opListen, opDial} {
			_, _, err := runLong(o, p, centralCfg(base))
			wantErrno(t, fmt.Sprintf("%s with a %d-byte name", o.sysName(), len(name)), err, syscall.ENOENT)
		}
	}
	assertUntouched(t, base)
}

// The spelling R/<name> is 11 bytes and the file name, so the routes through D
// carries a file name of directMax - 11 bytes (96 on Linux, 92 on macOS) and
// no more. One byte more is told before anything is made (the place keeps its
// mtime): a listen is then ENAMETOOLONG (Go has no fork, the SPEC's next way
// for a bind), and a dial goes on to its fallback routes (here the symlink
// under a safe place).
func TestTheRoutesThroughDCarryAFileNameOfDirectMaxLess11Bytes(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 || ms[0].want != routeThreadCwd {
		t.Skip("thread-cwd is refused here (proc is shorter)")
	}
	m := ms[0]
	base, fb := tmpDir(t), tmpDir(t)
	dir := filepath.Dir(nest(t, tmpDir(t), 600, "x"))
	fits := dir + "/" + strings.Repeat("f", directMax-11)
	l, r := listenLong(t, fits, m.cfg(base))
	if r != m.want {
		t.Fatalf("a %d-byte name: listen %s", directMax-11, r)
	}
	c, r := dialLong(t, fits, m.cfg(base))
	if r != m.want {
		t.Fatalf("a %d-byte name: dial %s", directMax-11, r)
	}
	ping(t, l, c)

	over := dir + "/" + strings.Repeat("o", directMax-10)
	cfg := m.cfg(base)
	cfg.tmpBases = []string{fb}
	markUntouched(t, base)
	_, _, err := runLong(opListen, over, cfg)
	wantErrno(t, fmt.Sprintf("listen on a %d-byte name", directMax-10), err, syscall.ENAMETOOLONG)
	if exists(over) {
		t.Fatal("something was created at the path")
	}
	ol := listenAt(t, over)
	c, r = dialLong(t, over, cfg)
	if r != routeSymlink {
		t.Fatalf("dial a %d-byte name: %s", directMax-10, r)
	}
	ping(t, ol, c)
	assertUntouched(t, base)
	assertEmpty(t, fb)
}

// Without any means (the thread cwd turned off, and on Linux /proc made to
// look absent: procSelfFD an empty directory) the routes through D are not
// attempted and nothing is made in the place: a listen of a long path is
// ENAMETOOLONG, a dial takes its fallback routes. Where only the thread cwd
// is off, Linux takes proc (TestTheRoutesThroughDListenAndDialAtThePath,
// "proc").
func TestWithoutAnyMeansAListenIsENAMETOOLONGAndADialFallsBack(t *testing.T) {
	t.Parallel()
	empty := tmpDir(t) + "/proc/self/fd"
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	base, fb := tmpDir(t), tmpDir(t)
	cfg := centralCfg(base)
	cfg.threadCwd, cfg.procSelfFD, cfg.tmpBases = false, empty, []string{fb}
	p := nest(t, tmpDir(t), 600, "s.sock")
	markUntouched(t, base)
	_, _, err := runLong(opListen, p, cfg)
	wantErrno(t, "listen", err, syscall.ENAMETOOLONG)
	l := listenAt(t, p)
	c, r := dialLong(t, p, cfg)
	if r != routeSymlink {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertUntouched(t, base)
}

// Through the real environment's inputs (configFromTmpdir), the means taken is
// the system's: the thread cwd where a thread may have a cwd of its own, else
// on Linux proc (Docker's default seccomp profile, a filter, rules
// the thread cwd out).
func TestTheRealInputsTakeTheSystemsMeans(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	p := nest(t, tmpDir(t), 600, "s.sock")
	cfg := configFromTmpdir("")
	cfg.dBases = []string{tmpDir(t)}
	l, r := listenLong(t, p, cfg)
	if r != ms[0].want {
		t.Fatalf("listen: %s, want %s", r, ms[0].want)
	}
	c, _ := dialLong(t, p, cfg)
	ping(t, l, c)
}

// The spelling the kernel keeps for a bound socket (the listener's Addr,
// getsockname) may be unlinked or stat'ed later by code that takes it for the
// path (a library cleaning up after a server, or a user's
// os.Remove(l.Addr().String())). Through the routes through D it names nothing
// anywhere: R is gone.
//
//   - With the thread cwd the spelling is R/<name>, resolved against whatever
//     cwd: a file named like the socket in the cwd (where a bare-name spelling
//     would lead) is not hit by unlink or stat.
//   - With proc it is /proc/self/fd/<D>/R/<name>: once D's fd number
//     is reused for another directory holding a file of the socket's name
//     (where /proc/self/fd/<n>/<name> would lead), unlink and stat do not hit
//     it either.
//
// The socket file at the path stays, and so does the decoy, also after the
// listener is closed (it does not unlink its address). Runs in a child
// process whose cwd is a temporary directory, to have a file in the cwd.
func TestTheSpellingTheKernelKeepsNamesNothing(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	decoy := cwd + "/s.sock"
	if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, m := range allMeans() {
		t.Run(m.name, func(t *testing.T) {
			p := nest(t, tmpDir(t), 600, "s.sock")
			l, _ := listenLong(t, p, m.cfg(tmpDir(t)))
			spelling := l.Addr().String()
			decoys := []string{decoy}
			if m.want == routeProc {
				decoys = append(decoys, reuseFDWithDecoy(t, spelling))
			}
			if _, err := os.Stat(spelling); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stat %q: %v, want it to name nothing", spelling, err)
			}
			if err := os.Remove(spelling); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unlink %q: %v, want it to name nothing", spelling, err)
			}
			l.Close()
			for _, d := range decoys {
				if b, err := os.ReadFile(d); err != nil || string(b) != "decoy" {
					t.Fatalf("the decoy %s was hit: %q, %v", d, b, err)
				}
			}
			if !isSocket(p) {
				t.Fatal("the socket file at the path is gone")
			}
		})
	}
}

// reuseFDWithDecoy takes the fd number in spelling (/proc/self/fd/<n>/...) for
// another directory holding a decoy file named like the socket, checks the
// premise that /proc/self/fd/<n>/<name> now names that decoy, and returns the
// decoy's path.
func reuseFDWithDecoy(t *testing.T, spelling string) string {
	t.Helper()
	rest := strings.TrimPrefix(spelling, "/proc/self/fd/")
	n, err := strconv.Atoi(rest[:strings.IndexByte(rest, '/')])
	if err != nil {
		t.Fatal(err)
	}
	other := tmpDir(t)
	decoy := other + "/s.sock"
	if err := os.WriteFile(decoy, []byte("decoy"), 0o600); err != nil {
		t.Fatal(err)
	}
	var extra []int
	defer func() {
		for _, fd := range extra {
			unix.Close(fd)
		}
	}()
	for range 64 {
		fd, err := unix.Open(other, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		if fd == n {
			t.Cleanup(func() { unix.Close(fd) })
			if b, err := os.ReadFile(fmt.Sprintf("/proc/self/fd/%d/s.sock", n)); err != nil || string(b) != "decoy" {
				t.Fatalf("the premise: /proc/self/fd/%d/s.sock names the decoy: %q, %v", n, b, err)
			}
			return decoy
		}
		extra = append(extra, fd)
	}
	t.Skipf("fd %d was not handed out again", n)
	return ""
}

// ----- the place for D -----

// D goes in the first place it can be made: a place that cannot be used
// (missing, a file, unless root a read-only directory) is passed over and
// the next is used, its mtime showing D was made there and removed. A place
// that is not safe (0777) or deeply nested (600 bytes) is used all the same:
// D is held by its fd, so neither its safety nor its length matters.
func TestDGoesInTheFirstPlaceItCanBeMade(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	m := ms[0]
	p := nest(t, tmpDir(t), 600, "s.sock")
	l := listenAt(t, p)
	d := tmpDir(t)
	file := d + "/file"
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := []string{d + "/missing", file}
	if os.Geteuid() != 0 {
		bad = append(bad, mkdirMode(t, d+"/readonly", 0o500))
	}
	deep := filepath.Dir(nest(t, tmpDir(t), 600, "x"))
	for _, good := range []string{tmpDir(t), mkdirMode(t, d+"/open", 0o777), deep} {
		for _, b := range bad {
			markUntouched(t, good)
			c, r := dialLong(t, p, m.cfg(b, good))
			if r != m.want {
				t.Fatalf("%s then %s: %s", b, good, r)
			}
			ping(t, l, c)
			assertTouched(t, good)
			assertEmpty(t, good)
		}
	}
	if os.Geteuid() != 0 {
		assertEmpty(t, d+"/readonly")
	}
}

// When no place given can be used, D goes in the socket's directory, named
// ".sockat.XXXXXXXXXX" (a user's directory: a leftover is recognisable as
// sockat's and stays out of sight). The route works, the directory's mtime
// shows D was made there, and nothing is left. makeD itself reports the name
// and place.
func TestWithNoPlaceGivenDGoesInTheSocketsDirectory(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	m := ms[0]
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := filepath.Dir(p)
	markUntouched(t, dir)
	l, r := listenLong(t, p, m.cfg("/nonexistent/a", "/nonexistent/b"))
	if r != m.want {
		t.Fatalf("listen: %s", r)
	}
	assertTouched(t, dir)
	assertNoSockatEntry(t, dir)
	c, _ := dialLong(t, p, m.cfg())
	ping(t, l, c)
	assertNoSockatEntry(t, dir)

	sdir := openTestDir(t, dir)
	h, err := makeD([]string{"/nonexistent"}, sdir, dir, m.want == routeProc, "/proc/self/fd", randomPart, randomPart)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\.sockat\.`+rPart+`$`).MatchString(h.name) || h.baseFD != int(sdir.Fd()) {
		t.Fatalf("D is %q in fd %d, want .sockat.* in the socket's directory (fd %d)", h.name, h.baseFD, sdir.Fd())
	}
	if !regexp.MustCompile(`^` + rPart + `$`).MatchString(h.r) {
		t.Fatalf("R is %q", h.r)
	}
	h.cleanup()
	assertNoSockatEntry(t, dir)
}

// A relative place (a relative TMPDIR) is opened once and held by its fd, so
// it serves as well as an absolute one: D is made in it, relative to the cwd.
// Runs in a child process whose cwd is a temporary directory.
func TestARelativePlaceForDIsUsed(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	if err := os.Mkdir("rel", 0o700); err != nil {
		t.Fatal(err)
	}
	markUntouched(t, "rel")
	p := nest(t, tmpDir(t), 600, "s.sock")
	l, r := listenLong(t, p, ms[0].cfg("rel"))
	if r != ms[0].want {
		t.Fatalf("listen: %s", r)
	}
	c, _ := dialLong(t, p, ms[0].cfg("rel"))
	ping(t, l, c)
	assertTouched(t, "rel")
	assertEmpty(t, "rel")
}

// When D can be made nowhere (no place given works and the socket's
// directory is not writable), a listen is ENAMETOOLONG (Go has no fork, the
// SPEC's D-less way for a bind) and a dial takes its fallback routes. Nothing
// is left in the socket's directory. Not as root, who may write anywhere.
func TestWhenDCanBeMadeNowhereAListenIsENAMETOOLONGAndADialFallsBack(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root may write in a read-only directory")
	}
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	fb := tmpDir(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := filepath.Dir(p)
	l := listenAt(t, p)
	mkdirMode(t, dir+"/x", 0o700)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	cfg := ms[0].cfg("/nonexistent")
	cfg.tmpBases = []string{fb}
	_, _, err := runLong(opListen, dir+"/o.sock", cfg)
	wantErrno(t, "listen", err, syscall.ENAMETOOLONG)
	// The first fallback: /proc/self/fd/<dirfd>/<name> on Linux, the symlink
	// under the safe place on macOS.
	want := routeSymlink
	if runtime.GOOS == "linux" {
		want = routeProcDirect
	}
	c, r := dialLong(t, p, cfg)
	if r != want {
		t.Fatalf("dial: %s, want %s", r, want)
	}
	ping(t, l, c)
	assertNoSockatEntry(t, dir)
}

// ----- a directory that is not the one made -----

// isOurPrivateDir is the check that D, opened by its name, is the directory
// just made and not one swapped in by someone who can write to the place: a
// directory, owned by the effective uid, with no permission for group and
// others (mkdir(0700) under any umask gives that; the mode is then set to
// 0700). Checked on fstat of real entries: our 0700, 0500 and 0300
// directories pass; a directory with a group or other bit (0750, 0705, 0770,
// 0755), a file of mode 0700, and (as root, chown) another user's 0700
// directory do not; nor does ours for another uid.
func TestIsOurPrivateDirTakesOnlyOurOwnPrivateDirectory(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	us := uint32(os.Geteuid())
	fstat := func(p string) *unix.Stat_t {
		t.Helper()
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			t.Fatal(err)
		}
		return &st
	}
	for _, mode := range []os.FileMode{0o700, 0o500, 0o300} {
		if !isOurPrivateDir(fstat(mkdirMode(t, fmt.Sprintf("%s/ok%o", d, mode), mode)), us) {
			t.Errorf("our directory of mode %o: want true", mode)
		}
	}
	if isOurPrivateDir(fstat(d+"/ok700"), us+1) {
		t.Error("our directory for another uid: want false")
	}
	for _, mode := range []os.FileMode{0o750, 0o705, 0o770, 0o755} {
		if isOurPrivateDir(fstat(mkdirMode(t, fmt.Sprintf("%s/m%o", d, mode), mode)), us) {
			t.Errorf("a directory of mode %o: want false", mode)
		}
	}
	if err := os.WriteFile(d+"/file", nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if isOurPrivateDir(fstat(d+"/file"), us) {
		t.Error("a file of mode 0700: want false")
	}
	if us == 0 {
		theirs := mkdirMode(t, d+"/theirs", 0o700)
		if err := os.Chown(theirs, 65534, -1); err != nil {
			t.Fatal(err)
		}
		if isOurPrivateDir(fstat(theirs), us) {
			t.Error("another user's 0700 directory: want false")
		}
	}
}

// D is made with mkdirat, which never takes an existing entry: a directory
// already at the name drawn (another user's (root only), one with group /
// other permission, one with something in it) makes the draw be taken again,
// and that directory is not used, its mode, owner and contents untouched. The
// names come from a stand-in generator so the collision is certain. With
// every name taken (16 draws), the place is given up and the next one used.
func TestDIsNeverAnExistingDirectory(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	dir := tmpDir(t)
	sdir := openTestDir(t, dir)
	var taken []string
	mk := func(name string, mode os.FileMode) {
		p := mkdirMode(t, base+"/"+linkPrefix+name, mode)
		if err := os.WriteFile(p+"/content", []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		taken = append(taken, name)
	}
	mk("AAAAAAAAAA", 0o755)
	mk("BBBBBBBBBB", 0o700)
	if os.Geteuid() == 0 {
		mk("CCCCCCCCCC", 0o700)
		if err := os.Chown(base+"/"+linkPrefix+"CCCCCCCCCC", 65534, -1); err != nil {
			t.Fatal(err)
		}
	}
	names := append(slicesClone(taken), "FFFFFFFFFF")
	calls := 0
	nextD := func() (string, error) { calls++; return names[calls-1], nil }
	proc := runtime.GOOS == "linux"
	h, err := makeD([]string{base}, sdir, dir, proc, "/proc/self/fd", nextD, randomPart)
	if err != nil {
		t.Fatal(err)
	}
	if h.name != linkPrefix+"FFFFFFFFFF" || calls != len(names) {
		t.Fatalf("D is %q after %d draws", h.name, calls)
	}
	h.cleanup()
	for _, n := range taken {
		b, err := os.ReadFile(base + "/" + linkPrefix + n + "/content")
		if err != nil || string(b) != "keep" {
			t.Fatalf("%s was touched: %q, %v", n, b, err)
		}
	}
	if exists(base + "/" + linkPrefix + "FFFFFFFFFF") {
		t.Fatal("D was not removed")
	}

	calls = 0
	h, err = makeD([]string{base}, sdir, dir, proc, "/proc/self/fd",
		func() (string, error) { calls++; return taken[0], nil }, randomPart)
	if err != nil {
		t.Fatal(err)
	}
	// 16 draws in the place, all taken; then the socket's directory, where
	// the same name (with the dot) is free at the first draw.
	if !strings.HasPrefix(h.name, "."+linkPrefix) || calls != linkAttempts+1 {
		t.Fatalf("D is %q after %d draws, want .sockat.* after %d", h.name, calls, linkAttempts+1)
	}
	h.cleanup()
	assertNoSockatEntry(t, dir)
}

func slicesClone(s []string) []string { return append([]string(nil), s...) }

// R is named like D's random part, drawn again on EEXIST up to 16 times; R
// points to the socket's directory as a real path, and is removed with D.
func TestRIsARandomSymlinkToTheRealSocketDirectory(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	dir := tmpDir(t)
	if err := os.Symlink(dir, base+"/via"); err != nil {
		t.Fatal(err)
	}
	real, err := realDir(base + "/via")
	if err != nil {
		t.Fatal(err)
	}
	sdir := openTestDir(t, dir)
	proc := runtime.GOOS == "linux"
	h, err := makeD([]string{base}, sdir, real, proc, "/proc/self/fd", randomPart, randomPart)
	if err != nil {
		t.Fatal(err)
	}
	dPath := base + "/" + h.name
	to, err := os.Readlink(dPath + "/" + h.r)
	if err != nil || to != real || strings.Contains(to, "/via") {
		t.Fatalf("R -> %q, %v; want the real path %q", to, err, real)
	}
	fi, err := os.Lstat(dPath)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("D: %v, %v; want a directory of mode 0700", fi.Mode(), err)
	}
	h.cleanup()
	if exists(dPath) {
		t.Fatal("D was not removed")
	}
}

// D's mode is set to 0700 whatever the umask: under a umask taking the
// owner's bits (0277 makes mkdir(0700) give 0500, 0477 gives 0300) D is still
// opened (for search only, or O_PATH with /proc) and the routes through D work,
// leaving nothing. A umask belongs to the whole process, hence a child.
//
// Linux without /proc under 0477 and not root, where D cannot be set to 0700,
// is TestWithoutProcAUmaskTakingTheOwnersReadBitLeavesNoD.
func TestTheRoutesThroughDWorkUnderAUmaskTakingTheOwnersBits(t *testing.T) {
	t.Parallel()
	for _, mask := range []string{"0277", "0477"} {
		t.Run(mask, func(t *testing.T) {
			runChild(t, "TestChildCentralUnderAUmask", tmpDir(t), "SOCKAT_UMASK="+mask)
		})
	}
}

func TestChildCentralUnderAUmask(t *testing.T) {
	asChild(t)
	mask, err := strconv.ParseUint(os.Getenv("SOCKAT_UMASK"), 8, 32)
	if err != nil {
		t.Fatal(err)
	}
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	base := tmpDir(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	syscall.Umask(int(mask))
	for _, m := range ms {
		q := filepath.Dir(p) + "/" + m.name + ".sock"
		l, r := listenLong(t, q, m.cfg(base))
		if r != m.want {
			t.Fatalf("%s: listen %s", m.name, r)
		}
		// The umask applies to the socket file too (0500 or 0300 here), and a
		// connect needs write permission on it: given back as for any
		// listener, so the dial's own D is what is tried under the umask.
		if err := os.Chmod(q, 0o700); err != nil {
			t.Fatal(err)
		}
		c, r := dialLong(t, q, m.cfg(base))
		if r != m.want {
			t.Fatalf("%s: dial %s", m.name, r)
		}
		ping(t, l, c)
		assertEmpty(t, base)
		assertNoSockatEntry(t, filepath.Dir(p))
	}
}

// ----- the thread cwd -----

// Thread-cwd changes the cwd of a thread of its own only: the
// caller's thread keeps the cwd it has, also when the caller gave its thread a
// cwd of its own (as SPEC warns, resetting a thread's cwd with
// pthread_fchdir_np(-1) would undo that). Checked on a goroutine locked to a
// thread whose own cwd is a chosen directory: after a listen and a dial
// through the route, (st_dev, st_ino) of "." on that thread is still that
// directory's. The thread is left locked, so the runtime discards it.
func TestThreadCwdLeavesTheCallersThreadCwdAlone(t *testing.T) {
	t.Parallel()
	if !threadCwdAvailable() {
		t.Skip("this system refuses a thread a cwd of its own")
	}
	own := tmpDir(t)
	want, _ := statID(own)
	f := openTestDir(t, own)
	p := nest(t, tmpDir(t), 600, "s.sock")
	base := tmpDir(t)
	type result struct {
		before, after fileID
		err           error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		if err := threadChdir(int(f.Fd())); err != nil {
			done <- result{err: err}
			return
		}
		before, _ := statID(".")
		l, _, err := runLong(opListen, p, centralCfg(base))
		if err == nil {
			var c endpoint
			c, _, err = runLong(opDial, p, centralCfg(base))
			if c != nil {
				c.Close()
			}
			l.Close()
		}
		after, _ := statID(".")
		done <- result{before, after, err}
	}()
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.before != want || r.after != want {
		t.Fatalf("the caller's thread cwd: %v before, %v after, want %v", r.before, r.after, want)
	}
}

// The thread a call ran on is discarded, not kept: 200 dials through thread-cwd leave the process with about as many threads
// as before (a thread kept parked per call would add 200). Counted in
// /proc/self/task on Linux, in a child process so other tests' threads do not
// count. Whether a discarded thread could be handed out again is checked by
// TestNoRouteChangesTheProcessCwd (a goroutine on it would find D, gone, as
// its cwd).
func TestThreadCwdDoesNotKeepThreads(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("threads are counted in /proc/self/task")
	}
	if !threadCwdAvailable() {
		t.Skip("this system refuses a thread a cwd of its own")
	}
	runChild(t, "TestChildThreadCount", tmpDir(t))
}

func TestChildThreadCount(t *testing.T) {
	asChild(t)
	count := func() int {
		es, err := os.ReadDir("/proc/self/task")
		if err != nil {
			t.Fatal(err)
		}
		return len(es)
	}
	p := nest(t, tmpDir(t), 600, "s.sock")
	base := tmpDir(t)
	l, _ := listenLong(t, p, centralCfg(base))
	before := count()
	for range 200 {
		c, r := dialLong(t, p, centralCfg(base))
		if r != routeThreadCwd {
			t.Fatalf("dial: %s", r)
		}
		c.Close()
		s, err := l.Accept()
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	if after := count(); after > before+16 {
		t.Fatalf("%d threads after 200 dials, %d before", after, before)
	}
}

// The socket's directory may be one this process cannot open (no read
// permission where directories are opened O_RDONLY): runLong then hands the
// routes through D no fd of it (sdir nil). The route needs none — R points to the
// directory by its real path, and D goes in the places given — so it still
// binds and connects at the path, leaving nothing; only the socket's
// directory as a place for D is not tried, and with no other place D can be
// made nowhere (errUnusable, nothing made).
func TestTheRoutesThroughDNeedNoFdOfTheSocketsDirectory(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	base := tmpDir(t)
	p := nest(t, tmpDir(t), 300, "s.sock")
	dir, name, _ := splitPath(p)
	for _, m := range ms {
		cfg := m.cfg(base)
		os.Remove(p)
		ep, r, err := centralRoute(opListen, dir, nil, name, cfg)
		if err != nil || r != m.want {
			t.Fatalf("%s: listen %s, %v", m.name, r, err)
		}
		l := ep.(*net.UnixListener)
		cep, r, err := centralRoute(opDial, dir, nil, name, cfg)
		if err != nil || r != m.want {
			t.Fatalf("%s: dial %s, %v", m.name, r, err)
		}
		ping(t, l, cep.(*net.UnixConn))
		assertEmpty(t, base)
		assertNoSockatEntry(t, dir)

		cfg.dBases = nil
		mark := dir
		markUntouched(t, mark)
		if _, _, err := centralRoute(opListen, dir, nil, "t.sock", cfg); !errors.Is(err, errUnusable) {
			t.Fatalf("%s: with no place and no fd of the socket's directory: %v, want errUnusable", m.name, err)
		}
		assertUntouched(t, mark)
	}
}

// ----- the bind's form without D -----

// When D can be made nowhere, a bind still goes through a symlink R of a
// random name: made in the socket's directory itself (.sockat.XXXXXXXXXX,
// pointing to that directory) and reached relative to the directory's fd, by
// the same means as with D (thread cwd, /proc variant). The socket is created
// at the path and works; R is removed, so the spelling the kernel keeps
// (.sockat.XXXXXXXXXX/<name>, or proc of it) names nothing later;
// the socket's directory holds nothing of sockat's afterwards.
func TestTheSockdirRoutesBindThroughRInTheSocketsDirectory(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	for _, m := range ms {
		p := nest(t, tmpDir(t), 300, "s.sock")
		dir, name, _ := splitPath(p)
		real, err := realDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		sdir, err := openDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		cfg := m.cfg()
		thread := cfg.threadCwd && threadCwdPossible()
		proc := procFDUsableFor(sdir, cfg.procSelfFD)
		ep, r, err := withoutD(opListen, sdir, real, name, thread, proc, cfg)
		sdir.Close()
		want := map[route]route{routeThreadCwd: routeThreadCwdSockdir, routeProc: routeProcSockdir}[m.want]
		if err != nil || r != want {
			t.Fatalf("%s: %s, %v, want %s", m.name, r, err, want)
		}
		l := ep.(*net.UnixListener)
		if !isSocket(p) {
			t.Fatalf("%s: no socket at the path", m.name)
		}
		spelled := l.Addr().String()
		if !regexp.MustCompile(`(^|/)\.sockat\.` + rPart + `/s\.sock$`).MatchString(spelled) {
			t.Fatalf("%s: the kernel keeps %q", m.name, spelled)
		}
		c, err := Dial(p)
		if err != nil {
			t.Fatal(err)
		}
		ping(t, l, c)
		assertNoSockatEntry(t, dir)
	}
}

// The form without D needs the socket's directory's fd and a spelling that
// fits: with no fd of it (it could not be opened), or with a file name too
// long for .sockat.XXXXXXXXXX/<name> (19 bytes more than the name), it cannot
// be used and makes nothing.
func TestTheSockdirRoutesNeedTheDirectorysFdAndASpellingThatFits(t *testing.T) {
	t.Parallel()
	ms := allMeans()
	if len(ms) == 0 {
		t.Skip("no route through D here")
	}
	m := ms[0]
	dir := tmpDir(t)
	real, err := realDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.cfg()
	markUntouched(t, dir)
	if _, _, err := withoutD(opListen, nil, real, "s.sock", true, true, cfg); !errors.Is(err, errUnusable) {
		t.Fatalf("no fd: %v, want errUnusable", err)
	}
	sdir, err := openDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sdir.Close()
	thread := cfg.threadCwd && threadCwdPossible()
	proc := procFDUsableFor(sdir, cfg.procSelfFD)
	long := strings.Repeat("n", directMax-len(noDPrefix)-rNameLen) // one byte over
	if _, _, err := withoutD(opListen, sdir, real, long, thread, proc, cfg); !errors.Is(err, errUnusable) {
		t.Fatalf("%d-byte name: %v, want errUnusable", len(long), err)
	}
	assertUntouched(t, dir)
	fits := long[1:]
	ep, _, err := withoutD(opListen, sdir, real, fits, thread, proc, cfg)
	if m.want == routeProc {
		// Proc is longer still: this name only fits the thread cwd.
		if !errors.Is(err, errUnusable) {
			t.Fatalf("%d-byte name by /proc: %v, want errUnusable", len(fits), err)
		}
		return
	}
	if err != nil {
		t.Fatalf("%d-byte name: %v", len(fits), err)
	}
	ep.Close()
	assertNoSockatEntry(t, dir)
}

// A D that cannot be opened right after it is made: a umask taking the owner's
// search bit (0177 makes mkdir(0700) give 0600) keeps an O_SEARCH open from
// working (macOS; Linux opens D O_PATH, which needs no permission on D, so
// there it is opened and used). The D just made is then looked at by its name
// and, being a directory of ours with no group / other permission, removed
// (AT_REMOVEDIR takes only an empty directory), and the next place is tried:
// every place in turn here, the socket's directory last. With D made nowhere,
// a listen takes thread-cwd-sockdir (R in the socket's directory; a symlink's
// mode is not used); a dial goes on to its fallbacks (symlink here). Nothing
// is left in any place or in the socket's directory, call after call, and
// the places were tried (their mtime moved). A umask belongs to the whole
// process, hence a child.
func TestADThatCannotBeOpenedIsRemoved(t *testing.T) {
	t.Parallel()
	if !threadCwdAvailable() {
		t.Skip("thread-cwd is refused here (the routes without it open D O_PATH)")
	}
	runChild(t, "TestChildUnopenableD", tmpDir(t))
}

func TestChildUnopenableD(t *testing.T) {
	asChild(t)
	b1, b2, safe := tmpDir(t), tmpDir(t), tmpDir(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	dir := filepath.Dir(p)
	cfg := centralCfg(b1, b2)
	cfg.tmpBases = []string{safe}
	listenWant, dialWant := routeThreadCwd, routeThreadCwd
	if runtime.GOOS == "darwin" {
		listenWant, dialWant = routeThreadCwdSockdir, routeSymlink
	}
	syscall.Umask(0o177)
	for i := range 3 {
		for _, b := range []string{b1, b2, dir} {
			markUntouched(t, b)
		}
		l, r := listenLong(t, p, cfg)
		if r != listenWant {
			t.Fatalf("listen %d: %s, want %s", i, r, listenWant)
		}
		if runtime.GOOS == "darwin" {
			assertTouched(t, b1)
			assertTouched(t, b2)
		}
		// The umask applies to the socket file too (0600): a connect needs
		// write permission, which 0600 has.
		c, r := dialLong(t, p, cfg)
		if r != dialWant {
			t.Fatalf("dial %d: %s, want %s", i, r, dialWant)
		}
		ping(t, l, c)
		l.Close()
		os.Remove(p)
		for _, b := range []string{b1, b2, safe} {
			assertEmpty(t, b)
		}
		assertNoSockatEntry(t, dir)
	}

	// What ListenAny reports for it: thread-cwd-sockdir, after thread-cwd
	// found no place for D (macOS; on Linux D is made and thread-cwd taken).
	ep, out, err := listenAny(p, cfg)
	l := must{t}.listener(ep, err)
	var names []route
	for _, sk := range out.skipped {
		names = append(names, sk.name)
	}
	wantSkipped := []route{routeStandard}
	if runtime.GOOS == "darwin" {
		wantSkipped = []route{routeStandard, routeThreadCwd}
	}
	if out.route != listenWant || !slices.Equal(out.losses, []string{lossRespelled}) || !slices.Equal(names, wantSkipped) {
		t.Fatalf("listen any: %+v, want %s after %v", out, listenWant, wantSkipped)
	}
	l.Close()
	os.Remove(p)

	// makeDIn itself: the error comes back and the place is left empty.
	if runtime.GOOS == "darwin" {
		fd, err := unix.Open(b1, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		if _, err := makeDIn(fd, linkPrefix, dir, false, "", randomPart, randomPart); err == nil {
			t.Fatal("makeDIn under umask 0177: want an error")
		}
		assertEmpty(t, b1)
	}
}
