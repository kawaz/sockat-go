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
// the symlink). The spellings of link-dir and link-file are made as
// ResolveForBind makes them (linkDirSpelling, linkFileSpelling).

// bound is what a bind produced besides the listener: the route, what its
// spelling is good for, the diagnostics' base, and the E kept for the
// spelling (link-dir, link-file), which SocketFile cleans up.
type bound struct {
	route    route
	spelling Spelling
	base     string
	link     *link
}

// bindCall is one bind, or one resolve, of a long path.
type bindCall struct {
	w     *world
	o     op     // opListen or opListenGram (a bind)
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

// newBindCall looks up the socket's directory of path, longer than
// directMax, first (stat, then its real path), so a missing one is ENOENT
// and one that is not a directory ENOTDIR, before anything else is decided
// or made; then Options.Base is checked (with UsableCrossUID as opts has
// it), making nothing.
func (w *world) newBindCall(path string, opts Options) (*bindCall, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return nil, syscallError("stat", err)
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return nil, syscallError("stat", unix.ENOTDIR)
	}
	real, err := realDir(dir)
	if err != nil {
		return nil, err
	}
	if opts.Base != "" {
		if err := w.checkGiven(opts.Base, opts.UsableCrossUID); err != nil {
			return nil, err
		}
	}
	return &bindCall{w: w, path: path, name: name, real: real,
		dirID: fileID{uint64(st.Dev), uint64(st.Ino)}, opts: opts}, nil
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
	try   func(*bindCall) (endpoint, bound, error)
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

// bindLong binds (and listens, for opListen) at path, longer than
// directMax, by the first route of the bind order that can be used
// (newBindCall first). ENAMETOOLONG when no route can be used.
func (w *world) bindLong(o op, path string, opts Options, tr *trace) (endpoint, bound, error) {
	c, err := w.newBindCall(path, opts)
	if err != nil {
		return nil, bound{}, err
	}
	c.o = o
	tr.add(routeStandard, "path too long")
	for _, s := range w.bindOrder() {
		ep, b, err := s.try(c)
		if errors.Is(err, errUnusable) {
			tr.add(s.route, reasonOf(err))
			continue
		}
		return ep, b, err
	}
	return nil, bound{}, errNameTooLong(o)
}

// base is the B for a route of this call: Options.Base's when given (its
// error returned as it is, an Options.Base error, not a reason to pass the
// route over, except for a spelling estimated not to fit); otherwise the
// world's B of the places 1 to 3 (cross: one others can follow), and, when
// none of them has or can make one (not for cross), the socket's
// directory's. fits estimates, before a B is made, whether a spelling of the
// route through it can fit: where it cannot, the route is unusable, and no
// other place is tried for it (the process's B does not depend on the length
// of the path).
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

// linked is a spelling through a resolve E, made for a bind or a resolve:
// the spelling, the E it goes through, what it is good for and the
// diagnostics' base.
type linked struct {
	spelling string
	e        *link
	sp       Spelling
	base     string
}

// linkDirSpelling makes link-dir's spelling <B>/<D>/<E>/<name>, E a symlink
// to the socket's directory (its real path), B spelt by procB (Linux with
// /proc, not for UsableCrossUID) or its real path (not for a B in the
// socket's directory, whose real path is longer than the path), the first
// that fits.
func (c *bindCall) linkDirSpelling() (linked, error) {
	useProc := hasProc && !c.opts.UsableCrossUID
	return c.throughB(useProc, c.real, "/"+c.name, true, func(bspell, e string) int { return linkDirLen(bspell, e, c.name) })
}

// linkFileSpelling makes link-file's spelling <B>/<D>/<E>, E a symlink to
// target (the socket file), B spelt by procB when useProc (a connect's
// resolve: a bind never follows a symlink where /proc is) or its real path,
// the first that fits.
func (c *bindCall) linkFileSpelling(target string, useProc bool) (linked, error) {
	return c.throughB(useProc, target, "", false, linkFileLen)
}

// throughB makes the spelling <B>/<D>/<E><rest> of length size(<B>, <E>), E
// a resolve E in the process's D of the call's B pointing to target. B is
// spelt by procB when useProc and it fits, otherwise by its real path when
// B is not in the socket's directory and it fits; the route is unusable
// when neither does (estimated before B is made, with the shortest procB and
// E). removable is what unlinking the spelling does (link-dir's E is
// followed, link-file's is not).
func (c *bindCall) throughB(useProc bool, target, rest string, removable bool, size func(bspell, e string) int) (linked, error) {
	w := c.w
	fits := func(kind, breal string) bool {
		if p := w.procBShortest(); useProc && p != "" && size(p, "0") <= directMax {
			return true
		}
		return kind != kindSockdir && size(breal, "0") <= directMax
	}
	b, err := c.base(c.opts.UsableCrossUID, fits)
	if err != nil {
		return linked{}, err
	}
	e := b.nextEName()
	spell, viaProc := "", false
	if useProc {
		if p, ok := w.procB(b); ok && size(p, e) <= directMax {
			spell, viaProc = p, true
		}
	}
	if spell == "" && b.kind != kindSockdir && size(b.real, e) <= directMax {
		spell = b.real
	}
	if spell == "" {
		return linked{}, unusable("spelling too long")
	}
	d, err := c.ownD(b)
	if err != nil {
		return linked{}, err
	}
	l, err := b.resolveE(d, target)
	if err != nil {
		return linked{}, unusable("E not made")
	}
	spelling := spell + "/" + d.name + "/" + l.name + rest
	if len(spelling) > directMax {
		// Another call took the E name estimated, and a longer one came.
		l.remove()
		return linked{}, unusable("spelling too long")
	}
	return linked{spelling: spelling, e: l, sp: linkedSpelling(b, viaProc, removable), base: baseValue(b, viaProc)}, nil
}

// linkDir binds by link-dir's spelling (linkDirSpelling). E stays for
// SocketFile's Cleanup; a failure of the bind removes it.
func (c *bindCall) linkDir() (endpoint, bound, error) {
	ln, err := c.linkDirSpelling()
	if err != nil {
		return nil, bound{}, err
	}
	return c.bindThrough(routeLinkDir, ln)
}

// linkFile binds by link-file's spelling through B's real path
// (linkFileSpelling), E pointing to the socket file (the path, made absolute
// against the cwd). Where bind(2) does not follow a symlink in the last
// component it fails; then, with nothing at the path, the route is unusable
// (SPEC-routes "link-file"); with something there, the failure is real
// (EADDRINUSE). E is removed either way.
func (c *bindCall) linkFile() (endpoint, bound, error) {
	target, ok := absTarget(c.path)
	if !ok {
		return nil, bound{}, unusable("cwd unreadable")
	}
	ln, err := c.linkFileSpelling(target, false)
	if err != nil {
		return nil, bound{}, err
	}
	ep, bd, err := c.bindThrough(routeLinkFile, ln)
	if err != nil {
		var st unix.Stat_t
		if unix.Lstat(target, &st) != nil {
			return nil, bound{}, unusable("bind did not follow the symlink")
		}
	}
	return ep, bd, err
}

// bindThrough binds by ln's spelling for the route r and returns what the
// bind is, with ln's E kept. A failure of the bind removes E before it is
// returned.
func (c *bindCall) bindThrough(r route, ln linked) (endpoint, bound, error) {
	ep, err := viaStd(c.o, ln.spelling, false)
	if err != nil {
		ln.e.remove()
		return nil, bound{route: r}, err
	}
	return ep, bound{route: r, spelling: ln.sp, base: ln.base, link: ln.e}, nil
}

// dangling binds by <E>/<name> relative to D (SPEC "thread-cwd と at の手順"):
// from a thread whose own cwd is D (thread-cwd: Linux, macOS), or by
// bindat(2) on D (at: FreeBSD). E, a symlink of a random name to the socket's
// directory (its real path), is removed right after, whatever the outcome,
// so the spelling the kernel keeps names nothing later (Dangling). D is in the
// B of the places 1 to 4 that does not look at UsableCrossUID (the spelling
// names nothing, for anyone). The listener does not remove its spelling on
// Close.
func (c *bindCall) dangling() (endpoint, bound, error) {
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
		ep, err = atOp(c.o, d.fd, spelling)
	} else {
		ep, err = inThreadCwd(c.o, d.fd, spelling)
		if errors.Is(err, errUnusable) {
			return nil, bound{}, unusable("thread cwd refused")
		}
	}
	if err != nil {
		return nil, bound{route: r}, err
	}
	if l, ok := ep.(*net.UnixListener); ok {
		l.SetUnlinkOnClose(false)
	}
	return ep, bound{route: r, spelling: Spelling{Kind: Dangling}}, nil
}

// listenEx is ListenEx on w.
func (w *world) listenEx(path string, opts Options) (*net.UnixListener, *Bound, error) {
	ep, b, err := w.bindReported(opListen, path, opts)
	if err != nil {
		return nil, nil, err
	}
	return ep.(*net.UnixListener), b, nil
}

// bindReported is the bind of ListenEx and ListenUnixgramEx (o): the bind
// (bindEx), reported, with the socket file recorded for Bound.File.
func (w *world) bindReported(o op, path string, opts Options) (endpoint, *Bound, error) {
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
	ep, b, err := bindEx(w, o, path, opts, tr)
	if err != nil {
		return nil, nil, &RouteError{Route: string(b.route), Skipped: tr.skips(), Err: err}
	}
	if l, ok := ep.(*net.UnixListener); ok {
		l.SetUnlinkOnClose(false)
	}
	f := &SocketFile{clean: &cleaner{e: b.link}}
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
			ep.Close()
			f.Cleanup()
			return nil, nil, &RouteError{Route: string(b.route), Skipped: tr.skips(), Err: opError(o, path, errNotRecorded)}
		}
		f.file, f.id = file, id
	}
	return ep, &Bound{File: f, Spelling: b.spelling, Route: string(b.route), Base: b.base, Skipped: tr.skips()}, nil
}

// bindEx is the bind of bindReported.
//
// Design rationale: a variable rather than a direct call to (*world).bind
// only so that a test (TestListenExWhoseCwdChangesBeforeTheBindRecordsNothing,
// in a child process) can change the cwd between realFile and the bind, which
// no input of bindReported can bring about. Nothing else assigns it.
var bindEx = (*world).bind

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
