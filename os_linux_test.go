package sockat

// Linux: the /proc route, the POSIX order when /proc cannot be used, abstract
// socket names, a seccomp filter refusing unshare(2), and the Linux list of
// ways.

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

// procCfg is the route order's inputs with the real /proc and bases as the
// places for the symlink.
func procCfg(bases ...string) *config {
	c := posixCfg(bases...)
	c.procSelfFD = "/proc/self/fd"
	return c
}

// ways are the five Linux routes, each with its own socket path and the route
// order's inputs that force it:
//
//	| way        | path                          | inputs                    | listen / dial |
//	|------------|-------------------------------|---------------------------|---------------|
//	| direct     | directMax bytes               | (the public order)        | direct        |
//	| proc       | 300 bytes under /tmp          | /proc, no place           | proc          |
//	| relative   | directMax bytes below the cwd | no /proc, no place        | relative      |
//	| symlink    | 600 bytes under /tmp          | no /proc, a short place   | symlink-dir / symlink-file |
//	| thread-cwd | 600 bytes under /tmp          | no /proc, no place        | thread-cwd    |
func ways(t *testing.T) []way {
	t.Helper()
	d, short := tmpDir(t), tmpDir(t)
	rel := belowCwdPath(t, directMax, "s.sock")
	return []way{
		{"direct", nest(t, d+"/direct", directMax, "s.sock"), nil, routeDirect, routeDirect},
		{"proc", nest(t, d+"/proc", 300, "s.sock"), procCfg(), routeProc, routeProc},
		{"relative", rel, posixCfg(), routeRelative, routeRelative},
		{"symlink", nest(t, d+"/symlink", 600, "s.sock"), posixCfg(short), routeSymlinkDir, routeSymlinkFile},
		{"thread-cwd", nest(t, d+"/thread", 600, "s.sock"), posixCfg(), routeThreadCwd, routeThreadCwd},
	}
}

// Every path over directMax bytes takes /proc/self/fd/<dirfd>/<name> when
// /proc is there, however long the directory part is (the directory never
// goes into the sockaddr). Nothing else is tried: the place for the symlink is
// untouched.
func TestLongPathsTakeTheProcRoute(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	markUntouched(t, base)
	for _, total := range []int{directMax + 1, 300, 1000} {
		p := nest(t, tmpDir(t), total, "s.sock")
		l, r := listenLong(t, p, procCfg(base))
		if r != routeProc || !isSocket(p) {
			t.Fatalf("%d: listen %s", total, r)
		}
		c, r := dialLong(t, p, procCfg(base))
		if r != routeProc {
			t.Fatalf("%d: dial %s", total, r)
		}
		ping(t, l, c)
	}
	assertUntouched(t, base)
}

// The kernel records the spelling that was bound, so the address of a
// listener bound through /proc is a /proc/self/fd path, which means nothing
// once the directory fd is closed (and nothing in other processes). It must
// still be readable without a panic; users keep their own path.
func TestTheAddressOfAListenerThroughProcIsTheProcSpelling(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 200, "s.sock")
	l, _ := listenLong(t, p, procCfg())
	a := l.Addr().String()
	if !strings.HasPrefix(a, "/proc/self/fd/") || !strings.HasSuffix(a, "/s.sock") {
		t.Fatalf("Addr %q", a)
	}
}

// "/proc/self/fd/<n>/" takes about 17 bytes of the 107 sun_path holds, so a
// file name of 95 bytes does not fit behind it. That makes the /proc route
// unusable for the path, as no /proc would, and the POSIX order runs with the
// real /proc in place (the path is far from the cwd, so not relative):
//
//   - listen: the link to the directory does not fit either (<place>/sockat.
//     XXXXXXXXXX/ + 95 > 107), so the thread-cwd route binds the bare name
//     where the system allows it, ENAMETOOLONG where not;
//   - dial: the link to the socket file takes it.
//
// A name of 80 bytes still fits behind /proc and takes that route.
func TestAFileNameThatDoesNotFitBehindProcTakesThePosixOrder(t *testing.T) {
	t.Parallel()
	d, base := tmpDir(t), tmpDir(t)
	p := d + "/" + strings.Repeat("n", 95)
	if rel := newRelative(p, d); rel != nil && len(rel.spelling) <= directMax {
		t.Fatalf("the premise: %s is not near the cwd", p)
	}
	ep, r, err := runLong(opListen, p, procCfg(base))
	if wantLastRoute(t, "listen", r, err) {
		ep.Close()
		os.Remove(p)
	}
	l := must{t}.listener(Listen(d + "/s.sock"))
	if err := os.Rename(d+"/s.sock", p); err != nil {
		t.Fatal(err)
	}
	c, r := dialLong(t, p, procCfg(base))
	if r != routeSymlinkFile {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)
	assertEmpty(t, base)

	fits := d + "/" + strings.Repeat("f", 80)
	l, r = listenLong(t, fits, procCfg(base))
	if r != routeProc {
		t.Fatalf("80 bytes: listen %s", r)
	}
	c, r = dialLong(t, fits, procCfg(base))
	if r != routeProc {
		t.Fatalf("80 bytes: dial %s", r)
	}
	ping(t, l, c)
}

// Without /proc (not mounted, as in some chroots and minimal containers) the
// /proc route cannot be used at all, so Linux takes the POSIX order instead of
// failing: the relative spelling for a path near the cwd, then the symlink
// under the first usable place, then the thread-cwd route. Both shapes of "no
// /proc" are checked: no /proc directory, and an empty /proc with nothing
// mounted on it. With the real /proc the same paths take the /proc route. The
// real /proc is untouched: only the place to look is changed (cfg.procSelfFD).
// A 95-byte file name, which does not fit behind /proc/self/fd/<n>/, is not
// held to that limit when that spelling is not used: the thread-cwd route
// binds it. Runs in a child process whose cwd is a temporary directory, to
// have a socket near the cwd.
func TestWithoutProcTheOrderIsRelativeThenSymlinkThenThreadCwd(t *testing.T) {
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
			d, base := tmpDir(t), tmpDir(t)
			near := belowCwdPath(t, directMax, "s.sock")
			far := nest(t, d+"/far", 600, "s.sock")
			farNoPlace := nest(t, d+"/noplace", 600, "s.sock")
			name95 := d + "/" + strings.Repeat("n", 95)
			withPlace := posixCfg(base)
			withPlace.procSelfFD = none.path
			noPlace := posixCfg()
			noPlace.procSelfFD = none.path
			for _, c := range []struct {
				path         string
				cfg          *config
				listen, dial route
			}{
				{near, withPlace, routeRelative, routeRelative},
				{far, withPlace, routeSymlinkDir, routeSymlinkFile},
				{farNoPlace, noPlace, routeThreadCwd, routeThreadCwd},
				{name95, noPlace, routeThreadCwd, routeThreadCwd},
			} {
				if c.listen == routeThreadCwd && !threadCwdAvailable() {
					_, _, err := runLong(opListen, c.path, c.cfg)
					wantErrno(t, "listen with only the thread-cwd route left", err, syscall.ENAMETOOLONG)
					continue
				}
				l, r := listenLong(t, c.path, c.cfg)
				if r != c.listen || !isSocket(c.path) {
					t.Fatalf("%d bytes: listen %s, want %s", len(c.path), r, c.listen)
				}
				cc, r := dialLong(t, c.path, c.cfg)
				if r != c.dial {
					t.Fatalf("%d bytes: dial %s, want %s", len(c.path), r, c.dial)
				}
				ping(t, l, cc)
				if c.path != name95 {
					cc, r = dialLong(t, c.path, procCfg(base))
					if r != routeProc {
						t.Fatalf("%d bytes with /proc: dial %s", len(c.path), r)
					}
					ping(t, l, cc)
				}
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
		_, _, err := runLong(o, p, posixCfg())
		wantErrno(t, o.sysName(), err, syscall.ENOENT)
	}
}

// Under a seccomp filter the thread-cwd route is not used at all: unshare(2)
// is not even called, since a filter may answer it by killing the process
// (SIGSYS) rather than with an error. The route reports itself unusable
// (errUnusable, never leaving the package) and the order ends in
// ENAMETOOLONG, the error of a path too long for sun_path; a missing directory
// stays ENOENT. Runs only under a filter (Docker's default profile:
// PR_GET_SECCOMP is 2); without one, TestTheThreadCwdRouteIsUsedWithoutASeccompFilter
// covers the other side, and TestUnderASeccompFilterThatKillsOnUnshareTheProcessSurvives
// makes a filter of its own.
func TestThreadCwdUnderASeccompFilterIsENAMETOOLONG(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode == 0 {
		t.Skip("no seccomp filter here; run under Docker's default seccomp profile")
	} else if mode != 2 {
		t.Fatalf("PR_GET_SECCOMP = %d, want 2 (a filter)", mode)
	}
	checkThreadCwdUnusable(t)
}

// checkThreadCwdUnusable checks that the thread-cwd route reports itself
// unusable and that a path only it would reach is ENAMETOOLONG.
func checkThreadCwdUnusable(t *testing.T) {
	t.Helper()
	d := tmpDir(t)
	p := nest(t, d, 600, "s.sock")
	dir := filepath.Dir(p)
	f := openTestDir(t, dir)
	for _, o := range []op{opListen, opDial} {
		if _, err := threadCwdRoute(o, f, "s.sock"); !errors.Is(err, errUnusable) {
			t.Fatalf("%s: %v, want errUnusable", o.sysName(), err)
		}
	}
	markUntouched(t, dir)
	for _, o := range []op{opListen, opDial} {
		_, _, err := runLong(o, p, posixCfg())
		wantErrno(t, o.sysName(), err, syscall.ENAMETOOLONG)
		_, _, err = runLong(o, dir+"/nope/s.sock", posixCfg())
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
	assertUntouched(t, dir)
}

// Without a seccomp filter (PR_GET_SECCOMP is 0: docker with
// seccomp=unconfined, or a plain host) the thread-cwd route is used: a path
// that nothing earlier reaches is bound and dialed on it.
func TestTheThreadCwdRouteIsUsedWithoutASeccompFilter(t *testing.T) {
	t.Parallel()
	if mode := seccompMode(t); mode != 0 {
		t.Skipf("under a seccomp filter (PR_GET_SECCOMP = %d)", mode)
	}
	p := nest(t, tmpDir(t), 600, "s.sock")
	l, r := listenLong(t, p, posixCfg())
	if r != routeThreadCwd || !isSocket(p) {
		t.Fatalf("listen: %s", r)
	}
	c, r := dialLong(t, p, posixCfg())
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
// ENAMETOOLONG; a route that called unshare(2) would kill it (SIGSYS).
func TestUnderASeccompFilterThatKillsOnUnshareTheProcessSurvives(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildUnderAKillingSeccompFilter", tmpDir(t))
}

func TestChildUnderAKillingSeccompFilter(t *testing.T) {
	asChild(t)
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
	checkThreadCwdUnusable(t)
}

// Why a listen never goes through a link to the socket file: Linux does not
// follow a symlink in the last component of a bind, it finds the name taken
// (EADDRINUSE), even when the link dangles. A connect follows it. Pinned so
// the reason for the two shapes of the symlink route stays checkable.
func TestLinuxBindsNotThroughALinkToTheFileButConnectsThroughIt(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	target, link := d+"/s.sock", d+"/link"
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := net.ListenUnix("unix", &net.UnixAddr{Name: link, Net: "unix"})
	wantErrno(t, "bind through a link to the file", err, syscall.EADDRINUSE)
	if exists(target) {
		t.Fatal("the bind went through the link")
	}
	l := must{t}.listener(Listen(target))
	c := must{t}.conn(Dial(link))
	ping(t, l, c)
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
