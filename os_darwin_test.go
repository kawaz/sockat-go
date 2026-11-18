package sockat

// macOS: the widened sockaddr (Dial only), O_SEARCH for the thread-cwd route's
// directory, and the macOS list of ways.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ways are the seven macOS routes, each with its own socket path and the route
// order's inputs that force it:
//
//	| way           | path                         | inputs              | listen     | dial          |
//	|---------------|------------------------------|---------------------|------------|---------------|
//	| direct        | directMax bytes              | (the public order)  | direct     | direct        |
//	| relative      | directMax bytes below the cwd | no place            | relative   | relative      |
//	| wide          | 200 bytes under /tmp         | no place            | thread-cwd | wide          |
//	| wide-relative | 240 bytes below the cwd      | no place            | thread-cwd | wide-relative |
//	| symlink       | 600 bytes under /tmp         | a short place       | symlink-dir | symlink-file |
//	| symlink-wide  | 600 bytes under /tmp         | a 150-byte place    | thread-cwd | symlink-wide  |
//	| thread-cwd    | 600 bytes under /tmp         | no place, no widening | thread-cwd | thread-cwd |
//
// A listen never uses the widened sockaddr, so where a dial goes wide the
// listen falls through to the thread-cwd route.
func ways(t *testing.T) []way {
	t.Helper()
	d, short := tmpDir(t), tmpDir(t)
	long := nest(t, tmpDir(t), 150, "base")
	mkdirMode(t, long, 0o700)
	rel := belowCwdPath(t, directMax, "s.sock")
	wrel := belowCwdPath(t, 240, "s.sock")
	if len(wrel) <= wideMax {
		t.Fatalf("the premise: %d bytes fit the widened sockaddr", len(wrel))
	}
	return []way{
		{"direct", nest(t, d+"/direct", directMax, "s.sock"), nil, routeDirect, routeDirect},
		{"relative", rel, posixCfg(), routeRelative, routeRelative},
		{"wide", nest(t, d+"/wide", 200, "s.sock"), posixCfg(), routeThreadCwd, routeWide},
		{"wide-relative", wrel, posixCfg(), routeThreadCwd, routeWideRelative},
		{"symlink", nest(t, d+"/symlink", 600, "s.sock"), posixCfg(short), routeSymlinkDir, routeSymlinkFile},
		{"symlink-wide", nest(t, d+"/symlinkwide", 600, "s.sock"), posixCfg(long), routeThreadCwd, routeSymlinkWide},
		{"thread-cwd", nest(t, d+"/thread", 600, "s.sock"), plainCfg(), routeThreadCwd, routeThreadCwd},
	}
}

// XNU takes a socket address of up to SOCK_MAXADDRLEN (255) bytes, so the
// widened form (sun_len and the address length set to 2 + the path length,
// no NUL) carries 255 - 2 = 253 bytes of path. Pinned against the running
// kernel: a dial to a 253-byte path reaches the listener there (and the route
// order picks the widened form for it); 254 bytes is ENAMETOOLONG before the
// kernel is asked.
func TestTheWidenedSockaddrCarries253BytesForADial(t *testing.T) {
	t.Parallel()
	if wideMax != 253 {
		t.Fatalf("wideMax = %d, want 253", wideMax)
	}
	d := tmpDir(t)
	p := nest(t, d, 253, "s.sock")
	l, _ := listenLong(t, p, posixCfg())
	c := must{t}.conn(wideOp(opDial, p))
	ping(t, l, c)
	c, r := dialLong(t, p, posixCfg())
	if r != routeWide {
		t.Fatalf("dial through the order: %s", r)
	}
	ping(t, l, c)

	q := nest(t, d, 254, "s.sock")
	_, err := wideOp(opDial, q)
	wantErrno(t, "254 bytes", err, syscall.ENAMETOOLONG)
}

// Why a listen never uses the widened sockaddr: a socket bound in the widened
// form cannot be handed to the net package. net.FileListener reads the
// socket's address with getsockname(2), which then reports more than a
// struct sockaddr_un holds, and the syscall package refuses that with EINVAL,
// although the bind itself went through and the socket file exists. (A
// dialed socket's own address is unbound, so net.FileConn takes it.) Pinned
// so that a Go release that accepted the long address would be noticed: then
// the widened form could serve a listen too. wideOp therefore refuses a
// listen (errUnusable, nothing created), and the route order sends a listen
// that only the widened form would carry to the thread-cwd route.
func TestTheWidenedSockaddrIsNotUsedForAListen(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 200, "s.sock")
	_, err := rawOp(opListen, func(sock int) error {
		sa := make([]byte, 2+len(p))
		sa[0] = byte(len(sa))
		sa[1] = unix.AF_UNIX
		copy(sa[2:], p)
		_, _, e := syscall.Syscall(unix.SYS_BIND, uintptr(sock), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)))
		if e != 0 {
			return e
		}
		return nil
	})
	if !errors.Is(err, syscall.EINVAL) || !isSocket(p) {
		t.Fatalf("the premise: a widened bind is made (%v) but not adopted (%v)", isSocket(p), err)
	}

	q := filepath.Dir(p) + "/q.sock"
	markUntouched(t, filepath.Dir(q))
	if _, err := wideOp(opListen, q); !errors.Is(err, errUnusable) {
		t.Fatalf("wideOp for a listen: %v, want errUnusable", err)
	}
	assertUntouched(t, filepath.Dir(q))
	l, r := listenLong(t, q, posixCfg())
	if r != routeThreadCwd {
		t.Fatalf("listen: %s, want %s", r, routeThreadCwd)
	}
	c, r := dialLong(t, q, posixCfg())
	if r != routeWide {
		t.Fatalf("dial: %s, want %s", r, routeWide)
	}
	ping(t, l, c)
}

// The answers that mean "this kernel does not take the widened form" (EINVAL
// for an address it will not take, ENAMETOOLONG for one over SOCK_MAXADDRLEN)
// make the order move on with the plain sockaddr; any other answer is the
// outcome of the call (ENOENT, ECONNREFUSED, ...) and is returned.
func TestOnlyEINVALAndENAMETOOLONGMeanTheWidenedFormIsRefused(t *testing.T) {
	t.Parallel()
	for _, e := range []syscall.Errno{syscall.EINVAL, syscall.ENAMETOOLONG} {
		if !refusesWide(syscallError("connect", e)) {
			t.Errorf("%v: want refused", e)
		}
	}
	for _, e := range []syscall.Errno{syscall.ENOENT, syscall.ECONNREFUSED, syscall.EADDRINUSE, syscall.EACCES} {
		if refusesWide(syscallError("connect", e)) {
			t.Errorf("%v: want an outcome", e)
		}
	}
}

// A kernel that refuses the widened form (stood in for by cfg.wide = false; a
// future macOS might stop taking oversized addresses) leaves a dial to the
// plain sockaddr: a 200-byte path far from the cwd goes through the symlink
// under a short place, or with no place on the thread-cwd route. A
// 150-byte place, usable only in the widened form, is then passed over.
func TestWithoutTheWidenedFormADialTakesThePlainRoutes(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 200, "s.sock")
	short := tmpDir(t)
	long := nest(t, tmpDir(t), 150, "base")
	mkdirMode(t, long, 0o700)
	markUntouched(t, long)
	l, _ := listenLong(t, p, posixCfg())
	for _, c := range []struct {
		cfg  *config
		want route
	}{
		{plainCfg(short), routeSymlinkFile},
		{plainCfg(long), routeThreadCwd},
		{plainCfg(), routeThreadCwd},
	} {
		cc, r := dialLong(t, p, c.cfg)
		if r != c.want {
			t.Fatalf("%v: %s, want %s", c.cfg.tmpBases, r, c.want)
		}
		ping(t, l, cc)
	}
	assertUntouched(t, long)
	assertEmpty(t, short)
}

// The thread-cwd route puts the file name in the plain sockaddr, and only a
// name too long for that (over 103 bytes) in the widened one, for a dial (the
// route order passes wide only then). Without the widened form such a name is
// ENAMETOOLONG, and so is a name over 253 bytes with it. The socket with the
// 150-byte name is bound under a short name and renamed, since no listen
// reaches it.
func TestTheThreadCwdRouteWidensOnlyANameTooLongForThePlainSockaddr(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	name := strings.Repeat("n", 150)
	p := nest(t, d, 600, name)
	dir := filepath.Dir(p)
	f := openTestDir(t, dir)
	l := must{t}.listener(threadCwdRoute(opListen, f, "s.sock", false))
	if err := os.Rename(dir+"/s.sock", p); err != nil {
		t.Fatal(err)
	}
	c := must{t}.conn(threadCwdRoute(opDial, f, name, true))
	ping(t, l, c)
	c, r := dialLong(t, p, posixCfg())
	if r != routeThreadCwd {
		t.Fatalf("dial through the order: %s", r)
	}
	ping(t, l, c)

	_, err := threadCwdRoute(opDial, f, name, false)
	wantErrno(t, "without the widened form", err, syscall.ENAMETOOLONG)
	_, err = threadCwdRoute(opDial, f, strings.Repeat("n", 254), true)
	wantErrno(t, "254 bytes", err, syscall.ENAMETOOLONG)
	_, _, err = runLong(opListen, dir+"/"+strings.Repeat("m", 150), posixCfg())
	wantErrno(t, "a listen", err, syscall.ENAMETOOLONG)
}

// unreadable makes dir mode 0300 (write and search, no read) and checks the
// premise that it can no longer be opened O_RDONLY. mkdirMode's cleanup gives
// it back 0700.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY, 0); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("the premise: no read permission: %v", err)
	}
}

// The thread-cwd route opens the directory O_SEARCH, which needs only search
// permission, as a bind / connect by the path itself does: in a directory of
// mode 0300 the route listens and dials. Opening it O_RDONLY would make the
// long path fail where a short one in the same directory works.
//
// A kernel that does not take O_SEARCH on a directory answers EISDIR or EINVAL;
// the directory is then opened O_RDONLY rather than given up on. The running
// kernel takes O_SEARCH, so its refusal is stood in for by handing that answer
// to searchOrReadOnly:
//
//   - EISDIR and EINVAL give an fd the route listens and dials through;
//   - that fd really comes from O_RDONLY: for the 0300 directory it is now
//     EACCES (and that is the error returned);
//   - any other answer is the real outcome and is returned without trying
//     O_RDONLY: an EACCES stays EACCES even for a readable directory (falling
//     back would hide a real permission failure).
func TestTheThreadCwdRouteOpensTheDirectoryForSearchOnly(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	locked := mkdirMode(t, d+"/locked", 0o700)
	p := nestTail(t, locked, 500, "s.sock")
	unreadable(t, filepath.Dir(p))
	t.Cleanup(func() { os.Chmod(filepath.Dir(p), 0o700) })
	l, r := listenLong(t, p, plainCfg())
	if r != routeThreadCwd || !isSocket(p) {
		t.Fatalf("listen in a directory without read permission: %s", r)
	}
	c, r := dialLong(t, p, plainCfg())
	if r != routeThreadCwd {
		t.Fatalf("dial: %s", r)
	}
	ping(t, l, c)

	readable := mkdirMode(t, d+"/readable", 0o700)
	for _, e := range []syscall.Errno{syscall.EISDIR, syscall.EINVAL} {
		f, err := searchOrReadOnly(readable, -1, e)
		if err != nil {
			t.Fatalf("%v: %v", e, err)
		}
		name := fmt.Sprintf("e%d.sock", int(e))
		l := must{t}.listener(threadCwdRoute(opListen, f, name, false))
		if !isSocket(readable + "/" + name) {
			t.Fatalf("%v: no socket in the directory", e)
		}
		c := must{t}.conn(threadCwdRoute(opDial, f, name, false))
		ping(t, l, c)
		f.Close()

		_, err = searchOrReadOnly(filepath.Dir(p), -1, e)
		wantErrno(t, e.Error()+" for a directory without read permission", err, syscall.EACCES)
	}
	_, err := searchOrReadOnly(readable, -1, syscall.EACCES)
	wantErrno(t, "an EACCES from O_SEARCH", err, syscall.EACCES)
}
