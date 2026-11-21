package sockat

// ResolveForBind and ResolveForConnect (resolve.go) through a test world
// (testWorld): the spellings they make, handed to the net package, bind and
// connect at the path; ForBind's SocketFile and the cleanup they share.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// resolveBindIn is w.resolveForBind(p, opts), failing the test on an error;
// the spelling is cleaned up at the end of the test.
func resolveBindIn(t *testing.T, w *world, p string, opts Options) *ForBind {
	t.Helper()
	f, err := w.resolveForBind(p, opts)
	if err != nil {
		t.Fatalf("%d bytes: %v", len(p), err)
	}
	t.Cleanup(func() { f.Cleanup() })
	return f
}

// resolveConnectIn is w.resolveForConnect(p, opts), failing the test on an
// error; the spelling is cleaned up at the end of the test.
func resolveConnectIn(t *testing.T, w *world, p string, opts Options) *ForConnect {
	t.Helper()
	f, err := w.resolveForConnect(p, opts)
	if err != nil {
		t.Fatalf("%d bytes: %v", len(p), err)
	}
	t.Cleanup(func() { f.Cleanup() })
	return f
}

// listenSpelling binds spelling with the net package, as a user of
// ResolveForBind does (the listener does not unlink its address on Close: it
// is the spelling, not the path); closed at the end of the test.
func listenSpelling(t *testing.T, spelling string) *net.UnixListener {
	t.Helper()
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: spelling, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on the spelling %s: %v", spelling, err)
	}
	l.SetUnlinkOnClose(false)
	t.Cleanup(func() { l.Close() })
	return l
}

// dialSpelling connects to spelling with the net package; closed at the end
// of the test.
func dialSpelling(t *testing.T, spelling string) *net.UnixConn {
	t.Helper()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: spelling, Net: "unix"})
	if err != nil {
		t.Fatalf("dial the spelling %s: %v", spelling, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// wantResolveError checks that err is a resolve's: a *RouteError with no
// route whose Err is an *os.PathError of Op "resolve" for path, wrapping
// errno.
func wantResolveError(t *testing.T, what, path string, err error, errno syscall.Errno) *RouteError {
	t.Helper()
	re := routeErr(t, err)
	var pe *os.PathError
	if !errors.As(re.Err, &pe) || pe.Op != "resolve" || pe.Path != path || !errors.Is(err, errno) || re.Route != "" {
		t.Fatalf("%s: %T %v (route %q), want resolve %s of the path", what, re.Err, err, re.Route, errno)
	}
	return re
}

// A long path whose file name a spelling through B fits is spelt by link-dir
// (procB where /proc can be used, the real path of B otherwise), Linked and
// Removable, and nothing is bound yet. The net package binds that spelling,
// and the socket file is at the path; ResolveForConnect spells the path by
// link-file (an E, named by the B's counter, that points to the path), and
// the net package connects through it to that listener. After the connect's
// Cleanup its E is gone (D stays); ForBind's SocketFile records the file, its
// Remove removes it and cleans up ForBind's E too, so ForBind's own Cleanup
// has nothing left to do. A spelling never needs more than the sockaddr.
func TestResolvedSpellingsBindAndConnectAtThePath(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 300, "s.sock")
	f := resolveBindIn(t, w, p, Options{})
	if f.Route != string(routeLinkDir) || f.Spelling.Kind != Linked || !f.Spelling.Removable || len(f.Path) > directMax {
		t.Fatalf("for bind: %s %q %+v", f.Route, f.Path, f.Spelling)
	}
	if want := []route{routeStandard}; !slices.Equal(skippedNames(t, f.Skipped), want) {
		t.Fatalf("skipped %v, want %v", f.Skipped, want)
	}
	if exists(p) {
		t.Fatal("the resolve bound")
	}
	l := listenSpelling(t, f.Path)
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	fc := resolveConnectIn(t, w, p, Options{})
	if fc.Route != string(routeLinkFile) || len(fc.Path) > directMax {
		t.Fatalf("for connect: %s %q", fc.Route, fc.Path)
	}
	e := filepath.Base(fc.Path)
	if _, err := strconv.ParseUint(e, 36, 64); err != nil {
		t.Fatalf("connect's E %q is not the counter's", e)
	}
	if target, err := os.Readlink(fc.Path); err != nil || target != p {
		t.Fatalf("connect's E points to %q, %v", target, err)
	}
	c := dialSpelling(t, fc.Path)
	ping(t, l, c)
	if err := fc.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(fc.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("connect's E after Cleanup: %v", err)
	}
	if fc.Cleanup() != nil {
		t.Fatal("a second Cleanup failed")
	}
	sf, err := f.SocketFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.Remove(); err != nil {
		t.Fatal(err)
	}
	if exists(p) {
		t.Fatal("Remove left the socket file")
	}
	if _, err := os.Lstat(filepath.Dir(f.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bind's E after Remove: %v", err)
	}
	if err := f.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if b := w.auto; b == nil || b.d == nil || !exists(b.real+"/"+b.d.name) {
		t.Fatal("D is gone")
	}
}

// ForBind's SocketFile follows the spelling when it is called: before the
// bind it leads to nothing (ENOENT), and with a file that is not a socket at
// the path (ENOTSOCK) it records nothing either; both are *os.PathError of
// Op "socketfile" for the caller's path, for a long path (link-dir) and a
// short one (standard). Once a socket is bound by the spelling it records
// it; a socket bound at the path in its place afterwards (the first renamed
// away) is not removed by its Remove.
func TestSocketFileOfAResolveRecordsOnlyASocketBoundByTheSpelling(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	for _, p := range []string{nest(t, tmpDir(t), 300, "s.sock"), tmpDir(t) + "/s.sock"} {
		f := resolveBindIn(t, w, p, Options{})
		for _, c := range []struct {
			what  string
			errno syscall.Errno
		}{{"before the bind", syscall.ENOENT}, {"a file there", syscall.ENOTSOCK}} {
			if c.errno == syscall.ENOTSOCK {
				if err := os.WriteFile(p, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := f.SocketFile()
			var pe *os.PathError
			if !errors.Is(err, c.errno) || !errors.As(err, &pe) || pe.Op != "socketfile" || pe.Path != p {
				t.Fatalf("%d bytes, %s: %v", len(p), c.what, err)
			}
		}
		os.Remove(p)
		listenSpelling(t, f.Path)
		sf, err := f.SocketFile()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(p, p+".old"); err != nil {
			t.Fatal(err)
		}
		listenSpelling(t, f.Path)
		if err := sf.Remove(); err != nil || !isSocket(p) {
			t.Fatalf("%d bytes: Remove of a replaced file: %v, socket there %v", len(p), err, isSocket(p))
		}
	}
}

// ResolveForBind only spells: where only a spelling relative to a directory
// held open reaches the path (a file name no spelling through B fits, and
// bind(2) not measured to follow a symlink), it is ENAMETOOLONG with standard
// and link-dir skipped and no E left. Where bind(2) is measured to follow
// (macOS, NetBSD) link-file spells it (Linked, not Removable), and where this
// kernel does follow, the net package binds it at the path.
// ResolveForConnect is ENAMETOOLONG when B can be made nowhere a spelling
// fits (an Options.Base too deep for its real path, and no /proc for
// procB), with standard and link-file skipped, and the B is not made.
func TestAResolveNoSpellingFitsIsENAMETOOLONG(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsNo })
	p := tmpDir(t) + "/" + strings.Repeat("s", 100)
	_, err := w.resolveForBind(p, Options{})
	re := wantResolveError(t, "for bind", p, err, syscall.ENAMETOOLONG)
	if want := []route{routeStandard, routeLinkDir}; !slices.Equal(skippedNames(t, re.Skipped), want) {
		t.Fatalf("skipped %v, want %v", re.Skipped, want)
	}
	if es := dEntries(t, w.auto); len(es) != 0 {
		t.Fatalf("D holds %v", es)
	}

	wf, _ := testWorld(t, func(c *config, _ testPlaces) { c.bindFollows = followsYes })
	f := resolveBindIn(t, wf, p, Options{})
	if f.Route != string(routeLinkFile) || f.Spelling.Kind != Linked || f.Spelling.Removable {
		t.Fatalf("followsYes: %s %+v", f.Route, f.Spelling)
	}
	if want := []route{routeStandard, routeLinkDir}; !slices.Equal(skippedNames(t, f.Skipped), want) {
		t.Fatalf("skipped %v, want %v", f.Skipped, want)
	}
	if bindFollowsSymlink() {
		listenSpelling(t, f.Path)
		if !isSocket(p) {
			t.Fatal("no socket at the path through link-file's spelling")
		}
	}

	wp, _ := testWorld(t, func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc" })
	g := nest(t, tmpDir(t), 120, "b")
	_, err = wp.resolveForConnect(p, Options{Base: g})
	re = wantResolveError(t, "for connect", p, err, syscall.ENAMETOOLONG)
	if want := []route{routeStandard, routeLinkFile}; !slices.Equal(skippedNames(t, re.Skipped), want) {
		t.Fatalf("skipped %v, want %v", re.Skipped, want)
	}
	if exists(g) {
		t.Fatal("the given B was made")
	}
}

// ResolveForConnect does not look at UsableCrossUID (a connect's spelling
// stays with no peer): with or without it, the spelling goes through the
// process's D in the B of /tmp, by procB where /proc can be used and by the
// real path of B where it cannot. ResolveForBind with UsableCrossUID and no
// B others can follow does not fall back to a spelling others cannot follow:
// ENAMETOOLONG. Options.Base gives the B and the base "given".
func TestResolveTakesItsBAsTheBindDoes(t *testing.T) {
	t.Parallel()
	for _, proc := range []bool{true, false} {
		w, _ := testWorld(t, func(c *config, _ testPlaces) {
			if !proc {
				c.proc = "/nonexistent/proc"
			}
		})
		p := nest(t, tmpDir(t), 300, "s.sock")
		for _, cross := range []bool{false, true} {
			fc := resolveConnectIn(t, w, p, Options{UsableCrossUID: cross})
			viaProc := proc && procHere()
			if w.auto == nil || w.auto.d == nil || strings.HasPrefix(fc.Path, "/proc/") != viaProc ||
				!viaProc && !strings.HasPrefix(fc.Path, w.auto.real+"/"+w.auto.d.name+"/") {
				t.Fatalf("proc %v, cross %v: %q (B %v)", proc, cross, fc.Path, w.auto)
			}
		}
		_, err := w.resolveForBind(p, Options{UsableCrossUID: true})
		wantResolveError(t, "cross-uid with no B others can follow", p, err, syscall.ENAMETOOLONG)
	}
	w, _ := testWorld(t, nil)
	g := tmpDir(t) + "/b"
	p := nest(t, tmpDir(t), 300, "s.sock")
	f := resolveBindIn(t, w, p, Options{Base: g})
	if f.Base != "given" || !exists(g) || !strings.Contains(f.Path, "/") {
		t.Fatalf("given: %q %q", f.Base, f.Path)
	}
	listenSpelling(t, f.Path)
	if !isSocket(p) {
		t.Fatal("no socket at the path through the given B")
	}
}

// What a resolve checks before any spelling, as Listen and Dial do: a
// missing directory is ENOENT and one that is a file ENOTDIR, a NUL is
// EINVAL even in a short path, a long path not ending with a name is EINVAL,
// and so is an Options.Base of the wrong form even for a short path; each is
// an *os.PathError of Op "resolve" for the path, and nothing is made in the
// places. A path the net package takes is spelt as it is: Original when
// absolute, CwdRelative when relative, with standard as the route and
// nothing skipped (its Cleanup does nothing); so is an abstract name of any
// length (Linux), whose SocketFile has no file to record or remove.
func TestResolveChecksThePathFirstAndSpellsAShortOneAsItIs(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	d := tmpDir(t)
	if err := os.WriteFile(d+"/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("d", 120)
	for _, c := range []struct {
		path  string
		opts  Options
		errno syscall.Errno
	}{
		{d + "/missing/" + long, Options{}, syscall.ENOENT},
		{d + "/file/" + long, Options{}, syscall.ENOTDIR},
		{d + "/a\x00b", Options{}, syscall.EINVAL},
		{nest(t, tmpDir(t), 300, "x") + "/", Options{}, syscall.EINVAL},
		{d + "/s.sock", Options{Base: "rel"}, syscall.EINVAL},
	} {
		_, err := w.resolveForBind(c.path, c.opts)
		wantResolveError(t, "for bind "+strconv.Quote(c.path), c.path, err, c.errno)
		_, err = w.resolveForConnect(c.path, c.opts)
		wantResolveError(t, "for connect "+strconv.Quote(c.path), c.path, err, c.errno)
	}
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertEmpty(t, p)
	}
	for _, c := range []struct {
		path string
		kind SpellingKind
	}{{d + "/s.sock", Original}, {"s.sock", CwdRelative}, {"@sockat-resolve-" + strings.Repeat("a", 120), Original}} {
		if strings.HasPrefix(c.path, "@") && !abstractNames {
			continue
		}
		f := resolveBindIn(t, w, c.path, Options{})
		if f.Path != stdSpelling(c.path) || f.Spelling != (Spelling{Kind: c.kind}) || f.Route != string(routeStandard) || f.Base != "" || len(f.Skipped) != 0 {
			t.Fatalf("for bind %s: %+v", c.path, f)
		}
		fc := resolveConnectIn(t, w, c.path, Options{})
		if fc.Path != stdSpelling(c.path) || fc.Route != string(routeStandard) || len(fc.Skipped) != 0 || fc.Cleanup() != nil {
			t.Fatalf("for connect %s: %+v", c.path, fc)
		}
		if strings.HasPrefix(c.path, "@") {
			sf, err := f.SocketFile()
			if err != nil || sf.Remove() != nil {
				t.Fatalf("SocketFile of an abstract name: %v", err)
			}
		}
	}
}

// A relative path the net package takes is CwdRelative: ForBind's
// SocketFile records the file only while the cwd is the one of the resolve,
// and is an error of Op "socketfile" after a change, even though the path
// then names a socket too: an unrelated one of the same name in the new cwd. The cwd is the
// process's, hence a child whose cwd is a fresh directory.
func TestSocketFileOfACwdRelativeResolveChecksTheCwd(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	w, _ := testWorld(t, nil)
	f := resolveBindIn(t, w, "s.sock", Options{})
	listenSpelling(t, f.Path)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	other := tmpDir(t)
	listenSpelling(t, other+"/s.sock")
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	_, err = f.SocketFile()
	var pe *os.PathError
	if !errors.Is(err, errCwdChangedSinceResolve) || !errors.As(err, &pe) || pe.Op != "socketfile" {
		t.Fatalf("after a change of the cwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	if _, err := f.SocketFile(); err != nil {
		t.Fatalf("back in the cwd of the resolve: %v", err)
	}
}

// ForBind's SocketFile of a CwdRelative spelling compares the cwd with the
// resolve's after the real path is found too (SPEC "remove": the cwd is
// compared after the realpath that records the file), not only before: the
// cwd is the process's, and another goroutine may change it between the
// comparison and the realpath. Here it changes right then (socketFileReal,
// in a child whose cwd is a fresh directory), to a directory holding an
// unrelated socket of the same name: SocketFile must be the error of a
// changed cwd, and not record (and so let Remove remove) that other socket.
func TestSocketFileOfACwdRelativeResolveChecksTheCwdAfterTheRealPath(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	w, _ := testWorld(t, nil)
	f := resolveBindIn(t, w, "s.sock", Options{})
	listenSpelling(t, f.Path)
	other := tmpDir(t)
	listenSpelling(t, other+"/s.sock")
	socketFileReal = func(path string) (string, error) {
		if err := os.Chdir(other); err != nil {
			t.Fatal(err)
		}
		return realFile(path)
	}
	t.Cleanup(func() { socketFileReal = realFile })
	sf, err := f.SocketFile()
	var pe *os.PathError
	if !errors.Is(err, errCwdChangedSinceResolve) || !errors.As(err, &pe) || pe.Op != "socketfile" {
		t.Fatalf("with the cwd changed before the real path: %v (recorded %+v)", err, sf)
	}
	if !isSocket(other + "/s.sock") {
		t.Fatal("the unrelated socket in the new cwd is gone")
	}
}

// The public functions go through the process's world (in the tests, the
// place TestMain made): a long path is spelt for a bind and for a connect,
// the net package binds and connects by them, and their Cleanup leaves no E.
func TestResolveForBindAndForConnect(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 300, "s.sock")
	f, err := ResolveForBind(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	l := listenSpelling(t, f.Path)
	fc, err := ResolveForConnect(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ping(t, l, dialSpelling(t, fc.Path))
	if err := errors.Join(fc.Cleanup(), f.Cleanup()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fc.Path, filepath.Dir(f.Path)} {
		if _, err := os.Lstat(s); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s after Cleanup: %v", s, err)
		}
	}
}
