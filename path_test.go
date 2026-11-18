package sockat

import (
	"errors"
	"slices"
	"syscall"
	"testing"
)

// splitPath decides which directory a long path's socket is created in and
// under which name, for every route that does not hand the whole path to the
// kernel (/proc, the symlink to the directory, the thread cwd, bindat). A wrong
// split creates the socket somewhere else than the path says, so each shape
// is pinned.
func TestSplitPathTakesTheBytesAfterTheLastSlash(t *testing.T) {
	t.Parallel()
	cases := []struct{ path, dir, name string }{
		// Ordinary absolute and relative paths.
		{"/d/e/s.sock", "/d/e", "s.sock"},
		{"d/s.sock", "d", "s.sock"},
		// A socket directly under the root keeps "/" as its directory (an
		// empty directory would open nothing, or the cwd).
		{"/s.sock", "/", "s.sock"},
		// A bare file name lives in the working directory.
		{"s.sock", ".", "s.sock"},
		// A doubled slash stays in the directory part; the kernel treats "d/"
		// and "d" alike, so the split does not normalise it.
		{"d//s.sock", "d/", "s.sock"},
		// ".." inside the directory part is kept verbatim for the kernel to
		// resolve (a lexical clean would be wrong across symlinks).
		{"/a/../s.sock", "/a/..", "s.sock"},
		// A name that merely starts with dots is a file name.
		{"/d/...", "/d", "..."},
		{"/d/.s", "/d", ".s"},
	}
	for _, c := range cases {
		dir, name, err := splitPath(c.path)
		if err != nil || dir != c.dir || name != c.name {
			t.Errorf("splitPath(%q) = %q, %q, %v; want %q, %q", c.path, dir, name, err, c.dir, c.name)
		}
	}
}

// A path whose last segment is empty, "." or ".." names a directory, not a
// socket file. path/filepath would turn "d/" and "d/." into the name "d";
// binding that would create a socket named after the directory in its parent,
// which is not what the path says. EINVAL keeps long paths failing where the
// kernel fails for short ones.
func TestSplitPathRefusesPathsThatDoNotEndWithAFileName(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"d/", "d/.", "d/..", "..", ".", "/", "", "/d/", "/d/.", "/d/.."} {
		if _, _, err := splitPath(p); !errors.Is(err, syscall.EINVAL) {
			t.Errorf("splitPath(%q): %v, want EINVAL", p, err)
		}
	}
}

// components is how the relative spelling compares paths: the way the kernel
// walks them, with empty segments and "." dropped, ".." kept as it is (it is
// not interpreted: across a symlink a lexical ".." would climb out of the
// wrong directory), and an absolute path starting with "/".
func TestComponentsFollowTheKernelsWalk(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want []string
	}{
		{"/a//b/./c/", []string{"/", "a", "b", "c"}},
		{"//a", []string{"/", "a"}},
		{"a/../b", []string{"a", "..", "b"}},
		{"./a", []string{"a"}},
		{"/", []string{"/"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := components(c.path); !slices.Equal(got, c.want) {
			t.Errorf("components(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// below cuts a spelling of the cwd off the front of a path. The cut is by
// components: a byte prefix would turn /w/abc/s with cwd /w/ab into "c/s",
// which names /w/ab/c/s, a different file.
func TestBelowCutsThePrefixByComponents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		target, prefix, rest string
		ok                   bool
	}{
		// Below the prefix: the rest of the path, unchanged.
		{"/w/a/s.sock", "/w", "a/s.sock", true},
		// Directly in the prefix: the file name.
		{"/w/s.sock", "/w", "s.sock", true},
		// A trailing slash or "." on the prefix is not a component.
		{"/w/s.sock", "/w/", "s.sock", true},
		{"/w/s.sock", "/w/.", "s.sock", true},
		// Doubled slashes in the target do not stop the match.
		{"/w//a/s", "/w", "a/s", true},
		// The root is a prefix of every absolute path.
		{"/a/s", "/", "a/s", true},
		// A byte prefix that is not a component prefix.
		{"/w/abc/s.sock", "/w/ab", "", false},
		// Not below at all.
		{"/x/s.sock", "/w", "", false},
		// The target is the prefix itself: no file name is left.
		{"/w", "/w", "", false},
		{"/w/", "/w", "", false},
		// The target is above the prefix.
		{"/w", "/w/x", "", false},
		// Relative inputs have no defined relation to the cwd.
		{"w/s", "/w", "", false},
		{"/w/s", "w", "", false},
	}
	for _, c := range cases {
		rest, ok := below(c.target, c.prefix)
		if rest != c.rest || ok != c.ok {
			t.Errorf("below(%q, %q) = %q, %v; want %q, %v", c.target, c.prefix, rest, ok, c.rest, c.ok)
		}
	}
}

// relativeTo is what lets a long absolute path fit sun_path as a short
// relative one when the socket is near the working directory: climb with ".."
// out of the part of the cwd the target does not share, then descend. Each row
// pins one shape.
func TestRelativeToClimbsOutOfTheUnsharedPartOfTheCwd(t *testing.T) {
	t.Parallel()
	cases := []struct{ target, cwd, want string }{
		// Below the cwd: no climbing.
		{"/w/a/s.sock", "/w", "a/s.sock"},
		// Directly in the cwd: just the file name.
		{"/w/s.sock", "/w", "s.sock"},
		// A sibling subtree: climb one, descend.
		{"/w/a/s.sock", "/w/b", "../a/s.sock"},
		// Nothing shared but the root: climb all the way up.
		{"/x/s.sock", "/w/b", "../../x/s.sock"},
		// The cwd is the root.
		{"/x/s.sock", "/", "x/s.sock"},
		// A byte prefix is not shared: /w/ab is not part of /w/abc.
		{"/w/abc/s", "/w/ab", "../abc/s"},
		// "." and doubled slashes in the target are not components.
		{"/w/./a//s.sock", "/w", "a/s.sock"},
		// The arithmetic is purely by components: a ".." in the target is not
		// interpreted (callers pass real paths, which have none), it just ends
		// the shared part.
		{"/w/../w/s.sock", "/w", "../w/s.sock"},
	}
	for _, c := range cases {
		got, ok := relativeTo(c.target, c.cwd)
		if !ok || got != c.want {
			t.Errorf("relativeTo(%q, %q) = %q, %v; want %q", c.target, c.cwd, got, ok, c.want)
		}
	}
}

// Only absolute inputs have a well-defined relation to the cwd, and the result
// must still end in a file name: false otherwise.
func TestRelativeToIsFalseWhenItCannotNameAFileBelow(t *testing.T) {
	t.Parallel()
	cases := []struct{ target, cwd string }{
		// A relative target is already relative to something else.
		{"a/s", "/w"},
		// A relative cwd is not what getcwd(3) returns.
		{"/a/s", "w"},
		// The target is the cwd or one of its ancestors: nothing to descend to.
		{"/w", "/w"},
		{"/w", "/w/x"},
		{"/", "/w"},
	}
	for _, c := range cases {
		if got, ok := relativeTo(c.target, c.cwd); ok {
			t.Errorf("relativeTo(%q, %q) = %q, true; want false", c.target, c.cwd, got)
		}
	}
}

// Go's net package reads a leading '@' as the name of an abstract socket on
// Linux (and illumos), with no file anywhere. A relative spelling a route
// makes for a file (the relative path, a bare file name) must therefore never
// start with '@': "./" in front keeps it a path to the same file. Only a
// leading '@' matters; one anywhere else is an ordinary byte.
func TestAsSpellingKeepsALeadingAtFromReadingAsAbstract(t *testing.T) {
	t.Parallel()
	cases := []struct{ rel, want string }{
		{"@s.sock", "./@s.sock"},
		{"@d/s.sock", "./@d/s.sock"},
		{"s@", "s@"},
		{"d/@s.sock", "d/@s.sock"},
		{"s.sock", "s.sock"},
		{"../@s", "../@s"},
	}
	for _, c := range cases {
		if got := asSpelling(c.rel); got != c.want {
			t.Errorf("asSpelling(%q) = %q, want %q", c.rel, got, c.want)
		}
	}
}

// sameComponents decides whether TMPDIR is /tmp itself, to try that place for
// the symlink once rather than twice. It compares spellings component by
// component: "/tmp/" and "/tmp" are the same spelling, "/private/tmp" is not
// (on macOS it names the same directory, but its length differs, and the
// length is what decides whether the symlink route fits).
func TestSameComponentsComparesSpellings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"/tmp", "/tmp", true},
		{"/tmp/", "/tmp", true},
		{"/tmp/.", "/tmp", true},
		{"//tmp", "/tmp", true},
		{"/private/tmp", "/tmp", false},
		{"/tmp/x", "/tmp", false},
		{"/tm", "/tmp", false},
		{"tmp", "/tmp", false},
	}
	for _, c := range cases {
		if got := sameComponents(c.a, c.b); got != c.want {
			t.Errorf("sameComponents(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// The places tried, from TMPDIR:
//
//   - for D (the central route): ${TMPDIR:-/tmp}, a relative TMPDIR too (D's
//     place is opened once and held by its fd, so the cwd does not matter
//     later), then /tmp; the socket's directory comes after them in makeD;
//   - for a dial's fallback symlink: TMPDIR when it is an absolute path (the
//     link's spelling would otherwise depend on the cwd), then /tmp.
//
// A TMPDIR too long to shorten anything must not leave a long path without
// /tmp. A TMPDIR that is /tmp (with or without a trailing slash) is tried
// once. Outside the tests every other input is the real system's: the thread
// cwd is tried, and /proc is looked for at /proc/self/fd.
func TestConfigFromTmpdirTriesTmpdirThenTmp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpdir      string
		d, fallback []string
	}{
		{"", []string{"/tmp"}, []string{"/tmp"}},
		{"/var/x/", []string{"/var/x/", "/tmp"}, []string{"/var/x/", "/tmp"}},
		{"rel", []string{"rel", "/tmp"}, []string{"/tmp"}},
		{"./rel", []string{"./rel", "/tmp"}, []string{"/tmp"}},
		{"/tmp", []string{"/tmp"}, []string{"/tmp"}},
		{"/tmp/", []string{"/tmp"}, []string{"/tmp"}},
		{"/private/tmp", []string{"/private/tmp", "/tmp"}, []string{"/private/tmp", "/tmp"}},
	}
	for _, c := range cases {
		cfg := configFromTmpdir(c.tmpdir)
		if !slices.Equal(cfg.dBases, c.d) {
			t.Errorf("configFromTmpdir(%q).dBases = %q, want %q", c.tmpdir, cfg.dBases, c.d)
		}
		if !slices.Equal(cfg.tmpBases, c.fallback) {
			t.Errorf("configFromTmpdir(%q).tmpBases = %q, want %q", c.tmpdir, cfg.tmpBases, c.fallback)
		}
		if !cfg.threadCwd || cfg.procSelfFD != "/proc/self/fd" {
			t.Errorf("configFromTmpdir(%q) = %+v", c.tmpdir, cfg)
		}
	}
}
