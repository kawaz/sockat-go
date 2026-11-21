package sockat

// Sweep and SweepBase (sweep.go) through test places: what they remove (the
// D of a process that has ended, with its symlinks) and what they leave.

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// holdD starts a child process of this test binary that makes its D in the B
// of a world whose /tmp is place (with a resolve E in it, as a bind leaves),
// and keeps it, alive, until the returned function is called; that function
// lets it end and waits for it. It returns the D's name.
func holdD(t *testing.T, place string) (string, func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildHoldD$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), childEnv+"=TestChildHoldD", "SOCKAT_PLACE="+place)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	end := func() {
		in.Close()
		io.Copy(io.Discard, out)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("the child holding D: %v", err)
		}
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "sockat-child: D="); ok {
			return d, end
		}
	}
	end()
	t.Fatal("the child reported no D")
	return "", nil
}

// TestChildHoldD is the child of holdD: it reports its D and waits for its
// stdin to close.
func TestChildHoldD(t *testing.T) {
	asChild(t)
	w := newWorld(placeConfig(os.Getenv("SOCKAT_PLACE")))
	defer closeWorld(w)
	b := autoOf(w, false)
	if b == nil {
		t.Fatal("no B")
	}
	d, err := b.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolveE(d, "/nonexistent"); err != nil {
		t.Fatal(err)
	}
	childReport("D=%s", d.name)
	io.Copy(io.Discard, os.Stdin)
}

// A D whose process is alive is left alone by sweep, the process's lock on
// it telling it apart; once the process has ended (its D still there, with
// the E it left), the next sweep removes the D and its symlinks. The B
// stays. The child is another process, as the processes that share a B are.
func TestSweepRemovesTheDOfAnEndedProcessOnly(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	name, end := holdD(t, ps.tmp)
	b := ps.tmp + "/" + uidName()
	if err := w.sweep(); err != nil {
		t.Fatal(err)
	}
	if !exists(b + "/" + name) {
		t.Fatal("sweep removed the D of a live process")
	}
	end()
	if len(entries(t, b+"/"+name)) == 0 {
		t.Fatal("the ended child's D lost its E before the sweep")
	}
	if err := w.sweep(); err != nil {
		t.Fatal(err)
	}
	if exists(b+"/"+name) || !exists(b) {
		t.Fatalf("after the child ended: D there %v, B there %v", exists(b+"/"+name), exists(b))
	}
}

// What sweep leaves in a B besides live D: a symlink right in B (the E of a
// connect that may be under way), a D holding anything but symlinks (a file,
// a directory), and this process's own D (locked by the process). A dead D
// that holds only symlinks goes. The B of a socket's directory
// (.sockat.<random>) is not looked at, even with a dead D in it. A place with
// no B gets none.
func TestSweepLeavesWhatIsNotADeadDOfSymlinks(t *testing.T) {
	t.Parallel()
	w, ps := testWorld(t, nil)
	b := autoOf(w, false)
	own, err := b.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(p string) {
		t.Helper()
		if err := os.Mkdir(p, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	ln := func(p string) {
		t.Helper()
		if err := os.Symlink("/nonexistent", p); err != nil {
			t.Fatal(err)
		}
	}
	ln(b.real + "/connectE")
	// The D below are named as a D is (10 letters and digits), so that what
	// keeps them is what they hold, not their name.
	mk(b.real + "/withFile00")
	if err := os.WriteFile(b.real+"/withFile00/f", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mk(b.real + "/withDir000")
	mk(b.real + "/withDir000/sub")
	mk(b.real + "/deadD00000")
	ln(b.real + "/deadD00000/0")
	ln(b.real + "/deadD00000/1")
	sd := tmpDir(t)
	others := []string{sd + "/.sockat.AbCdEfGhIj"}
	for _, o := range others {
		mk(o)
		mk(o + "/deadD00000")
		ln(o + "/deadD00000/0")
	}
	if err := w.sweep(); err != nil {
		t.Fatal(err)
	}
	want := []string{"connectE", own.name, "withDir000", "withFile00"}
	slices.Sort(want)
	if got := entries(t, b.real); !slices.Equal(got, want) {
		t.Fatalf("B holds %v, want %v", got, want)
	}
	for _, o := range others {
		if !exists(o + "/deadD00000/0") {
			t.Fatalf("sweep looked into %s", o)
		}
	}
	for _, p := range []string{ps.runtime, ps.tmpdir} {
		assertEmpty(t, p)
	}
}

// Sweep and SweepBase only look at entries of B named as a D is (10 letters
// and digits, as randomChars makes them): a directory of another name is not
// one this library made, so it stays even when it is unlocked and holds only
// symlinks (an application's links/ in its Options.Base, say) or nothing at
// all. Names one character short or long, or with a character outside the
// letters and digits, are not of the form either. A dead D of the form next
// to them still goes, so the names are what keeps them.
func TestSweepLooksAtNamesOfTheFormOfDOnly(t *testing.T) {
	t.Parallel()
	notD := []string{"links", "empty", "abcdefghi", "abcdefghijk", "abcde_ghij", "abcde.ghij"}
	fill := func(b string) {
		t.Helper()
		for _, n := range append(slices.Clone(notD), "deadD00000") {
			if err := os.Mkdir(b+"/"+n, 0o711); err != nil {
				t.Fatal(err)
			}
			if n != "empty" {
				if err := os.Symlink("/nonexistent", b+"/"+n+"/0"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	want := slices.Clone(notD)
	slices.Sort(want)

	w, _ := testWorld(t, nil)
	b := autoOf(w, false)
	if b == nil {
		t.Fatal("no B")
	}
	fill(b.real)
	if err := w.sweep(); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, b.real); !slices.Equal(got, want) {
		t.Fatalf("after sweep B holds %v, want %v", got, want)
	}

	d := tmpDir(t)
	g := d + "/b"
	if _, err := w.givenBase(g, anyFits); err != nil {
		t.Fatal(err)
	}
	fill(g)
	if err := SweepBase(g); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, g); !slices.Equal(got, want) {
		t.Fatalf("after SweepBase the given B holds %v, want %v", got, want)
	}
}

// SweepBase sweeps the B given as Options.Base as Sweep sweeps the places:
// a dead D goes, the D of a live world stays. It makes nothing: a base not
// there, or whose parent is not there, is no error. A base of the wrong form
// is EINVAL, a symlink EPERM, a file ENOTDIR, a B writable by its group EPERM,
// each an *os.PathError of Op "sweep" for the base.
func TestSweepBaseSweepsTheGivenB(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	g := d + "/b"
	if _, err := w.givenBase(g, anyFits); err != nil {
		t.Fatal(err)
	}
	gb := w.given[mustStatID(t, g)]
	live, err := gb.ownD(&w.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(g+"/deadD00000", 0o711); err != nil {
		t.Fatal(err)
	}
	if err := SweepBase(g); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, g); !slices.Equal(got, []string{live.name}) {
		t.Fatalf("the given B holds %v", got)
	}
	for _, p := range []string{d + "/missing", d + "/missing/b"} {
		if err := SweepBase(p); err != nil || exists(p) {
			t.Fatalf("SweepBase(%s): %v", p, err)
		}
	}
	if err := os.Symlink(g, d+"/link"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d+"/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(d+"/groupw", 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(d+"/groupw", 0o770)
	for _, c := range []struct {
		base  string
		errno syscall.Errno
	}{{"rel", syscall.EINVAL}, {d + "/", syscall.EINVAL}, {d + "/link", syscall.EPERM}, {d + "/file", syscall.ENOTDIR}, {d + "/groupw", syscall.EPERM}} {
		err := SweepBase(c.base)
		var pe *os.PathError
		if !errors.Is(err, c.errno) || !errors.As(err, &pe) || pe.Op != "sweep" || pe.Path != c.base {
			t.Errorf("SweepBase(%q) = %v, want %v", c.base, err, c.errno)
		}
	}
}

// SweepBase checks base as Options.Base is checked (SPEC "sweep"): when the
// directory base would be in is a regular file, the error is ENOTDIR, as it
// is for a long bind with that Options.Base, and not EPERM (the file is not a
// directory others could replace: it is no directory at all). Nothing is
// made or touched in the directory holding the file.
func TestSweepBaseOfABaseInAFileIsENOTDIRAsForOptionsBase(t *testing.T) {
	t.Parallel()
	w, _ := testWorld(t, nil)
	d := tmpDir(t)
	if err := os.WriteFile(d+"/file", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	markUntouched(t, d)
	base := d + "/file/b"
	_, _, err := w.listenEx(nest(t, tmpDir(t), 300, "s.sock"), Options{Base: base})
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("a long bind with the base %s: %v, want ENOTDIR", base, err)
	}
	err = SweepBase(base)
	var pe *os.PathError
	if !errors.Is(err, syscall.ENOTDIR) || !errors.As(err, &pe) || pe.Op != "sweep" || pe.Path != base {
		t.Fatalf("SweepBase(%q) = %v, want ENOTDIR", base, err)
	}
	assertUntouched(t, d)
}

// Sweep goes through the process's world (in the tests, the place TestMain
// made, which only this test binary and its children use): this process's
// own D, made by a long bind, survives it. Not parallel, so that no other
// test of this binary is between making and locking its D meanwhile.
func TestSweep(t *testing.T) {
	p := nest(t, tmpDir(t), 300, "s.sock")
	listenPublic(t, p)
	if err := Sweep(); err != nil {
		t.Fatal(err)
	}
	for _, b := range defaultWorld.bases() {
		if b.d != nil && !exists(b.real+"/"+b.d.name) {
			t.Fatalf("Sweep removed this process's D in %s", b.real)
		}
	}
}
