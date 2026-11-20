package sockat

// ListenEx's SocketFile: Remove removes the socket file later, Cleanup the E
// the spelling goes through (SPEC "remove と cleanup").
//
// No listener of ListenEx removes its socket file on Close (short paths
// included), so removing it is Remove's alone. Remove remembers the file
// bound by its real path and (st_dev, st_ino) and removes it only while that
// path still names that file; it does not close the listener (Go's own Close
// is the way to do that); a call after a success does nothing, and one after
// a failure tries again.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// exPaths are a short path (the net package itself) and a long one (link-dir
// through the real /tmp's B), each in a directory of its own.
func exPaths(t *testing.T) []string {
	t.Helper()
	return []string{tmpDir(t) + "/s.sock", nest(t, tmpDir(t), 600, "s.sock")}
}

// listenExAt is ListenEx(p) with the default Options, failing the test on an
// error; it returns the listener (closed at the end of the test) and
// Bound.File's Remove.
func listenExAt(t *testing.T, p string) (*net.UnixListener, func() error) {
	t.Helper()
	l, b, err := ListenEx(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if !isSocket(p) {
		t.Fatalf("no socket at %s", p)
	}
	return l, b.File.Remove
}

// remove removes the socket file and leaves the listener open: it still
// takes calls (a past deadline makes Accept time out rather than report it
// closed). The socket can no longer be dialed by its path.
func TestRemoveRemovesTheSocketFileAndLeavesTheListenerOpen(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		l, remove := listenExAt(t, p)
		if err := remove(); err != nil {
			t.Fatalf("%d bytes: %v", len(p), err)
		}
		if exists(p) {
			t.Fatalf("%d bytes: the socket file is still there", len(p))
		}
		l.SetDeadline(time.Now().Add(-time.Second))
		if _, err := l.Accept(); !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("%d bytes: the listener: %v, want it open", len(p), err)
		}
		if _, err := Dial(p); !errors.Is(err, syscall.ENOENT) {
			t.Fatalf("%d bytes: dial after remove: %v", len(p), err)
		}
	}
}

// Close does not remove the socket file, for a short path too (where
// net.ListenUnix's own listener would): the file stays until remove, which
// removes it after the Close as well.
func TestCloseLeavesTheFileAndRemoveRemovesItAfterwards(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		l, remove := listenExAt(t, p)
		l.Close()
		if !isSocket(p) {
			t.Fatalf("%d bytes: Close removed the socket file", len(p))
		}
		if err := remove(); err != nil || exists(p) {
			t.Fatalf("%d bytes: remove after Close: %v, file there %v", len(p), err, exists(p))
		}
	}
}

// A file that replaced the socket at the path meanwhile (a regular file, or
// another socket bound there by someone else) is not the one bound: remove
// leaves it and reports nil. Nor is a file at all removed when the socket is
// already gone.
func TestRemoveLeavesWhatReplacedTheSocket(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		_, remove := listenExAt(t, p)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := remove(); err != nil {
			t.Fatalf("%d bytes: %v", len(p), err)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != "other" {
			t.Fatalf("%d bytes: the replacing file: %q, %v", len(p), b, err)
		}

		os.Remove(p)
		_, remove = listenExAt(t, p)
		os.Remove(p)
		other := listenAt(t, p)
		if err := remove(); err != nil || !isSocket(p) {
			t.Fatalf("%d bytes: another socket at the path: %v, there %v", len(p), err, isSocket(p))
		}
		other.Close()
		os.Remove(p)

		_, remove = listenExAt(t, p)
		os.Remove(p)
		if err := remove(); err != nil {
			t.Fatalf("%d bytes: remove of a gone file: %v", len(p), err)
		}
	}
}

// A second remove does nothing, even when what is at the path then is the
// very file bound (same (st_dev, st_ino): a hard link kept before the first
// remove and renamed back to the path), which a remove that checked again
// would remove; nor when a new file has been put there.
func TestASecondRemoveDoesNothing(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		_, remove := listenExAt(t, p)
		keep := filepath.Dir(p) + "/k"
		if err := os.Link(p, keep); err != nil {
			t.Fatal(err)
		}
		if err := remove(); err != nil || exists(p) {
			t.Fatalf("%d bytes: first remove: %v, file there %v", len(p), err, exists(p))
		}
		if err := os.Rename(keep, p); err != nil {
			t.Fatal(err)
		}
		if err := remove(); err != nil || !isSocket(p) {
			t.Fatalf("%d bytes: second remove: %v, the same file removed again %v", len(p), err, !isSocket(p))
		}
		os.Remove(p)
		if err := os.WriteFile(p, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := remove(); err != nil {
			t.Fatalf("%d bytes: third remove: %v", len(p), err)
		}
		if b, _ := os.ReadFile(p); string(b) != "new" {
			t.Fatalf("%d bytes: a later remove removed a new file", len(p))
		}
	}
}

// remove remembers the socket file by the real path of its directory, so a
// symlink on the way retargeted since does not send it elsewhere: with
// <d>/via -> <d>/a at the listen and -> <d>/b at the remove, the socket in a
// is removed and a file of the same name in b is not.
func TestRemoveRemembersTheRealPath(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	for _, sub := range []string{"/a", "/b"} {
		if err := os.Mkdir(d+sub, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(d+"/a", d+"/via"); err != nil {
		t.Fatal(err)
	}
	_, remove := listenExAt(t, d+"/via/s.sock")
	if err := os.WriteFile(d+"/b/s.sock", []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d + "/via"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(d+"/b", d+"/via"); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if exists(d + "/a/s.sock") {
		t.Fatal("the socket file in a is still there")
	}
	if b, _ := os.ReadFile(d + "/b/s.sock"); string(b) != "b" {
		t.Fatal("the file in b was removed")
	}
}

// A relative path is remembered against the cwd of the listen: after a chdir
// to a directory with a file of the same name, remove removes the socket in
// the first directory, not that file. Runs in a child process because it
// changes the cwd.
func TestRemoveOfARelativePathIsNotRetargetedByAChdir(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	if err := os.Mkdir(d+"/other", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/other/s.sock", []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	runChild(t, "TestChildRemoveAfterAChdir", d)
	if exists(d + "/s.sock") {
		t.Fatal("the socket file is still there")
	}
	if b, _ := os.ReadFile(d + "/other/s.sock"); string(b) != "other" {
		t.Fatal("the file in the new cwd was removed")
	}
}

func TestChildRemoveAfterAChdir(t *testing.T) {
	asChild(t)
	_, remove := listenExAt(t, "s.sock")
	if err := os.Chdir("other"); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
}

// A relative path is resolved against the cwd twice, once for the real path
// ListenEx records and once by the bind, and another goroutine may change the
// cwd in between. Then the bind lands below the new cwd while the record would
// name the same name below the old one: an unrelated file there, which remove
// would remove. So when the cwd differs after the bind, ListenEx treats the
// file as not recorded (SPEC bind_ex): it returns a *RouteError reaching
// errNotRecorded and no listener or Bound, leaves the file of the same name
// in the old cwd as it was, and does not remove the socket file it bound in
// the new cwd either (a file not recorded is left). Without a change of the
// cwd, the same relative path listens and remove removes its socket as
// before. Both a short path (the net package) and a long one (link-dir) are
// checked, in a child process each, since the cwd belongs to the whole
// process.
func TestListenExWhoseCwdChangesBeforeTheBindRecordsNothing(t *testing.T) {
	t.Parallel()
	long := strings.Repeat(strings.Repeat("d", 60)+"/", 3) + "s.sock"
	if len(long) <= directMax {
		t.Fatalf("%d bytes is not long", len(long))
	}
	rels := []string{"s.sock", long}
	for _, rel := range rels {
		d := tmpDir(t)
		for _, cwd := range []string{"a", "b"} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(d, cwd, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		old, moved := filepath.Join(d, "a", rel), filepath.Join(d, "b", rel)

		// Unchanged cwd: listens, and remove (in the child) removes it.
		runChild(t, "TestChildListenExWithACwdChange", d+"/a", "SOCKAT_REL="+rel, "SOCKAT_CHDIR=")
		if exists(old) {
			t.Fatalf("%d bytes: the socket file is still there after remove", len(rel))
		}

		// Changed cwd: not recorded; neither file is touched.
		if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		runChild(t, "TestChildListenExWithACwdChange", d+"/a", "SOCKAT_REL="+rel, "SOCKAT_CHDIR="+d+"/b")
		if b, err := os.ReadFile(old); err != nil || string(b) != "old" {
			t.Fatalf("%d bytes: the file in the old cwd: %q, %v", len(rel), b, err)
		}
		if !isSocket(moved) {
			t.Fatalf("%d bytes: the socket file bound in the new cwd is not there", len(rel))
		}
	}
}

func TestChildListenExWithACwdChange(t *testing.T) {
	asChild(t)
	rel, to := os.Getenv("SOCKAT_REL"), os.Getenv("SOCKAT_CHDIR")
	bindEx = func(w *world, o op, path string, opts Options, tr *trace) (endpoint, bound, error) {
		if to != "" {
			if err := os.Chdir(to); err != nil {
				t.Fatal(err)
			}
		}
		return w.bind(o, path, opts, tr)
	}
	t.Cleanup(func() { bindEx = (*world).bind })
	l, b, err := ListenEx(rel, Options{})
	if to == "" {
		if err != nil {
			t.Fatal(err)
		}
		l.Close()
		if err := b.File.Remove(); err != nil {
			t.Fatal(err)
		}
		return
	}
	re := routeErr(t, err)
	if !errors.Is(err, errNotRecorded) || l != nil || b != nil || re.Route == "" {
		t.Fatalf("%v, %v, route %q: %v", l, b, re.Route, err)
	}
}

// ListenEx records the socket file (its real path and (st_dev, st_ino)) the
// moment the bind returns, so that remove removes only that file. When the
// bind went through but the file cannot be recorded (here: it is gone by the
// time of the lstat), ListenEx must not hand out a listener whose remove it
// cannot make safe: it closes the listener and returns a *RouteError reaching
// errNotRecorded, whose Route is the route that bound, with no listener or
// Bound. The listener really is closed (a second Close says so), the E of a
// spelling through B is cleaned up (nobody could do it later), and the path
// can be listened on again. Both a short path (the net package) and a
// long one (the routes through D, where a listen of a long path has one) are
// checked, in a child process each, since the seam bindEx is the whole
// process's.
func TestListenExThatCannotRecordTheBoundFileClosesTheListener(t *testing.T) {
	t.Parallel()
	long := strings.Repeat(strings.Repeat("d", 60)+"/", 3) + "s.sock"
	if len(long) <= directMax {
		t.Fatalf("%d bytes is not long", len(long))
	}
	rels := []string{"s.sock", long}
	for _, rel := range rels {
		d := tmpDir(t)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(d, rel)), 0o700); err != nil {
			t.Fatal(err)
		}
		runChild(t, "TestChildListenExThatCannotRecordTheBoundFile", d, "SOCKAT_PATH="+filepath.Join(d, rel))
	}
}

func TestChildListenExThatCannotRecordTheBoundFile(t *testing.T) {
	asChild(t)
	path := os.Getenv("SOCKAT_PATH")
	var bl *net.UnixListener
	var e *link
	bindEx = func(w *world, o op, p string, opts Options, tr *trace) (endpoint, bound, error) {
		l, b, err := w.listen(p, opts, tr)
		if err != nil {
			return nil, b, err
		}
		bl, e = l, b.link
		// The file the bind made is gone before ListenEx looks at it.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return l, b, nil
	}
	t.Cleanup(func() { bindEx = (*world).bind })
	l, b, err := ListenEx(path, Options{})
	if bl == nil {
		t.Fatalf("the bind did not go through: %v", err)
	}
	re := routeErr(t, err)
	if !errors.Is(err, errNotRecorded) || l != nil || b != nil || re.Route == "" {
		t.Fatalf("%v, %v, route %q: %v", l, b, re.Route, err)
	}
	if err := bl.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("the listener was not closed by ListenEx: Close again: %v", err)
	}
	if e != nil && exists(e.path()) {
		t.Fatalf("the E of the spelling (%s) was left", e.path())
	}
	bindEx = (*world).bind
	l2, err := Listen(path)
	if err != nil {
		t.Fatalf("listen again on the same path: %v", err)
	}
	l2.Close()
}

// A path ListenEx refuses comes back with its error and no listener or
// Bound; an abstract name (where the kernel has abstract names: Linux,
// Android) is Original through standard and has no file, so its Remove does
// nothing.
func TestListenExErrorsAndAbstractNames(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	l, b, err := ListenEx(d+"/nope/s.sock", Options{})
	if !errors.Is(err, syscall.ENOENT) || l != nil || b != nil {
		t.Fatalf("%v, %v, %v", l, b, err)
	}
	if !abstractNames {
		return
	}
	l, b, err = ListenEx("@sockat-ex-"+t.Name(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if b.Spelling != (Spelling{Kind: Original}) || b.Route != "standard" {
		t.Fatalf("an abstract name: %+v", b)
	}
	if err := b.File.Remove(); err != nil {
		t.Fatal(err)
	}
}

// When remove cannot check the file (lstat fails otherwise than for a
// missing file: here EACCES, its directory having lost search permission), it
// does not know whether the file is the one bound, so it reports an error
// rather than nil, and removes nothing. Once the file can be checked again, a
// later call does nothing (remove runs once). A file that is merely gone is
// nil (TestRemoveLeavesWhatReplacedTheSocket and the others). Root ignores
// the permission, so as root it runs in a child that has dropped to nobody
// (unprivileged), which makes it run in the Linux container too.
func TestRemoveThatCannotCheckTheFileIsAnError(t *testing.T) {
	t.Parallel()
	if !unprivileged(t) {
		return
	}
	for _, p := range exPaths(t) {
		l, remove := listenExAt(t, p)
		l.Close()
		dir := filepath.Dir(p)
		if err := os.Chmod(dir, 0o600); err != nil {
			t.Fatal(err)
		}
		err := remove()
		os.Chmod(dir, 0o700)
		if !errors.Is(err, syscall.EACCES) {
			t.Fatalf("%d bytes: %v, want EACCES", len(p), err)
		}
		if !exists(p) {
			t.Fatalf("%d bytes: the socket file was removed", len(p))
		}
		os.Remove(p)
	}
}

// aliasCase is a symlink of the caller's at the path to bind, and where it
// leads: links are the symlinks from path on, each to the next (relative
// when in the same directory, absolute otherwise), actual the file a bind
// that follows them makes.
type aliasCase struct {
	what         string
	path, actual string
	links        []string
}

// aliasCases are symlinks at a short path (the net package) and a long one
// (link-dir), holding a relative target, an absolute one into another
// directory, and a chain of two, each in directories of their own. The
// symlinks are made; the socket file is not.
func aliasCases(t *testing.T) []aliasCase {
	t.Helper()
	mk := func(what, path, actual string, links ...string) aliasCase {
		t.Helper()
		chain := append([]string{path}, links...)
		for i, l := range chain {
			next := actual
			if i+1 < len(chain) {
				next = chain[i+1]
			}
			target := next
			if filepath.Dir(next) == filepath.Dir(l) {
				target = filepath.Base(next)
			}
			if err := os.Symlink(target, l); err != nil {
				t.Fatal(err)
			}
		}
		return aliasCase{what: what, path: path, actual: actual, links: chain}
	}
	short := tmpDir(t)
	long := filepath.Dir(nest(t, tmpDir(t), 600, "alias.sock"))
	abs := tmpDir(t)
	chain := tmpDir(t)
	return []aliasCase{
		mk("short, relative", short+"/alias.sock", short+"/actual.sock"),
		mk("long, relative", long+"/alias.sock", long+"/actual.sock"),
		mk("short, absolute", abs+"/alias.sock", tmpDir(t)+"/actual.sock"),
		mk("short, a chain", chain+"/alias.sock", chain+"/actual.sock", chain+"/alias2.sock"),
	}
}

// assertLinks checks that the caller's symlinks are all still there, as
// symlinks: Remove and a refused bind must not take them.
func assertLinks(t *testing.T, c aliasCase) {
	t.Helper()
	for _, l := range c.links {
		if fi, err := os.Lstat(l); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s: the caller's symlink %s is gone or replaced: %v", c.what, l, err)
		}
	}
}

// A symlink of the caller's at the last component of the path (alias.sock ->
// actual.sock): where bind(2) follows it (macOS, NetBSD) the socket is made
// where it leads, so ListenEx records the socket file reached (SPEC
// "remove"), and Remove removes actual.sock and leaves the caller's
// symlink, which is not ours to remove. A relative target is taken relative
// to the symlink's directory, as the kernel takes it; a chain is followed to
// its end. Where bind(2) does not follow it (Linux, FreeBSD, ...), the bind
// fails as the standard one does, with EADDRINUSE, and leaves the symlink
// and makes nothing where it leads.
func TestRemoveAfterABindThroughASymlinkRemovesWhereItLeads(t *testing.T) {
	t.Parallel()
	for _, c := range aliasCases(t) {
		l, b, err := ListenEx(c.path, Options{})
		if !bindFollowsSymlink() {
			if !errors.Is(err, syscall.EADDRINUSE) || l != nil || b != nil {
				t.Fatalf("%s: %v, %v, %v; want EADDRINUSE", c.what, l, b, err)
			}
			if exists(c.actual) {
				t.Fatalf("%s: a refused bind made %s", c.what, c.actual)
			}
			assertLinks(t, c)
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		t.Cleanup(func() { l.Close() })
		if !isSocket(c.actual) {
			t.Fatalf("%s: no socket at %s", c.what, c.actual)
		}
		if err := b.File.Remove(); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if exists(c.actual) {
			t.Fatalf("%s: Remove left the socket file %s", c.what, c.actual)
		}
		assertLinks(t, c)
	}
}

// ForBind.SocketFile follows a symlink of the caller's at the last component
// of the path as ListenEx does (SPEC "ForBind の SocketFile"): after a bind by
// the spelling that went through it, it records the socket file where it
// leads, and its Remove removes that and leaves the symlink. Before the
// bind the symlink leads to nothing (ENOENT), and a file that is not a
// socket where it leads is not recorded (ENOTSOCK), both *os.PathError of
// Op "socketfile" for the caller's path, and neither removes anything. Only
// where bind(2) follows a symlink: elsewhere no bind through one succeeds
// (TestRemoveAfterABindThroughASymlinkRemovesWhereItLeads).
func TestSocketFileOfAResolveFollowsASymlinkAtThePath(t *testing.T) {
	t.Parallel()
	if !bindFollowsSymlink() {
		t.Skip("bind(2) here does not follow a symlink in the last component")
	}
	w, _ := testWorld(t, nil)
	for _, c := range aliasCases(t) {
		f := resolveBindIn(t, w, c.path, Options{})
		wantErr := func(when string, errno syscall.Errno) {
			t.Helper()
			_, err := f.SocketFile()
			var pe *os.PathError
			if !errors.Is(err, errno) || !errors.As(err, &pe) || pe.Op != "socketfile" || pe.Path != c.path {
				t.Fatalf("%s, %s: %v, want %v", c.what, when, err, errno)
			}
		}
		wantErr("before the bind", syscall.ENOENT)
		if err := os.WriteFile(c.actual, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		wantErr("a file where it leads", syscall.ENOTSOCK)
		if !exists(c.actual) {
			t.Fatalf("%s: the file where the symlink leads was removed", c.what)
		}
		os.Remove(c.actual)
		listenSpelling(t, f.Path)
		if !isSocket(c.actual) {
			t.Fatalf("%s: no socket at %s", c.what, c.actual)
		}
		sf, err := f.SocketFile()
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if err := sf.Remove(); err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if exists(c.actual) {
			t.Fatalf("%s: Remove left the socket file %s", c.what, c.actual)
		}
		assertLinks(t, c)
	}
}

// When what a symlink at the path leads to is not a socket by the time
// ListenEx looks (here the socket the bind made is replaced by a regular
// file), the socket file cannot be recorded (SPEC "remove"):
// ListenEx closes the listener, cleans up the E, and returns a *RouteError
// reaching errNotRecorded with no listener or Bound. It removes nothing: the
// file where the symlink leads and the caller's symlink stay. Only where
// bind(2) follows a symlink; in a child process, since the seam bindEx is the
// whole process's. A short path and a long one (link-dir) are both checked.
func TestListenExThroughASymlinkToANonSocketRecordsNothing(t *testing.T) {
	t.Parallel()
	if !bindFollowsSymlink() {
		t.Skip("bind(2) here does not follow a symlink in the last component")
	}
	for _, c := range aliasCases(t)[:2] {
		runChild(t, "TestChildListenExThroughASymlinkToANonSocket", filepath.Dir(c.path), "SOCKAT_PATH="+c.path, "SOCKAT_ACTUAL="+c.actual)
		if fi, err := os.Lstat(c.actual); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s: the file where the symlink leads: %v", c.what, err)
		}
		assertLinks(t, c)
	}
}

func TestChildListenExThroughASymlinkToANonSocket(t *testing.T) {
	asChild(t)
	path, actual := os.Getenv("SOCKAT_PATH"), os.Getenv("SOCKAT_ACTUAL")
	var bl *net.UnixListener
	var e *link
	bindEx = func(w *world, o op, p string, opts Options, tr *trace) (endpoint, bound, error) {
		l, b, err := w.listen(p, opts, tr)
		if err != nil {
			return nil, b, err
		}
		bl, e = l, b.link
		// The socket the bind made, where the symlink leads, is replaced by
		// a regular file before ListenEx looks at it.
		if err := os.Remove(actual); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(actual, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return l, b, nil
	}
	t.Cleanup(func() { bindEx = (*world).bind })
	l, b, err := ListenEx(path, Options{})
	if bl == nil {
		t.Fatalf("the bind did not go through: %v", err)
	}
	re := routeErr(t, err)
	if !errors.Is(err, errNotRecorded) || l != nil || b != nil || re.Route == "" {
		t.Fatalf("%v, %v, route %q: %v", l, b, re.Route, err)
	}
	if err := bl.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("the listener was not closed by ListenEx: Close again: %v", err)
	}
	if e != nil && exists(e.path()) {
		t.Fatalf("the E of the spelling (%s) was left", e.path())
	}
}

// The cleanups inside a call keep a failure to remove E to themselves (the
// call's outcome is the bind's or the connect's), but a Cleanup the caller
// calls reports it, and is tried again by the next call until it succeeds
// (SPEC "remove と cleanup": a failure kept and returned for ever would leave
// no way to clean up). With D made read-only, so that E cannot be unlinked
// (EACCES), Cleanup of ListenEx's SocketFile, of a ForBind and of a
// ForConnect returns an *os.PathError of Op "unlink" for E on every call and
// leaves E; once D's mode is given back, the next Cleanup removes E and
// returns nil, and a Cleanup after that success does nothing and returns nil.
// Remove reports the cleanup's error too, after removing the socket file,
// which it can (its directory is not D); the next Remove, with D given back,
// removes E and does not go back to the file: its directory is then made
// unsearchable, so a second look at the file would fail with EACCES. Root
// ignores the permission, so as root it runs in a child that has dropped to
// nobody (unprivileged).
func TestCleanupRetriesAfterAFailureToRemoveE(t *testing.T) {
	t.Parallel()
	if !unprivileged(t) {
		return
	}
	w, _ := testWorld(t, nil)
	// chmod sets the mode of d until the returned function gives it back.
	chmod := func(d string, mode os.FileMode) func() {
		t.Helper()
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(d, mode); err != nil {
			t.Fatal(err)
		}
		back := func() { os.Chmod(d, fi.Mode().Perm()) }
		t.Cleanup(back)
		return back
	}
	readOnly := func(e *link) func() { return chmod(filepath.Dir(e.path()), 0o500) }
	// retried fails to clean up E while D is read-only, every time, then
	// gives D back and expects the next cleanup to remove E and the one
	// after it to do nothing.
	retried := func(what string, e *link, cleanup func() error) {
		t.Helper()
		back := readOnly(e)
		for range 2 {
			err := cleanup()
			var pe *os.PathError
			if !errors.Is(err, syscall.EACCES) || !errors.As(err, &pe) || pe.Op != "unlink" || pe.Path != e.path() {
				t.Fatalf("%s: Cleanup: %v, want EACCES unlinking %s", what, err, e.path())
			}
		}
		if !exists(e.path()) {
			t.Fatalf("%s: E is gone", what)
		}
		back()
		if err := cleanup(); err != nil || exists(e.path()) {
			t.Fatalf("%s: Cleanup after D is given back: %v, E there %v; want nil and E gone", what, err, exists(e.path()))
		}
		if err := cleanup(); err != nil {
			t.Fatalf("%s: Cleanup after a success: %v", what, err)
		}
	}

	p := nest(t, tmpDir(t), 600, "s.sock")
	_, b := listenExIn(t, w, p, Options{})
	e := b.File.clean.e
	if e == nil {
		t.Fatalf("ListenEx of %d bytes made no E (%s)", len(p), b.Route)
	}
	retried("ListenEx", e, b.File.Cleanup)

	p = nest(t, tmpDir(t), 600, "s.sock")
	_, b = listenExIn(t, w, p, Options{})
	e = b.File.clean.e
	back := readOnly(e)
	if err := b.File.Remove(); !errors.Is(err, syscall.EACCES) || exists(p) {
		t.Fatalf("Remove: %v, socket file there %v; want EACCES and the file removed", err, exists(p))
	}
	back()
	chmod(filepath.Dir(b.File.file), 0)
	if err := b.File.Remove(); err != nil || exists(e.path()) {
		t.Fatalf("Remove after D is given back: %v, E there %v; want nil (the file not looked at again) and E gone", err, exists(e.path()))
	}

	p = nest(t, tmpDir(t), 600, "s.sock")
	fb := resolveBindIn(t, w, p, Options{})
	if fb.clean.e == nil {
		t.Fatalf("ResolveForBind made no E (%s)", fb.Route)
	}
	retried("ForBind", fb.clean.e, fb.Cleanup)

	l := listenAt(t, p)
	defer l.Close()
	fc := resolveConnectIn(t, w, p, Options{})
	if fc.clean.e == nil {
		t.Fatalf("ResolveForConnect made no E (%s)", fc.Route)
	}
	retried("ForConnect", fc.clean.e, fc.Cleanup)
}

// Remove tries the socket file again after failing to remove it, and only
// while the path still names the file bound (SPEC "remove と cleanup": a
// failed removal is retried; SPEC "remove": what is no longer the file bound
// is left). With the socket file's directory read-only, Remove returns EACCES
// and leaves the file (the cleanup of E, in D, succeeds); with the mode given
// back, the next Remove removes it. Where the file is replaced between the
// failure and the retry, the retry checks (st_dev, st_ino) again and leaves
// the replacement, returning nil. Root ignores the permission, so as root it
// runs in a child that has dropped to nobody (unprivileged).
func TestRemoveRetriesTheFileAfterAFailure(t *testing.T) {
	t.Parallel()
	if !unprivileged(t) {
		return
	}
	w, _ := testWorld(t, nil)
	for _, replace := range []bool{false, true} {
		p := nest(t, tmpDir(t), 600, "s.sock")
		_, b := listenExIn(t, w, p, Options{})
		dir := filepath.Dir(b.File.file)
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, fi.Mode().Perm()) })
		err = b.File.Remove()
		var pe *os.PathError
		if !errors.Is(err, syscall.EACCES) || !errors.As(err, &pe) || !exists(p) {
			t.Fatalf("replace %v: Remove in a read-only directory: %v, file there %v; want EACCES and the file left", replace, err, exists(p))
		}
		if e := b.File.clean.e; e != nil && exists(e.path()) {
			t.Fatalf("replace %v: E (%s) left by the first Remove", replace, e.path())
		}
		if err := os.Chmod(dir, fi.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		if replace {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := b.File.Remove(); err != nil || exists(p) != replace {
			t.Fatalf("replace %v: Remove again: %v, file there %v", replace, err, exists(p))
		}
		if err := b.File.Remove(); err != nil || exists(p) != replace {
			t.Fatalf("replace %v: Remove after a success: %v, file there %v", replace, err, exists(p))
		}
	}
}

// Concurrent calls of Cleanup and Remove remove one at a time, among all
// that share an E, and each thing once (SPEC "remove と cleanup": however
// many calls race, one removal at a time and each done once). The results
// alone cannot show it, as an unlink of what is already gone counts as a
// success, so the unlinks are counted: unlinkE and unlinkFile are replaced by
// wrappers that note how many are in flight in the same group (the things
// sharing an E: ListenEx's E and socket file; a ForBind's E and the socket
// file its SocketFiles name), yielding inside to widen the window, and how
// many ran on each path. Many goroutines, started together, call Remove and
// Cleanup of ListenEx's SocketFile, and Cleanup of a ForBind, and Remove and
// Cleanup of two SocketFiles it gave for the same file (ForBind.SocketFile
// called twice). Every call returns nil, each E and socket file is unlinked
// exactly once, no two unlinks of a group run at a time, and all are gone. Replacing package variables is not safe beside parallel
// tests, so the body runs in a child process of its own; run with -race, it
// also checks the state the calls share.
func TestCleanupAndRemoveUnlinkOnceWhenConcurrent(t *testing.T) {
	t.Parallel()
	if os.Getenv(childEnv) != t.Name() {
		runChild(t, t.Name(), "/")
		return
	}
	var mu sync.Mutex
	group := map[string]string{} // path -> its group, set before the calls
	ran := map[string]int{}
	inFlight := map[string]int{}
	overlaps := map[string]int{} // group -> unlinks begun beside another
	watch := func(path string, unlink func() error) error {
		g := group[path]
		mu.Lock()
		inFlight[g]++
		if inFlight[g] > 1 {
			overlaps[g]++
		}
		mu.Unlock()
		for range 100 {
			runtime.Gosched()
		}
		err := unlink()
		mu.Lock()
		inFlight[g]--
		ran[path]++
		mu.Unlock()
		return err
	}
	unlinkE = func(e *link) error { return watch(e.path(), e.remove) }
	unlinkFile = func(name string) error { return watch(name, func() error { return os.Remove(name) }) }
	t.Cleanup(func() { unlinkE, unlinkFile = (*link).remove, os.Remove })

	w, _ := testWorld(t, nil)
	p := nest(t, tmpDir(t), 600, "s.sock")
	_, b := listenExIn(t, w, p, Options{})
	if b.File.clean.e == nil {
		t.Fatalf("ListenEx of %d bytes made no E (%s)", len(p), b.Route)
	}
	q := nest(t, tmpDir(t), 600, "s.sock")
	fb := resolveBindIn(t, w, q, Options{})
	if fb.clean.e == nil {
		t.Fatalf("ResolveForBind made no E (%s)", fb.Route)
	}
	listenSpelling(t, fb.Path)
	sf, err := fb.SocketFile()
	if err != nil {
		t.Fatal(err)
	}
	sf2, err := fb.SocketFile()
	if err != nil {
		t.Fatal(err)
	}
	if sf2.file != sf.file {
		t.Fatalf("two SocketFiles of one ForBind name %s and %s", sf.file, sf2.file)
	}
	group[b.File.file], group[b.File.clean.e.path()] = "ListenEx", "ListenEx"
	group[sf.file], group[fb.clean.e.path()] = "ForBind", "ForBind"
	calls := []func() error{b.File.Remove, b.File.Cleanup, fb.Cleanup, sf.Cleanup, sf.Remove, sf2.Remove, sf2.Cleanup}

	const n = 64
	start := make(chan struct{})
	errs := make(chan error, n*len(calls))
	var wg sync.WaitGroup
	for i := range n * len(calls) {
		wg.Go(func() {
			<-start
			errs <- calls[i%len(calls)]()
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent call: %v", err)
		}
	}
	if len(overlaps) != 0 {
		t.Fatalf("unlinks of a group overlapped (group: times): %v", overlaps)
	}
	for _, path := range []string{b.File.file, b.File.clean.e.path(), sf.file, fb.clean.e.path()} {
		if ran[path] != 1 {
			t.Fatalf("%s (group %s) unlinked %d times, want 1", path, group[path], ran[path])
		}
		if exists(path) {
			t.Fatalf("%s is left", path)
		}
	}
}
