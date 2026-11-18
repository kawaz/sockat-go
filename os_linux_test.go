package sockat

// Linux: the /proc form of the central route and the /proc fallback of a
// dial, what is used without /proc, abstract socket names, a seccomp filter
// (no thread cwd), and the Linux list of ways.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// procCfg is the route order's inputs with the thread cwd off and the real
// /proc: the central route only in its /proc form (with no place for D but
// the socket's directory), and bases as the places for a dial's symlink.
func procCfg(bases ...string) *config {
	return &config{tmpBases: bases, procSelfFD: "/proc/self/fd", threadCwd: false}
}

// ways are the Linux routes, each with its own socket path and the route
// order's inputs that force it:
//
//	| way        | path                          | inputs                              | listen     | dial         |
//	|------------|-------------------------------|-------------------------------------|------------|--------------|
//	| direct     | directMax bytes               | (the public order)                  | direct     | direct       |
//	| thread-cwd | 600 bytes, name s.sock        | D in a place                        | thread-cwd | thread-cwd   |
//	| proc-d     | 600 bytes, name s.sock        | thread cwd off, D in a place        | proc-d     | proc-d       |
//	| proc       | 600 bytes, an 85-byte name    | thread cwd off (the /proc form of the central route does not fit) | (listenAt) | proc |
//	| held-link  | 600 bytes, a 97-byte name     | past the central route; an unsafe place | (listenAt) | held-link |
//	| symlink    | 600 bytes, a 97-byte name     | past the central route; a safe place | (listenAt) | symlink-file |
//	| relative   | directMax bytes below the cwd | no means, no /proc                  | (listenAt) | relative     |
func ways(t *testing.T) []way {
	t.Helper()
	d := tmpDir(t)
	dThread, dProc, dFallback := tmpDir(t), tmpDir(t), tmpDir(t)
	unsafe, safe := unsafePlace(t), tmpDir(t)
	procD := centralCfg(dProc)
	procD.threadCwd = false
	proc := procCfg()
	proc.dBases = []string{dFallback}
	n85, n97 := strings.Repeat("p", 85), strings.Repeat("q", 97)
	return []way{
		{"direct", nest(t, d+"/direct", directMax, "s.sock"), nil, routeDirect, routeDirect},
		{"thread-cwd", nest(t, d+"/thread", 600, "s.sock"), centralCfg(dThread), routeThreadCwd, routeThreadCwd},
		{"proc-d", nest(t, d+"/procd", 600, "s.sock"), procD, routeProcD, routeProcD},
		{"proc", nest(t, d+"/proc", 600, n85), proc, "", routeProc},
		{"held-link", nest(t, d+"/held", 600, n97), procCfg(unsafe), "", routeHeldLink},
		{"symlink", nest(t, d+"/symlink", 600, n97), procCfg(safe), "", routeSymlinkFile},
		{"relative", belowCwdPath(t, directMax, "s.sock"), fallbackCfg(), "", routeRelative},
	}
}

// The /proc fallback of a dial: when the central route cannot be used (here
// the thread cwd is off and an 85-byte name is too long for its /proc form,
// which holds R/ too), a dial goes through /proc/self/fd/<dirfd>/<name>,
// however long the directory part is. A listen of the same path is
// ENAMETOOLONG: it has no fallback. D was made in its place and removed
// (the length of its /proc form depends on D's fd, known only once D is
// open), and the place for a symlink is untouched.
func TestADialFallsBackToProcWhenTheCentralRouteCannotBeUsed(t *testing.T) {
	t.Parallel()
	base, dBase := tmpDir(t), tmpDir(t)
	markUntouched(t, base)
	cfg := procCfg(base)
	cfg.dBases = []string{dBase}
	for _, total := range []int{directMax + 1 + 85, 300, 1000} {
		p := nest(t, tmpDir(t), total, strings.Repeat("p", 85))
		_, _, err := runLong(opListen, p, cfg)
		wantErrno(t, fmt.Sprintf("%d: listen", total), err, syscall.ENAMETOOLONG)
		l := listenAt(t, p)
		c, r := dialLong(t, p, cfg)
		if r != routeProc {
			t.Fatalf("%d: dial %s", total, r)
		}
		ping(t, l, c)
		assertEmpty(t, dBase)
	}
	assertUntouched(t, base)
}

// A file name over directMax - 11 bytes (97 here) fits no form of the central
// route, which is told before anything is made (the place for D keeps its
// mtime): a listen is ENAMETOOLONG; a dial, its name too long for
// /proc/self/fd/<n>/ too, goes through the symlink under a safe place.
func TestAFileNamePastTheCentralRouteIsListenedNowhereAndDialedThroughTheSymlink(t *testing.T) {
	t.Parallel()
	d, base, dBase := tmpDir(t), tmpDir(t), tmpDir(t)
	p := d + "/" + strings.Repeat("n", 97)
	cfg := centralCfg(dBase)
	cfg.tmpBases = []string{base}
	markUntouched(t, dBase)
	_, _, err := runLong(opListen, p, cfg)
	wantErrno(t, "listen", err, syscall.ENAMETOOLONG)
	l := listenAt(t, p)
	c, r := dialLong(t, p, cfg)
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertEmpty(t, base)
	assertUntouched(t, dBase)
}

// Without /proc (not mounted, as in some chroots and minimal containers; both
// shapes: no /proc directory, and an empty one) the central route still has
// the thread cwd (D then opened O_RDONLY, as no /proc means no O_PATH trick)
// where the system allows it. Without it (here turned off), a listen of a
// long path is ENAMETOOLONG and a dial takes the fallbacks of a system without
// /proc: the relative spelling for a path near the cwd, the symlink under a
// safe place for one far from it. Runs in a child process whose cwd is a
// temporary directory, to have a socket near the cwd.
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
				l, r := listenLong(t, far, cfg)
				if r != routeThreadCwd {
					t.Fatalf("listen: %s", r)
				}
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
				_, _, err := runLong(opListen, c.path, cfg)
				wantErrno(t, "listen", err, syscall.ENAMETOOLONG)
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
	for _, o := range []op{opListen, opDial} {
		_, _, err := runLong(o, p, fallbackCfg())
		wantErrno(t, o.sysName(), err, syscall.ENOENT)
	}
}

// Under a seccomp filter the thread-cwd means is not used at all: unshare(2)
// is not even called, since a filter may answer it by killing the process
// (SIGSYS) rather than with an error. With /proc the central route then takes
// its /proc form, through the real inputs as well; without /proc it has no
// means, so a listen of a long path is ENAMETOOLONG (nothing made in the place
// for D) and a dial with no fallback left is too. Runs only under a filter
// (Docker's default profile: PR_GET_SECCOMP is 2); without one,
// TestTheThreadCwdMeansIsUsedWithoutASeccompFilter covers the other side, and
// TestUnderASeccompFilterThatKillsOnUnshareTheProcessSurvives makes a filter
// of its own.
func TestUnderASeccompFilterTheCentralRouteTakesTheProcForm(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode == 0 {
		t.Skip("no seccomp filter here; run under Docker's default seccomp profile")
	} else if mode != 2 {
		t.Fatalf("PR_GET_SECCOMP = %d, want 2 (a filter)", mode)
	}
	checkThreadCwdUnusable(t)
}

// checkThreadCwdUnusable checks, under a seccomp filter, that the thread-cwd
// means reports itself unusable, that the central route takes its /proc form
// instead (with the test's inputs and with the real ones), and that without
// /proc a long path is ENAMETOOLONG with nothing made.
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
	dBase := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	real := configFromTmpdir("")
	real.dBases = []string{dBase}
	for i, cfg := range []*config{centralCfg(dBase), real} {
		q := fmt.Sprintf("%s/%d.sock", filepath.Dir(p), i)
		l, r := listenLong(t, q, cfg)
		if r != routeProcD {
			t.Fatalf("listen: %s, want %s", r, routeProcD)
		}
		c, r := dialLong(t, q, cfg)
		if r != routeProcD {
			t.Fatalf("dial: %s, want %s", r, routeProcD)
		}
		ping(t, l, c)
		assertEmpty(t, dBase)
	}
	none := centralCfg(dBase)
	none.procSelfFD = noProc
	markUntouched(t, dBase)
	for _, o := range []op{opListen, opDial} {
		_, _, err := runLong(o, filepath.Dir(p)+"/n.sock", none)
		wantErrno(t, o.sysName()+" without /proc", err, syscall.ENAMETOOLONG)
		_, _, err = runLong(o, filepath.Dir(p)+"/nope/s.sock", none)
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
	assertUntouched(t, dBase)
}

// Without a seccomp filter (PR_GET_SECCOMP is 0: docker with
// seccomp=unconfined, or a plain host) the central route takes the thread-cwd
// means, through the real inputs too.
func TestTheThreadCwdMeansIsUsedWithoutASeccompFilter(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode != 0 {
		t.Skipf("under a seccomp filter (PR_GET_SECCOMP = %d)", mode)
	}
	p := nest(t, tmpDir(t), 600, "s.sock")
	cfg := configFromTmpdir("")
	cfg.dBases = []string{tmpDir(t)}
	l, r := listenLong(t, p, cfg)
	if r != routeThreadCwd || !isSocket(p) {
		t.Fatalf("listen: %s", r)
	}
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
// then runs the route order to the thread-cwd route. It must survive and get
// the /proc form of the central route; a route that called unshare(2) would
// kill it (SIGSYS).
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
		ep, r, err := run(opListen, name)
		l := must{t}.listener(ep, err)
		if r != routeDirect {
			t.Fatalf("%q: %s", name, r)
		}
		ep, r, err = run(opDial, name)
		c := must{t}.conn(ep, err)
		if r != routeDirect {
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

// Without /proc, D is opened O_PATH (no read permission needed) and checked
// first; its mode is then set by opening it again by its name O_RDONLY. Under
// a umask of 0477, mkdir(0700) gives 0300, and that second open fails for a
// non-root user (EACCES): D has passed the check and so is known to be ours,
// and it is removed rather than left behind. The central route then has no
// place for D: a listen is ENAMETOOLONG and nothing is left in the place or in
// the socket's directory, however many times it is called. As root, the
// second open succeeds and the route is used, also leaving nothing. A umask
// belongs to the whole process, hence a child.
func TestWithoutProcAUmaskTakingTheOwnersReadBitLeavesNoD(t *testing.T) {
	t.Parallel()
	if !threadCwdAvailable() {
		t.Skip("the thread cwd cannot be used here (the only means without /proc)")
	}
	runChild(t, "TestChildWithoutProcUnderUmask0477", tmpDir(t))
}

func TestChildWithoutProcUnderUmask0477(t *testing.T) {
	asChild(t)
	base := tmpDir(t)
	dir := tmpDir(t)
	p := nest(t, dir, 300, "s.sock")
	cfg := &config{dBases: []string{base}, procSelfFD: "/nonexistent/proc/self/fd", threadCwd: true}
	syscall.Umask(0o477)
	for i := range 3 {
		ep, r, err := runLong(opListen, p, cfg)
		if os.Geteuid() == 0 {
			if err != nil || r != routeThreadCwd {
				t.Fatalf("as root, listen %d: %s, %v", i, r, err)
			}
			ep.Close()
			os.Remove(p)
		} else if !errors.Is(err, syscall.ENAMETOOLONG) {
			t.Fatalf("listen %d: %s, %v, want ENAMETOOLONG", i, r, err)
		}
		assertEmpty(t, base)
		assertNoSockatEntry(t, filepath.Dir(p))
	}
}
