package sockat

// FreeBSD: a listen through bindat(2) relative to D's fd (no thread needed),
// and the connect order: at-direct (connectat(2) relative to the socket's
// directory, with the file name), then link-file (an E of a random name
// right in B, unlinked after the connect). Compiled for FreeBSD on every
// change; run on a FreeBSD machine (CI) only.

import (
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// A bind no link-dir spelling fits takes at (bindat(2) on D, E/<name>): a
// name of directMax - 11 bytes (92) still fits E/<name>, its spelling
// Dangling and the socket at the path; one byte more is ENAMETOOLONG for a
// bind and still at-direct for a dial (connectat(2) with the bare name, up to
// 103 bytes). Of any long path whose name fits, a dial takes at-direct and
// makes nothing in the places. Past 103 bytes at-direct cannot be used and a
// dial takes link-file (whose spelling holds no file name), leaving no E in
// B and no D. A missing directory is ENOENT for both.
func TestAtAndAtDirectBindAndConnect(t *testing.T) {
	t.Parallel()
	if got, want := stepNames(dialSteps(false)), []route{routeAtDirect, routeLinkFile}; !slices.Equal(got, want) {
		t.Fatalf("the connect order: %v, want %v", got, want)
	}
	w, ps := testWorld(t, nil)
	for _, total := range []int{directMax + 1, 300, 1000} {
		p := nest(t, tmpDir(t), total, "s.sock")
		l := listenAt(t, p)
		markUntouched(t, ps.tmp)
		c, r, sks, err := dialTraced(t, w, p, Options{})
		if err != nil || r != routeAtDirect || !slices.Equal(namesOf(t, sks), []route{routeStandard}) {
			t.Fatalf("%d: dial %s after %v, %v", total, r, sks, err)
		}
		assertUntouched(t, ps.tmp)
		checkStdTypes(t, l, c)
	}
	d := tmpDir(t)
	fits := d + "/" + strings.Repeat("f", directMax-11)
	// The binds go through a world of their own: at makes D, which the
	// dials, checked to make none, must not find.
	wb, _ := testWorld(t, nil)
	l, b, err := wb.listen(fits, nil)
	if err != nil || b.route != routeAt || b.spelling.Kind != Dangling || !isSocket(fits) {
		t.Fatalf("a %d-byte name: listen %s %v, %v", directMax-11, b.route, b.spelling, err)
	}
	t.Cleanup(func() { l.Close() })
	over := d + "/" + strings.Repeat("o", directMax-10)
	_, _, err = wb.listen(over, nil)
	wantErrno(t, "listen past at", err, syscall.ENAMETOOLONG)
	if err := syscall.Rename(fits, over); err != nil {
		t.Fatal(err)
	}
	c, r := dialIn(t, w, over)
	if r != routeAtDirect {
		t.Fatalf("dial past at: %s", r)
	}
	ping(t, l, c)
	long := d + "/" + strings.Repeat("n", directMax+1)
	ll := listenAt(t, long)
	cl, r, sks, err := dialTraced(t, w, long, Options{})
	if err != nil || r != routeLinkFile || !slices.Equal(namesOf(t, sks), []route{routeStandard, routeAtDirect}) {
		t.Fatalf("dial a %d-byte name: %s after %v, %v", directMax+1, r, sks, err)
	}
	ping(t, ll, cl)
	assertNoDOrE(t, w)
	missing := d + "/nope/" + strings.Repeat("n", 200)
	_, _, err = w.dial(missing, Options{}, nil)
	wantErrno(t, "dial in a missing directory", err, syscall.ENOENT)
	_, _, err = w.listen(missing, nil)
	wantErrno(t, "listen in a missing directory", err, syscall.ENOENT)
}

// at-direct needs the socket's directory open, and FreeBSD opens it
// O_RDONLY: in a directory without read permission (0300) at-direct cannot be
// used, and a dial takes link-file, which needs only search permission (the
// kernel follows E to the path). As root, who may read any directory, it runs
// in a child that has dropped to nobody (unprivileged).
func TestLinkFileIsTakenWhereAtDirectCannotBeUsed(t *testing.T) {
	t.Parallel()
	if !unprivileged(t) {
		return
	}
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	p := nest(t, d, 300, "s.sock")
	l := listenAt(t, p)
	dir, _, _ := splitPath(p)
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	c, r, sks, err := dialTraced(t, w, p, Options{})
	if err != nil || r != routeLinkFile || !slices.Equal(namesOf(t, sks), []route{routeStandard, routeAtDirect}) {
		t.Fatalf("dial: %s after %v, %v", r, sks, err)
	}
	ping(t, l, c)
	assertNoDOrE(t, w)
}

// A datagram socket takes FreeBSD's routes as a stream socket does: bound by
// at (bindat(2) on D) where no link-dir spelling fits, with its own socket
// type (a datagram arrives), and connected by at-direct (connectat(2)) with
// a name of up to 103 bytes, by link-file past that.
func TestAtAndAtDirectForADatagramSocket(t *testing.T) {
	t.Parallel()
	wb, _ := testWorld(t, nil)
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	p := d + "/" + strings.Repeat("f", directMax-11)
	s, b := bindGramIn(t, wb, p, Options{})
	if b.Route != string(routeAt) || b.Spelling.Kind != Dangling {
		t.Fatalf("bind: %s %+v", b.Route, b.Spelling)
	}
	c, r := connectGramIn(t, w, p)
	if r != routeAtDirect {
		t.Fatalf("connect: %s", r)
	}
	c.Write([]byte("at"))
	recv(t, s, "at")
	long := d + "/" + strings.Repeat("n", directMax+1)
	sl := gramAt(t, long)
	cl, r := connectGramIn(t, w, long)
	if r != routeLinkFile {
		t.Fatalf("connect a %d-byte name: %s", directMax+1, r)
	}
	cl.Write([]byte("lf"))
	recv(t, sl, "lf")
	assertNoDOrE(t, w)
}
