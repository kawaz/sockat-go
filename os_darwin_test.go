package sockat

// macOS: O_SEARCH for the socket's directory of thread-cwd-direct, why the
// widened sockaddr is not used (and how the connect order records it), and
// the macOS list of ways.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ways are the macOS connect routes, each with its own socket path and the
// test world that forces it. Every spelling goes in the plain sockaddr.
//
//	| way               | path                          | world          | dial              |
//	|-------------------|-------------------------------|----------------|-------------------|
//	| standard          | directMax bytes               | (public)       | standard          |
//	| relative          | directMax bytes below the cwd | as is          | relative          |
//	| thread-cwd-direct | 600 bytes, directName         | as is          | thread-cwd-direct |
//	| link-file         | 600 bytes, directName         | thread cwd off | link-file         |
func ways(t *testing.T) []way {
	t.Helper()
	d := tmpDir(t)
	asIs, psAsIs := testWorld(t, nil)
	threadOff, psOff := testWorld(t, func(c *config, _ testPlaces) { c.threadCwd = false })
	return []way{
		{"standard", nest(t, d+"/direct", directMax, "s.sock"), nil, testPlaces{}, routeStandard, routeStandard},
		{"relative", belowCwdPath(t, directMax, "s.sock"), asIs, psAsIs, "", routeRelative},
		{"thread-cwd-direct", nest(t, d+"/thread", 600, directName), asIs, psAsIs, "", routeThreadCwdDirect},
		{"link-file", nest(t, d+"/link", 600, directName), threadOff, psOff, "", routeLinkFile},
	}
}

// widened is a sockaddr_un for path in the widened form XNU takes: sun_len and
// the address length 2 + the path length (past sizeof(struct sockaddr_un) for
// a long path), no NUL. Up to 253 bytes of path fit (SOCK_MAXADDRLEN 255 less
// the length and family bytes).
func widened(path string) []byte {
	sa := make([]byte, 2+len(path))
	sa[0] = byte(len(sa))
	sa[1] = unix.AF_UNIX
	copy(sa[2:], path)
	return sa
}

// rawSocket is a stream socket made with the system call, as an *os.File
// that owns it.
func rawSocket(t *testing.T) *os.File {
	t.Helper()
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), "")
	t.Cleanup(func() { f.Close() })
	return f
}

// sockcall makes bind(2) or connect(2) (trap) on fd with the raw address sa.
func sockcall(trap uintptr, fd int, sa []byte) error {
	_, _, e := syscall.Syscall(trap, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa)))
	if e != 0 {
		return e
	}
	return nil
}

// Why sockat-go does not use the widened sockaddr that SPEC has on macOS
// (the kernel takes it: a 200-byte path binds and connects in it), pinned with
// raw system calls made only here:
//
//   - a socket bound in the widened form cannot become a *net.UnixListener:
//     net.FileListener reads the address with getsockname(2), which reports
//     more than struct sockaddr_un holds, and the syscall package refuses it
//     with EINVAL (the socket file exists all the same);
//   - a socket connected in the widened form to a peer bound that way becomes
//     a *net.UnixConn whose RemoteAddr() is a nil net.Addr (getpeername's
//     EINVAL is ignored), so RemoteAddr().String() panics. net.DialUnix to the
//     same socket through a short spelling (a symlink to it) reports that
//     spelling instead: the address can always be read.
//
// So a listen could not be made at all and a dial would break SPEC's "the
// address can be read without failing". Should a Go release accept the long
// address, this test fails and the widened form can be reconsidered.
func TestWhyTheWidenedSockaddrIsNotUsed(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	p := nest(t, d, 200, "s.sock")

	srv := rawSocket(t)
	if err := sockcall(unix.SYS_BIND, int(srv.Fd()), widened(p)); err != nil {
		t.Fatalf("the kernel takes a widened bind: %v", err)
	}
	if !isSocket(p) {
		t.Fatal("no socket file after a widened bind")
	}
	if err := unix.Listen(int(srv.Fd()), 8); err != nil {
		t.Fatal(err)
	}
	if _, err := net.FileListener(srv); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("net.FileListener of a widened bind: %v, want EINVAL", err)
	}

	cli := rawSocket(t)
	if err := sockcall(unix.SYS_CONNECT, int(cli.Fd()), widened(p)); err != nil {
		t.Fatalf("the kernel takes a widened connect: %v", err)
	}
	c, err := net.FileConn(cli)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ra := c.RemoteAddr(); ra != nil {
		t.Fatalf("RemoteAddr of a widened connect: %v, want a nil net.Addr", ra)
	}

	link := tmpDir(t) + "/l"
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	sc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: link, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	if sc.RemoteAddr() == nil {
		t.Fatal("net.DialUnix left RemoteAddr nil")
	}
}

// Widened, second in the connect order on macOS, is not made in sockat-go
// (TestWhyTheWidenedSockaddrIsNotUsed): a dial that passes over relative
// records widened as unusable with that reason, makes nothing for it (the
// places stay untouched), and goes on to thread-cwd-direct. The order is
// relative, widened, thread-cwd-direct, link-file.
func TestWidenedIsRecordedAsNotMadeAndPassedOver(t *testing.T) {
	t.Parallel()
	if got, want := stepNames(dialSteps(false)), []route{routeRelative, routeWidened, routeThreadCwdDirect, routeLinkFile}; !slices.Equal(got, want) {
		t.Fatalf("the connect order: %v, want %v", got, want)
	}
	p := nest(t, tmpDir(t), 200, "s.sock")
	l := listenAt(t, p)
	w, ps := testWorld(t, nil)
	markUntouched(t, ps.tmp)
	c, r, sks, err := dialTraced(t, w, p, Options{})
	if err != nil || r != routeThreadCwdDirect {
		t.Fatalf("dial: %s, %v, want %s", r, err, routeThreadCwdDirect)
	}
	ping(t, l, c)
	want := []route{routeStandard, routeRelative, routeWidened}
	if got := namesOf(t, sks); !slices.Equal(got, want) || sks[2].reason != noWidenedRoute {
		t.Fatalf("skipped %v, want %v with widened for %q", sks, want, noWidenedRoute)
	}
	assertUntouched(t, ps.tmp)
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

// The socket's directory is opened O_SEARCH, which needs only search
// permission, as a bind / connect by the path itself does: in a directory of
// mode 0300 a bind takes link-dir and a dial thread-cwd-direct, which uses
// the directory's fd as its thread's cwd. Opening it O_RDONLY would make the
// long path fail where a short one in the same directory works.
//
// A kernel that does not take O_SEARCH on a directory answers EISDIR or EINVAL;
// the directory is then opened O_RDONLY rather than given up on. The running
// kernel takes O_SEARCH, so its refusal is stood in for by handing that answer
// to searchOrReadOnly:
//
//   - EISDIR and EINVAL give an fd that serves as the socket's directory: a
//     thread whose cwd is that fd listens and dials by the file name;
//   - that fd really comes from O_RDONLY: for the 0300 directory it is now
//     EACCES (and that is the error returned);
//   - any other answer is the real outcome and is returned without trying
//     O_RDONLY: an EACCES stays EACCES even for a readable directory (falling
//     back would hide a real permission failure).
func TestTheSocketsDirectoryIsOpenedForSearchOnly(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	locked := mkdirMode(t, d+"/locked", 0o700)
	p := nestTail(t, locked, 500, "s.sock")
	unreadable(t, filepath.Dir(p))
	t.Cleanup(func() { os.Chmod(filepath.Dir(p), 0o700) })
	w, _ := testWorld(t, nil)
	l, b, err := w.listen(p, Options{}, nil)
	if err != nil || b.route != routeLinkDir || !isSocket(p) {
		t.Fatalf("listen in a directory without read permission: %s, %v", b.route, err)
	}
	t.Cleanup(func() { l.Close() })
	c, r := dialIn(t, w, p)
	if r != routeThreadCwdDirect {
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
		l := must{t}.listener(inThreadCwd(opListen, int(f.Fd()), name))
		if !isSocket(readable + "/" + name) {
			t.Fatalf("%v: no socket in the directory", e)
		}
		c := must{t}.conn(inThreadCwd(opDial, int(f.Fd()), name))
		ping(t, l, c)
		f.Close()

		_, err = searchOrReadOnly(filepath.Dir(p), -1, e)
		wantErrno(t, e.Error()+" for a directory without read permission", err, syscall.EACCES)
	}
	_, err = searchOrReadOnly(readable, -1, syscall.EACCES)
	wantErrno(t, "an EACCES from O_SEARCH", err, syscall.EACCES)
}
