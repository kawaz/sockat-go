package sockat

// A relative path of a bind or a resolve made absolute (absolute.go, SPEC
// "相対パスを絶対パスにする"), a long absolute path handed to the standard
// library by its real path, and a connect that keeps a relative path as it is.
// The cwd belongs to the whole process, so every test that needs a chosen cwd
// runs in a child process (runChild, inTmpCwd).

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// noRouteName is a file name that, relative to any cwd of the tests, no
// absolute candidate fits (the cwd's spelling and "/" come in front of
// directMax-3 bytes), the standard library takes as it is (with room for
// illumos's "./" in front of an '@'), and no spelling of thread-cwd or at
// fits (<E>/<name>).
var noRouteName = strings.Repeat("n", directMax-3)

// noRouteWorld is a test world where no route of a bind or a resolve can be
// used for noRouteName: no B can be had (place 1 missing, no places 2 and 3,
// and no /proc for a B in the socket's directory, whose real path is too long
// to spell it), no thread cwd, and at's spelling does not fit.
func noRouteWorld(t *testing.T) *world {
	t.Helper()
	w, _ := testWorld(t, func(c *config, ps testPlaces) {
		c.tmp = ps.tmp + "/missing"
		c.getenv = func(string) string { return "" }
		c.proc = "/nonexistent/proc"
		c.threadCwd = false
	})
	return w
}

// longDir is a fresh directory under /tmp whose path, and real path, is
// longer than directMax, so no socket path in it fits as it is.
func longDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(nest(t, tmpDir(t), directMax+40, "x"))
}

// symlink makes link pointing to target.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// assertPlacesEmpty checks that nothing was made in w's places (no B, D or
// E): what a bind or a resolve through the standard library leaves.
func assertPlacesEmpty(t *testing.T, w *world, ps testPlaces) {
	t.Helper()
	assertNoDOrE(t, w)
	for _, p := range []string{ps.tmp, ps.runtime, ps.tmpdir} {
		assertEmpty(t, p)
	}
}

// localAddr is the address the kernel keeps for a socket bound by a bind (a
// listener's, or a datagram socket's).
func localAddr(ep endpoint) string {
	switch e := ep.(type) {
	case *net.UnixListener:
		return e.Addr().String()
	case *net.UnixConn:
		return e.LocalAddr().String()
	}
	return ""
}

// A short relative path is bound through its absolute candidate, the cwd's
// real path and the file name (SPEC "相対パスを絶対パスにする"): through the
// standard library (route standard, nothing skipped), Original, with the
// candidate as the address the kernel keeps, the socket file where the
// relative path names it, and nothing made in the places; a stream bind and
// a datagram one alike. Both resolves spell it the same, Original. That is
// why it is done: after the cwd changes, to a directory holding unrelated
// files of the same names, the default Listen's Close removes the socket file
// bound (its address is absolute) and not the other one, ListenEx's Remove
// does the same, and a reply to a datagram sender bound by a relative path
// still arrives (its address is absolute). Were the relative spelling kept,
// all three would go to the new cwd. In a child whose cwd is a fresh
// directory.
func TestARelativePathIsBoundThroughItsAbsoluteCandidate(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	real, err := realDir(".")
	if err != nil {
		t.Fatal(err)
	}
	w, ps := testWorld(t, nil)
	for _, c := range []struct {
		o    op
		name string
	}{{opListen, "s.sock"}, {opListenGram, "g.sock"}} {
		tr := &trace{}
		ep, b, err := w.bind(c.o, c.name, Options{}, tr, eOwn)
		if err != nil {
			t.Fatal(err)
		}
		defer ep.Close()
		if b.route != routeStandard || b.spelling != (Spelling{Kind: Original}) || len(tr.skipped) != 0 {
			t.Fatalf("%s: %+v, skipped %v, want Original through standard", c.name, b, tr.skipped)
		}
		if a := localAddr(ep); a != real+"/"+c.name {
			t.Fatalf("%s: the address kept %q, want %q", c.name, a, real+"/"+c.name)
		}
		if !isSocket(c.name) {
			t.Fatalf("%s: no socket file in the cwd", c.name)
		}
	}
	f := resolveBindIn(t, w, "r.sock", Options{})
	fc := resolveConnectIn(t, w, "r.sock", Options{})
	for _, g := range []struct {
		what     string
		path     string
		spelling Spelling
		skipped  []Skip
	}{{"for bind", f.Path, f.Spelling, f.Skipped}, {"for connect", fc.Path, fc.Spelling, fc.Skipped}} {
		if g.path != real+"/r.sock" || g.spelling != (Spelling{Kind: Original}) || len(g.skipped) != 0 {
			t.Fatalf("resolve %s: %q %+v %v, want the absolute candidate, Original", g.what, g.path, g.spelling, g.skipped)
		}
	}
	assertPlacesEmpty(t, w, ps)

	lp := listenPublic(t, "p.sock")
	lx, bx, err := ListenEx("x.sock", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lx.Close()
	sender, err := ListenUnixgram("c.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: tmpDir(t) + "/r", Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	for _, c := range []*net.UnixConn{sender, receiver} {
		c.SetDeadline(nowPlusGuard())
	}
	if _, err := sender.WriteToUnix([]byte("q"), receiver.LocalAddr().(*net.UnixAddr)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	_, from, err := receiver.ReadFromUnix(buf)
	if err != nil {
		t.Fatal(err)
	}

	other := tmpDir(t)
	for _, n := range []string{"p.sock", "x.sock"} {
		listenAt(t, other+"/"+n)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.WriteToUnix([]byte("a"), from); err != nil {
		t.Fatalf("the reply to the sender's address %q after a change of the cwd: %v", from, err)
	}
	if n, err := sender.Read(buf); err != nil || string(buf[:n]) != "a" {
		t.Fatalf("the reply: %q, %v", buf[:n], err)
	}
	lp.Close()
	if err := bx.File.Remove(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"p.sock", "x.sock"} {
		if exists(real + "/" + n) {
			t.Fatalf("%s is still in the cwd of the bind", n)
		}
		if !isSocket(other + "/" + n) {
			t.Fatalf("the unrelated %s in the new cwd is gone", n)
		}
	}
}

// Which absolute candidate a relative path takes, and when it takes none
// (SPEC "相対パスを絶対パスにする", SPEC-routes "絶対パスの候補の作り方"):
//
//   - a cwd entered through a symlink in a long directory, whose real path is
//     short: the real-path candidate;
//   - a cwd whose real path is long, entered through a short symlink ($PWD):
//     the $PWD candidate, $PWD and the file name;
//   - the same with a $PWD that does not name the cwd (stale): no $PWD
//     candidate, so the routes, and nothing made where $PWD names;
//   - "../t/s.sock" with a stale $PWD naming a sibling of the cwd, through
//     a short symlink: folding it would reach the right directory, but a
//     $PWD that does not name the cwd is not used at all, so the routes;
//   - a deep cwd ($PWD as long as the real path): the routes;
//   - "../t/s.sock" from a cwd reached through a symlink, where $PWD's "t" by
//     lexical folding (the symlink's parent's) is another directory than the
//     kernel's (the parent of the symlink's target's): no $PWD candidate, so
//     the routes, and nothing made in the lexical "t";
//   - "../../s.sock" from two levels below a short symlink to a long
//     directory: the $PWD candidate folded, the symlink and the file name.
//
// A candidate goes to the standard library (route standard, Original, nothing
// skipped or made in the places); with none, the path takes the routes
// (standard skipped first, and a spelling through B or D), and the socket file
// is where the relative path names it either way. The resolves spell it the
// same. In a child each, whose cwd and $PWD the case sets.
func TestARelativePathTakesTheAbsoluteCandidateThatFits(t *testing.T) {
	t.Parallel()
	type tc struct {
		name, cwd, pwd, rel, want, sock, untouched string
	}
	var cases []tc
	{
		r := tmpDir(t)
		l := longDir(t)
		symlink(t, r, l+"/l")
		cases = append(cases, tc{"real path", l + "/l", l + "/l", "s.sock", mustRealDir(t, r) + "/s.sock", r + "/s.sock", ""})
	}
	{
		lr := longDir(t)
		s := tmpDir(t) + "/s"
		symlink(t, lr, s)
		cases = append(cases, tc{"$PWD", s, s, "s.sock", s + "/s.sock", lr + "/s.sock", ""})
	}
	{
		lr := longDir(t)
		s := tmpDir(t) + "/s"
		symlink(t, lr, s)
		stale := tmpDir(t)
		cases = append(cases, tc{"stale $PWD", s, stale, "s.sock", "", lr + "/s.sock", stale})
	}
	{
		lr := longDir(t)
		for _, d := range []string{"a", "b", "t"} {
			if err := os.Mkdir(lr+"/"+d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		s := tmpDir(t) + "/s"
		symlink(t, lr, s)
		cases = append(cases, tc{"stale $PWD, same folded directory", lr + "/a", s + "/b", "../t/s.sock", "", lr + "/t/s.sock", ""})
	}
	{
		lr := longDir(t)
		cases = append(cases, tc{"deep cwd", lr, lr, "s.sock", "", lr + "/s.sock", ""})
	}
	{
		lr := longDir(t)
		for _, d := range []string{"x", "t"} {
			if err := os.Mkdir(lr+"/"+d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		top := tmpDir(t)
		if err := os.Mkdir(top+"/t", 0o700); err != nil {
			t.Fatal(err)
		}
		symlink(t, lr+"/x", top+"/s")
		cases = append(cases, tc{"a/sym/..", top + "/s", top + "/s", "../t/s.sock", "", lr + "/t/s.sock", top + "/t"})
	}
	{
		lr := longDir(t)
		if err := os.MkdirAll(lr+"/d/e", 0o700); err != nil {
			t.Fatal(err)
		}
		s := tmpDir(t) + "/s"
		symlink(t, lr, s)
		cases = append(cases, tc{"folded ..", s + "/d/e", s + "/d/e", "../../s.sock", s + "/s.sock", lr + "/s.sock", ""})
	}
	for _, c := range cases {
		out := runChild(t, "TestChildARelativePathCandidate", c.cwd, "PWD="+c.pwd,
			"SOCKAT_REL="+c.rel, "SOCKAT_WANT="+c.want, "SOCKAT_SOCK="+c.sock)
		if got := readChildReport(t, out); got != "ok" {
			t.Fatalf("%s: %s", c.name, got)
		}
		if !isSocket(c.sock) {
			t.Fatalf("%s: no socket file at %s", c.name, c.sock)
		}
		if c.untouched != "" {
			assertEmpty(t, c.untouched)
		}
	}
}

func TestChildARelativePathCandidate(t *testing.T) {
	asChild(t)
	rel, want, sock := os.Getenv("SOCKAT_REL"), os.Getenv("SOCKAT_WANT"), os.Getenv("SOCKAT_SOCK")
	w, ps := testWorld(t, nil)
	f := resolveBindIn(t, w, rel, Options{})
	fc := resolveConnectIn(t, w, rel, Options{})
	if want != "" {
		if f.Path != want || f.Spelling != (Spelling{Kind: Original}) || f.Route != string(routeStandard) || len(f.Skipped) != 0 {
			t.Fatalf("resolve for bind: %+v, want %q, Original", f, want)
		}
		if fc.Path != want || fc.Spelling != (Spelling{Kind: Original}) || fc.Route != string(routeStandard) || len(fc.Skipped) != 0 {
			t.Fatalf("resolve for connect: %+v, want %q, Original", fc, want)
		}
		assertPlacesEmpty(t, w, ps)
	} else {
		if f.Spelling.Kind != Linked || len(f.Skipped) == 0 || f.Skipped[0].Name != string(routeStandard) {
			t.Fatalf("resolve for bind: %+v, want a spelling through B", f)
		}
		if fc.Spelling.Kind != Linked || len(fc.Skipped) == 0 || fc.Skipped[0].Name != string(routeStandard) {
			t.Fatalf("resolve for connect: %+v, want a spelling through B", fc)
		}
	}
	tr := &trace{}
	ep, b, err := w.bind(opListen, rel, Options{}, tr, eShared)
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	ep.(*net.UnixListener).SetUnlinkOnClose(false)
	if want != "" {
		if b.route != routeStandard || b.spelling != (Spelling{Kind: Original}) || len(tr.skipped) != 0 || localAddr(ep) != want {
			t.Fatalf("bind: %+v, skipped %v, address %q, want %q, Original through standard", b, tr.skipped, localAddr(ep), want)
		}
		assertPlacesEmpty(t, w, ps)
	} else if k := b.spelling.Kind; (k != Linked && k != Dangling) || len(tr.skipped) == 0 || tr.skipped[0].name != routeStandard {
		t.Fatalf("bind: %+v, skipped %v, want a route", b, tr.skipped)
	}
	if !isSocket(sock) {
		t.Fatalf("no socket file at %s", sock)
	}
	childReport("ok")
}

// A relative path that no absolute candidate fits and no route can be used
// for (noRouteWorld) is handed to the standard library as it is, as the
// standard library would bind it, rather than fail (SPEC
// "相対パスを絶対パスにする" 5): route standard, CwdRelative, the relative
// path as the address the kernel keeps, and skipped the routes found
// unusable, without standard, the route taken. The resolves spell it as it
// is, CwdRelative, with skipped their routes (link-dir and, where bind(2)
// follows a symlink, link-file; link-file for a connect). Such a path too
// long for the standard library is ENAMETOOLONG, with standard skipped first.
// In a child whose cwd is a fresh directory.
func TestARelativePathNoRouteReachesIsBoundAsItIs(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	w := noRouteWorld(t)
	var order []route
	for _, s := range w.bindOrder() {
		order = append(order, s.route)
	}
	resolveOrder := []route{routeLinkDir}
	if w.cfg.bindFollows == followsYes {
		resolveOrder = append(resolveOrder, routeLinkFile)
	}
	for _, o := range []op{opListen, opListenGram} {
		tr := &trace{}
		ep, b, err := w.bind(o, noRouteName, Options{}, tr, eOwn)
		if err != nil {
			t.Fatal(err)
		}
		if b.route != routeStandard || b.spelling != (Spelling{Kind: CwdRelative}) || localAddr(ep) != stdSpelling(noRouteName) || !slices.Equal(namesOf(t, tr.skipped), order) {
			t.Fatalf("%s: %+v, address %q, skipped %v, want CwdRelative through standard after %v", o.network(), b, localAddr(ep), tr.skipped, order)
		}
		ep.Close()
		if err := os.Remove(noRouteName); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	f := resolveBindIn(t, w, noRouteName, Options{})
	if f.Path != stdSpelling(noRouteName) || f.Spelling != (Spelling{Kind: CwdRelative}) || f.Route != string(routeStandard) || !slices.Equal(skippedNames(t, f.Skipped), resolveOrder) {
		t.Fatalf("resolve for bind: %+v, want the path itself, CwdRelative, after %v", f, resolveOrder)
	}
	fc := resolveConnectIn(t, w, noRouteName, Options{})
	if fc.Path != stdSpelling(noRouteName) || fc.Spelling != (Spelling{Kind: CwdRelative}) || fc.Route != string(routeStandard) || !slices.Equal(skippedNames(t, fc.Skipped), []route{routeLinkFile}) {
		t.Fatalf("resolve for connect: %+v, want the path itself, CwdRelative, after link-file", fc)
	}

	long := strings.Repeat("n", directMax+1)
	tr := &trace{}
	_, _, err := w.bind(opListen, long, Options{}, tr, eOwn)
	wantOpError(t, "a long relative path", opListen, long, err, syscall.ENAMETOOLONG)
	if got := namesOf(t, tr.skipped); len(got) == 0 || got[0] != routeStandard || !slices.Equal(got[1:], order) {
		t.Fatalf("a long relative path: skipped %v, want standard and %v", got, order)
	}
	_, err = w.resolveForBind(long, Options{})
	re := wantResolveError(t, "resolve for bind of a long relative path", long, err, syscall.ENAMETOOLONG)
	if got := skippedNames(t, re.Skipped); len(got) == 0 || got[0] != routeStandard {
		t.Fatalf("resolve for bind of a long relative path: skipped %v, want standard first", got)
	}
}

// A cwd deeper than PATH_MAX has no real path, its length being the cause
// (SPEC names ENAMETOOLONG and ERANGE; Go's getcwd fails ENAMETOOLONG on
// Linux and EINVAL on macOS, pastPathMax), and no $PWD that stat can follow,
// so a relative path has no absolute candidate and no route can be used for
// it (each needs the socket's directory's real path): that is no error, and
// a short relative path is handed to the standard library as it is, as the
// standard library would bind it (SPEC "相対パスを絶対パスにする" 5):
// CwdRelative, route standard, nothing skipped since no route was tried,
// and the socket file in the cwd; a stream bind and a datagram one alike, the
// public Listen and ListenUnixgram included. The resolves spell it as it is,
// CwdRelative. A relative path too long for the standard library is
// ENAMETOOLONG, with no route and nothing skipped. ListenEx and
// ListenUnixgramEx bind it too but cannot record the socket file (its real
// path cannot be had), so they fail as when a file cannot be recorded: an
// error reaching errNotRecorded with the route that bound, the listener
// closed, and the socket file left. The getcwd error is checked to be one of
// a cwd past PATH_MAX on every system, its errno in the failure (measured on
// Linux and macOS only). In a child whose cwd is a fresh directory, from which
// it descends.
func TestACwdPastPathMaxBindsARelativePathAsItIs(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	seg := strings.Repeat("d", 200)
	for i := 0; ; i++ {
		if _, err := syscall.Getwd(); err != nil {
			if !pastPathMax(err) {
				var no syscall.Errno
				errors.As(err, &no)
				t.Fatalf("%s: getcwd at %d levels: %v (errno %d), not an error of a cwd past PATH_MAX", runtime.GOOS, i, err, int(no))
			}
			break
		}
		if i > 4096/200+1 {
			t.Fatalf("getcwd still works %d levels down", i)
		}
		if err := os.Mkdir(seg, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(seg); err != nil {
			t.Fatal(err)
		}
	}
	w, ps := testWorld(t, nil)
	for _, c := range []struct {
		o    op
		name string
	}{{opListen, "s.sock"}, {opListenGram, "g.sock"}} {
		tr := &trace{}
		ep, b, err := w.bind(c.o, c.name, Options{}, tr, eShared)
		if err != nil {
			t.Fatal(err)
		}
		defer ep.Close()
		if b.route != routeStandard || b.spelling != (Spelling{Kind: CwdRelative}) || len(tr.skipped) != 0 || localAddr(ep) != c.name {
			t.Fatalf("%s: %+v, skipped %v, address %q, want CwdRelative through standard", c.name, b, tr.skipped, localAddr(ep))
		}
		if !isSocket(c.name) {
			t.Fatalf("%s: no socket file in the cwd", c.name)
		}
	}
	f := resolveBindIn(t, w, "r.sock", Options{})
	fc := resolveConnectIn(t, w, "r.sock", Options{})
	if f.Path != "r.sock" || f.Spelling != (Spelling{Kind: CwdRelative}) || len(f.Skipped) != 0 {
		t.Fatalf("resolve for bind: %+v, want the path itself, CwdRelative", f)
	}
	if fc.Path != "r.sock" || fc.Spelling != (Spelling{Kind: CwdRelative}) || len(fc.Skipped) != 0 {
		t.Fatalf("resolve for connect: %+v, want the path itself, CwdRelative", fc)
	}
	long := strings.Repeat("n", directMax+1)
	tr := &trace{}
	_, b, err := w.bind(opListen, long, Options{}, tr, eShared)
	wantOpError(t, "a long relative path", opListen, long, err, syscall.ENAMETOOLONG)
	if b.route != "" || len(tr.skipped) != 0 {
		t.Fatalf("a long relative path: %+v, skipped %v, want no route and nothing skipped", b, tr.skipped)
	}
	assertPlacesEmpty(t, w, ps)

	lp := listenPublic(t, "p.sock")
	if !isSocket("p.sock") || lp.Addr().String() != "p.sock" {
		t.Fatalf("Listen: address %q", lp.Addr())
	}
	cg, err := ListenUnixgram("pg.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer cg.Close()
	if !isSocket("pg.sock") {
		t.Fatal("ListenUnixgram: no socket file in the cwd")
	}
	lx, bx, err := ListenEx("x.sock", Options{})
	if re := routeErr(t, err); !errors.Is(err, errNotRecorded) || lx != nil || bx != nil || re.Route != string(routeStandard) {
		t.Fatalf("ListenEx: %v, %v, route %q: %v", lx, bx, re.Route, err)
	}
	gx, bg, err := ListenUnixgramEx("xg.sock", Options{})
	if re := routeErr(t, err); !errors.Is(err, errNotRecorded) || gx != nil || bg != nil || re.Route != string(routeStandard) {
		t.Fatalf("ListenUnixgramEx: %v, %v, route %q: %v", gx, bg, re.Route, err)
	}
	for _, n := range []string{"x.sock", "xg.sock"} {
		if !isSocket(n) {
			t.Fatalf("%s: the socket file bound was not left", n)
		}
	}
}

// A short relative path whose directory is missing is ENOENT, found when its
// real-path candidate looks the directory up and then by the routes' lookup
// of the socket's directory, before any route: no route, nothing skipped,
// nothing made (SPEC "エラー"); for a bind and both resolves.
func TestARelativePathWhoseDirectoryIsMissingIsENOENT(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	w, ps := testWorld(t, nil)
	const p = "none/s.sock"
	tr := &trace{}
	_, b, err := w.bind(opListen, p, Options{}, tr, eOwn)
	wantOpError(t, "bind", opListen, p, err, syscall.ENOENT)
	if b.route != "" || len(tr.skipped) != 0 {
		t.Fatalf("bind: %+v, skipped %v, want no route and nothing skipped", b, tr.skipped)
	}
	_, err = w.resolveForBind(p, Options{})
	if re := wantResolveError(t, "resolve for bind", p, err, syscall.ENOENT); len(re.Skipped) != 0 {
		t.Fatalf("resolve for bind: skipped %v", re.Skipped)
	}
	_, err = w.resolveForConnect(p, Options{})
	if re := wantResolveError(t, "resolve for connect", p, err, syscall.ENOENT); len(re.Skipped) != 0 {
		t.Fatalf("resolve for connect: skipped %v", re.Skipped)
	}
	assertPlacesEmpty(t, w, ps)
}

// Options.Base and the absolute candidates (SPEC "相対パスを絶対パスにする"):
// a candidate handed to the standard library uses no B, so Options.Base is
// not looked at (beyond its form), as for a short absolute path; here one
// whose parent is missing does not stop a short relative path nor a long
// absolute one whose real path fits, both Original through standard. A
// relative path that takes the routes (a deep cwd) has Options.Base checked
// as a long path has, and its error is returned (ENOENT, no route, nothing
// skipped) rather than taken for routes unusable, which would bind the
// relative path as it is. In a child whose cwd is a fresh directory.
func TestOptionsBaseIsNotLookedAtForAnAbsoluteCandidate(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	bad := Options{Base: tmpDir(t) + "/missing/b"}
	w, ps := testWorld(t, nil)
	r := tmpDir(t)
	l := longDir(t)
	symlink(t, r, l+"/l")
	for _, p := range []string{"s.sock", l + "/l/a.sock"} {
		tr := &trace{}
		ep, b, err := w.bind(opListen, p, bad, tr, eOwn)
		if err != nil {
			t.Fatalf("%d bytes: %v", len(p), err)
		}
		ep.Close()
		if b.route != routeStandard || b.spelling != (Spelling{Kind: Original}) {
			t.Fatalf("%d bytes: %+v, want Original through standard", len(p), b)
		}
		f := resolveBindIn(t, w, strings.TrimSuffix(p, ".sock")+"-r.sock", bad)
		if f.Spelling != (Spelling{Kind: Original}) {
			t.Fatalf("resolve %d bytes: %+v, want Original", len(p), f)
		}
	}
	assertPlacesEmpty(t, w, ps)

	deep := longDir(t)
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	tr := &trace{}
	_, b, err := w.bind(opListen, "s.sock", bad, tr, eOwn)
	wantOpError(t, "a deep cwd", opListen, "s.sock", err, syscall.ENOENT)
	if b.route != "" || len(tr.skipped) != 0 || exists(deep+"/s.sock") {
		t.Fatalf("a deep cwd: %+v, skipped %v, want the Options.Base error before any route", b, tr.skipped)
	}
	_, err = w.resolveForBind("s.sock", bad)
	wantResolveError(t, "resolve in a deep cwd", "s.sock", err, syscall.ENOENT)
}

// A bind of an absolute candidate that fails (here EADDRINUSE: a file is at
// the path) returns that failure: the other candidate, the routes and the
// relative path itself are not tried after it (SPEC "相対パスを絶対パスにする",
// SPEC-routes "経路の失敗の扱い"), so nothing is made in the places and
// nothing is skipped. In a child whose cwd is a fresh directory.
func TestARelativeCandidateWhoseBindFailsIsNotTriedAgain(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	if err := os.WriteFile("s.sock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w, ps := testWorld(t, nil)
	for _, o := range []op{opListen, opListenGram} {
		tr := &trace{}
		_, b, err := w.bind(o, "s.sock", Options{}, tr, eOwn)
		wantErrno(t, o.network(), err, syscall.EADDRINUSE)
		var oe *net.OpError
		if !errors.As(err, &oe) || oe.Addr == nil || oe.Addr.String() != "s.sock" {
			t.Fatalf("%s: %v, want a *net.OpError for the caller's path", o.network(), err)
		}
		if b.route != routeStandard || len(tr.skipped) != 0 {
			t.Fatalf("%s: %+v, skipped %v, want the failure of standard", o.network(), b, tr.skipped)
		}
	}
	assertPlacesEmpty(t, w, ps)
}

// A long absolute path whose real path fits (it goes through a symlink to a
// short directory) is handed to the standard library by that real path, the
// socket's directory's real path and the file name, before any route is
// tried (SPEC "相対パスを絶対パスにする"): route standard, Original, nothing
// skipped (though the path is long) and nothing made in the places, the real
// path as the address the kernel keeps, a stream bind and a datagram one
// alike, and the resolves spell it the same. The default listener's Close
// removes the socket file (its address names it); ListenEx's Remove does.
// A failure of that bind (EADDRINUSE) is returned as it is, with no route
// tried after it.
func TestALongAbsolutePathWhoseRealPathFitsGoesToTheStandardLibrary(t *testing.T) {
	t.Parallel()
	r := tmpDir(t)
	real := mustRealDir(t, r)
	l := longDir(t)
	symlink(t, r, l+"/l")
	via := l + "/l/"
	if len(via+"s.sock") <= directMax {
		t.Fatalf("%d bytes is not long", len(via+"s.sock"))
	}
	w, ps := testWorld(t, nil)
	for _, c := range []struct {
		o    op
		name string
	}{{opListen, "s.sock"}, {opListenGram, "g.sock"}} {
		tr := &trace{}
		ep, b, err := w.bind(c.o, via+c.name, Options{}, tr, eShared)
		if err != nil {
			t.Fatal(err)
		}
		if b.route != routeStandard || b.spelling != (Spelling{Kind: Original}) || len(tr.skipped) != 0 || localAddr(ep) != real+"/"+c.name {
			t.Fatalf("%s: %+v, skipped %v, address %q, want %q, Original through standard", c.name, b, tr.skipped, localAddr(ep), real+"/"+c.name)
		}
		if !isSocket(via + c.name) {
			t.Fatalf("%s: no socket file at the path", c.name)
		}
		ep.Close()
	}
	if exists(via + "s.sock") {
		t.Fatal("the listener's Close left the socket file")
	}
	f := resolveBindIn(t, w, via+"r.sock", Options{})
	fc := resolveConnectIn(t, w, via+"r.sock", Options{})
	if f.Path != real+"/r.sock" || f.Spelling != (Spelling{Kind: Original}) || len(f.Skipped) != 0 {
		t.Fatalf("resolve for bind: %+v, want the real path, Original", f)
	}
	if fc.Path != real+"/r.sock" || fc.Spelling != (Spelling{Kind: Original}) || len(fc.Skipped) != 0 {
		t.Fatalf("resolve for connect: %+v, want the real path, Original", fc)
	}
	lx, bx, err := w.listenEx(via+"x.sock", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lx.Close()
	if err := bx.File.Remove(); err != nil || exists(r+"/x.sock") {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.WriteFile(r+"/f.sock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tr := &trace{}
	_, b, err := w.bind(opListen, via+"f.sock", Options{}, tr, eShared)
	wantOpError(t, "a file at the path", opListen, via+"f.sock", err, syscall.EADDRINUSE)
	if b.route != routeStandard || len(tr.skipped) != 0 {
		t.Fatalf("a file at the path: %+v, skipped %v, want the failure of standard", b, tr.skipped)
	}
	assertPlacesEmpty(t, w, ps)
}

// A connect keeps a relative path as it is (SPEC "相対パスを絶対パスにする":
// its spelling stays with no peer, and the cwd's later changes do not
// matter): from a deep cwd, where the path made absolute would be too long,
// a short relative path still goes to the standard library (route standard,
// nothing skipped), and nothing is made in the places; a stream connect and a
// datagram one alike. A resolve for a connect of the same path, whose
// spelling may be kept, makes it absolute: a spelling through B. In a child
// whose cwd is a deep directory.
func TestAConnectKeepsARelativePathAsItIs(t *testing.T) {
	t.Parallel()
	d := longDir(t)
	runChild(t, "TestChildAConnectKeepsARelativePath", d)
}

func TestChildAConnectKeepsARelativePath(t *testing.T) {
	asChild(t)
	cwd, err := realDir(".")
	if err != nil {
		t.Fatal(err)
	}
	l := listenAt(t, cwd+"/s.sock")
	short := tmpDir(t) + "/g"
	g, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: short, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if err := os.Rename(short, cwd+"/g.sock"); err != nil {
		t.Fatal(err)
	}
	w, ps := testWorld(t, nil)
	c, r, sks, err := dialTraced(t, w, "s.sock", Options{})
	if err != nil || r != routeStandard || len(sks) != 0 {
		t.Fatalf("dial: %s, skipped %v: %v", r, sks, err)
	}
	ping(t, l, c)
	tr := &trace{}
	ep, r, err := w.connect(opDialGram, "g.sock", Options{}, tr)
	if err != nil || r != routeStandard || len(tr.skipped) != 0 {
		t.Fatalf("datagram connect: %s, skipped %v: %v", r, tr.skipped, err)
	}
	defer ep.Close()
	g.SetDeadline(nowPlusGuard())
	if _, err := ep.(*net.UnixConn).Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if n, err := g.Read(buf); err != nil || string(buf[:n]) != "x" {
		t.Fatalf("datagram read %q, %v", buf[:n], err)
	}
	assertPlacesEmpty(t, w, ps)
	fc := resolveConnectIn(t, w, "s.sock", Options{})
	if fc.Spelling.Kind != Linked || !isAbs(fc.Path) {
		t.Fatalf("resolve for connect: %+v, want an absolute spelling through B", fc)
	}
}

// ForConnect tells what its spelling is good for, as ForBind does (SPEC
// "綴りの性質 Spelling"): a long path spelt by link-file is Linked, not
// Removable (unlinking the spelling removes E, a symlink to the socket file,
// and the file stays), and lasts as long as the process where B is spelt by
// procB (Linux with /proc), until the cleanup where it is spelt by its real
// path (here also on Linux with no /proc). (Original and CwdRelative:
// TestResolveChecksThePathFirstAndSpellsAShortOneAsItIs,
// TestARelativePathIsBoundThroughItsAbsoluteCandidate,
// TestARelativePathNoRouteReachesIsBoundAsItIs.)
func TestForConnectOfALinkFileSpellingIsLinked(t *testing.T) {
	t.Parallel()
	p := nest(t, tmpDir(t), 300, "s.sock")
	proc := UntilCleanup
	if hasProc {
		proc = UntilProcess
	}
	for _, c := range []struct {
		what  string
		mod   func(*config, testPlaces)
		until Until
	}{{"/proc as it is", nil, proc}, {"no /proc", func(c *config, _ testPlaces) { c.proc = "/nonexistent/proc" }, UntilCleanup}} {
		w, _ := testWorld(t, c.mod)
		fc := resolveConnectIn(t, w, p, Options{})
		if fc.Route != string(routeLinkFile) || fc.Spelling.Kind != Linked || fc.Spelling.Removable || fc.Spelling.Until != c.until {
			t.Fatalf("%s: %+v, want Linked, not Removable, until %s", c.what, fc, c.until)
		}
	}
}
