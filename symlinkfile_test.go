package sockat

// symlink-file in a listen (SPEC "symlink-file"): after standard, a listen
// makes a symlink <place>/sockat.XXXXXXXXXX to the socket file in a safe
// place (the places a dial's symlink-file uses) and binds by it, removing the
// symlink right after, success or failure. Where bind(2) follows a symlink in
// the last component (macOS), the socket is made at the path and the
// spelling holds no file name, so the file name's length no longer matters.
// Where it does not (Linux: EADDRINUSE, nothing made), the route is unusable;
// Linux does not even try it. A failure with something at the path is that
// failure, not a sign that the bind did not follow.

import (
	"errors"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// symlinkFileCfg is the route order's inputs with place for symlink-file and
// dBase for D, the means of the routes through D as the system has them.
func symlinkFileCfg(place, dBase string) *config {
	return &config{dBases: []string{dBase}, tmpBases: []string{place}, procSelfFD: "/proc/self/fd", threadCwd: true}
}

// SPEC's premise, pinned where it was measured (SPEC "試す順番"): the bind(2)
// of macOS and NetBSD follows a symlink in the last component to a path where
// nothing is yet; that of Linux, FreeBSD, OpenBSD and illumos does not, and a
// listen there never tries symlink-file (listenTriesSymlinkFile), so a kernel
// that changed would go unnoticed but for this test. Elsewhere it is only
// reported; the tests below follow what the kernel does there.
func TestWhetherABindFollowsASymlinkIsAsObserved(t *testing.T) {
	t.Parallel()
	got := bindFollowsSymlink()
	switch runtime.GOOS {
	case "darwin", "netbsd":
		if !got {
			t.Fatalf("%s's bind did not follow the symlink", runtime.GOOS)
		}
		if !listenTriesSymlinkFile {
			t.Fatalf("a listen on %s does not try symlink-file", runtime.GOOS)
		}
	case "linux", "android", "freebsd", "openbsd", "illumos":
		if got {
			t.Fatalf("%s's bind followed the symlink", runtime.GOOS)
		}
		if listenTriesSymlinkFile {
			t.Fatalf("a listen on %s tries symlink-file", runtime.GOOS)
		}
	default:
		t.Logf("bind follows a symlink here: %v", got)
	}
}

// A listen of a long path with a safe place for symlink-file:
//
//   - where a listen tries it and the bind follows the symlink: symlink-file,
//     after standard alone (the place for D untouched); the socket is at the
//     path and reachable; the losses are [respelled] only; the kernel keeps
//     <place>/sockat.XXXXXXXXXX, gone by then (unlinking that spelling could
//     only hit an entry of the place); the place is empty afterwards. The
//     listener does not remove the socket file on Close (its address is the
//     spelling), and ListenEx's discard removes it;
//   - where a listen tries it and the bind does not follow (a system not
//     confirmed): symlink-file is skipped after standard, and the link it made
//     is gone; the listen goes on to the routes through D;
//   - where a listen does not try it (Linux): the place is untouched and
//     symlink-file is not among the routes skipped.
func TestAListenTakesSymlinkFileWhereTheBindFollowsIt(t *testing.T) {
	t.Parallel()
	place, dBase := tmpDir(t), tmpDir(t)
	cfg := symlinkFileCfg(place, dBase)
	p := nest(t, tmpDir(t), 600, "s.sock")
	markUntouched(t, place)
	markUntouched(t, dBase)
	l, discard, res, err := listenEx(p, NoLastResort, cfg)
	var skipped []route
	if err != nil {
		skipped = skippedNames(t, routeErr(t, err).Skipped)
	} else {
		skipped = skippedNames(t, res.Skipped)
		t.Cleanup(func() { l.Close(); discard() })
	}
	switch {
	case !listenTriesSymlinkFile:
		assertUntouched(t, place)
		if slices.Contains(skipped, routeSymlinkFile) || res.Route == string(routeSymlinkFile) {
			t.Fatalf("symlink-file tried: %+v, %v", res, err)
		}
	case bindFollowsSymlink():
		if err != nil || res.Route != string(routeSymlinkFile) || !slices.Equal(skipped, []route{routeStandard}) || !slices.Equal(res.Losses, []Loss{Respelled}) {
			t.Fatalf("%+v, %v; want symlink-file after standard, losing respelled", res, err)
		}
		assertUntouched(t, dBase)
		assertTouched(t, place)
		assertEmpty(t, place)
		spelling := regexp.MustCompile(`^` + regexp.QuoteMeta(place) + `/sockat\.[A-Za-z0-9]{10}$`)
		if a := l.Addr().String(); !spelling.MatchString(a) || exists(a) {
			t.Fatalf("the kernel keeps %q (exists %v)", a, exists(a))
		}
		c, err := Dial(p)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		ping(t, l, c)
		l.Close()
		if !isSocket(p) {
			t.Fatal("Close removed the socket file")
		}
		if err := discard(); err != nil || exists(p) {
			t.Fatalf("discard: %v, the file there %v", err, exists(p))
		}
	default:
		if len(skipped) < 2 || skipped[1] != routeSymlinkFile || res.Route == string(routeSymlinkFile) {
			t.Fatalf("%+v, %v; want symlink-file skipped after standard", res, err)
		}
		assertTouched(t, place)
		assertEmpty(t, place)
	}
}

// With something at the path (a file, a socket someone listens on), the bind
// through symlink-file fails, and since something is there the failure is
// real: EADDRINUSE through symlink-file, the next route not tried (the place
// for D untouched), the link gone, and what was at the path left as it was.
// It is so whether or not the bind follows the symlink (a bind that does not
// also answers EADDRINUSE, and the path is looked up either way).
func TestASymlinkFileBindWithSomethingAtThePathIsThatError(t *testing.T) {
	t.Parallel()
	if !listenTriesSymlinkFile {
		t.Skip("a listen does not try symlink-file here")
	}
	place, dBase := tmpDir(t), tmpDir(t)
	cfg := symlinkFileCfg(place, dBase)
	dir := tmpDir(t)
	file := nest(t, dir, 600, "f.sock")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	live := file[:len(file)-len("f.sock")] + "l.sock"
	other := listenAt(t, live)
	for _, p := range []string{file, live} {
		markUntouched(t, dBase)
		_, _, _, err := listenEx(p, UseLastResort, cfg)
		wantOpError(t, p, opListen, p, err, syscall.EADDRINUSE)
		if re := routeErr(t, err); re.Route != string(routeSymlinkFile) || !slices.Equal(skippedNames(t, re.Skipped), []route{routeStandard}) {
			t.Fatalf("%s: %+v, want symlink-file after standard", p, re)
		}
		assertUntouched(t, dBase)
		assertEmpty(t, place)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "file" {
		t.Fatalf("the file: %q, %v", b, err)
	}
	c, err := Dial(live)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ping(t, other, c)
}

// Where the bind does not follow the symlink (Linux, the kernel at hand: the
// order there never tries it, so the route is called directly), symlink-file
// is unusable once the bind fails with nothing at the path: nothing is left at
// the path or in the place (the link made there is removed).
func TestASymlinkFileBindTheKernelDoesNotFollowIsUnusableAndLeavesNothing(t *testing.T) {
	t.Parallel()
	if bindFollowsSymlink() {
		t.Skip("the bind follows a symlink here")
	}
	place := tmpDir(t)
	p := nest(t, tmpDir(t), 600, "s.sock")
	markUntouched(t, place)
	ep, err := symlinkFile(opListen, p, []string{place})
	if ep != nil || !errors.Is(err, errUnusable) {
		t.Fatalf("%v, %v; want unusable", ep, err)
	}
	if exists(p) {
		t.Fatal("something was created at the path")
	}
	assertTouched(t, place)
	assertEmpty(t, place)
}

// macOS: the file name's length no longer limits a listen (symlink-file) nor
// a dial (symlink-file too, after relative, widened and thread-cwd, none of
// which carries the name). A 200-byte file name under a 600-byte path is
// listened on and dialed through the public order with the real
// environment's inputs, and the two talk. Wherever else the bind follows a
// symlink, the same holds.
func TestAFileNameOf200BytesListensAndDialsWhereTheBindFollowsASymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && !(listenTriesSymlinkFile && bindFollowsSymlink()) {
		t.Skip("a listen has no route for a 200-byte file name here")
	}
	p := nest(t, tmpDir(t), 600, strings.Repeat("n", 200))
	l, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !isSocket(p) {
		t.Fatal("no socket at the path")
	}
	c, err := Dial(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ping(t, l, c)
}
