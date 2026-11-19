package sockat_test

// The public face of ListenEx and DialEx: the net package's listener /
// connection and a Result: the Losses of the route (SPEC's names), the Route
// taken and the routes Skipped before it (both for diagnostics); a failure is
// a *sockat.RouteError. The routes themselves are forced and checked inside
// the package (lastresort_test.go and the others); here, what the public API
// reports in the real environment.

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/kawaz/sockat-go"
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
	c, err := sockat.Dial(p)
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
	for l, want := range map[sockat.Loss]string{
		sockat.Respelled:           "respelled",
		sockat.Forked:              "forked",
		sockat.RecreatableSpelling: "recreatable-spelling",
		sockat.CwdRelative:         "cwd-relative",
	} {
		if string(l) != want {
			t.Errorf("%q, want %q", string(l), want)
		}
	}
}

// The choice of the last resort is a bool underneath, NoLastResort the zero
// value: a LastResort left unset tries no more than Listen / Dial do.
func TestNoLastResortIsTheZeroValue(t *testing.T) {
	t.Parallel()
	var lr sockat.LastResort
	if lr != sockat.NoLastResort || sockat.UseLastResort == sockat.NoLastResort {
		t.Fatalf("zero %v, NoLastResort %v, UseLastResort %v", lr, sockat.NoLastResort, sockat.UseLastResort)
	}
}

// Has tells whether a loss is among a Result's losses, whatever their order;
// a Result with no losses has none.
func TestResultHas(t *testing.T) {
	t.Parallel()
	r := sockat.Result{Losses: []sockat.Loss{sockat.Respelled, sockat.RecreatableSpelling}}
	if !r.Has(sockat.Respelled) || !r.Has(sockat.RecreatableSpelling) || r.Has(sockat.Forked) {
		t.Fatalf("%+v", r)
	}
	if (sockat.Result{}).Has(sockat.Respelled) || (sockat.Result{Losses: []sockat.Loss{}}).Has(sockat.Respelled) {
		t.Fatal("an empty Result has a loss")
	}
}

// losses is a Result's losses as strings, in order.
func losses(r sockat.Result) []string {
	var out []string
	for _, l := range r.Losses {
		out = append(out, string(l))
	}
	return out
}

// longRoutes are the routes a listen of a long path may take here, with or
// without the last resort, where the default routes reach it: symlink-file
// where a listen tries it and bind(2) follows a symlink (macOS, NetBSD), then
// the routes through D.
func longRoutes() []string {
	rs := []string{"thread-cwd", "proc", "at"}
	if sockat.ListenTriesSymlinkFileHere && sockat.BindFollowsSymlinkHere() {
		rs = append(rs, "symlink-file")
	}
	return rs
}

// A short path is the net package's own: no losses (an empty list, not nil),
// Route "standard", nothing skipped, and the listener works. A long path that
// Listen reaches here goes through symlink-file or D: [respelled], Route one
// of those, with the last resort or without.
func TestListenExReportsTheLossesAndTheRoute(t *testing.T) {
	t.Parallel()
	for _, lr := range []sockat.LastResort{sockat.NoLastResort, sockat.UseLastResort} {
		short := tmpDir(t) + "/s.sock"
		l, discard, res, err := sockat.ListenEx(short, lr)
		if err != nil || res.Losses == nil || len(res.Losses) != 0 || res.Has(sockat.Respelled) || res.Route != "standard" || res.Skipped == nil || len(res.Skipped) != 0 {
			t.Fatalf("%v: a short path: %+v, %v", lr, res, err)
		}
		talk(t, l, short)
		l.Close()
		discard()

		if !longListen() {
			continue
		}
		p := deep(t, "s.sock")
		l, discard, res, err = sockat.ListenEx(p, lr)
		if err != nil || strings.Join(losses(res), ",") != "respelled" || !res.Has(sockat.Respelled) || res.Has(sockat.RecreatableSpelling) {
			t.Fatalf("%v: a long path: %+v, %v", lr, res, err)
		}
		if !slices.Contains(longRoutes(), res.Route) {
			t.Fatalf("%v: a long path: route %q, want one of %v", lr, res.Route, longRoutes())
		}
		talk(t, l, p)
		l.Close()
		discard()
	}
}

// longListen: a listen of a long path has a route here without the last
// resort: through D (Linux, macOS, FreeBSD), or symlink-file where a listen
// tries it and bind(2) follows a symlink (NetBSD).
func longListen() bool {
	switch runtime.GOOS {
	case "linux", "darwin", "freebsd":
		return true
	}
	return sockat.ListenTriesSymlinkFileHere && sockat.BindFollowsSymlinkHere()
}

// A failure is a *sockat.RouteError with no listener and an empty Result. It
// wraps the net package's *net.OpError, so errors.Is reaches the errno and
// errors.As the *net.OpError. Its Route is the route that bound and failed:
//
//   - a missing directory: ENOENT from the lookup before any route; no Route,
//     nothing skipped;
//   - a file already at a long path: EADDRINUSE from the route that bound
//     (where a listen has one): symlink-file where a listen tries it (macOS,
//     NetBSD: the bind through the link fails and the file is found at the
//     path, which makes the failure real, SPEC "試す順番"), otherwise the
//     first route through D the system has (Linux: thread-cwd or proc;
//     FreeBSD: at), symlink-file not being tried there;
//   - a 254-byte file name: ENAMETOOLONG with the last resort, every route
//     unusable, so no Route, and the routes skipped end with symlink-dir, the
//     last tried. Where a listen tries symlink-file and bind(2) follows a
//     symlink (macOS, NetBSD), symlink-file reaches such a name: no failure
//     there.
func TestAListenExFailureIsARouteErrorWithTheRouteThatFailed(t *testing.T) {
	t.Parallel()
	check := func(what, path string, errno syscall.Errno) *sockat.RouteError {
		t.Helper()
		l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
		var re *sockat.RouteError
		var oe *net.OpError
		if l != nil || discard != nil || !errors.As(err, &re) || !errors.Is(err, errno) || !errors.As(err, &oe) || oe.Addr.String() != path {
			t.Fatalf("%s: %v, %T %v", what, l, err, err)
		}
		if res.Losses != nil || res.Route != "" || res.Skipped != nil {
			t.Fatalf("%s: a Result %+v beside the error", what, res)
		}
		return re
	}
	re := check("a missing directory", tmpDir(t)+"/nope/"+strings.Repeat("n", 200), syscall.ENOENT)
	if re.Route != "" || len(re.Skipped) != 0 {
		t.Fatalf("a missing directory: %+v", re)
	}

	if longListen() {
		p := deep(t, "s.sock")
		l, discard, _, err := sockat.ListenEx(p, sockat.NoLastResort)
		if err != nil {
			t.Fatal(err)
		}
		defer discard()
		defer l.Close()
		re = check("an existing socket", p, syscall.EADDRINUSE)
		want := []string{"thread-cwd", "proc", "at"}
		if sockat.ListenTriesSymlinkFileHere {
			want = []string{"symlink-file"}
		}
		if !slices.Contains(want, re.Route) {
			t.Fatalf("an existing socket: route %q, want the route that bound, one of %v", re.Route, want)
		}
	}

	name := tmpDir(t) + "/" + strings.Repeat("n", 254)
	if sockat.ListenTriesSymlinkFileHere && sockat.BindFollowsSymlinkHere() {
		l, discard, res, err := sockat.ListenEx(name, sockat.UseLastResort)
		if err != nil || res.Route != "symlink-file" || strings.Join(losses(res), ",") != "respelled" {
			t.Fatalf("a 254-byte name where bind follows a symlink: %+v, %v", res, err)
		}
		talk(t, l, name)
		l.Close()
		discard()
		return
	}
	re = check("a 254-byte name", name, syscall.ENAMETOOLONG)
	if re.Route != "" || len(re.Skipped) == 0 || re.Skipped[len(re.Skipped)-1].Name != "symlink-dir" {
		t.Fatalf("a 254-byte name: %+v, want no route and symlink-dir skipped last", re)
	}
}

// The case that brought the last resort in: on Linux under a seccomp filter
// (no thread cwd: Docker's default profile), an 82-byte file name does not
// fit proc (/proc/self/fd/<n>/ and R/), so Listen is ENAMETOOLONG; ListenEx
// with the last resort binds through symlink-dir in /tmp (sticky, shared:
// TMPDIR and XDG_RUNTIME_DIR unset there) with the losses [respelled,
// recreatable-spelling], and /tmp keeps nothing of it. The routes skipped
// come in the order SPEC tries them: thread-cwd (the filter), then proc (too
// long), then fork-cwd (which Go cannot make); D is never made, so no -sockdir
// route is tried. Without the last resort it is Listen's ENAMETOOLONG, with
// the same routes skipped and nothing after them.
func TestUnderASeccompFilterAnEightyTwoByteNameIsRecreatableSpelling(t *testing.T) {
	t.Parallel()
	if !seccompFiltered() {
		t.Skip("no seccomp filter here; run on Linux under Docker's default profile")
	}
	if os.Getenv("TMPDIR") != "" || os.Getenv("XDG_RUNTIME_DIR") != "" {
		t.Skip("TMPDIR or XDG_RUNTIME_DIR is set: a place of the user's own may come first")
	}
	p := deep(t, strings.Repeat("n", 82))
	wantSkipped := "standard,thread-cwd,proc,fork-cwd"
	_, _, _, err := sockat.ListenEx(p, sockat.NoLastResort)
	var re *sockat.RouteError
	if !errors.As(err, &re) || !errors.Is(err, syscall.ENAMETOOLONG) || skipNames(re.Skipped) != wantSkipped {
		t.Fatalf("no last resort: %v, want ENAMETOOLONG after %s", err, wantSkipped)
	}

	before, _ := os.ReadDir("/tmp")
	l, discard, res, err := sockat.ListenEx(p, sockat.UseLastResort)
	if err != nil || strings.Join(losses(res), ",") != "respelled,recreatable-spelling" || res.Route != "symlink-dir" {
		t.Fatalf("%+v, %v", res, err)
	}
	if skipNames(res.Skipped) != wantSkipped {
		t.Fatalf("skipped %+v, want %s", res.Skipped, wantSkipped)
	}
	t.Logf("skipped: %+v", res.Skipped)
	defer discard()
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

// skipNames is the names of sks joined by commas, each checked to come with a
// reason.
func skipNames(sks []sockat.Skip) string {
	var names []string
	for _, s := range sks {
		if s.Reason == "" {
			return "(" + s.Name + " without a reason)"
		}
		names = append(names, s.Name)
	}
	return strings.Join(names, ",")
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
// A relative path long enough to go through the routes is respelled only: the
// kernel keeps another spelling, not the relative path. An absolute short
// path loses nothing (TestListenExReportsTheLossesAndTheRoute), nor does an
// abstract name (Linux), which is no path. The relative paths need a chosen
// cwd, which belongs to the whole process: they run in a child whose cwd is a
// temporary directory.
func TestARelativePathTakenAsItIsIsCwdRelative(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildRelativePaths$", "-test.v", "-test.count=1")
	cmd.Dir = tmpDir(t)
	cmd.Env = append(os.Environ(), "SOCKAT_EX_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestChildRelativePaths") {
		t.Fatalf("child: %v\n%s", err, out)
	}

	if runtime.GOOS == "linux" {
		l, _, res, err := sockat.ListenEx("@sockat-ex-"+t.Name(), sockat.UseLastResort)
		if err != nil || len(res.Losses) != 0 || res.Route != "standard" {
			t.Fatalf("an abstract name: %+v, %v", res, err)
		}
		l.Close()
	}
}

func TestChildRelativePaths(t *testing.T) {
	if os.Getenv("SOCKAT_EX_CHILD") == "" {
		t.Skip("runs only in a child process started by another test")
	}
	l, discard, res, err := sockat.ListenEx("s.sock", sockat.UseLastResort)
	if err != nil || strings.Join(losses(res), ",") != "cwd-relative" || !res.Has(sockat.CwdRelative) || res.Route != "standard" {
		t.Fatalf("a short relative path: %+v, %v", res, err)
	}
	l.Close()
	discard()
	if !longListen() {
		return
	}
	rel := strings.Repeat("d", 60) + "/" + strings.Repeat("d", 60)
	if err := os.MkdirAll(rel, 0o700); err != nil {
		t.Fatal(err)
	}
	l, discard, res, err = sockat.ListenEx(rel+"/s.sock", sockat.UseLastResort)
	if err != nil || strings.Join(losses(res), ",") != "respelled" || res.Has(sockat.CwdRelative) {
		t.Fatalf("a long relative path: %+v, %v", res, err)
	}
	if fi, err := os.Lstat(rel + "/s.sock"); err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("no socket at the relative path: %v", err)
	}
	l.Close()
	discard()
}

// A short path goes to the standard library; when that bind fails (here
// EADDRINUSE, a file being at the path), the route that failed is standard,
// and nothing was skipped before it. A dial of a short path where nothing is
// is the same: ENOENT through standard.
func TestAShortPathsFailureNamesStandard(t *testing.T) {
	t.Parallel()
	p := tmpDir(t) + "/s.sock"
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := sockat.ListenEx(p, sockat.UseLastResort)
	var re *sockat.RouteError
	if !errors.As(err, &re) || !errors.Is(err, syscall.EADDRINUSE) || re.Route != "standard" || len(re.Skipped) != 0 {
		t.Fatalf("listen: %T %+v, want EADDRINUSE through standard", err, err)
	}
	_, _, err = sockat.DialEx(tmpDir(t)+"/nope.sock", sockat.UseLastResort)
	if !errors.As(err, &re) || !errors.Is(err, syscall.ENOENT) || re.Route != "standard" || len(re.Skipped) != 0 {
		t.Fatalf("dial: %T %+v, want ENOENT through standard", err, err)
	}
}

// DialEx with the last resort or without reaches what Dial reaches, and
// loses nothing (a connect leaves no spelling behind): a short path through
// standard, a long one through a route of the connect order after standard
// (the path is long). Once the socket file is gone, the failure is ENOENT
// from the route that connected (standard for the short path), a
// *sockat.RouteError wrapping the *net.OpError.
func TestDialExReportsTheRouteAndNoLosses(t *testing.T) {
	t.Parallel()
	for _, lr := range []sockat.LastResort{sockat.NoLastResort, sockat.UseLastResort} {
		for _, p := range []string{tmpDir(t) + "/s.sock", deep(t, "s.sock")} {
			l, discard, _, err := sockat.ListenEx(p, sockat.NoLastResort)
			if err != nil {
				if !longListen() && errors.Is(err, syscall.ENAMETOOLONG) {
					continue
				}
				t.Fatal(err)
			}
			c, res, err := sockat.DialEx(p, lr)
			if err != nil || res.Losses == nil || len(res.Losses) != 0 {
				t.Fatalf("%v, %d bytes: %+v, %v", lr, len(p), res, err)
			}
			if len(p) <= 103 && (res.Route != "standard" || len(res.Skipped) != 0) {
				t.Fatalf("%v, a short path: %+v", lr, res)
			}
			if len(p) > 107 && (res.Route == "standard" || len(res.Skipped) == 0 || res.Skipped[0].Name != "standard") {
				t.Fatalf("%v, a long path: %+v, want a route after standard", lr, res)
			}
			c.Close()
			l.Close()
			discard()

			_, _, err = sockat.DialEx(p, lr)
			var re *sockat.RouteError
			var oe *net.OpError
			if !errors.As(err, &re) || !errors.Is(err, syscall.ENOENT) || !errors.As(err, &oe) || oe.Addr.String() != p || re.Route == "" {
				t.Fatalf("%v, %d bytes, nothing there: %T %v (%+v)", lr, len(p), err, err, re)
			}
		}
	}
}
