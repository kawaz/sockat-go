package sockat

// B, D and E (base.go, dir.go, link.go), through a world whose places are
// fresh directories under /tmp (testWorld): the process's /tmp,
// $XDG_RUNTIME_DIR and $TMPDIR are never written to by these tests.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// testPlaces are a test world's places 1 to 3, each a fresh directory of
// mode 0700.
type testPlaces struct{ tmp, runtime, tmpdir string }

// testWorld is a world whose places are fresh directories (all three set),
// with the real /proc, the system's table of bind(2) following a symlink, and
// the thread cwd on. mod, unless nil, changes the configuration before the
// world is made. Its fds are closed at the end of the test.
func testWorld(t *testing.T, mod func(*config, testPlaces)) (*world, testPlaces) {
	t.Helper()
	root := tmpDir(t)
	ps := testPlaces{root + "/tmp", root + "/run", root + "/tmpdir"}
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := envConfig()
	cfg.tmp = ps.tmp
	env := map[string]string{"XDG_RUNTIME_DIR": ps.runtime, "TMPDIR": ps.tmpdir}
	cfg.getenv = func(k string) string { return env[k] }
	if mod != nil {
		mod(&cfg, ps)
	}
	w := newWorld(cfg)
	t.Cleanup(func() { closeWorld(w) })
	return w, ps
}

// closeWorld closes the fds of every B and D w holds.
func closeWorld(w *world) {
	for _, b := range w.bases() {
		if b.d != nil {
			unix.Close(b.d.fd)
		}
		unix.Close(b.fd)
	}
}

// anyFits is a fitsFunc that lets every place be tried.
func anyFits(string, string) bool { return true }

// autoOf is w's B of the places 1 to 3 (cross: one others can follow) with
// no estimate in the way, nil when there is none.
func autoOf(w *world, cross bool) *base {
	b, _ := w.autoBase(cross, anyFits)
	return b
}

// uidName is sockat-<euid>, B's name in /tmp and $TMPDIR.
func uidName() string { return "sockat-" + strconv.Itoa(os.Geteuid()) }

// modeOf is the permission bits (with the sticky bit) of p itself.
func modeOf(t *testing.T, p string) uint32 {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		t.Fatal(err)
	}
	return uint32(st.Mode) & 0o7777
}

// entries are the names in dir.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// randomRE is ten letters or digits, the random part of a name.
const randomRE = `[A-Za-z0-9]{10}`

// B is made in the first place, /tmp, named sockat-<euid>, a directory of
// mode 0711 (others may search it, so a real-path spelling through it can be
// followed by another uid, but not list or write it), kept by the world and
// used again by the next call; the other places are left untouched. Its kind
// is "tmp" and its real path is the place's real path.
func TestBIsMadeInTheFirstPlaceAndKept(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	markUntouched(t, ps.runtime)
	markUntouched(t, ps.tmpdir)
	b := autoOf(w, false)
	if b == nil {
		t.Fatal("no B")
	}
	real, err := realDir(ps.tmp)
	if err != nil {
		t.Fatal(err)
	}
	if b.kind != kindTmp || b.real != real+"/"+uidName() {
		t.Fatalf("B %s (%s), want %s/%s (tmp)", b.real, b.kind, real, uidName())
	}
	if m := modeOf(t, ps.tmp+"/"+uidName()); m != 0o711 {
		t.Fatalf("B's mode %o, want 0711", m)
	}
	if again := autoOf(w, false); again != b {
		t.Fatal("the next call made another B")
	}
	assertUntouched(t, ps.runtime)
	assertUntouched(t, ps.tmpdir)
}

// A B already in the place, a directory of ours that others cannot write
// to, is used as it is, its mode left alone (another process made it, maybe
// with another umask or by an older rule; SPEC-routes "B の作り方" step 5),
// and nothing else is made in the place.
func TestAnExistingBOfOursIsUsedWithItsMode(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	if err := os.Mkdir(ps.tmp+"/"+uidName(), 0o700); err != nil {
		t.Fatal(err)
	}
	b := autoOf(w, false)
	if b == nil || !strings.HasSuffix(b.real, "/"+uidName()) {
		t.Fatalf("B %v", b)
	}
	if m := modeOf(t, ps.tmp+"/"+uidName()); m != 0o700 {
		t.Fatalf("the existing B's mode became %o", m)
	}
	if es := entries(t, ps.tmp); len(es) != 1 {
		t.Fatalf("the place holds %v", es)
	}
}

// What is at B's name but fails the check (a symlink, even to a directory
// of ours; a file; a directory others can write to) is not used: B falls
// back to sockat-<euid>.<10 random letters or digits> in the same place
// (SPEC-routes "B の作り方" step 5), and what was there is left as it was.
// Someone who can write to B, or who put the symlink there, could otherwise
// redirect the spellings through it.
func TestABAtTheNameThatFailsTheCheckFallsBackToARandomName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		make func(t *testing.T, at string)
	}{
		{"symlink", func(t *testing.T, at string) {
			if err := os.Symlink(tmpDir(t), at); err != nil {
				t.Fatal(err)
			}
		}},
		{"file", func(t *testing.T, at string) {
			if err := os.WriteFile(at, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"group-writable", func(t *testing.T, at string) {
			if err := os.Mkdir(at, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(at, 0o770); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w, ps := testWorld(t, nil)
			at := ps.tmp + "/" + uidName()
			c.make(t, at)
			var before unix.Stat_t
			if err := unix.Lstat(at, &before); err != nil {
				t.Fatal(err)
			}
			b := autoOf(w, false)
			if b == nil {
				t.Fatal("no B")
			}
			if !regexp.MustCompile(`/` + uidName() + `\.` + randomRE + `$`).MatchString(b.real) {
				t.Fatalf("B %s, want the fallback name", b.real)
			}
			var after unix.Stat_t
			if err := unix.Lstat(at, &after); err != nil || after.Mode != before.Mode || after.Ino != before.Ino {
				t.Fatalf("what was at %s changed: %v", at, err)
			}
		})
	}
}

// A B owned by another user is not used either (it falls back to the random
// name), and a place one of whose directories on the way is owned by
// another user is passed over for the next place: that user could rename
// what is in it. Needs root to give a directory away, so it runs in the
// Linux container.
func TestABOrAPlaceOfAnotherOwnerIsNotUsed(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("needs root to give a directory to another user")
	}
	t.Run("B", func(t *testing.T) {
		w, ps := testWorld(t, nil)
		at := ps.tmp + "/" + uidName()
		if err := os.Mkdir(at, 0o711); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(at, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		b := autoOf(w, false)
		if b == nil || !regexp.MustCompile(`/`+uidName()+`\.`+randomRE+`$`).MatchString(b.real) {
			t.Fatalf("B %v, want the fallback name", b)
		}
	})
	t.Run("place", func(t *testing.T) {
		w, ps := testWorld(t, nil)
		if err := os.Chown(ps.tmp, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		b := autoOf(w, false)
		if b == nil || b.kind != kindRuntime {
			t.Fatalf("B %v, want the next place ($XDG_RUNTIME_DIR)", b)
		}
		assertEmpty(t, ps.tmp)
	})
}

// A place one of whose directories on the way others can write to (no
// sticky bit) is passed over, untouched, for the next place: someone could
// replace a directory on the way, and every spelling through B with it. A
// sticky one of ours (as /tmp is root's) is used.
func TestAPlaceOthersCanWriteToOnTheWayIsPassedOver(t *testing.T) {
	t.Parallel()
	t.Run("writable", func(t *testing.T) {
		t.Parallel()
		w, ps := testWorld(t, nil)
		if err := os.Chmod(ps.tmp, 0o777); err != nil {
			t.Fatal(err)
		}
		markUntouched(t, ps.tmp)
		b := autoOf(w, false)
		if b == nil || b.kind != kindRuntime {
			t.Fatalf("B %v, want the next place", b)
		}
		assertUntouched(t, ps.tmp)
		if !strings.HasSuffix(b.real, "/sockat") {
			t.Fatalf("B in $XDG_RUNTIME_DIR is %s, want .../sockat", b.real)
		}
	})
	t.Run("sticky", func(t *testing.T) {
		t.Parallel()
		w, ps := testWorld(t, nil)
		if err := unix.Chmod(ps.tmp, 0o1777); err != nil {
			t.Fatal(err)
		}
		if b := autoOf(w, false); b == nil || b.kind != kindTmp {
			t.Fatalf("B %v, want the sticky place", b)
		}
	})
}

// The places are tried in order, /tmp, $XDG_RUNTIME_DIR, $TMPDIR: each one
// that cannot be used (missing here) is passed over. An empty or relative
// $XDG_RUNTIME_DIR / $TMPDIR is no place at all. With none usable there is no
// B, and nothing is remembered: once a place is there, the next call makes B
// in it.
func TestThePlacesAreTriedInOrder(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	for _, p := range []string{ps.tmp, ps.runtime} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if b := autoOf(w, false); b == nil || b.kind != kindTmpdir || !strings.HasSuffix(b.real, "/"+uidName()) {
		t.Fatalf("B %v, want $TMPDIR/%s", b, uidName())
	}

	w2, ps2 := testWorld(t, func(c *config, ps testPlaces) {
		env := map[string]string{"XDG_RUNTIME_DIR": "", "TMPDIR": "relative/dir"}
		c.getenv = func(k string) string { return env[k] }
	})
	if err := os.Remove(ps2.tmp); err != nil {
		t.Fatal(err)
	}
	if ps := w2.autoPlaces(); len(ps) != 1 {
		t.Fatalf("places %v, want /tmp's only", ps)
	}
	if b := autoOf(w2, false); b != nil {
		t.Fatalf("B %s with no place", b.real)
	}
	if err := os.Mkdir(ps2.tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if b := autoOf(w2, false); b == nil || b.kind != kindTmp {
		t.Fatalf("B %v once the place is there", b)
	}
}

// Where the spelling is known not to fit through the first place that can
// have B (the estimate from the place's real path), no B is made there, and
// none in a later place either even where it would fit (SPEC-routes
// "実体の置き場", hub's answer of 2026-11-21): the process's B is the first
// place in order whatever the length of the path, so every process of the
// user finds it in the same place, and a route that does not fit through it
// is unusable (errSpellingTooLong). The estimate is given the place's kind
// and the real path B would have. Once a call that fits has made B there, a
// held B is returned whatever the estimate (the route checks its spelling).
func TestNoBIsMadeWhereTheSpellingCannotFitAndNoOtherPlaceIsTried(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	var seen []string
	firstNot := func(kind, real string) bool { seen = append(seen, kind+" "+real); return kind != kindTmp }
	if b, err := w.autoBase(false, firstNot); b != nil || !errors.Is(err, errSpellingTooLong) {
		t.Fatalf("B %v, %v; want none, spelling too long", b, err)
	}
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertEmpty(t, p)
	}
	real, _ := realDir(ps.tmp)
	if len(seen) != 1 || seen[0] != "tmp "+real+"/"+uidName() {
		t.Fatalf("estimated %q, want /tmp's alone", seen)
	}
	b := autoOf(w, false)
	if b == nil || b.kind != kindTmp {
		t.Fatalf("B %v once a spelling fits, want /tmp's", b)
	}
	if again, err := w.autoBase(false, func(string, string) bool { return false }); again != b || err != nil {
		t.Fatalf("held B %v, %v", again, err)
	}
	// A place that cannot have B (missing) is passed over before the
	// estimate: the estimate is of the place B would really be made in.
	w2, ps2 := testWorld(t, nil)
	os.Remove(ps2.tmp)
	seen = nil
	if b, err := w2.autoBase(false, firstNot); b == nil || b.kind != kindRuntime || err != nil {
		t.Fatalf("B %v, %v; want $XDG_RUNTIME_DIR's", b, err)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "runtime ") {
		t.Fatalf("estimated %q, want $XDG_RUNTIME_DIR's alone", seen)
	}
}

// crossUID holds only when B and every directory on the way to it have
// search permission for others (SPEC-routes "別の uid と PrivateTmp"): a
// place of mode 0700 gives a B others cannot follow even though B is 0711.
// A usable_cross_uid bind takes the first place where it holds (autoCross),
// passing over an existing B of mode 0700 there, and leaves the process's
// other B (auto) as it is; where auto holds it already, autoCross is auto.
func TestTheBOfAUsableCrossUIDBindHasSearchPermissionForOthersAllTheWay(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	auto := autoOf(w, false)
	if auto == nil || auto.crossUID {
		t.Fatalf("B in a 0700 place: %v", auto)
	}
	if b := autoOf(w, true); b != nil {
		t.Fatalf("a cross-uid B %s where every place is 0700", b.real)
	}
	root, _, _ := splitPath(ps.tmp)
	for _, p := range []string{root, ps.tmpdir} {
		if err := os.Chmod(p, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	cross := autoOf(w, true)
	if cross == nil || cross.kind != kindTmpdir || !cross.crossUID {
		t.Fatalf("cross-uid B %v, want $TMPDIR's (the 0700 B in /tmp passed over)", cross)
	}
	if autoOf(w, false) != auto || autoOf(w, true) != cross {
		t.Fatal("the world's B changed")
	}

	w2, ps2 := testWorld(t, nil)
	root2, _, _ := splitPath(ps2.tmp)
	for _, p := range []string{root2, ps2.tmp} {
		if err := os.Chmod(p, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	a := autoOf(w2, false)
	if c := autoOf(w2, true); a == nil || c != a || !a.crossUID {
		t.Fatalf("auto %v, cross %v: want one B", a, c)
	}
}

// tmpPath (a real-path spelling not usable with a private /tmp) holds when
// the place as configured, or B's real path, goes through a directory a
// private /tmp replaces, component by component or below its real path; the
// test places are under /tmp, so with /tmp among them it holds, and with
// only another directory it does not.
func TestTmpPathIsAboutTheDirectoriesAPrivateTmpReplaces(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	if b := autoOf(w, false); b == nil || !b.tmpPath {
		t.Fatalf("B under /tmp: %v", b)
	}
	w2, _ := testWorld(t, func(c *config, _ testPlaces) { c.privateTmps = []string{"/nonexistent-private-tmp"} })
	if b := autoOf(w2, false); b == nil || b.tmpPath {
		t.Fatalf("B under no private tmp: %v", b)
	}
	for _, c := range []struct {
		p    string
		want bool
	}{
		{"/tmp", true}, {"/tmp/", true}, {"/tmp/a/b", true}, {"/var/tmp/x", true},
		{"/tmpx", false}, {"/var/tmpx/y", false}, {"/var/folders/x", false},
	} {
		if got := envWorld().throughPrivateTmp(c.p); got != c.want {
			t.Errorf("throughPrivateTmp(%q) = %v", c.p, got)
		}
	}
	if real, err := realDir("/tmp"); err == nil && real != "/tmp" {
		if !envWorld().throughPrivateTmp(real + "/sockat-0") {
			t.Errorf("%s/sockat-0 (the real path of /tmp) is not through /tmp", real)
		}
	}
}

// envWorld is a world of the process's environment, for its pure functions
// (nothing is made through it).
func envWorld() *world { return newWorld(envConfig()) }

// The B of the socket's directory (the place 4) is named .sockat.<10 random>,
// one per directory and kept, its kind "sockdir".
func TestTheBOfASocketsDirectory(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	real, _ := realDir(d)
	id, _ := statID(d)
	b := w.sockdirBase(real, id)
	if b == nil || b.kind != kindSockdir || !regexp.MustCompile(`^`+regexp.QuoteMeta(real)+`/\.sockat\.`+randomRE+`$`).MatchString(b.real) {
		t.Fatalf("B %v", b)
	}
	if w.sockdirBase(real, id) != b {
		t.Fatal("another B for the same directory")
	}
}

// Options.Base is checked before anything is made (SPEC-routes
// "B の作り方"): a relative path or one not ending with a name is an argument
// error (EINVAL), a missing parent ENOENT, a parent or B that is not a
// directory ENOTDIR, a symlink at B, B not ours or writable by others, or a
// directory on the way writable by others, EPERM, and with
// usable_cross_uid a directory on the way or B without search permission for
// others EPERM too. The check makes nothing anywhere; a B that passes is made
// later with mode 0711, kept, and given kind "given".
func TestOptionsBaseIsCheckedAndMakesNothing(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		markUntouched(t, p)
	}
	d := tmpDir(t)
	if err := os.WriteFile(d+"/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"own", "groupw", "own0700"} {
		if err := os.Mkdir(d+"/"+s, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(d+"/groupw", 0o770)
	os.Chmod(d+"/own", 0o711)
	if err := os.Symlink(d+"/own", d+"/link"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(d+"/open", 0o777); err != nil {
		t.Fatal(err)
	}
	os.Chmod(d+"/open", 0o777)
	for _, c := range []struct {
		base  string
		cross bool
		want  error
	}{
		{"rel/b", false, syscall.EINVAL},
		{d + "/b/", false, syscall.EINVAL},
		{d + "/..", false, syscall.EINVAL},
		{"/", false, syscall.EINVAL},
		{d + "/missing/b", false, syscall.ENOENT},
		{d + "/file/b", false, syscall.ENOTDIR},
		{d + "/file", false, syscall.ENOTDIR},
		{d + "/link", false, syscall.EPERM},
		{d + "/groupw", false, syscall.EPERM},
		{d + "/open/b", false, syscall.EPERM},
		{d + "/own/b", true, syscall.EPERM},
		{d + "/b", false, nil},
		{d + "/own", false, nil},
		{d + "/own0700", false, nil},
	} {
		err := w.checkGiven(c.base, c.cross)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("checkGiven(%q, cross %v) = %v, want %v", c.base, c.cross, err, c.want)
		}
		var pe *os.PathError
		if err != nil && (!errors.As(err, &pe) || pe.Path != c.base) {
			t.Errorf("checkGiven(%q): %T %v is not an *os.PathError of the base", c.base, err, err)
		}
	}
	if exists(d + "/b") {
		t.Fatal("the check made B")
	}
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertUntouched(t, p)
	}
	b, err := w.givenBase(d+"/b", anyFits)
	if err != nil || b.kind != kindGiven || modeOf(t, d+"/b") != 0o711 {
		t.Fatalf("given B %v, %v", b, err)
	}
	if again, err := w.givenBase(d+"/b", anyFits); err != nil || again != b {
		t.Fatalf("given again: %v, %v", again, err)
	}
	if b, err := w.givenBase(d+"/own0700", anyFits); err != nil || modeOf(t, d+"/own0700") != 0o700 || b.crossUID {
		t.Fatalf("an existing given B: %v, %v", b, err)
	}
	if err := os.Chmod(d, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := w.checkGiven(d+"/own/b", true); err != nil {
		t.Fatalf("cross-uid base below directories others can search: %v", err)
	}
}

// The safety of Options.Base (the ancestors, B's owner, nobody else writing)
// is checked at every call, but whether others can search it all the way
// (UsableCrossUID) is decided when the world first holds the B and not
// looked at again (SPEC-routes "実体の置き場"): a held B made 0711 under
// directories others can search stays good for a cross-uid bind after it is
// set to 0700, a held B of 0700 stays EPERM after it is set to 0711, while a
// B the world does not hold is judged by its mode now. A held B made
// writable by its group fails at the next call whatever was decided before.
func TestTheCrossUIDJudgmentOfAGivenBIsTheOneWhenItWasFirstHeld(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	if err := os.Chmod(d, 0o711); err != nil {
		t.Fatal(err)
	}
	open, closed := d+"/open", d+"/closed"
	if _, err := w.givenBase(open, anyFits); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	if b, err := w.givenBase(closed, anyFits); err != nil || b.crossUID {
		t.Fatalf("a 0700 given B: %+v, %v", b, err)
	}
	os.Chmod(open, 0o700)
	os.Chmod(closed, 0o711)
	if err := w.checkGiven(open, true); err != nil {
		t.Fatalf("a held B judged usable across uids, now 0700: %v", err)
	}
	if err := w.checkGiven(closed, true); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("a held B judged not usable across uids, now 0711: %v", err)
	}
	other, _ := testWorld(t, nil)
	if err := other.checkGiven(open, true); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("a 0700 B not held: %v", err)
	}
	if err := other.checkGiven(closed, true); err != nil {
		t.Fatalf("a 0711 B not held: %v", err)
	}
	os.Chmod(closed, 0o731)
	if err := w.checkGiven(closed, false); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("a held B its group can write to: %v", err)
	}
}

// Under a umask that takes the owner's read bit (0477), mkdirat(0700) makes
// a directory of mode 0300 the owner could not open for reading: B and D are
// set to 0711 by their name before they are opened, so B and D are made all
// the same, of mode 0711, and so is the B of Options.Base. The umask belongs
// to the whole process, hence a child.
func TestUnderAUmaskTakingTheOwnersBitsBAndDAreMadeOf0711(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildBUnderAUmask", tmpDir(t))
}

func TestChildBUnderAUmask(t *testing.T) {
	asChild(t)
	if !unprivileged(t) {
		return
	}
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	syscall.Umask(0o477)
	b := autoOf(w, false)
	var dd *procD
	var err error
	if b != nil {
		dd, err = b.ownD(&w.cfg)
	}
	g, gerr := w.givenBase(d+"/b", anyFits)
	syscall.Umask(0o077)
	if b == nil || err != nil {
		t.Fatalf("B %v, D: %v", b, err)
	}
	if m := modeOf(t, b.real); m != 0o711 {
		t.Fatalf("B's mode %o", m)
	}
	if m := modeOf(t, b.real+"/"+dd.name); m != 0o711 {
		t.Fatalf("D's mode %o", m)
	}
	if gerr != nil || modeOf(t, g.real) != 0o711 {
		t.Fatalf("the given B: %v", gerr)
	}
}

// A B or D the world holds that was removed (an old systemd-tmpfiles, macOS's
// cleaning of $TMPDIR, a hand) is dropped at the next call and made again
// (SPEC-routes "実体の置き場", hub 13cb9201), whether it is gone (st_nlink
// 0) or its name now names another directory (removed and made again by
// someone): a D removed is made again in the same B; a B removed (with its
// D) is made again, and so is its D; a B whose name was given to another
// directory is passed over for that directory. The B of the socket's
// directory and Options.Base's are made again the same way. The new B and D
// are on disk where the names say.
func TestARemovedBOrDIsMadeAgain(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	b := autoOf(w, false)
	d, err := b.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Rmdir(b.real + "/" + d.name); err != nil {
		t.Fatal(err)
	}
	d2, err := b.ownD(&w.cfg)
	if err != nil || d2 == d || !exists(b.real+"/"+d2.name) {
		t.Fatalf("D after its removal: %+v, %v", d2, err)
	}
	if err := os.RemoveAll(ps.tmp + "/" + uidName()); err != nil {
		t.Fatal(err)
	}
	b2 := autoOf(w, false)
	if b2 == nil || b2 == b || b2.kind != kindTmp || !exists(ps.tmp+"/"+uidName()) {
		t.Fatalf("B after its removal: %+v", b2)
	}
	if d3, err := b2.ownD(&w.cfg); err != nil || !exists(b2.real+"/"+d3.name) {
		t.Fatalf("D in the B made again: %+v, %v", d3, err)
	}
	if err := os.Rename(ps.tmp+"/"+uidName(), ps.tmp+"/moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ps.tmp+"/"+uidName(), 0o700); err != nil {
		t.Fatal(err)
	}
	b3 := autoOf(w, false)
	if b3 == nil || b3 == b2 || b3.id != mustStatID(t, ps.tmp+"/"+uidName()) {
		t.Fatalf("B whose name names another directory: %+v", b3)
	}

	sd := tmpDir(t)
	real, _ := realDir(sd)
	id, _ := statID(sd)
	s := w.sockdirBase(real, id)
	os.RemoveAll(s.real)
	if s2 := w.sockdirBase(real, id); s2 == nil || s2 == s || !exists(s2.real) {
		t.Fatalf("the socket's directory's B after its removal: %+v", s2)
	}
	g := tmpDir(t) + "/b"
	gb, err := w.givenBase(g, anyFits)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(g)
	if gb2, err := w.givenBase(g, anyFits); err != nil || gb2 == gb || !exists(g) {
		t.Fatalf("Options.Base's B after its removal: %+v, %v", gb2, err)
	}
}

// D is made in B once per (process, B), named by 10 random letters or
// digits, mode 0711 (so a real-path spelling through a 0711 B can be
// followed by another uid), and held with a shared lock: another open of it
// cannot take an exclusive lock (which is how sweep() tells a live D), while
// a shared one can (another live process's D).
func TestDIsMadeOnceLockedAndOf0711(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	b := autoOf(w, false)
	d, err := b.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^` + randomRE + `$`).MatchString(d.name) {
		t.Fatalf("D's name %q", d.name)
	}
	if again, err := b.ownD(&w.cfg); err != nil || again != d {
		t.Fatalf("D again: %v %v", again, err)
	}
	p := b.real + "/" + d.name
	if m := modeOf(t, p); m != 0o711 {
		t.Fatalf("D's mode %o", m)
	}
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("an exclusive lock on a held D: %v", err)
	}
	if err := unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB); err != nil {
		t.Fatalf("a shared lock on a held D: %v", err)
	}
	if es := entries(t, b.real); len(es) != 1 || es[0] != d.name {
		t.Fatalf("B holds %v", es)
	}
}

// A D taken away between its mkdirat and the check of its name after the
// lock (another process's sweep() that saw it unlocked and removed it; here
// renamed away, another directory put at its name, just before the lock) is
// not used: its fd is dropped, D is made again under another name, and what
// was put at the first name is left alone. The hook is a package variable,
// hence a child.
func TestADTakenAwayBeforeItIsLockedIsMadeAgain(t *testing.T) {
	t.Parallel()
	runChild(t, "TestChildDTakenAway", tmpDir(t))
}

func TestChildDTakenAway(t *testing.T) {
	asChild(t)
	w, _ := testWorld(t, nil)
	b := autoOf(w, false)
	var first string
	lockD = func(fd, how int) error {
		if first == "" {
			es := entries(t, b.real)
			if len(es) != 1 {
				t.Fatalf("B holds %v", es)
			}
			first = es[0]
			if err := os.Rename(b.real+"/"+first, b.real+"/gone"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(b.real+"/"+first, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return unix.Flock(fd, how)
	}
	d, err := b.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d.name == first || d.name == "gone" {
		t.Fatalf("D %s is the one taken away", d.name)
	}
	if m := modeOf(t, b.real+"/"+first); m != 0o700 {
		t.Fatalf("what was put at %s changed (mode %o)", first, m)
	}
	var st unix.Stat_t
	if err := unix.Fstat(d.fd, &st); err != nil {
		t.Fatal(err)
	}
	if id, _ := statID(b.real + "/" + d.name); id != (fileID{uint64(st.Dev), uint64(st.Ino)}) {
		t.Fatal("D's fd is not the directory at its name")
	}
}

// E: a resolve E is named by B's counter in base 36 (0, 1, ..., a, ...),
// points to its target and is removed by remove (a second remove, or one of
// an E already gone, is no error); a call's E is 10 random letters or digits.
// Both are made in D.
func TestEIsACounterForResolveAndRandomForACall(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	b := autoOf(w, false)
	d, err := b.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var made []string
	for i := range 12 {
		if n := b.nextEName(); n != strconv.FormatUint(uint64(i), 36) {
			t.Fatalf("next E name %q at %d", n, i)
		}
		e, err := b.resolveE(d, "/target/"+strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, e.name)
		if got, err := os.Readlink(e.path()); err != nil || got != "/target/"+strconv.Itoa(i) {
			t.Fatalf("E %s -> %q, %v", e.path(), got, err)
		}
	}
	if made[10] != "a" || made[11] != "b" {
		t.Fatalf("names %v", made)
	}
	e, err := b.callE(d, "/call", w.cfg.random)
	if err != nil || !regexp.MustCompile(`^`+randomRE+`$`).MatchString(e.name) {
		t.Fatalf("call E %v %v", e, err)
	}
	for range 2 {
		if err := e.remove(); err != nil {
			t.Fatal(err)
		}
	}
	if exists(e.path()) {
		t.Fatal("E left after remove")
	}
	if es := entries(t, b.real+"/"+d.name); len(es) != 12 {
		t.Fatalf("D holds %v", es)
	}
}

// procB is used only when /proc/<pid>/fd/<B's fd> is B itself (same
// (st_dev, st_ino)): with the real /proc on Linux it is; with /proc missing,
// or a /proc whose <pid>/fd/<fd> is some other directory (a /proc of another
// PID namespace), it is not. Elsewhere there is no procB.
func TestProcBIsUsedOnlyWhereItIsB(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	b := autoOf(w, false)
	p, ok := w.procB(b)
	if !hasProc {
		if ok {
			t.Fatalf("procB %s without /proc", p)
		}
		return
	}
	if !ok || p != fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), b.fd) {
		t.Fatalf("procB %q %v", p, ok)
	}
	fake := tmpDir(t)
	w.cfg.proc = fake
	if _, ok := w.procB(b); ok {
		t.Fatal("procB through a missing /proc")
	}
	if err := os.MkdirAll(fmt.Sprintf("%s/%d/fd/%d", fake, os.Getpid(), b.fd), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.procB(b); ok {
		t.Fatal("procB through a /proc whose fd is another directory")
	}
}

// The base values of the diagnostics: procB of a B in a temporary place is
// "proc", a real path the place's kind; a B of the socket's directory or
// Options.Base is that B whatever the spelling (SPEC-routes "綴りの形").
func TestBaseValues(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		kind    string
		viaProc bool
		want    string
	}{
		{kindTmp, true, "proc"}, {kindRuntime, true, "proc"}, {kindTmpdir, true, "proc"},
		{kindTmp, false, "tmp"}, {kindRuntime, false, "runtime"}, {kindTmpdir, false, "tmpdir"},
		{kindSockdir, true, "sockdir"}, {kindGiven, true, "given"}, {kindGiven, false, "given"},
	} {
		if got := baseValue(&base{kind: c.kind}, c.viaProc); got != c.want {
			t.Errorf("baseValue(%s, proc %v) = %q", c.kind, c.viaProc, got)
		}
	}
}

// Many goroutines of one process, and several processes, make B and D at
// once in the same places: every one gets a B, there is one B in the place
// (sockat-<euid>, no fallback), each process one D (the goroutines share
// it), and every process's D is distinct. Run in the parent and in children
// started at the same time (each child does what the parent does).
func TestConcurrentCallsMakeOneBAndOneDPerProcess(t *testing.T) {
	t.Parallel()
	root := tmpDir(t)
	place := root + "/tmp"
	if err := os.Mkdir(place, 0o700); err != nil {
		t.Fatal(err)
	}
	const children = 4
	outs := make([]string, children)
	errs := make([]error, children)
	var wg sync.WaitGroup
	for i := range children {
		wg.Go(func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestChildConcurrentBAndD$", "-test.v", "-test.count=1")
			cmd.Env = append(os.Environ(), childEnv+"=TestChildConcurrentBAndD", "SOCKAT_PLACE="+place)
			out, err := cmd.CombinedOutput()
			outs[i], errs[i] = string(out), err
		})
	}
	mine := concurrentBAndD(t, place)
	wg.Wait()
	ds := map[string]bool{mine: true}
	for i := range children {
		if errs[i] != nil || !strings.Contains(outs[i], "--- PASS: TestChildConcurrentBAndD") {
			t.Fatalf("child %d: %v\n%s", i, errs[i], outs[i])
		}
		ds[readChildReport(t, outs[i])] = true
	}
	if len(ds) != children+1 {
		t.Fatalf("D names %v, want %d distinct", ds, children+1)
	}
	if es := entries(t, place); len(es) != 1 || es[0] != uidName() {
		t.Fatalf("the place holds %v", es)
	}
}

func TestChildConcurrentBAndD(t *testing.T) {
	asChild(t)
	childReport("%s", concurrentBAndD(t, os.Getenv("SOCKAT_PLACE")))
}

// concurrentBAndD makes B and D from 32 goroutines of a world whose /tmp is
// place, and returns the one D's name.
func concurrentBAndD(t *testing.T, place string) string {
	t.Helper()
	cfg := envConfig()
	cfg.tmp = place
	cfg.getenv = func(string) string { return "" }
	w := newWorld(cfg)
	defer closeWorld(w)
	const n = 32
	got := make([]*procD, n)
	bs := make([]*base, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			bs[i] = autoOf(w, false)
			if bs[i] != nil {
				got[i], _ = bs[i].ownD(&w.cfg)
			}
		})
	}
	close(start)
	wg.Wait()
	for i := range n {
		if bs[i] == nil || bs[i] != bs[0] || got[i] == nil || got[i] != got[0] {
			t.Fatalf("goroutine %d: B %v D %v, goroutine 0: B %v D %v", i, bs[i], got[i], bs[0], got[0])
		}
	}
	if !strings.HasSuffix(bs[0].real, "/"+uidName()) {
		t.Fatalf("B %s", bs[0].real)
	}
	return got[0].name
}

// The ancestors' check: the root, root-owned directories and our own pass;
// one others can write to fails unless sticky; with otherX a directory
// without search permission for others fails. Run against the process's own
// directories, which this test creates under /tmp.
func TestCheckAncestors(t *testing.T) {
	t.Parallel()
	d := tmpDir(t)
	real, _ := realDir(d)
	if err := checkAncestors(real, false); err != nil {
		t.Fatal(err)
	}
	if err := checkAncestors(real, true); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("a 0700 directory for another uid: %v", err)
	}
	os.Chmod(d, 0o711)
	if err := checkAncestors(real, true); err != nil {
		t.Fatalf("a 0711 directory under /tmp for another uid: %v", err)
	}
	os.Chmod(d, 0o777)
	if err := checkAncestors(real, false); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("a directory others can write to: %v", err)
	}
	unix.Chmod(d, 0o1777)
	if err := checkAncestors(real, false); err != nil {
		t.Fatalf("a sticky directory of ours: %v", err)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if err := checkAncestors("/", false); err != nil {
			t.Fatalf("/: %v", err)
		}
	}
}
