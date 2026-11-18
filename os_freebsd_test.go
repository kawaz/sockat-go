package sockat

// FreeBSD: bindat(2) / connectat(2) relative to the directory's fd, the only
// route, for any directory length. Compiled for FreeBSD on every change;
// run on a FreeBSD machine only.

import (
	"strings"
	"syscall"
	"testing"
)

// Every path over directMax bytes, however long its directory part, is bound
// and connected by its file name relative to the directory's fd: the socket is
// at the given path, the two talk, and they are the net package's types in
// working order. The file name itself still goes in a sockaddr: over 103
// bytes it is ENAMETOOLONG. A missing directory is ENOENT from opening it.
func TestLongPathsTakeBindatAndConnectat(t *testing.T) {
	t.Parallel()
	for _, total := range []int{directMax + 1, 300, 1000} {
		p := nest(t, tmpDir(t), total, "s.sock")
		ep, r, err := runLong(opListen, p, nil)
		l := must{t}.listener(ep, err)
		if r != routeAt || !isSocket(p) {
			t.Fatalf("%d: listen %s", total, r)
		}
		ep, r, err = runLong(opDial, p, nil)
		c := must{t}.conn(ep, err)
		if r != routeAt {
			t.Fatalf("%d: dial %s", total, r)
		}
		checkStdTypes(t, l, c)
	}
	d := tmpDir(t)
	for _, o := range []op{opListen, opDial} {
		_, _, err := runLong(o, d+"/"+strings.Repeat("n", directMax+1), nil)
		wantErrno(t, o.sysName()+" a long name", err, syscall.ENAMETOOLONG)
		_, _, err = runLong(o, d+"/nope/"+strings.Repeat("n", 200), nil)
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
}
