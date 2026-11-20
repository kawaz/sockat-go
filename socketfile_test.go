package sockat

// ListenEx's SocketFile: Remove removes the socket file later, Cleanup the E
// the spelling goes through (SPEC "remove と cleanup").
//
// No listener of ListenEx removes its socket file on Close (short paths
// included), so removing it is Remove's alone. Remove remembers the file
// bound by its real path and (st_dev, st_ino) and removes it only while that
// path still names that file; it does not close the listener (Go's own Close
// is the way to do that); a second call does nothing.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	bindEx = func(w *world, path string, opts Options, tr *trace) (*net.UnixListener, bound, error) {
		if to != "" {
			if err := os.Chdir(to); err != nil {
				t.Fatal(err)
			}
		}
		return w.listen(path, opts, tr)
	}
	t.Cleanup(func() { bindEx = (*world).listen })
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
	bindEx = func(w *world, p string, opts Options, tr *trace) (*net.UnixListener, bound, error) {
		l, b, err := w.listen(p, opts, tr)
		if err != nil {
			return l, b, err
		}
		bl, e = l, b.link
		// The file the bind made is gone before ListenEx looks at it.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return l, b, nil
	}
	t.Cleanup(func() { bindEx = (*world).listen })
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
	bindEx = (*world).listen
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
