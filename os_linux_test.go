package sockat

// Linux: the connect order with and without /proc, proc-direct, what is used
// without /proc, abstract socket names, a seccomp filter (no thread cwd), and
// the Linux list of ways.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// procCfg is the route order's inputs with the thread cwd off and the real
// /proc: of the routes through D only proc and proc-held (with no place for D
// but the socket's directory), and bases as the places for symlink-file.
func procCfg(bases ...string) *config {
	return &config{tmpBases: bases, procSelfFD: "/proc/self/fd", threadCwd: false}
}

// ways are the Linux routes, each with its own socket path and the route
// order's inputs that force it. With /proc a dial takes proc-direct for a name
// that fits it (proc-direct is first in the connect order, and proc, about 11
// bytes longer, practically never fits where it does not), so proc is a
// listen's way only:
//
//	| way          | path                          | inputs                                  | listen     | dial         |
//	|--------------|-------------------------------|-----------------------------------------|------------|--------------|
//	| standard     | directMax bytes               | (the public order)                      | standard   | standard     |
//	| thread-cwd   | 600 bytes, a 92-byte name     | D in a place (proc-direct does not fit) | (listenAt) | thread-cwd   |
//	| proc         | 600 bytes, name s.sock        | thread cwd off, D in a place            | (listenAt) | proc-direct  |
//	| proc-direct  | 600 bytes, an 85-byte name    | thread cwd off (proc does not fit)      | (listenAt) | proc-direct  |
//	| proc-held    | 600 bytes, a 97-byte name     | thread cwd off, D in an unsafe place    | (listenAt) | proc-held    |
//	| symlink-file | 600 bytes, a 97-byte name     | no means, no /proc; a safe place        | (listenAt) | symlink-file |
//	| relative     | directMax bytes below the cwd | no means, no /proc                      | (listenAt) | relative     |
func ways(t *testing.T) []way {
	t.Helper()
	d := tmpDir(t)
	dThread, dProc, dDirect := tmpDir(t), tmpDir(t), tmpDir(t)
	unsafe, safe := unsafePlace(t), tmpDir(t)
	procD := centralCfg(dProc)
	procD.threadCwd = false
	direct := procCfg()
	direct.dBases = []string{dDirect}
	held := procCfg()
	held.dBases = []string{unsafe}
	n85, n97 := strings.Repeat("p", 85), strings.Repeat("q", 97)
	return []way{
		{"standard", nest(t, d+"/direct", directMax, "s.sock"), nil, routeStandard, routeStandard},
		{"thread-cwd", nest(t, d+"/thread", 600, threadName), centralCfg(dThread), "", routeThreadCwd},
		{"proc", nest(t, d+"/procd", 600, "s.sock"), procD, "", routeProcDirect},
		{"proc-direct", nest(t, d+"/proc", 600, n85), direct, "", routeProcDirect},
		{"proc-held", nest(t, d+"/held", 600, n97), held, "", routeProcHeld},
		{"symlink-file", nest(t, d+"/symlink", 600, n97), fallbackCfg(safe), "", routeSymlinkFile},
		{"relative", belowCwdPath(t, directMax, "s.sock"), fallbackCfg(), "", routeRelative},
	}
}

// Proc-direct, the first of the connect order with /proc: a dial goes through
// /proc/self/fd/<dirfd>/<name>, however long the directory part is, without
// making anything: the place for D and the place for symlink-file are untouched.
func TestADialTakesProcDirectFirstAndMakesNothing(t *testing.T) {
	t.Parallel()
	base, dBase := tmpDir(t), tmpDir(t)
	markUntouched(t, base)
	markUntouched(t, dBase)
	cfg := procCfg(base)
	cfg.dBases = []string{dBase}
	for _, total := range []int{directMax + 1 + 85, 300, 1000} {
		p := nest(t, tmpDir(t), total, strings.Repeat("p", 85))
		l := listenAt(t, p)
		c, r := dialLong(t, p, cfg)
		if r != routeProcDirect {
			t.Fatalf("%d: dial %s", total, r)
		}
		ping(t, l, c)
	}
	assertUntouched(t, dBase)
	assertUntouched(t, base)
}

// A file name over directMax - 11 bytes (97 here) fits no R/<name> nor
// proc-direct: a dial passes over thread-cwd and proc and takes proc-held,
// whose spelling holds no file name: D is made in its place (and removed),
// and symlink-file, later in the connect order, is not reached (its safe
// place is untouched).
func TestADialOfAFileNamePastTheRoutesThroughDTakesProcHeld(t *testing.T) {
	t.Parallel()
	d, base, dBase := tmpDir(t), tmpDir(t), tmpDir(t)
	p := d + "/" + strings.Repeat("n", 97)
	cfg := centralCfg(dBase)
	cfg.tmpBases = []string{base}
	markUntouched(t, dBase)
	markUntouched(t, base)
	l := listenAt(t, p)
	c, r := dialLong(t, p, cfg)
	if r != routeProcHeld {
		t.Fatalf("dial: %s, want %s", r, routeProcHeld)
	}
	ping(t, l, c)
	assertTouched(t, dBase)
	assertEmpty(t, dBase)
	assertUntouched(t, base)
}

// Without /proc (not mounted, as in some chroots and minimal containers; both
// shapes: no /proc directory, and an empty one) a dial's routes through D
// still have the thread cwd (D's mode then set through a second open by name,
// as there is no /proc to chmod through) where the system allows it. Without
// it (here turned off), a dial takes the other routes of the connect order
// without /proc: relative for a path near the cwd, symlink under a safe place
// for one far from it. Runs in a child process whose cwd is a temporary
// directory, to have a socket near the cwd.
func TestWithoutProcTheThreadCwdOrTheFallbacksWithoutProcAreUsed(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	empty := tmpDir(t) + "/proc/self/fd"
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, none := range []struct{ name, path string }{{"no /proc", noProc}, {"empty /proc", empty}} {
		t.Run(none.name, func(t *testing.T) {
			dBase, base := tmpDir(t), tmpDir(t)
			far := nest(t, tmpDir(t), 600, "s.sock")
			if threadCwdAvailable() {
				cfg := centralCfg(dBase)
				cfg.procSelfFD = none.path
				l := listenAt(t, far)
				c, r := dialLong(t, far, cfg)
				if r != routeThreadCwd {
					t.Fatalf("dial: %s", r)
				}
				ping(t, l, c)
				assertEmpty(t, dBase)
			}
			cfg := fallbackCfg(base)
			cfg.procSelfFD = none.path
			near := belowCwdPath(t, directMax, "s.sock")
			farther := nest(t, tmpDir(t), 600, "s.sock")
			for _, c := range []struct {
				path string
				want route
			}{{near, routeRelative}, {farther, routeSymlinkFile}} {
				l := listenAt(t, c.path)
				cc, r := dialLong(t, c.path, cfg)
				if r != c.want {
					t.Fatalf("%d bytes: dial %s, want %s", len(c.path), r, c.want)
				}
				ping(t, l, cc)
			}
			assertEmpty(t, base)
		})
	}
}

// Without /proc, a missing directory is still ENOENT (from opening it, before
// any route is chosen), not anything about /proc.
func TestWithoutProcAMissingDirectoryIsENOENT(t *testing.T) {
	t.Parallel()
	p := tmpDir(t) + "/nope/" + strings.Repeat("n", 200)
	_, _, err := runDialLong(p, fallbackCfg())
	wantErrno(t, "dial", err, syscall.ENOENT)
}

// Under a seccomp filter thread-cwd is not used at all: unshare(2) is not
// even called, since a filter may answer it by killing the process (SIGSYS)
// rather than with an error. A bind of a long path then takes link-dir
// through procB (base "proc"), with the test's places and the real ones alike
// (a dial of the same path takes proc-direct, first in its order); a file
// name no link-dir spelling fits (92 bytes) is ENAMETOOLONG, thread-cwd in
// skipped for the filter. Runs only under a filter (Docker's default profile:
// PR_GET_SECCOMP is 2); without one, TestThreadCwdIsUsedWithoutASeccompFilter
// covers the other side, and
// TestUnderASeccompFilterThatKillsOnUnshareTheProcessSurvives makes a filter
// of its own.
func TestUnderASeccompFilterProcIsTaken(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode == 0 {
		t.Skip("no seccomp filter here; run under Docker's default seccomp profile")
	} else if mode != 2 {
		t.Fatalf("PR_GET_SECCOMP = %d, want 2 (a filter)", mode)
	}
	checkThreadCwdUnusable(t)
}

// checkThreadCwdUnusable checks, under a seccomp filter, that the thread cwd
// reports itself unusable, that a bind takes link-dir through procB instead
// and a dial proc-direct, and that a bind no link-dir spelling fits is
// ENAMETOOLONG with thread-cwd skipped.
func checkThreadCwdUnusable(t *testing.T) {
	t.Helper()
	if threadCwdPossible() {
		t.Fatal("threadCwdPossible under a seccomp filter")
	}
	d := tmpDir(t)
	f := openTestDir(t, d)
	for _, o := range []op{opListen, opDial} {
		if _, err := inThreadCwd(o, int(f.Fd()), "s.sock"); !errors.Is(err, errUnusable) {
			t.Fatalf("%s: %v, want errUnusable", o.sysName(), err)
		}
	}
	p := nest(t, d, 600, "s.sock")
	w, _ := testWorld(t, nil)
	l, b, err := w.listen(p, Options{}, nil)
	if err != nil || b.route != routeLinkDir || b.base != "proc" {
		t.Fatalf("listen: %s %s, %v", b.route, b.base, err)
	}
	t.Cleanup(func() { l.Close() })
	c, r := dialLong(t, p, configFromTmpdir(""))
	if r != routeProcDirect {
		t.Fatalf("dial: %s, want %s", r, routeProcDirect)
	}
	ping(t, l, c)
	tr := &trace{}
	_, _, err = w.listen(filepath.Dir(p)+"/"+threadName, Options{}, tr)
	wantErrno(t, "listen of a 92-byte name", err, syscall.ENAMETOOLONG)
	if got := namesOf(t, tr.skipped); !slices.Equal(got, []route{routeStandard, routeLinkDir, routeThreadCwd}) || tr.skipped[2].reason != "seccomp filter" {
		t.Fatalf("skipped %v", tr.skipped)
	}
}

// Without a seccomp filter (PR_GET_SECCOMP is 0: docker with
// seccomp=unconfined, or a plain host) a bind of a file name no link-dir
// spelling fits (threadName, 92 bytes) takes thread-cwd (Dangling), and a dial
// of it thread-cwd too (a name past proc-direct, the route before it in the
// connect order).
func TestThreadCwdIsUsedWithoutASeccompFilter(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode != 0 {
		t.Skipf("under a seccomp filter (PR_GET_SECCOMP = %d)", mode)
	}
	p := nest(t, tmpDir(t), 600, threadName)
	w, _ := testWorld(t, nil)
	l, b, err := w.listen(p, Options{}, nil)
	if err != nil || b.route != routeThreadCwd || b.spelling.Kind != Dangling || !isSocket(p) {
		t.Fatalf("listen: %s %v, %v", b.route, b.spelling, err)
	}
	t.Cleanup(func() { l.Close() })
	cfg := configFromTmpdir("")
	cfg.dBases = []string{tmpDir(t)}
	c, r := dialLong(t, p, cfg)
	if r != routeThreadCwd {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
}

// The reason the route looks at PR_GET_SECCOMP instead of trying unshare(2):
// a filter may kill the process for it. A child process installs such a
// filter on itself (unshare(2) answered with SECCOMP_RET_KILL_PROCESS, as
// systemd's SystemCallFilter= does by default; every other call allowed) and
// then binds past link-dir to the thread-cwd route. It must survive and get
// ENAMETOOLONG; a route that called unshare(2) would kill it (SIGSYS).
func TestUnderASeccompFilterThatKillsOnUnshareTheProcessSurvives(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildUnderAKillingSeccompFilter", tmpDir(t))
}

func TestChildUnderAKillingSeccompFilter(t *testing.T) {
	asChild(t)
	installKillOnUnshareFilter(t)
	checkThreadCwdUnusable(t)
}

// An abstract socket name (a leading NUL, or Go's '@' for it) has no place in
// the file system, so no route applies: it goes to the net package as it is,
// whatever its length, and no file appears. The net package alone decides
// what happens: a name too long for sun_path fails exactly as with the net
// package (the same error), and a NUL later in the name is not checked
// (abstract names are bytes; only a path is cut at a NUL).
func TestAbstractNamesGoToTheNetPackageAsTheyAre(t *testing.T) {
	t.Parallel()
	cwd, _ := os.Getwd()
	for i, prefix := range []string{"@", "\x00"} {
		name := fmt.Sprintf("%ssockat-test-%d-%d", prefix, os.Getpid(), i)
		l, b, err := defaultWorld.listen(name, Options{}, nil)
		if err != nil || b.route != routeStandard || b.spelling.Kind != Original {
			t.Fatalf("%q: %s %v, %v", name, b.route, b.spelling, err)
		}
		t.Cleanup(func() { l.Close() })
		ep, r, err := runDial(name)
		c := must{t}.conn(ep, err)
		if r != routeStandard {
			t.Fatalf("%q: %s", name, r)
		}
		ping(t, l, c)
		if exists(cwd+"/"+name[1:]) || exists(cwd+"/"+name) {
			t.Fatalf("%q: a file was created", name)
		}

		withNUL := name + "\x00x"
		l = must{t}.listener(Listen(withNUL))
		c = must{t}.conn(Dial(withNUL))
		ping(t, l, c)

		long := prefix + strings.Repeat("a", 200)
		_, want := net.ListenUnix("unix", &net.UnixAddr{Name: long, Net: "unix"})
		_, err = Listen(long)
		if want == nil || err == nil || err.Error() != want.Error() {
			t.Fatalf("%d bytes: %v, want the net package's %v", len(long), err, want)
		}
	}
}

// installKillOnUnshareFilter puts on this process a seccomp filter that
// answers unshare(2) with SECCOMP_RET_KILL_PROCESS (as systemd's
// SystemCallFilter= does by default) and allows every other call. Only for a
// child process: a filter cannot be taken off again.
func installKillOnUnshareFilter(t *testing.T) {
	t.Helper()
	filter := []unix.SockFilter{
		// The system call number is the first field of struct seccomp_data.
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 1, K: unix.SYS_UNSHARE},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// TSYNC puts the filter on every thread of the process, not only this one.
	if _, _, e := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog))); e != 0 {
		t.Fatal(e)
	}
	runtime.KeepAlive(filter)
	if mode := seccompMode(t); mode != 2 {
		t.Fatalf("PR_GET_SECCOMP = %d after installing the filter", mode)
	}
}

// D's check is made on what was actually opened. A directory swapped in
// between the mkdirat and the open differs in its owner (or has group / other
// bits); such a directory is stood in for by making makeDIn's own mkdirat
// produce one (root only): makeDIn runs on a thread whose filesystem uid is
// 65534 (setfsuid(2) is per thread), so D belongs to 65534 while the effective
// uid stays 0. makeDIn must not use it: errUnusable, no R created in it, and it
// is left alone (not known to be ours), as is everything in it.
func TestADOfAnotherOwnerIsNotUsed(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("only root can create a directory as another user")
	}
	parent := tmpDir(t)
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	base := mkdirMode(t, parent+"/place", 0o777)
	baseFD, err := unix.Open(base, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(baseFD)
	real := tmpDir(t)
	done := make(chan error, 1)
	go func() {
		// Never unlocked: the thread's filesystem uid is changed, so the
		// thread is discarded with the goroutine.
		runtime.LockOSThread()
		if err := unix.Setfsuid(65534); err != nil {
			done <- err
			return
		}
		_, err := makeDIn(baseFD, linkPrefix, real, true, "/proc/self/fd", randomPart, randomPart)
		done <- err
	}()
	if err := <-done; !errors.Is(err, errUnusable) {
		t.Fatalf("%v, want errUnusable", err)
	}
	es, err := os.ReadDir(base)
	if err != nil || len(es) != 1 {
		t.Fatalf("entries of the place: %v, %v", es, err)
	}
	var st unix.Stat_t
	if err := unix.Lstat(base+"/"+es[0].Name(), &st); err != nil || st.Uid != 65534 {
		t.Fatalf("the directory left: uid %d, %v", st.Uid, err)
	}
	if in, err := os.ReadDir(base + "/" + es[0].Name()); err != nil || len(in) != 0 {
		t.Fatalf("in the directory left: %v, %v (R was made in it)", in, err)
	}
}

// ----- the connect order with /proc -----

// The connect order with /proc (SPEC "connect の順"): proc-direct,
// thread-cwd, proc, proc-held, symlink-file, then ENAMETOOLONG. Each route is
// reached by making the ones before it unusable, through the whole order
// (runLong), and the routes passed over are recorded in the order's
// sequence, after standard (the path is long) and with none that is not in
// the order (relative, widened):
//
//   - a name of 85 bytes: proc-direct, which makes nothing (the place for D
//     keeps its mtime);
//   - a name past proc-direct (threadName, 92 bytes): thread-cwd where a
//     thread may have a cwd of its own; otherwise (a seccomp filter) proc is
//     too long for it as well, and proc-held takes it;
//   - a name of 95 bytes with the thread cwd off: proc-held;
//   - proc, whose spelling is about 11 bytes longer than proc-direct's, is
//     reached only where proc-direct cannot be used for another reason: the
//     socket's directory not opened (sdir nil), forced with dialFrom.
//
// Thread-cwd, proc and proc-held make D in its place and remove it. The safe
// place for symlink-file, last in the order, is never reached (its mtime is
// unchanged). Symlink and ENAMETOOLONG need D made nowhere:
// TestWhenDCanBeMadeNowhereADialPassesTheRoutesThroughDWithoutRemakingD.
func TestTheConnectOrderWithProc(t *testing.T) {
	t.Parallel()
	order := []route{routeProcDirect, routeThreadCwd, routeProc, routeProcHeld, routeSymlinkFile}
	if got := stepNames(dialSteps(true)); !slices.Equal(got, order) {
		t.Fatalf("the connect order: %v, want %v", got, order)
	}
	before := func(r route) []route {
		return append([]route{routeStandard}, order[:slices.Index(order, r)]...)
	}
	dBase, safe := tmpDir(t), tmpDir(t)
	cfgOf := func(thread bool) *config {
		return &config{dBases: []string{dBase}, tmpBases: []string{safe}, procSelfFD: "/proc/self/fd", threadCwd: thread}
	}
	past := routeThreadCwd
	if !threadCwdAvailable() {
		past = routeProcHeld
	}
	cases := []struct {
		name string
		path string
		cfg  *config
		want route
	}{
		{"an 85-byte name", nest(t, tmpDir(t), 300, strings.Repeat("p", 85)), cfgOf(true), routeProcDirect},
		{"a name past proc-direct", nest(t, tmpDir(t), 300, threadName), cfgOf(true), past},
		{"a 95-byte name, the thread cwd off", nest(t, tmpDir(t), 300, strings.Repeat("q", 95)), cfgOf(false), routeProcHeld},
	}
	markUntouched(t, safe)
	for _, c := range cases {
		l := listenAt(t, c.path)
		markUntouched(t, dBase)
		ep, r, skipped, err := dialTraced(t, c.path, c.cfg)
		if err != nil || r != c.want {
			t.Fatalf("%s: %s, %v, want %s", c.name, r, err, c.want)
		}
		if got := namesOf(t, skipped); !slices.Equal(got, before(c.want)) {
			t.Fatalf("%s: skipped %v, want %v", c.name, skipped, before(c.want))
		}
		ping(t, l, ep.(*net.UnixConn))
		if c.want == routeProcDirect {
			assertUntouched(t, dBase)
		} else {
			assertTouched(t, dBase)
			assertEmpty(t, dBase)
		}
	}

	p := nest(t, tmpDir(t), 300, "s.sock")
	l := listenAt(t, p)
	markUntouched(t, dBase)
	cfg := cfgOf(false)
	cfg.trace = &trace{}
	ep, r, err := dialFrom(t, p, nil, cfg, routeProcDirect)
	if err != nil || r != routeProc {
		t.Fatalf("no socket's directory: %s, %v, want %s", r, err, routeProc)
	}
	if got, want := namesOf(t, cfg.trace.skipped), []route{routeProcDirect, routeThreadCwd}; !slices.Equal(got, want) {
		t.Fatalf("no socket's directory: skipped %v, want %v", cfg.trace.skipped, want)
	}
	c := must{t}.conn(ep, err)
	ping(t, l, c)
	assertTouched(t, dBase)
	assertEmpty(t, dBase)
	assertUntouched(t, safe)
}

// D is made at most once in a dial, and when it can be made nowhere the
// routes that use it are passed over without making it again (SPEC "D と
// R"). D is made nowhere here: the place given is read-only (0500) and so is
// the socket's directory. The file name (95 bytes) is past proc-direct and
// proc; thread-cwd carries it, so where a thread may have a cwd of its own,
// thread-cwd is the route that tries to make D; under a seccomp filter
// proc-held is. Either way:
//
//   - thread-cwd, proc and proc-held are recorded as unusable, in that order
//     after proc-direct, proc-held for want of D;
//   - D's name is drawn once per place (2), not again for proc-held after
//     thread-cwd found no place (that would draw 4);
//   - the dial goes on to symlink-file under the safe place; with no place for
//     symlink-file either, it is ENAMETOOLONG with symlink-file recorded last.
//
// Nothing is left in either place or the socket's directory. As root, who
// may write in a read-only directory, it runs in a child that has dropped to
// nobody (unprivileged), so that it runs in the Linux container too.
func TestWhenDCanBeMadeNowhereADialPassesTheRoutesThroughDWithoutRemakingD(t *testing.T) {
	t.Parallel()
	if !unprivileged(t) {
		return
	}
	ro := mkdirMode(t, tmpDir(t)+"/ro", 0o500)
	dir := tmpDir(t)
	p := dir + "/" + strings.Repeat("n", 95)
	l := listenAt(t, p)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	safe := tmpDir(t)
	cfg := centralCfg(ro)
	cfg.tmpBases = []string{safe}
	cfg.trace = &trace{}
	real, err := realDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, name, _ := splitPath(p)
	d := newDialer(p, dir, real, name, openTestDir(t, dir), cfg)
	draws := 0
	d.nextD = func() (string, error) { draws++; return randomPart() }
	ep, r, err := d.run(dialSteps(d.procUsable))
	d.close()
	c := must{t}.conn(ep, err)
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s, want %s", r, routeSymlinkFile)
	}
	want := []route{routeProcDirect, routeThreadCwd, routeProc, routeProcHeld}
	if got := namesOf(t, cfg.trace.skipped); !slices.Equal(got, want) {
		t.Fatalf("skipped %v, want %v", cfg.trace.skipped, want)
	}
	if held := cfg.trace.skipped[3]; held.reason != "no place for D" {
		t.Fatalf("proc-held skipped for %q, want for want of D", held.reason)
	}
	if draws != 2 {
		t.Fatalf("D's name drawn %d times, want 2 (once per place)", draws)
	}
	ping(t, l, c)

	none := centralCfg(ro)
	_, _, skipped, err := dialTraced(t, p, none)
	wantErrno(t, "dial with no place for symlink", err, syscall.ENAMETOOLONG)
	if got, want := namesOf(t, skipped), append(append([]route{routeStandard}, want...), routeSymlinkFile); !slices.Equal(got, want) {
		t.Fatalf("no place for symlink: skipped %v, want %v", skipped, want)
	}
	assertEmpty(t, ro)
	assertEmpty(t, safe)
	assertNoSockatEntry(t, dir)
}

// D made by one route is shared by the next: proc makes D (its spelling fits
// when estimated with a one-digit fd, before D is open), finds that the real
// spelling /proc/self/fd/<D>/R/<name> does not fit (D's fd has two digits),
// and proc-held uses that same D rather than making another: D's name is
// drawn once, and the dial arrives through proc-held. The socket's directory
// is not opened (sdir nil), so proc-direct cannot be used, and the thread cwd
// is off; the file name is 80 bytes (/proc/self/fd/<n>/ plus R/ plus 80 is
// 107 with a one-digit fd, 108 with two). D's place is empty afterwards and
// the safe place for symlink-file untouched. Runs in a child process, which first
// takes fds 0 to 9 so that D's fd has two digits.
func TestDIsMadeOnceAndSharedByProcAndProcHeld(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildDSharedByProcAndProcHeld", tmpDir(t))
}

func TestChildDSharedByProcAndProcHeld(t *testing.T) {
	asChild(t)
	for {
		fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		if fd >= 10 {
			unix.Close(fd)
			break
		}
	}
	place, safe := tmpDir(t), tmpDir(t)
	p := nest(t, tmpDir(t), 300, strings.Repeat("n", 80))
	dir := filepath.Dir(p)
	l := listenAt(t, p)
	markUntouched(t, place)
	markUntouched(t, safe)
	cfg := heldCfg(place, safe)
	cfg.trace = &trace{}
	real, err := realDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, name, _ := splitPath(p)
	d := newDialer(p, dir, real, name, nil, cfg)
	draws := 0
	d.nextD = func() (string, error) { draws++; return randomPart() }
	ep, r, err := d.run(dialSteps(d.procUsable))
	d.close()
	c := must{t}.conn(ep, err)
	want := []route{routeProcDirect, routeThreadCwd, routeProc}
	if got := namesOf(t, cfg.trace.skipped); r != routeProcHeld || !slices.Equal(got, want) {
		t.Fatalf("dial: %s after %v, want %s after %v", r, cfg.trace.skipped, routeProcHeld, want)
	}
	if draws != 1 {
		t.Fatalf("D's name drawn %d times, want 1 (D shared)", draws)
	}
	ping(t, l, c)
	assertTouched(t, place)
	assertEmpty(t, place)
	assertUntouched(t, safe)
}
