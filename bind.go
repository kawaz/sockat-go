package sockat

import (
	"errors"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// The bind order of a long path (SPEC "bind の順"):
//
//	| system                  | order                                             |
//	|-------------------------|---------------------------------------------------|
//	| Linux, Android          | link-dir (procB, then the real path), thread-cwd |
//	| macOS                   | link-dir, link-file, thread-cwd                   |
//	| FreeBSD                 | link-dir, at                                      |
//	| NetBSD                  | link-dir, link-file                               |
//	| OpenBSD, illumos        | link-dir                                          |
//	| other Unix              | link-dir, link-file (told at run time)            |
//
// link-dir and link-file leave their spelling (B/D/E/<name>, B/D/E) for as
// long as E is there, so whatever later unlinks or looks up the spelling the
// kernel keeps reaches the path; thread-cwd and at, for a file name no
// spelling through B fits, bind through an E removed right after (Dangling).
// A route that cannot be used is passed over (recorded in the trace); the
// first route that binds decides, its failure included (DR-0011), except
// link-file's failure where nothing is at the path (bind(2) did not follow
// the symlink).

// bound is what a bind produced besides the listener: the route, what its
// spelling is good for, the diagnostics' base, and the E kept for the
// spelling (link-dir, link-file), which SocketFile cleans up.
type bound struct {
	route    route
	spelling Spelling
	base     string
	link     *link
}

// bindCall is one bind of a long path.
type bindCall struct {
	w     *world
	path  string // as the caller spelt it
	name  string // the file name
	real  string // the socket's directory as a real path
	dirID fileID // the socket's directory's (st_dev, st_ino)
	opts  Options
	// noD is the B in which D could not be made during this call: the later
	// routes of the call do not try to make it there again (SPEC-routes
	// "実体の置き場"), and do not look for another B either.
	noD map[*base]bool
}

// ownD is b's D for a route of this call: errUnusable when it could not be
// made, for this route and, without trying again, every later one.
func (c *bindCall) ownD(b *base) (*procD, error) {
	if c.noD[b] {
		return nil, unusable("D not made")
	}
	d, err := b.ownD(&c.w.cfg)
	if err != nil {
		if c.noD == nil {
			c.noD = map[*base]bool{}
		}
		c.noD[b] = true
		return nil, unusable("D not made")
	}
	return d, nil
}

// bindStep is one route of the bind order.
type bindStep struct {
	route route
	try   func(*bindCall) (*net.UnixListener, bound, error)
}

// bindOrder is the system's bind order after standard: link-dir; link-file
// where bind(2) is not measured not to follow a symlink; thread-cwd where a
// thread may have a cwd of its own, at where bindat(2) is (FreeBSD).
func (w *world) bindOrder() []bindStep {
	steps := []bindStep{{routeLinkDir, (*bindCall).linkDir}}
	if w.cfg.bindFollows != followsNo {
		steps = append(steps, bindStep{routeLinkFile, (*bindCall).linkFile})
	}
	switch {
	case atSupported:
		steps = append(steps, bindStep{routeAt, (*bindCall).dangling})
	case hasThreadCwd:
		steps = append(steps, bindStep{routeThreadCwd, (*bindCall).dangling})
	}
	return steps
}

// bindLong binds (and listens) at path, longer than directMax, by the first
// route of the bind order that can be used.
//
// The socket's directory is looked up first (stat, then its real path), so a
// missing one is ENOENT and one that is not a directory ENOTDIR, before
// anything else is decided or made; then Options.Base is checked, making
// nothing. ENAMETOOLONG when no route can be used.
func (w *world) bindLong(path string, opts Options, tr *trace) (*net.UnixListener, bound, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, bound{}, err
	}
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return nil, bound{}, syscallError("stat", err)
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return nil, bound{}, syscallError("stat", unix.ENOTDIR)
	}
	real, err := realDir(dir)
	if err != nil {
		return nil, bound{}, err
	}
	if opts.Base != "" {
		if err := w.checkGiven(opts.Base, opts.UsableCrossUID); err != nil {
			return nil, bound{}, err
		}
	}
	tr.add(routeStandard, "path too long")
	c := &bindCall{w: w, path: path, name: name, real: real,
		dirID: fileID{uint64(st.Dev), uint64(st.Ino)}, opts: opts}
	for _, s := range w.bindOrder() {
		l, b, err := s.try(c)
		if errors.Is(err, errUnusable) {
			tr.add(s.route, reasonOf(err))
			continue
		}
		return l, b, err
	}
	return nil, bound{}, errNameTooLong(opListen)
}

// base is the B for a route of this bind: Options.Base's when given (its
// error returned as it is, an Options.Base error, not a reason to pass the
// route over, but for a spelling estimated not to fit); otherwise the world's B of the places 1 to 3 (cross: one
// others can follow), and, when none of them has or can make one (not for
// cross), the socket's directory's. fits estimates, before a B is made,
// whether a spelling of the route through it can fit: where it cannot, the
// route is unusable, and no other place is tried for it (the process's B does
// not depend on the length of the path).
func (c *bindCall) base(cross bool, fits fitsFunc) (*base, error) {
	if c.opts.Base != "" {
		return c.w.givenBase(c.opts.Base, fits)
	}
	b, err := c.w.autoBase(cross, fits)
	if !errors.Is(err, errNoBase) {
		return b, err
	}
	if cross {
		return nil, unusable("no base others can follow")
	}
	if !fits(kindSockdir, "") {
		return nil, errSpellingTooLong
	}
	if b := c.w.sockdirBase(c.real, c.dirID); b != nil {
		return b, nil
	}
	return nil, errNoBase
}

// linkDir binds by <B>/<D>/<E>/<name>, E a symlink to the socket's directory
// (its real path), B spelt by procB (Linux with /proc, not for
// UsableCrossUID) or its real path (not for a B in the socket's directory,
// whose real path is longer than the path), the first that fits. E stays for
// SocketFile's Cleanup; a failure of the bind removes it.
func (c *bindCall) linkDir() (*net.UnixListener, bound, error) {
	w := c.w
	useProc := hasProc && !c.opts.UsableCrossUID
	fits := func(kind, breal string) bool {
		if useProc && linkDirLen(w.procBShortest(), "0", c.name) <= directMax {
			return true
		}
		return kind != kindSockdir && linkDirLen(breal, "0", c.name) <= directMax
	}
	b, err := c.base(c.opts.UsableCrossUID, fits)
	if err != nil {
		return nil, bound{}, err
	}
	e := b.nextEName()
	spell, viaProc := "", false
	if useProc {
		if p, ok := w.procB(b); ok && linkDirLen(p, e, c.name) <= directMax {
			spell, viaProc = p, true
		}
	}
	if spell == "" && b.kind != kindSockdir && linkDirLen(b.real, e, c.name) <= directMax {
		spell = b.real
	}
	if spell == "" {
		return nil, bound{}, unusable("spelling too long")
	}
	return c.throughE(b, spell, c.real, "/"+c.name, routeLinkDir, linkedSpelling(b, viaProc, true), baseValue(b, viaProc))
}

// linkFile binds by <B>/<D>/<E>, E a symlink to the socket file (the path,
// made absolute against the cwd), B spelt by its real path. Where bind(2)
// does not follow a symlink in the last component it fails; then, with
// nothing at the path, the route is unusable (SPEC-routes "link-file"); with
// something there, the failure is real (EADDRINUSE). E is removed either way.
func (c *bindCall) linkFile() (*net.UnixListener, bound, error) {
	fits := func(kind, breal string) bool {
		return kind != kindSockdir && linkFileLen(breal, "0") <= directMax
	}
	b, err := c.base(c.opts.UsableCrossUID, fits)
	if err != nil {
		return nil, bound{}, err
	}
	if b.kind == kindSockdir || linkFileLen(b.real, b.nextEName()) > directMax {
		return nil, bound{}, unusable("spelling too long")
	}
	target, ok := absTarget(c.path)
	if !ok {
		return nil, bound{}, unusable("cwd unreadable")
	}
	l, bd, err := c.throughE(b, b.real, target, "", routeLinkFile, linkedSpelling(b, false, false), baseValue(b, false))
	if err != nil && !errors.Is(err, errUnusable) {
		var st unix.Stat_t
		if unix.Lstat(target, &st) != nil {
			return nil, bound{}, unusable("bind did not follow the symlink")
		}
	}
	return l, bd, err
}

// throughE binds by <spell>/<D>/<E><rest>, E a resolve E in b's D pointing to
// target, and returns what the bind is (route, sp, baseVal) with E kept. A
// failure of the bind removes E before it is returned.
func (c *bindCall) throughE(b *base, spell, target, rest string, r route, sp Spelling, baseVal string) (*net.UnixListener, bound, error) {
	d, err := c.ownD(b)
	if err != nil {
		return nil, bound{}, err
	}
	e, err := b.resolveE(d, target)
	if err != nil {
		return nil, bound{}, unusable("E not made")
	}
	spelling := spell + "/" + d.name + "/" + e.name + rest
	if len(spelling) > directMax {
		// Another call took the E name estimated, and a longer one came.
		e.remove()
		return nil, bound{}, unusable("spelling too long")
	}
	ep, err := viaStd(opListen, spelling, false)
	if err != nil {
		e.remove()
		return nil, bound{route: r}, err
	}
	return ep.(*net.UnixListener), bound{route: r, spelling: sp, base: baseVal, link: e}, nil
}

// dangling binds by <E>/<name> relative to D (SPEC "thread-cwd と at の手順"):
// from a thread whose own cwd is D (thread-cwd: Linux, macOS), or by
// bindat(2) on D (at: FreeBSD). E, a symlink of a random name to the socket's
// directory (its real path), is removed right after, whatever the outcome,
// so the spelling the kernel keeps names nothing later (Dangling). D is in the
// B of the places 1 to 4 that does not look at UsableCrossUID (the spelling
// names nothing, for anyone). The listener does not remove its spelling on
// Close.
func (c *bindCall) dangling() (*net.UnixListener, bound, error) {
	w := c.w
	r := routeThreadCwd
	if atSupported {
		r = routeAt
	}
	if danglingLen(c.name) > directMax {
		return nil, bound{}, unusable("spelling too long")
	}
	if !atSupported {
		if !w.cfg.threadCwd {
			return nil, bound{}, unusable("thread cwd off")
		}
		if !threadCwdPossible() {
			return nil, bound{}, unusable("seccomp filter")
		}
	}
	b, err := c.base(false, func(string, string) bool { return true })
	if err != nil {
		return nil, bound{}, err
	}
	d, err := c.ownD(b)
	if err != nil {
		return nil, bound{}, err
	}
	e, err := b.callE(d, c.real, w.cfg.random)
	if err != nil {
		return nil, bound{}, unusable("E not made")
	}
	defer e.remove()
	spelling := e.name + "/" + c.name
	var ep endpoint
	if atSupported {
		ep, err = atOp(opListen, d.fd, spelling)
	} else {
		ep, err = inThreadCwd(opListen, d.fd, spelling)
		if errors.Is(err, errUnusable) {
			return nil, bound{}, unusable("thread cwd refused")
		}
	}
	if err != nil {
		return nil, bound{route: r}, err
	}
	l := ep.(*net.UnixListener)
	l.SetUnlinkOnClose(false)
	return l, bound{route: r, spelling: Spelling{Kind: Dangling}}, nil
}

// listenEx is ListenEx on w.
func (w *world) listenEx(path string, opts Options) (*net.UnixListener, *Bound, error) {
	tr := &trace{}
	// An abstract name has no file to record or remove.
	file, ferr := "", error(nil)
	// A relative path is resolved against the cwd twice, by realFile and by
	// the bind, and the cwd is the process's: another goroutine may change it
	// in between. It is read before the first and compared after the bind.
	rel := !abstractName(path) && !isAbs(path)
	var cwd fileID
	cwdOK := true
	if !abstractName(path) {
		if rel {
			cwd, cwdOK = statID(".")
		}
		// The real path is resolved before the bind, as the bind resolves
		// it; the file's identity is read the moment the bind returns.
		file, ferr = realFile(path)
	}
	l, b, err := bindEx(w, path, opts, tr)
	if err != nil {
		return nil, nil, &RouteError{Route: string(b.route), Skipped: tr.skips(), Err: err}
	}
	l.SetUnlinkOnClose(false)
	f := &SocketFile{e: b.link}
	if !abstractName(path) {
		var id fileID
		ok := ferr == nil && cwdOK
		if ok {
			id, ok = lstatID(file)
		}
		if ok && rel {
			// Another cwd means the bind may have resolved path elsewhere
			// than file, which would then name an unrelated file of the same
			// name. (A cwd changed and changed back is not seen.)
			now, same := statID(".")
			ok = same && now == cwd
		}
		if !ok {
			l.Close()
			f.Cleanup()
			return nil, nil, &RouteError{Route: string(b.route), Skipped: tr.skips(), Err: opError(opListen, path, errNotRecorded)}
		}
		f.file, f.id = file, id
	}
	return l, &Bound{File: f, Spelling: b.spelling, Route: string(b.route), Base: b.base, Skipped: tr.skips()}, nil
}

// bindEx is the bind of listenEx.
//
// Design rationale: a variable rather than a direct call to (*world).listen
// only so that a test (TestListenExWhoseCwdChangesBeforeTheBindRecordsNothing,
// in a child process) can change the cwd between realFile and the bind, which
// no input of listenEx can bring about. Nothing else assigns it.
var bindEx = (*world).listen

// absTarget is the caller's spelling made absolute against the cwd, for a
// symlink to hold: the kernel then resolves the link as it resolves the
// caller's path. false when the cwd cannot be read.
func absTarget(target string) (string, bool) {
	if isAbs(target) {
		return target, true
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		return "", false
	}
	return cwd + "/" + target, true
}
