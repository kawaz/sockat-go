package sockat

// FreeBSD: a listen through bindat(2) relative to D's fd (no thread needed),
// and the connect order: at-direct (connectat(2) relative to the socket's
// directory, with the file name), then at (connectat(2) relative to D's fd).
// Compiled for FreeBSD on every change; run on a FreeBSD machine (CI) only.

import (
	"slices"
	"strings"
	"syscall"
	"testing"
)

// freebsdCfg is the route order's inputs with D placed in dBases.
func freebsdCfg(dBases ...string) *config {
	return &config{dBases: dBases, procSelfFD: "/proc/self/fd", threadCwd: true}
}

// Every path over directMax bytes, however long its directory part, is bound
// as R/<name> relative to D's fd (at) and connected by at-direct, the first of
// the connect order (the name relative to the socket's directory), which makes
// nothing: the socket is at the given path, the two talk, they are the net
// package's types in working order, and D's place is empty afterwards.
// R/<name> holds the name and 11 bytes, so a name of directMax - 11 bytes (92)
// still takes at for a listen; one byte more is ENAMETOOLONG for a listen and
// still at-direct for a dial (up to 103 bytes). Past that, neither at-direct
// nor at fits: ENAMETOOLONG, with both recorded as unusable in that order. A
// missing directory is ENOENT from opening it.
func TestAtAndAtDirectBindAndConnect(t *testing.T) {
	t.Parallel()
	if got, want := stepNames(dialSteps(false)), []route{routeAtDirect, routeAt}; !slices.Equal(got, want) {
		t.Fatalf("the connect order: %v, want %v", got, want)
	}
	base := tmpDir(t)
	for _, total := range []int{directMax + 1, 300, 1000} {
		p := nest(t, tmpDir(t), total, "s.sock")
		ep, r, err := runLong(opListen, p, freebsdCfg(base))
		l := must{t}.listener(ep, err)
		if r != routeAt || !isSocket(p) {
			t.Fatalf("%d: listen %s", total, r)
		}
		markUntouched(t, base)
		ep, r, sks, err := dialTraced(t, p, freebsdCfg(base))
		c := must{t}.conn(ep, err)
		if r != routeAtDirect || !slices.Equal(namesOf(t, sks), []route{routeStandard}) {
			t.Fatalf("%d: dial %s after %v", total, r, sks)
		}
		assertUntouched(t, base)
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
	wantErrno(t, "listen past at", err, syscall.ENAMETOOLONG)
	if err := syscall.Rename(fits, over); err != nil {
		t.Fatal(err)
	}
	ep, r, err = runLong(opDial, over, freebsdCfg(base))
	c := must{t}.conn(ep, err)
	if r != routeAtDirect {
		t.Fatalf("dial past at: %s", r)
	}
	ping(t, l, c)
	long := d + "/" + strings.Repeat("n", directMax+1)
	_, _, err = runLong(opListen, long, freebsdCfg(base))
	wantErrno(t, "listen a long name", err, syscall.ENAMETOOLONG)
	_, _, sks, err := dialTraced(t, long, freebsdCfg(base))
	wantErrno(t, "dial a long name", err, syscall.ENAMETOOLONG)
	if want := []route{routeStandard, routeAtDirect, routeAt}; !slices.Equal(namesOf(t, sks), want) {
		t.Fatalf("dial a long name: skipped %v, want %v", sks, want)
	}
	for _, o := range []op{opListen, opDial} {
		_, _, err = runLong(o, d+"/nope/"+strings.Repeat("n", 200), freebsdCfg(base))
		wantErrno(t, o.sysName()+" in a missing directory", err, syscall.ENOENT)
	}
	assertEmpty(t, base)
}

// At, second in the connect order, is reached where at-direct cannot be used:
// the socket's directory not opened (no read permission, as FreeBSD opens it
// O_RDONLY; stood in for by dialFrom with sdir nil). It connects through
// R/<name> relative to D (made in its place: its mtime moved), at-direct is
// recorded as unusable before it, and D's place is empty afterwards.
func TestAtIsTakenWhereAtDirectCannotBeUsed(t *testing.T) {
	t.Parallel()
	base := tmpDir(t)
	p := nest(t, tmpDir(t), 300, "s.sock")
	l := listenAt(t, p)
	markUntouched(t, base)
	cfg := freebsdCfg(base)
	cfg.trace = &trace{}
	ep, r, err := dialFrom(t, p, nil, cfg, routeAtDirect)
	c := must{t}.conn(ep, err)
	if r != routeAt || !slices.Equal(namesOf(t, cfg.trace.skipped), []route{routeAtDirect}) {
		t.Fatalf("dial: %s after %v, want %s after %s", r, cfg.trace.skipped, routeAt, routeAtDirect)
	}
	ping(t, l, c)
	assertTouched(t, base)
	assertEmpty(t, base)
}
