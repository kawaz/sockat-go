package sockat

// ListenEx: Listen, with discard to remove the socket file later.
//
// No listener of sockat's removes its socket file on Close (for a long path
// the address the kernel keeps is not the path; ListenEx makes a short path's
// listener the same), so removing it is discard's alone. discard remembers the
// file it bound by its real path and (st_dev, st_ino) and removes it only
// while that path still names that file; it does not close the listener (Go's
// own Close is the way to do that); a second call does nothing.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// exPaths are a short path (the net package itself) and a long one (the
// central route), each in a directory of its own. The long one only where a
// listen of a long path has a route (not on NetBSD, OpenBSD, illumos).
func exPaths(t *testing.T) []string {
	t.Helper()
	ps := []string{tmpDir(t) + "/s.sock"}
	if longListen() {
		ps = append(ps, nest(t, tmpDir(t), 600, "s.sock"))
	}
	return ps
}

func listenEx(t *testing.T, p string) (*net.UnixListener, func() error) {
	t.Helper()
	l, discard, err := ListenEx(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if !isSocket(p) {
		t.Fatalf("no socket at %s", p)
	}
	return l, discard
}

// discard removes the socket file and leaves the listener open: it still
// takes calls (a past deadline makes Accept time out rather than report it
// closed). The socket can no longer be dialed by its path.
func TestDiscardRemovesTheSocketFileAndLeavesTheListenerOpen(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		l, discard := listenEx(t, p)
		if err := discard(); err != nil {
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
			t.Fatalf("%d bytes: dial after discard: %v", len(p), err)
		}
	}
}

// Close does not remove the socket file, for a short path too (where
// net.ListenUnix's own listener would): the file stays until discard, which
// removes it after the Close as well.
func TestCloseLeavesTheFileAndDiscardRemovesItAfterwards(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		l, discard := listenEx(t, p)
		l.Close()
		if !isSocket(p) {
			t.Fatalf("%d bytes: Close removed the socket file", len(p))
		}
		if err := discard(); err != nil || exists(p) {
			t.Fatalf("%d bytes: discard after Close: %v, file there %v", len(p), err, exists(p))
		}
	}
}

// A file that replaced the socket at the path meanwhile (a regular file, or
// another socket bound there by someone else) is not the one bound: discard
// leaves it and reports nil. Nor is a file at all removed when the socket is
// already gone.
func TestDiscardLeavesWhatReplacedTheSocket(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		_, discard := listenEx(t, p)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := discard(); err != nil {
			t.Fatalf("%d bytes: %v", len(p), err)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != "other" {
			t.Fatalf("%d bytes: the replacing file: %q, %v", len(p), b, err)
		}

		os.Remove(p)
		_, discard = listenEx(t, p)
		os.Remove(p)
		other := listenAt(t, p)
		if err := discard(); err != nil || !isSocket(p) {
			t.Fatalf("%d bytes: another socket at the path: %v, there %v", len(p), err, isSocket(p))
		}
		other.Close()
		os.Remove(p)

		_, discard = listenEx(t, p)
		os.Remove(p)
		if err := discard(); err != nil {
			t.Fatalf("%d bytes: discard of a gone file: %v", len(p), err)
		}
	}
}

// A second discard does nothing, even when what is at the path then is the
// very file bound (same (st_dev, st_ino): a hard link kept before the first
// discard and renamed back to the path), which a discard that checked again
// would remove; nor when a new file has been put there.
func TestASecondDiscardDoesNothing(t *testing.T) {
	t.Parallel()
	for _, p := range exPaths(t) {
		_, discard := listenEx(t, p)
		keep := filepath.Dir(p) + "/k"
		if err := os.Link(p, keep); err != nil {
			t.Fatal(err)
		}
		if err := discard(); err != nil || exists(p) {
			t.Fatalf("%d bytes: first discard: %v, file there %v", len(p), err, exists(p))
		}
		if err := os.Rename(keep, p); err != nil {
			t.Fatal(err)
		}
		if err := discard(); err != nil || !isSocket(p) {
			t.Fatalf("%d bytes: second discard: %v, the same file removed again %v", len(p), err, !isSocket(p))
		}
		os.Remove(p)
		if err := os.WriteFile(p, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := discard(); err != nil {
			t.Fatalf("%d bytes: third discard: %v", len(p), err)
		}
		if b, _ := os.ReadFile(p); string(b) != "new" {
			t.Fatalf("%d bytes: a later discard removed a new file", len(p))
		}
	}
}

// discard remembers the socket file by the real path of its directory, so a
// symlink on the way retargeted since does not send it elsewhere: with
// <d>/via -> <d>/a at the listen and -> <d>/b at the discard, the socket in a
// is removed and a file of the same name in b is not.
func TestDiscardRemembersTheRealPath(t *testing.T) {
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
	_, discard := listenEx(t, d+"/via/s.sock")
	if err := os.WriteFile(d+"/b/s.sock", []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d + "/via"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(d+"/b", d+"/via"); err != nil {
		t.Fatal(err)
	}
	if err := discard(); err != nil {
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
// to a directory with a file of the same name, discard removes the socket in
// the first directory, not that file. Runs in a child process because it
// changes the cwd.
func TestDiscardOfARelativePathIsNotRetargetedByAChdir(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	if err := os.Mkdir(d+"/other", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/other/s.sock", []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	runChild(t, "TestChildDiscardAfterAChdir", d)
	if exists(d + "/s.sock") {
		t.Fatal("the socket file is still there")
	}
	if b, _ := os.ReadFile(d + "/other/s.sock"); string(b) != "other" {
		t.Fatal("the file in the new cwd was removed")
	}
}

func TestChildDiscardAfterAChdir(t *testing.T) {
	asChild(t)
	_, discard := listenEx(t, "s.sock")
	if err := os.Chdir("other"); err != nil {
		t.Fatal(err)
	}
	if err := discard(); err != nil {
		t.Fatal(err)
	}
}

// A path ListenEx refuses comes back with its error and no listener or
// discard; an abstract name (Linux) has no file, so its discard does nothing.
func TestListenExErrorsAndAbstractNames(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	l, discard, err := ListenEx(d + "/nope/s.sock")
	if !errors.Is(err, syscall.ENOENT) || l != nil || discard != nil {
		t.Fatalf("%v, %v, %v", l, discard != nil, err)
	}
	if !abstractNames {
		return
	}
	l, discard, err = ListenEx("@sockat-ex-" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := discard(); err != nil {
		t.Fatal(err)
	}
}
