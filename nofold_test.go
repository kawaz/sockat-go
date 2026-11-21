package sockat

// A path is never folded lexically (SPEC "長いパス": ".." and "." are not
// removed on the string). To the kernel "a/sym/.." is the parent of the
// directory sym leads to, not "a": the directory of a path is looked up as
// the kernel looks it up, for the spelling a route hands to the kernel, for
// what an E points to, and for the socket file Remove removes.
//
// Every test here goes through a fork (newFork): <r>/a/sym is a symlink to
// <r>/b/target, so <r>/a/sym/.. is <r>/b to the kernel and <r>/a when folded
// on the string. The directories of a long path are made below <r>/b (where
// the kernel goes) and, as a decoy, below <r>/a too (where folding goes), so
// that a folded lookup lands on a real directory rather than failing, and
// the test sees where the socket was put. A decoy socket at the folded place
// stands for another server's socket, which nothing of sockat may touch.

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// fork is a directory r holding a/, b/target/ and the symlink
// a/sym -> <r>/b/target, and the spelling given of <r>/a/sym/.. (absolute,
// or relative to a cwd chosen for it).
type fork struct{ r, given string }

func newFork(t *testing.T, r string) fork {
	t.Helper()
	for _, d := range []string{r + "/a", r + "/b/target"} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(r+"/b/target", r+"/a/sym"); err != nil {
		t.Fatal(err)
	}
	return fork{r, r + "/a/sym/../"}
}

// via is the path given (tail below the spelling of <r>/a/sym/..), kernel
// where the kernel takes it (<r>/b/<tail>), folded where folding it on the
// string would take it (<r>/a/<tail>).
func (f fork) via(tail string) string    { return f.given + tail }
func (f fork) kernel(tail string) string { return f.r + "/b/" + tail }
func (f fork) folded(tail string) string { return f.r + "/a/" + tail }

// decoy is what is at the folded place of a path.
type decoy int

const (
	decoyNone   decoy = iota // the folded place's directory is not there
	decoyDirs                // the directory is there, empty
	decoySocket              // another server's socket is there, listening
)

func (d decoy) String() string {
	return [...]string{"no decoy", "decoy directories", "decoy socket"}[d]
}

// longTail makes the directories of a tail ending with name such that
// via(tail) is total bytes, below <r>/b and, from decoyDirs on, below <r>/a
// too; with decoySocket a listening socket is put at folded(tail). It
// returns the tail.
func (f fork) longTail(t *testing.T, total int, name string, d decoy) string {
	t.Helper()
	p := nestTail(t, f.r+"/b", total-len(f.via("")), name)
	tail := p[len(f.kernel("")):]
	if d >= decoyDirs {
		dir, _, _ := splitPath(f.folded(tail))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if d == decoySocket {
		listenAt(t, f.folded(tail))
	}
	return tail
}

// checkPlaced checks that the bind of tail put the socket where the kernel
// takes the path, and left the folded place as it was (nothing there, or
// the decoy socket).
func (f fork) checkPlaced(t *testing.T, what, tail string, d decoy) {
	t.Helper()
	if !isSocket(f.kernel(tail)) {
		t.Fatalf("%s, %s: no socket where the kernel takes the path (%s)", what, d, f.kernel(tail))
	}
	if d == decoySocket {
		if !isSocket(f.folded(tail)) {
			t.Fatalf("%s, %s: the decoy socket is gone", what, d)
		}
	} else if exists(f.folded(tail)) {
		t.Fatalf("%s, %s: a socket where folding takes the path (%s)", what, d, f.folded(tail))
	}
}

// checkRemoved checks what a Remove left: no socket where the kernel takes
// the path, and the folded place as it was.
func (f fork) checkRemoved(t *testing.T, what, tail string, d decoy) {
	t.Helper()
	if exists(f.kernel(tail)) {
		t.Errorf("%s, %s: Remove left the socket file %s", what, d, f.kernel(tail))
	}
	if d == decoySocket && !isSocket(f.folded(tail)) {
		t.Errorf("%s, %s: Remove removed the decoy socket at the folded place", what, d)
	}
	if t.Failed() {
		t.FailNow()
	}
}

// sameFile reports whether a and b name the same file now (stat, following
// symlinks).
func sameFile(a, b string) bool {
	ia, ok1 := statID(a)
	ib, ok2 := statID(b)
	return ok1 && ok2 && ia == ib
}

// A long path through <r>/a/sym/.. is bound where the kernel takes it, on
// every route of the bind order here: link-dir (procB, and the real path of
// B without /proc), thread-cwd / at (a file name past link-dir, link-file
// off), and link-file where bind(2) follows a symlink. The E of link-dir and
// thread-cwd / at points to the socket's directory as the kernel finds it
// (<r>/b/...), so the socket file is made there, never at <r>/a/...: with
// no directory there (folding would fail with ENOENT), an empty one
// (folding would put the socket there) or another server's socket there
// (folding would fail with EADDRINUSE). The spelling the kernel keeps (a
// Linked one) names that socket file, a dial by the path reaches the
// listener, and Remove removes that file and leaves the decoy socket alone.
func TestALongBindIsNotFoldedLexically(t *testing.T) {
	t.Parallel()
	type bindCase struct {
		what   string
		mod    func(*config, testPlaces)
		name   string
		want   route
		linked bool // the spelling the kernel keeps names the socket file
	}
	cases := []bindCase{{"link-dir", nil, "s.sock", routeLinkDir, true}}
	if procHere() {
		cases = append(cases, bindCase{"link-dir without /proc", func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc" }, "s.sock", routeLinkDir, true})
	}
	noLinkFile := func(c *config, _ testPlaces) { c.bindFollows = followsNo }
	switch {
	case atSupported:
		cases = append(cases, bindCase{"at", noLinkFile, threadName, routeAt, false})
	case hasThreadCwd && threadCwdAvailable():
		cases = append(cases, bindCase{"thread-cwd", noLinkFile, threadName, routeThreadCwd, false})
	}
	if bindFollowsSymlink() {
		cases = append(cases, bindCase{"link-file", func(c *config, _ testPlaces) { c.bindFollows = followsYes }, strings.Repeat("n", 100), routeLinkFile, true})
	}
	for _, c := range cases {
		for _, d := range []decoy{decoyNone, decoyDirs, decoySocket} {
			t.Run(c.what+", "+d.String(), func(t *testing.T) {
				w, _ := testWorld(t, c.mod)
				f := newFork(t, tmpDir(t))
				tail := f.longTail(t, 600, c.name, d)
				p := f.via(tail)
				l, b, err := w.listenEx(p, Options{})
				if err != nil {
					t.Fatalf("%s, %s: %v", c.what, d, err)
				}
				t.Cleanup(func() { l.Close() })
				if b.Route != string(c.want) {
					t.Fatalf("%s, %s: route %s", c.what, d, b.Route)
				}
				f.checkPlaced(t, c.what, tail, d)
				if c.linked && !sameFile(l.Addr().String(), f.kernel(tail)) {
					t.Fatalf("%s, %s: the spelling %s does not name %s", c.what, d, l.Addr(), f.kernel(tail))
				}
				conn, err := Dial(p)
				if err != nil {
					t.Fatalf("%s, %s: dial: %v", c.what, d, err)
				}
				ping(t, l, conn)
				conn.Close()
				if err := b.File.Remove(); err != nil {
					t.Fatalf("%s, %s: Remove: %v", c.what, d, err)
				}
				f.checkRemoved(t, c.what, tail, d)
			})
		}
	}
}

// A short path through <r>/a/sym/.. (the net package binds it; the kernel
// puts the socket at <r>/b/s.sock) is recorded by ListenEx, and by
// SocketFile of ResolveForBind, as the kernel resolved it: with nothing at
// <r>/a/s.sock (where folding would find nothing to record) and with
// another server's socket there (which folding would record), Remove
// removes <r>/b/s.sock and leaves <r>/a/s.sock alone.
func TestAShortPathIsNotFoldedLexically(t *testing.T) {
	t.Parallel()
	for _, d := range []decoy{decoyDirs, decoySocket} {
		t.Run(d.String(), func(t *testing.T) {
			f := newFork(t, tmpDir(t))
			if d == decoySocket {
				listenAt(t, f.folded("s.sock"))
			}
			l, b, err := ListenEx(f.via("s.sock"), Options{})
			if err != nil {
				t.Fatalf("ListenEx, %s: %v", d, err)
			}
			t.Cleanup(func() { l.Close() })
			if b.Route != string(routeStandard) {
				t.Fatalf("ListenEx, %s: route %s", d, b.Route)
			}
			f.checkPlaced(t, "ListenEx", "s.sock", d)
			if err := b.File.Remove(); err != nil {
				t.Fatalf("ListenEx, %s: Remove: %v", d, err)
			}
			f.checkRemoved(t, "ListenEx", "s.sock", d)

			f = newFork(t, tmpDir(t))
			if d == decoySocket {
				listenAt(t, f.folded("s.sock"))
			}
			fb, err := ResolveForBind(f.via("s.sock"), Options{})
			if err != nil {
				t.Fatalf("ResolveForBind, %s: %v", d, err)
			}
			if fb.Path != f.via("s.sock") {
				t.Fatalf("ResolveForBind, %s: spelt %q, want the path as it is", d, fb.Path)
			}
			listenSpelling(t, fb.Path)
			f.checkPlaced(t, "ResolveForBind", "s.sock", d)
			sf, err := fb.SocketFile()
			if err != nil {
				t.Fatalf("ResolveForBind, %s: SocketFile: %v", d, err)
			}
			if err := sf.Remove(); err != nil {
				t.Fatalf("ResolveForBind, %s: Remove: %v", d, err)
			}
			f.checkRemoved(t, "ResolveForBind", "s.sock", d)
		})
	}
}

// The spellings of a resolve of a long path through <r>/a/sym/.. name the
// place the kernel takes the path to: the net package binding ForBind's
// Path (link-dir) puts the socket at <r>/b/..., never at <r>/a/...;
// ForConnect's Path (link-file) reaches that listener; SocketFile's Remove
// removes that file and leaves a decoy socket at <r>/a/... alone.
func TestALongResolveIsNotFoldedLexically(t *testing.T) {
	t.Parallel()
	for _, d := range []decoy{decoyNone, decoyDirs, decoySocket} {
		t.Run(d.String(), func(t *testing.T) {
			w, _ := testWorld(t, nil)
			f := newFork(t, tmpDir(t))
			tail := f.longTail(t, 600, "s.sock", d)
			p := f.via(tail)
			fb := resolveBindIn(t, w, p, Options{})
			if fb.Route != string(routeLinkDir) {
				t.Fatalf("%s: route %s", d, fb.Route)
			}
			l := listenSpelling(t, fb.Path)
			f.checkPlaced(t, "ResolveForBind", tail, d)
			if !sameFile(fb.Path, f.kernel(tail)) {
				t.Fatalf("%s: the spelling %s does not name %s", d, fb.Path, f.kernel(tail))
			}
			fc := resolveConnectIn(t, w, p, Options{})
			ping(t, l, dialSpelling(t, fc.Path))
			sf, err := fb.SocketFile()
			if err != nil {
				t.Fatalf("%s: SocketFile: %v", d, err)
			}
			if err := sf.Remove(); err != nil {
				t.Fatalf("%s: Remove: %v", d, err)
			}
			f.checkRemoved(t, "ResolveForBind", tail, d)
		})
	}
}

// A dial of a long path through <r>/a/sym/.. reaches the listener where
// the kernel takes the path (<r>/b/...), on every route of the connect
// order here that a path far from the cwd takes (proc-direct,
// thread-cwd-direct and link-file on Linux; thread-cwd-direct and link-file
// on macOS; the system's order elsewhere): with no directory at the folded
// place (the directory is looked up as the kernel looks it up, so it is not
// ENOENT), with an empty one, and with another server's socket there (not
// the one reached).
func TestALongDialIsNotFoldedLexically(t *testing.T) {
	t.Parallel()
	type dialCase struct {
		what string
		mod  func(*config, testPlaces)
		name string
		want route // "" for any
	}
	threadOff := func(c *config, _ testPlaces) { c.threadCwd = false }
	var cases []dialCase
	switch runtime.GOOS {
	case "linux":
		if procHere() {
			cases = append(cases, dialCase{"proc-direct", nil, strings.Repeat("p", 85), routeProcDirect})
		}
		if threadCwdAvailable() {
			cases = append(cases, dialCase{"thread-cwd-direct", nil, directName, routeThreadCwdDirect})
		}
		cases = append(cases,
			dialCase{"link-file", threadOff, directName, routeLinkFile},
			dialCase{"link-file without /proc", func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc"; c.threadCwd = false }, directName, routeLinkFile})
	case "darwin":
		cases = append(cases,
			dialCase{"thread-cwd-direct", nil, directName, routeThreadCwdDirect},
			dialCase{"link-file", threadOff, directName, routeLinkFile})
	default:
		cases = append(cases, dialCase{"the system's order", nil, directName, ""})
	}
	for _, c := range cases {
		for _, d := range []decoy{decoyNone, decoyDirs, decoySocket} {
			t.Run(c.what+", "+d.String(), func(t *testing.T) {
				w, _ := testWorld(t, c.mod)
				f := newFork(t, tmpDir(t))
				tail := f.longTail(t, 600, c.name, d)
				l := listenAt(t, f.kernel(tail))
				conn, r, err := w.dial(f.via(tail), Options{}, nil)
				if err != nil {
					t.Fatalf("%s, %s: %v", c.what, d, err)
				}
				if c.want != "" && r != c.want {
					t.Fatalf("%s, %s: route %s", c.what, d, r)
				}
				ping(t, l, conn)
				conn.Close()
			})
		}
	}
}

// A relative path is looked up from the cwd as the kernel looks it up, not
// by folding it onto the cwd's spelling: from a cwd <r> the path
// a/sym/../<tail>, and from a cwd entered through the symlink <r>/a/sym
// (getcwd(3) says <r>/b/target, $PWD says <r>/a/sym) the path ../<tail>.
// The kernel takes both to <r>/b/<tail>; folding the second onto $PWD (as
// os.Getwd reports the cwd) would take it to <r>/a/<tail>. In a child
// process with each cwd, see TestChildRelativeIsNotFoldedLexically.
func TestARelativePathIsNotFoldedLexically(t *testing.T) {
	t.Parallel()
	t.Run("from <r>", func(t *testing.T) {
		r := tmpDir(t)
		newFork(t, r)
		runChild(t, "TestChildRelativeIsNotFoldedLexically", r, "SOCKAT_FORK="+r, "SOCKAT_GIVEN=a/sym/../")
	})
	t.Run("from <r>/a/sym", func(t *testing.T) {
		r := tmpDir(t)
		newFork(t, r)
		runChild(t, "TestChildRelativeIsNotFoldedLexically", r+"/a/sym", "SOCKAT_FORK="+r, "SOCKAT_GIVEN=../")
	})
}

// In a child process whose cwd is SOCKAT_FORK (or its a/sym), with the
// relative spelling SOCKAT_GIVEN of <r>/a/sym/..: ListenEx of a short path
// and of a long one (link-dir) puts the socket where the kernel takes the
// path, and its Remove removes that file and leaves a decoy socket at the
// folded place alone; so does SocketFile of ResolveForBind of the short
// path; a dial of the long path reaches the listener. From the cwd <r>, a
// dial of the long path spelt absolute, whose spelling relative to the cwd
// fits, takes relative (no /proc, no thread cwd) and reaches the listener.
func TestChildRelativeIsNotFoldedLexically(t *testing.T) {
	asChild(t)
	f := fork{os.Getenv("SOCKAT_FORK"), os.Getenv("SOCKAT_GIVEN")}
	for i, d := range []decoy{decoyDirs, decoySocket} {
		t.Run("short, "+d.String(), func(t *testing.T) {
			name := "s" + string(rune('0'+i)) + ".sock"
			if d == decoySocket {
				listenAt(t, f.folded(name))
			}
			l, b, err := ListenEx(f.via(name), Options{})
			if err != nil {
				t.Fatalf("short, %s: %v", d, err)
			}
			t.Cleanup(func() { l.Close() })
			f.checkPlaced(t, "short", name, d)
			if err := b.File.Remove(); err != nil {
				t.Fatalf("short, %s: Remove: %v", d, err)
			}
			f.checkRemoved(t, "short", name, d)
		})
		t.Run("resolve, "+d.String(), func(t *testing.T) {
			name := "r" + string(rune('0'+i)) + ".sock"
			if d == decoySocket {
				listenAt(t, f.folded(name))
			}
			fb, err := ResolveForBind(f.via(name), Options{})
			if err != nil {
				t.Fatalf("resolve, %s: %v", d, err)
			}
			listenSpelling(t, fb.Path)
			f.checkPlaced(t, "resolve", name, d)
			sf, err := fb.SocketFile()
			if err != nil {
				t.Fatalf("resolve, %s: SocketFile: %v", d, err)
			}
			if err := sf.Remove(); err != nil {
				t.Fatalf("resolve, %s: Remove: %v", d, err)
			}
			f.checkRemoved(t, "resolve", name, d)
		})
	}
	for i, d := range []decoy{decoyNone, decoyDirs, decoySocket} {
		t.Run("long, "+d.String(), func(t *testing.T) {
			tail := f.longTail(t, 600, "l"+string(rune('0'+i))+".sock", d)
			l, b, err := ListenEx(f.via(tail), Options{})
			if err != nil {
				t.Fatalf("long, %s: %v", d, err)
			}
			t.Cleanup(func() { l.Close() })
			f.checkPlaced(t, "long", tail, d)
			conn, err := Dial(f.via(tail))
			if err != nil {
				t.Fatalf("long, %s: dial: %v", d, err)
			}
			ping(t, l, conn)
			conn.Close()
			if err := b.File.Remove(); err != nil {
				t.Fatalf("long, %s: Remove: %v", d, err)
			}
			f.checkRemoved(t, "long", tail, d)
		})
	}
	if f.given != "a/sym/../" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range []decoy{decoyNone, decoyDirs, decoySocket} {
		t.Run("relative, "+d.String(), func(t *testing.T) {
			tail := f.longTail(t, directMax, "d"+string(rune('0'+i))+".sock", d)
			w, _ := testWorld(t, func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc"; c.threadCwd = false })
			l := listenAt(t, f.kernel(tail))
			abs := cwd + "/" + f.via(tail)
			conn, r, err := w.dial(abs, Options{}, nil)
			if err != nil {
				t.Fatalf("relative, %s: %v", d, err)
			}
			if r != routeRelative {
				t.Fatalf("relative, %s: route %s", d, r)
			}
			ping(t, l, conn)
			conn.Close()
		})
	}
}

// Where bind(2) follows a symlink in the last component (macOS, NetBSD), a
// symlink at the path whose target goes through a/sym/.. (relative to the
// symlink's directory) leads the bind to <r>/b/s.sock: ListenEx records
// that socket file, following the target as the kernel does, and its
// Remove removes it, leaving the caller's symlink and a decoy socket at
// <r>/a/s.sock alone; so does SocketFile of ResolveForBind.
func TestASymlinkAtThePathIsFollowedWithoutFolding(t *testing.T) {
	t.Parallel()
	if !bindFollowsSymlink() {
		t.Skip("bind(2) here does not follow a symlink in the last component")
	}
	for _, resolve := range []bool{false, true} {
		t.Run(map[bool]string{false: "ListenEx", true: "ResolveForBind"}[resolve], func(t *testing.T) {
			f := newFork(t, tmpDir(t))
			listenAt(t, f.folded("s.sock"))
			link := f.r + "/link"
			if err := os.Symlink("a/sym/../s.sock", link); err != nil {
				t.Fatal(err)
			}
			var sf *SocketFile
			if resolve {
				fb, err := ResolveForBind(link, Options{})
				if err != nil {
					t.Fatal(err)
				}
				listenSpelling(t, fb.Path)
				if sf, err = fb.SocketFile(); err != nil {
					t.Fatalf("resolve: SocketFile: %v", err)
				}
			} else {
				l, b, err := ListenEx(link, Options{})
				if err != nil {
					t.Fatalf("ListenEx: %v", err)
				}
				t.Cleanup(func() { l.Close() })
				sf = b.File
			}
			f.checkPlaced(t, "through a symlink", "s.sock", decoySocket)
			if err := sf.Remove(); err != nil {
				t.Fatalf("resolve %v: Remove: %v", resolve, err)
			}
			f.checkRemoved(t, "through a symlink", "s.sock", decoySocket)
			if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("resolve %v: the caller's symlink: %v", resolve, err)
			}
		})
	}
}
