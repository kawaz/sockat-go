package atanycost_test

// The public face of ListenAny: atanycost.Listen returns the net package's
// listener and a Result: the Losses of the route (SPEC's names), the Route
// taken and the routes Skipped before it (both for diagnostics); a failure is
// an *atanycost.Error. The routes
// themselves are forced and checked in package sockat (anyroute_test.go);
// here, what the public API reports in the real environment.

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

// The losses are named as SPEC names them ("失うもの"); their strings are those
// names, so they can be compared with the other implementations and logged.
func TestLossesAreSPECsNames(t *testing.T) {
	t.Parallel()
	for l, want := range map[atanycost.Loss]string{
		atanycost.Respelled:           "respelled",
		atanycost.Forked:              "forked",
		atanycost.RecreatableSpelling: "recreatable-spelling",
		atanycost.CwdRelative:         "cwd-relative",
	} {
		if string(l) != want {
			t.Errorf("%q, want %q", string(l), want)
		}
	}
}

// Has tells whether a loss is among a Result's losses, whatever their order;
// a Result with no losses has none.
func TestResultHas(t *testing.T) {
	t.Parallel()
	r := atanycost.Result{Losses: []atanycost.Loss{atanycost.Respelled, atanycost.RecreatableSpelling}}
	if !r.Has(atanycost.Respelled) || !r.Has(atanycost.RecreatableSpelling) || r.Has(atanycost.Forked) {
		t.Fatalf("%+v", r)
	}
	if (atanycost.Result{}).Has(atanycost.Respelled) || (atanycost.Result{Losses: []atanycost.Loss{}}).Has(atanycost.Respelled) {
		t.Fatal("an empty Result has a loss")
	}
}

// losses is a Result's losses as strings, in order.
func losses(r atanycost.Result) []string {
	var out []string
	for _, l := range r.Losses {
		out = append(out, string(l))
	}
	return out
}

// A short path is the net package's own: no losses (an empty list, not nil),
// Route "standard", nothing skipped, and the listener works. A long path that
// Listen reaches here goes through D: [respelled], Route one of the routes
// through D.
func TestListenReportsTheLossesAndTheRoute(t *testing.T) {
	t.Parallel()
	short := tmpDir(t) + "/s.sock"
	l, res, err := atanycost.Listen(short)
	if err != nil || res.Losses == nil || len(res.Losses) != 0 || res.Has(atanycost.Respelled) || res.Route != "standard" || len(res.Skipped) != 0 {
		t.Fatalf("a short path: %+v, %v", res, err)
	}
	talk(t, l, short)
	l.Close()

	if !longListen() {
		return
	}
	p := deep(t, "s.sock")
	l, res, err = atanycost.Listen(p)
	if err != nil || strings.Join(losses(res), ",") != "respelled" || !res.Has(atanycost.Respelled) || res.Has(atanycost.RecreatableSpelling) {
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

// longListen: a listen of a long path has a route through D here.
func longListen() bool {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
		return true
	}
	return false
}

// A failure is an *atanycost.Error with no listener and an empty Result. It
// wraps the net package's *net.OpError, so errors.Is reaches the errno and
// errors.As the *net.OpError. Its Route is the route that bound and failed:
//
//   - a missing directory: ENOENT from the lookup before any route; no Route,
//     nothing skipped;
//   - a file already at a long path: EADDRINUSE from the route through D that
//     bound (where a listen has one);
//   - a 254-byte file name: ENAMETOOLONG, every route unusable, so no Route,
//     and the routes skipped end with symlink, the last tried.
func TestAFailureIsAnErrorWithTheRouteThatFailed(t *testing.T) {
	t.Parallel()
	check := func(what, path string, errno syscall.Errno) *atanycost.Error {
		t.Helper()
		l, res, err := atanycost.Listen(path)
		var ae *atanycost.Error
		var oe *net.OpError
		if l != nil || !errors.As(err, &ae) || !errors.Is(err, errno) || !errors.As(err, &oe) || oe.Addr.String() != path {
			t.Fatalf("%s: %v, %T %v", what, l, err, err)
		}
		if res.Losses != nil || res.Route != "" || res.Skipped != nil {
			t.Fatalf("%s: a Result %+v beside the error", what, res)
		}
		return ae
	}
	ae := check("a missing directory", tmpDir(t)+"/nope/"+strings.Repeat("n", 200), syscall.ENOENT)
	if ae.Route != "" || len(ae.Skipped) != 0 {
		t.Fatalf("a missing directory: %+v", ae)
	}

	if longListen() {
		p := deep(t, "s.sock")
		l, _, err := atanycost.Listen(p)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		ae = check("an existing socket", p, syscall.EADDRINUSE)
		switch ae.Route {
		case "thread-cwd", "proc", "at":
		default:
			t.Fatalf("an existing socket: route %q, want the route through D that bound", ae.Route)
		}
	}

	ae = check("a 254-byte name", tmpDir(t)+"/"+strings.Repeat("n", 254), syscall.ENAMETOOLONG)
	if ae.Route != "" || len(ae.Skipped) == 0 || ae.Skipped[len(ae.Skipped)-1].Name != "symlink" {
		t.Fatalf("a 254-byte name: %+v, want no route and symlink skipped last", ae)
	}
}

// The case that brought ListenAny in: on Linux under a seccomp filter (no
// thread cwd: Docker's default profile), an 82-byte file name does not fit
// proc (/proc/self/fd/<n>/ and R/), so Listen is ENAMETOOLONG; ListenAny
// binds through symlink in /tmp (sticky, shared: TMPDIR and XDG_RUNTIME_DIR
// unset there) with the losses [respelled, recreatable-spelling], and /tmp
// keeps nothing of it. The
// routes skipped come in the order SPEC tries them: thread-cwd (the filter),
// then proc (too long); D is never made, so no -sockdir route is tried.
func TestUnderASeccompFilterAnEightyTwoByteNameIsRecreatableSpelling(t *testing.T) {
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
	if err != nil || strings.Join(losses(res), ",") != "respelled,recreatable-spelling" || res.Route != "symlink" {
		t.Fatalf("%+v, %v", res, err)
	}
	var names []string
	for _, s := range res.Skipped {
		if s.Reason == "" {
			t.Errorf("%s skipped without a reason", s.Name)
		}
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "standard,thread-cwd,proc" {
		t.Fatalf("skipped %+v, want standard, thread-cwd, then proc", res.Skipped)
	}
	t.Logf("skipped: %+v", res.Skipped)
	defer l.Close()
	talk(t, l, p)
	// Other tests (other packages too) make and remove sockat.* entries in
	// /tmp at the same time, so only a symlink of ours is looked for: one
	// pointing to the socket's directory.
	real, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadDir("/tmp")
	for _, e := range after {
		if !strings.HasPrefix(e.Name(), "sockat.") || contains(before, e.Name()) {
			continue
		}
		if to, err := os.Readlink("/tmp/" + e.Name()); err == nil && to == real {
			t.Fatalf("left in /tmp: %s -> %s", e.Name(), to)
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

// A relative path that the standard library takes as it is (short) leaves the
// kernel a path relative to the cwd: Losses [cwd-relative], Route "standard".
// A relative path long enough to go through D is respelled only: the kernel
// keeps R/<name>, not the relative path. An absolute short path loses nothing
// (TestListenReportsTheLossesAndTheRoute), nor does an abstract name (Linux),
// which is no path. The relative paths need a chosen cwd, which belongs to the
// whole process: they run in a child whose cwd is a temporary directory.
func TestARelativePathTakenAsItIsIsCwdRelative(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildRelativePaths$", "-test.v", "-test.count=1")
	cmd.Dir = tmpDir(t)
	cmd.Env = append(os.Environ(), "SOCKAT_ANY_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestChildRelativePaths") {
		t.Fatalf("child: %v\n%s", err, out)
	}

	if runtime.GOOS == "linux" {
		l, res, err := atanycost.Listen("@sockat-any-" + t.Name())
		if err != nil || len(res.Losses) != 0 || res.Route != "standard" {
			t.Fatalf("an abstract name: %+v, %v", res, err)
		}
		l.Close()
	}
}

func TestChildRelativePaths(t *testing.T) {
	if os.Getenv("SOCKAT_ANY_CHILD") == "" {
		t.Skip("runs only in a child process started by another test")
	}
	l, res, err := atanycost.Listen("s.sock")
	if err != nil || strings.Join(losses(res), ",") != "cwd-relative" || !res.Has(atanycost.CwdRelative) || res.Route != "standard" {
		t.Fatalf("a short relative path: %+v, %v", res, err)
	}
	l.Close()
	if !longListen() {
		return
	}
	rel := strings.Repeat("d", 60) + "/" + strings.Repeat("d", 60)
	if err := os.MkdirAll(rel, 0o700); err != nil {
		t.Fatal(err)
	}
	l, res, err = atanycost.Listen(rel + "/s.sock")
	if err != nil || strings.Join(losses(res), ",") != "respelled" || res.Has(atanycost.CwdRelative) {
		t.Fatalf("a long relative path: %+v, %v", res, err)
	}
	if fi, err := os.Lstat(rel + "/s.sock"); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("no socket at the relative path: %v", err)
	}
	l.Close()
}

// A short path goes to the standard library; when that bind fails (here
// EADDRINUSE, a file being at the path), the route that failed is standard,
// and nothing was skipped before it.
func TestAShortPathsFailureNamesStandard(t *testing.T) {
	t.Parallel()
	d, err := os.MkdirTemp("/tmp", "sockat-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := d + "/s.sock"
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = atanycost.Listen(p)
	var ae *atanycost.Error
	if !errors.As(err, &ae) || !errors.Is(err, syscall.EADDRINUSE) || ae.Route != "standard" || len(ae.Skipped) != 0 {
		t.Fatalf("%T %+v, want EADDRINUSE through standard", err, err)
	}
}
