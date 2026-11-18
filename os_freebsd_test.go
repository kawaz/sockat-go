package sockat

// FreeBSD: the central route through bindat(2) / connectat(2) relative to D's
// fd (no thread needed), and a dial's fallback, connectat(2) relative to the
// socket's directory. Compiled for FreeBSD on every change; run on a FreeBSD
// machine (CI) only.

import (
	"strings"
	"syscall"
	"testing"
)

// freebsdCfg is the route order's inputs with D placed in dBases.
func freebsdCfg(dBases ...string) *config {
	return &config{dBases: dBases, procSelfFD: "/proc/self/fd", threadCwd: true}
}

// Every path over directMax bytes, however long its directory part, is bound
// and connected as R/<name> relative to D's fd: the socket is at the given
// path, the two talk, they are the net package's types in working order, and
// D's place is empty afterwards. R/<name> holds the name and 11 bytes, so a
// name of directMax - 11 bytes (92) still takes it; one byte more is
// ENAMETOOLONG for a listen and a connectat relative to the socket's
// directory for a dial (up to 103 bytes; past that ENAMETOOLONG). A missing
// directory is ENOENT from opening it.
func TestTheCentralRouteTakesBindatAndConnectat(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	for _, total := range []int{directMax + 1, 300, 1000} {
		p := nest(t, tmpDir(t), total, "s.sock")
		ep, r, err := runLong(opListen, p, freebsdCfg(base))
		l := must{t}.listener(ep, err)
		if r != routeAt || !isSocket(p) {
			t.Fatalf("%d: listen %s", total, r)
		}
		ep, r, err = runLong(opDial, p, freebsdCfg(base))
		c := must{t}.conn(ep, err)
		if r != routeAt {
			t.Fatalf("%d: dial %s", total, r)
		}
		checkStdTypes(t, l, c)
		assertEmpty(t, base)
	}
	d := tmpDir(t)
	fits := d + "/" + strings.Repeat("f", directMax-11)
	ep, r, err := runLong(opListen, fits, freebsdCfg(base))
	l := must{t}.listener(ep, err)
	if r != routeAt {
		t.Fatalf("a %d-byte name: listen %s", directMax-11, r)
	}
	over := d + "/" + strings.Repeat("o", directMax-10)
	_, _, err = runLong(opListen, over, freebsdCfg(base))
	wantErrno(t, "listen past the central route", err, syscall.ENAMETOOLONG)
	if err := syscall.Rename(fits, over); err != nil {
		t.Fatal(err)
	}
	ep, r, err = runLong(opDial, over, freebsdCfg(base))
	c := must{t}.conn(ep, err)
	if r != routeConnectAt {
		t.Fatalf("dial past the central route: %s", r)
	}
	ping(t, l, c)
	for _, o := range []op{opListen, opDial} {
		_, _, err := runLong(o, d+"/"+strings.Repeat("n", directMax+1), freebsdCfg(base))
		wantErrno(t, o.sysName()+" a long name", err, syscall.ENAMETOOLONG)
		_, _, err = runLong(o, d+"/nope/"+strings.Repeat("n", 200), freebsdCfg(base))
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
	assertEmpty(t, base)
}
