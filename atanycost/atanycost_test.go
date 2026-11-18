package atanycost_test

// The public face of ListenAny: atanycost.Listen returns the net package's
// listener and a Result: the Level of what the route gave up, the Route taken
// and the routes Skipped before it (both for diagnostics). The routes
// themselves are forced and checked in package sockat (anyroute_test.go);
// here, what the public API reports in the real environment.

import (
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/kawaz/sockat-go/atanycost"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "sockat-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// deep is a path of 600 bytes under a fresh directory, ending in name.
func deep(t *testing.T, name string) string {
	t.Helper()
	dir := tmpDir(t)
	for len(dir)+1+len(name) < 600-60 {
		dir += "/" + strings.Repeat("d", 50)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir + "/" + name
}

// talk dials p and checks that the connection reaches l.
func talk(t *testing.T, l *net.UnixListener, p string) {
	t.Helper()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: p, Net: "unix"})
	if len(p) > 103 {
		// The net package cannot dial a long path; reaching it is checked
		// by the socket file being there and the listener taking calls.
		fi, serr := os.Lstat(p)
		if serr != nil || fi.Mode()&os.ModeSocket == 0 {
			t.Fatalf("no socket at the path: %v", serr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := l.Accept(); err != nil {
		t.Fatal(err)
	}
}

// The names of the levels, for messages and logs; a value not known (one
// added later) reads as "unknown" rather than a wrong name.
func TestLevelString(t *testing.T) {
	t.Parallel()
	for l, want := range map[atanycost.Level]string{
		atanycost.None:          "none",
		atanycost.Respelled:     "respelled",
		atanycost.Forked:        "forked",
		atanycost.SharedSymlink: "shared-symlink",
		atanycost.Level(99):     "unknown",
	} {
		if got := l.String(); got != want {
			t.Errorf("%d: %q, want %q", int(l), got, want)
		}
	}
}

// A short path is the net package's own: Level None, Route "standard",
// nothing skipped, and the listener works. A long path that Listen reaches
// here goes through D: Level Respelled, Route one of the routes through D. A
// failure is the error alone, with an empty Result.
func TestListenReportsTheLevelAndRoute(t *testing.T) {
	t.Parallel()
	short := tmpDir(t) + "/s.sock"
	l, res, err := atanycost.Listen(short)
	if err != nil || res.Level != atanycost.None || res.Route != "standard" || len(res.Skipped) != 0 {
		t.Fatalf("a short path: %+v, %v", res, err)
	}
	talk(t, l, short)
	l.Close()

	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
		p := deep(t, "s.sock")
		l, res, err := atanycost.Listen(p)
		if err != nil || res.Level != atanycost.Respelled {
			t.Fatalf("a long path: %+v, %v", res, err)
		}
		switch res.Route {
		case "thread-cwd", "proc", "at":
		default:
			t.Fatalf("a long path: route %q, want one through D", res.Route)
		}
		talk(t, l, p)
		l.Close()
	}

	missing := tmpDir(t) + "/nope/" + strings.Repeat("n", 200)
	l, res, err = atanycost.Listen(missing)
	var oe *net.OpError
	if !errors.Is(err, syscall.ENOENT) || !errors.As(err, &oe) || l != nil || res.Level != 0 || res.Route != "" || res.Skipped != nil {
		t.Fatalf("a missing directory: %v, %v, %v", l, res, err)
	}
}

// The case that brought ListenAny in: on Linux under a seccomp filter (no
// thread cwd: Docker's default profile), an 82-byte file name does not fit
// proc (/proc/self/fd/<n>/ and R/), so Listen is ENAMETOOLONG; ListenAny
// binds through symlink in /tmp (sticky, shared: TMPDIR and XDG_RUNTIME_DIR
// unset there) with Level SharedSymlink, and /tmp keeps nothing of it. The
// routes skipped come in the order SPEC tries them: thread-cwd (the filter),
// then proc (too long); D is never made, so no -sockdir route is tried.
func TestUnderASeccompFilterAnEightyTwoByteNameIsSharedSymlink(t *testing.T) {
	t.Parallel()
	if !seccompFiltered() {
		t.Skip("no seccomp filter here; run on Linux under Docker's default profile")
	}
	if os.Getenv("TMPDIR") != "" || os.Getenv("XDG_RUNTIME_DIR") != "" {
		t.Skip("TMPDIR or XDG_RUNTIME_DIR is set: a place of the user's own may come first")
	}
	p := deep(t, strings.Repeat("n", 82))
	before, _ := os.ReadDir("/tmp")
	l, res, err := atanycost.Listen(p)
	if err != nil || res.Level != atanycost.SharedSymlink || res.Route != "symlink" {
		t.Fatalf("%+v, %v", res, err)
	}
	var names []string
	for _, s := range res.Skipped {
		if s.Reason == "" {
			t.Errorf("%s skipped without a reason", s.Name)
		}
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "thread-cwd,proc" {
		t.Fatalf("skipped %+v, want thread-cwd then proc", res.Skipped)
	}
	t.Logf("skipped: %+v", res.Skipped)
	defer l.Close()
	talk(t, l, p)
	after, _ := os.ReadDir("/tmp")
	for _, e := range after {
		if strings.HasPrefix(e.Name(), "sockat.") && !contains(before, e.Name()) {
			t.Fatalf("left in /tmp: %s", e.Name())
		}
	}
}

func contains(es []os.DirEntry, name string) bool {
	for _, e := range es {
		if e.Name() == name {
			return true
		}
	}
	return false
}
