package sockat

// Linux: the connect order with and without /proc, proc-direct, abstract
// socket names, a seccomp filter (no thread cwd), D of another owner, and
// the Linux list of ways.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// noProcWorld is a test world whose /proc is said to be at a path that does
// not exist, standing in for a system without /proc (some chroots and
// minimal containers).
func noProcWorld(t *testing.T, threadCwd bool) (*world, testPlaces) {
	t.Helper()
	return testWorld(t, func(c *config, _ testPlaces) {
		c.proc = "/nonexistent/proc"
		c.threadCwd = threadCwd
	})
}

// ways are the Linux connect routes, each with its own socket path and the
// test world that forces it. With /proc a dial takes proc-direct for a name
// that fits it; a 100-byte name (directName) is past it:
//
//	| way               | path                          | world                    | dial              |
//	|-------------------|-------------------------------|--------------------------|-------------------|
//	| standard          | directMax bytes               | (the public order)       | standard          |
//	| proc-direct       | 600 bytes, an 85-byte name    | as is                    | proc-direct       |
//	| thread-cwd-direct | 600 bytes, directName         | as is                    | thread-cwd-direct |
//	| link-file         | 600 bytes, directName         | thread cwd off           | link-file         |
//	| relative          | directMax bytes below the cwd | no /proc, thread cwd off | relative          |
//	| link-file-no-proc | 600 bytes, directName         | no /proc, thread cwd off | link-file         |
func ways(t *testing.T) []way {
	t.Helper()
	d := tmpDir(t)
	asIs, psAsIs := testWorld(t, nil)
	threadOff, psOff := testWorld(t, func(c *config, _ testPlaces) { c.threadCwd = false })
	noProc, psNoProc := noProcWorld(t, false)
	noProc2, psNoProc2 := noProcWorld(t, false)
	return []way{
		{"standard", nest(t, d+"/direct", directMax, "s.sock"), nil, testPlaces{}, routeStandard, routeStandard},
		{"proc-direct", nest(t, d+"/proc", 600, strings.Repeat("p", 85)), asIs, psAsIs, "", routeProcDirect},
		{"thread-cwd-direct", nest(t, d+"/thread", 600, directName), asIs, psAsIs, "", routeThreadCwdDirect},
		{"link-file", nest(t, d+"/link", 600, directName), threadOff, psOff, "", routeLinkFile},
		{"relative", belowCwdPath(t, directMax, "s.sock"), noProc, psNoProc, "", routeRelative},
		{"link-file-no-proc", nest(t, d+"/linknp", 600, directName), noProc2, psNoProc2, "", routeLinkFile},
	}
}

// Proc-direct, the first of the connect order with /proc: a dial goes through
// /proc/self/fd/<dirfd>/<name>, however long the directory part is, without
// making anything: the places are untouched (no B is made for a connect that
// does not reach link-file).
func TestADialTakesProcDirectFirstAndMakesNothing(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	for _, d := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		markUntouched(t, d)
	}
	for _, total := range []int{directMax + 1 + 85, 300, 1000} {
		p := nest(t, tmpDir(t), total, strings.Repeat("p", 85))
		l := listenAt(t, p)
		c, r, sk, err := dialTraced(t, w, p, Options{})
		if err != nil || r != routeProcDirect || !slices.Equal(namesOf(t, sk), []route{routeStandard}) {
			t.Fatalf("%d: dial %s after %v, %v", total, r, sk, err)
		}
		ping(t, l, c)
	}
	for _, d := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertUntouched(t, d)
	}
	if len(w.bases()) != 0 {
		t.Fatal("a B was made")
	}
}

// Which row of the connect order a dial takes is decided once, by whether
// /proc can be used (SPEC "connect の順"): with it, proc-direct and no
// relative; without it (no /proc directory, or an empty one), relative and no
// proc-direct, which is then not even skipped (hub's answer 4: skipped holds
// only the routes of the call's row). A 100-byte name far from the cwd passes
// every route before link-file with the thread cwd off; in skipped, after
// standard, come exactly the row's routes before it. Runs in a child whose
// cwd is a temporary directory, so relative does not fit.
func TestTheConnectRowIsDecidedByWhetherProcCanBeUsed(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	empty := tmpDir(t) + "/proc"
	if err := os.MkdirAll(empty+"/self/fd", 0o700); err != nil {
		t.Fatal(err)
	}
	p := nest(t, tmpDir(t), 600, directName)
	l := listenAt(t, p)
	for _, c := range []struct {
		name string
		proc string
		want []route
	}{
		{"with /proc", "/proc", []route{routeStandard, routeProcDirect, routeThreadCwdDirect}},
		{"no /proc", "/nonexistent/proc", []route{routeStandard, routeRelative, routeThreadCwdDirect}},
		{"an empty /proc", empty, []route{routeStandard, routeRelative, routeThreadCwdDirect}},
	} {
		w, _ := testWorld(t, func(cfg *config, _ testPlaces) { cfg.proc, cfg.threadCwd = c.proc, false })
		conn, r, sk, err := dialTraced(t, w, p, Options{})
		if err != nil || r != routeLinkFile || !slices.Equal(namesOf(t, sk), c.want) {
			t.Fatalf("%s: %s after %v, %v; want link-file after %v", c.name, r, sk, err, c.want)
		}
		ping(t, l, conn)
	}
	if got, want := stepNames(dialSteps(true)), []route{routeProcDirect, routeThreadCwdDirect, routeLinkFile}; !slices.Equal(got, want) {
		t.Fatalf("the order with /proc: %v", got)
	}
	if got, want := stepNames(dialSteps(false)), []route{routeRelative, routeThreadCwdDirect, routeLinkFile}; !slices.Equal(got, want) {
		t.Fatalf("the order without /proc: %v", got)
	}
}

// /proc shows a directory only where <proc>/self/fd/<fd> is the directory the
// fd holds (the same (st_dev, st_ino)), procShows: a /proc whose entry names another
// directory (a PID namespace and a /proc mounted that disagree, stood in for
// by a fake /proc) is not used, one whose entry is the directory is, and a
// /proc with no such entry is not.
func TestProcIsUsableOnlyWhereItsFdEntryIsTheDirectory(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	f := openTestDir(t, d)
	fake := tmpDir(t)
	fdDir := fake + "/self/fd"
	if err := os.MkdirAll(fdDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := fdDir + "/" + strconv.Itoa(int(f.Fd()))
	if procShows(fdDir, int(f.Fd())) {
		t.Fatal("usable with no entry for the fd")
	}
	if err := os.Mkdir(entry, 0o700); err != nil {
		t.Fatal(err)
	}
	if procShows(fdDir, int(f.Fd())) {
		t.Fatal("usable with an entry that is another directory")
	}
	os.Remove(entry)
	if err := os.Symlink(d, entry); err != nil {
		t.Fatal(err)
	}
	if !procShows(fdDir, int(f.Fd())) {
		t.Fatal("not usable with an entry that is the directory")
	}
	if procHere() && (!procShows("/proc/self/fd", int(f.Fd())) || !procUsable("/proc")) {
		t.Fatal("the real /proc not usable")
	}
}

// The checks of /proc are three (SPEC "用語", `/proc` を使える): the
// connect's row is chosen by / opened for the check, and proc-direct checks
// the fd of the call's socket directory on its own. A /proc whose
// self/fd entries all name / (a fake one: the entry of the / opened for the
// row matches, the one of the socket's directory does not) gives the row of
// proc-direct, in which proc-direct is unusable for the directory: skipped
// is standard, proc-direct and thread-cwd-direct (off), not relative (the
// other row's), and the dial goes on to link-file. Run in a child whose cwd is a
// temporary directory, so that its fds are few (the fake entries cover 0 to
// 1023) and relative would not fit.
func TestTheConnectRowIsChosenByRootAndProcDirectChecksTheSocketsDirectory(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	fake := tmpDir(t)
	fds := fake + "/self/fd"
	if err := os.MkdirAll(fds, 0o700); err != nil {
		t.Fatal(err)
	}
	for n := range 1024 {
		if err := os.Symlink("/", fds+"/"+strconv.Itoa(n)); err != nil {
			t.Fatal(err)
		}
	}
	p := nest(t, tmpDir(t), 600, directName)
	l := listenAt(t, p)
	w, _ := testWorld(t, func(c *config, _ testPlaces) { c.proc, c.threadCwd = fake, false })
	conn, r, sk, err := dialTraced(t, w, p, Options{})
	if err != nil || r != routeLinkFile || !slices.Equal(namesOf(t, sk), []route{routeStandard, routeProcDirect, routeThreadCwdDirect}) {
		t.Fatalf("%s after %v, %v; want link-file after standard, proc-direct, thread-cwd-direct", r, sk, err)
	}
	ping(t, l, conn)
}

// The estimate of procB before B is made (SPEC-routes "綴りの形") checks /proc
// as procB itself does, by the (st_dev, st_ino) of an fd's entry under
// /proc/<pid>/fd, with a directory opened for the check: a /proc whose
// /proc/<pid>/fd is there but has no entry that is the directory (a PID
// namespace and a /proc that disagree, stood in for by a fake one) does not
// count as a procB that fits. So where only procB would fit (the place's
// real path too long for link-dir), no B is made in the place (SPEC-routes
// "実体の置き場": none where the spelling is known not to fit), and the bind
// goes on past link-dir (to thread-cwd, off here, as it makes a B of its own
// wherever its spelling, relative to D, fits).
func TestTheEstimateOfProcBChecksProcAsProcBDoes(t *testing.T) {
	t.Parallel()
	fake := tmpDir(t)
	if err := os.MkdirAll(fmt.Sprintf("%s/%d/fd", fake, os.Getpid()), 0o700); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Dir(nest(t, tmpDir(t), 100, "x"))
	w, _ := testWorld(t, func(c *config, _ testPlaces) {
		c.proc, c.tmp, c.threadCwd = fake, deep, false
		c.getenv = func(string) string { return "" }
	})
	markUntouched(t, deep)
	tr := &trace{}
	l, _, err := w.listen(nest(t, tmpDir(t), 300, "s.sock"), tr)
	if err == nil {
		t.Cleanup(func() { l.Close() })
	}
	if got := namesOf(t, tr.skipped); len(got) < 2 || got[1] != routeLinkDir {
		t.Fatalf("skipped %v, %v; want link-dir passed over", tr.skipped, err)
	}
	assertUntouched(t, deep)
}

// Without /proc, a missing directory is still ENOENT (from looking it up,
// before any route is chosen), with no route and nothing skipped.
func TestWithoutProcAMissingDirectoryIsENOENT(t *testing.T) {
	t.Parallel()
	w, _ := noProcWorld(t, true)
	p := tmpDir(t) + "/nope/" + strings.Repeat("n", 200)
	_, r, sk, err := dialTraced(t, w, p, Options{})
	wantErrno(t, "dial", err, syscall.ENOENT)
	if r != "" || len(sk) != 0 {
		t.Fatalf("route %q, skipped %v", r, sk)
	}
}

// Under a seccomp filter thread-cwd is not used at all: unshare(2) is not
// even called, since a filter may answer it by killing the process (SIGSYS)
// rather than with an error. A bind of a long path then takes link-dir
// through procB (base "proc"), and a dial of the same path proc-direct, first
// in its order; a file name no link-dir spelling fits (92 bytes) is
// ENAMETOOLONG for a bind, thread-cwd in skipped for the filter, and a file
// name past proc-direct (100 bytes) is dialed by link-file, thread-cwd-direct
// in skipped for the filter. Runs only under a filter (Docker's default profile:
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
// and a dial proc-direct, that a bind no link-dir spelling fits is
// ENAMETOOLONG with thread-cwd skipped, and that a dial past proc-direct
// passes thread-cwd-direct over for link-file.
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
	l, b, err := w.listen(p, nil)
	if err != nil || b.route != routeLinkDir || b.base != "proc" {
		t.Fatalf("listen: %s %s, %v", b.route, b.base, err)
	}
	t.Cleanup(func() { l.Close() })
	c, r := dialIn(t, w, p)
	if r != routeProcDirect {
		t.Fatalf("dial: %s, want %s", r, routeProcDirect)
	}
	ping(t, l, c)
	tr := &trace{}
	_, _, err = w.listen(filepath.Dir(p)+"/"+threadName, tr)
	wantErrno(t, "listen of a 92-byte name", err, syscall.ENAMETOOLONG)
	if got := namesOf(t, tr.skipped); !slices.Equal(got, []route{routeStandard, routeLinkDir, routeThreadCwd}) || tr.skipped[2].reason != "seccomp filter" {
		t.Fatalf("skipped %v", tr.skipped)
	}
	q := filepath.Dir(p) + "/" + directName
	lq := listenAt(t, q)
	cq, r, sk, err := dialTraced(t, w, q, Options{})
	if err != nil || r != routeLinkFile {
		t.Fatalf("dial of a 100-byte name: %s, %v", r, err)
	}
	if got := namesOf(t, sk); !slices.Equal(got, []route{routeStandard, routeProcDirect, routeThreadCwdDirect}) || sk[2].reason != "seccomp filter" {
		t.Fatalf("dial of a 100-byte name: skipped %v", sk)
	}
	ping(t, lq, cq)
}

// Without a seccomp filter (PR_GET_SECCOMP is 0: docker with
// seccomp=unconfined, or a plain host) a bind of a file name no link-dir
// spelling fits (threadName, 92 bytes) takes thread-cwd (Dangling), and a dial
// of it thread-cwd-direct (a name past proc-direct, the route before it in
// the connect order), which makes nothing in the places.
func TestThreadCwdIsUsedWithoutASeccompFilter(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode != 0 {
		t.Skipf("under a seccomp filter (PR_GET_SECCOMP = %d)", mode)
	}
	p := nest(t, tmpDir(t), 600, threadName)
	w, _ := testWorld(t, nil)
	l, b, err := w.listen(p, nil)
	if err != nil || b.route != routeThreadCwd || b.spelling.Kind != Dangling || !isSocket(p) {
		t.Fatalf("listen: %s %v, %v", b.route, b.spelling, err)
	}
	t.Cleanup(func() { l.Close() })
	w2, ps := testWorld(t, nil)
	markUntouched(t, ps.tmp)
	c, r := dialIn(t, w2, p)
	if r != routeThreadCwdDirect {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertUntouched(t, ps.tmp)
}

// The reason the route looks at PR_GET_SECCOMP instead of trying unshare(2):
// a filter may kill the process for it. A child process installs such a
// filter on itself (unshare(2) answered with SECCOMP_RET_KILL_PROCESS, as
// systemd's SystemCallFilter= does by default; every other call allowed) and
// then binds past link-dir to the thread-cwd route and dials past
// proc-direct to thread-cwd-direct. It must survive (the bind ENAMETOOLONG,
// the dial through link-file); a route that called unshare(2) would kill it
// (SIGSYS).
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
		l, b, err := defaultWorld.listen(name, nil)
		if err != nil || b.route != routeStandard || b.spelling.Kind != Original {
			t.Fatalf("%q: %s %v, %v", name, b.route, b.spelling, err)
		}
		t.Cleanup(func() { l.Close() })
		c, r, err := defaultWorld.dial(name, Options{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
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

// A D is used only once it is checked to be what was made (SPEC-routes
// "D と E" step 2): a directory of ours. A directory swapped in between the
// mkdirat and the open differs in its owner; such a directory is stood in
// for by making makeOwnD's own mkdirat produce one (root only): makeOwnD runs
// on a thread whose filesystem uid is 65534 (setfsuid(2) is per thread), so D
// belongs to 65534 while the effective uid stays 0. makeOwnD must not use it
// (errNotOurs, no lock taken on it), and it is left alone (not known to be
// ours), as is everything in it.
func TestADOfAnotherOwnerIsNotUsed(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("only root can create a directory as another user")
	}
	parent := tmpDir(t)
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	b := parent + "/b"
	if err := os.Mkdir(b, 0o777); err != nil {
		t.Fatal(err)
	}
	os.Chmod(b, 0o777)
	bfd, err := unix.Open(b, dirOpenFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(bfd)
	done := make(chan error, 1)
	go func() {
		// Never unlocked: the thread's filesystem uid is changed, so the
		// thread is discarded with the goroutine.
		runtime.LockOSThread()
		if err := unix.Setfsuid(65534); err != nil {
			done <- err
			return
		}
		_, err := makeOwnD(bfd, randomChars)
		done <- err
	}()
	if err := <-done; !errors.Is(err, errNotOurs) {
		t.Fatalf("%v, want errNotOurs", err)
	}
	es, err := os.ReadDir(b)
	if err != nil || len(es) != 1 {
		t.Fatalf("entries of B: %v, %v", es, err)
	}
	var st unix.Stat_t
	if err := unix.Lstat(b+"/"+es[0].Name(), &st); err != nil || st.Uid != 65534 {
		t.Fatalf("the directory left: uid %d, %v", st.Uid, err)
	}
	if in, err := os.ReadDir(b + "/" + es[0].Name()); err != nil || len(in) != 0 {
		t.Fatalf("in the directory left: %v, %v", in, err)
	}
}
