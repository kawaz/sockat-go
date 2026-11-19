package sockat

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// The routes through D (SPEC "D と R"): a directory D of our own, held by its
// fd, with a symlink R of a random name in it pointing to the socket's
// directory; the bind / connect goes by R/<name> relative to D's fd.
//
// After the cleanup R exists nowhere, so the spelling the kernel keeps for a
// bound socket (getsockname, and getpeername on the other side) names nothing
// wherever it is resolved later: not a file of the same name in someone's cwd,
// not one behind a reused fd number.

// runLong is the routes for a path longer than directMax.
//
// The socket's directory is looked up (stat, then its real path) first, so a
// missing one is ENOENT, one that is not a directory ENOTDIR, and a failure to
// resolve it its own error, before anything else is decided or made. It is
// then opened for the routes that need its fd (the socket's directory as a
// place for D, a dial's proc-direct and at-direct); when it cannot be opened
// (no read permission where directories are opened O_RDONLY), sdir is nil and
// those routes cannot be used. Then a listen takes its order (listenRoutes)
// and a dial the system's connect order (dialer); when no route can be used,
// the error is ENAMETOOLONG.
func runLong(o op, path string, cfg *config) (endpoint, route, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return nil, "", syscallError("stat", err)
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return nil, "", syscallError("stat", unix.ENOTDIR)
	}
	// Its real path is looked up now too: an error there is returned as it
	// is, rather than coming out as ENAMETOOLONG once no route could be used.
	real, err := realDir(dir)
	if err != nil {
		return nil, "", err
	}
	// The routes are tried from here: standard, first in their order, was
	// not usable for the path's length.
	cfg.trace.add(routeStandard, "path too long")
	sdir, err := openDir(dir)
	if err != nil {
		sdir = nil
	} else {
		defer sdir.Close()
	}
	var ep endpoint
	var r route
	if o == opListen {
		ep, r, err = listenRoutes(path, sdir, real, name, cfg)
	} else {
		d := newDialer(path, dir, real, name, sdir, cfg)
		ep, r, err = d.run(dialSteps(d.procUsable))
		d.close()
	}
	if errors.Is(err, errUnusable) {
		return nil, "", errNameTooLong(o)
	}
	return ep, r, err
}

// rNameLen is the length of R's name, so R/<name> is rNameLen+1+len(name).
const rNameLen = 10

// listenRoutes binds name inside the socket's directory (real, opened as
// sdir; path as the caller spelt it), by the first route that can be used:
//
//	| route        | system                    | spelling                          |
//	|--------------|---------------------------|-----------------------------------|
//	| symlink-file | not Linux                 | <place>/sockat.XXXXXXXXXX         |
//	| at           | FreeBSD                   | bindat(D, R/<name>)               |
//	| thread-cwd   | Linux, macOS              | R/<name> from a thread in D       |
//	| proc         | Linux with /proc          | /proc/self/fd/<D>/R/<name>        |
//	| fork-cwd     | (none: Go has no fork)    |                                   |
//
// symlink-file needs the kernel to follow a symlink in the last component of
// a bind, which is told at run time (symlinkFile); Linux does not, and is
// not tried. For the routes through D, what can be told without making
// anything (the length, a seccomp filter, /proc) is checked first; nothing is
// made when no means can be used. When D can be made nowhere, the same means
// without D (withoutD). errUnusable when no route can be used (nothing left
// behind).
//
// D is made once and removed when the routes through D are done: no later
// route of a listen uses it (fork-cwd, which would, Go does not have).
func listenRoutes(path string, sdir *os.File, real, name string, cfg *config) (endpoint, route, error) {
	if listenTriesSymlinkFile {
		ep, err := symlinkFile(opListen, path, cfg.tmpBases)
		if !errors.Is(err, errUnusable) {
			return ep, routeSymlinkFile, err
		}
		cfg.trace.add(routeSymlinkFile, reasonOf(err))
	}
	thread := cfg.threadCwd && threadCwdPossible()
	proc := procFDUsableFor(sdir, cfg.procSelfFD)
	ep, r, err := throughD(sdir, real, name, thread, proc, cfg)
	if errors.Is(err, errNoD) {
		// The routes through D were tried and found unusable, fork-cwd (which
		// Go cannot make) among them; then the form without D.
		cfg.trace.add(routeForkCwd, noForkRoute)
		ep, r, err = withoutD(sdir, real, name, thread, proc, cfg)
		if errors.Is(err, errUnusable) {
			cfg.trace.add(routeForkCwdSockdir, noForkRoute)
		}
		return ep, r, err
	}
	if errors.Is(err, errUnusable) {
		// SPEC's next route for a bind; Go cannot make it.
		cfg.trace.add(routeForkCwd, noForkRoute)
	}
	return ep, r, err
}

// errNoD means that D could be made in no place (the form without D comes
// next for a bind).
var errNoD = errors.New("sockat: no place for D")

// throughD is a bind's routes through D: what can be told without making
// anything (the length, the means) first, then D, then the means in order.
// errNoD when D could be made nowhere; errUnusable when the routes cannot be
// used.
func throughD(sdir *os.File, real, name string, thread, proc bool, cfg *config) (endpoint, route, error) {
	spellingLen := rNameLen + 1 + len(name)
	if spellingLen > directMax {
		cfg.trace.skipMeans(dRoutes, "spelling too long")
		return nil, "", errUnusable
	}
	// With the /proc variant as the only means, its spelling must fit even
	// with a one-digit fd (the shortest it can be); D's real fd is checked
	// again once D is made.
	if !atSupported && !thread && proc && len(cfg.procSelfFD)+len("/0/")+spellingLen > directMax {
		cfg.trace.skipThread(dRoutes, cfg)
		cfg.trace.add(routeProc, "spelling too long")
		return nil, "", errUnusable
	}
	if !atSupported && !thread && !proc {
		cfg.trace.skipThread(dRoutes, cfg)
		cfg.trace.skipProc(dRoutes)
		return nil, "", errUnusable
	}
	d, err := makeD(cfg.dBases, sdir, real, proc, cfg.procSelfFD, randomPart, randomPart)
	if err != nil {
		cfg.trace.skipMeans(dRoutes, "no place for D")
		return nil, "", errNoD
	}
	defer d.cleanup()
	return byMeans(d.fd, d.r+"/"+name, thread, proc, cfg, dRoutes)
}

// routes names the routes of the means, for D or without it.
type routes struct{ at, thread, proc route }

var (
	dRoutes   = routes{routeAt, routeThreadCwd, routeProc}
	noDRoutes = routes{routeAtSockdir, routeThreadCwdSockdir, routeProcSockdir}
)

// byMeans binds through spelling relative to the directory fd, by the first
// means that can be used: bindat (FreeBSD), a thread whose cwd is fd,
// /proc/self/fd/<fd>/<spelling>. errUnusable when none can.
func byMeans(fd int, spelling string, thread, proc bool, cfg *config, rs routes) (endpoint, route, error) {
	if atSupported {
		ep, err := atOp(opListen, fd, spelling)
		return ep, rs.at, err
	}
	if thread {
		ep, err := inThreadCwd(opListen, fd, spelling)
		if !errors.Is(err, errUnusable) {
			return ep, rs.thread, err
		}
		cfg.trace.add(rs.thread, "thread cwd refused")
	} else {
		cfg.trace.skipThread(rs, cfg)
	}
	if !proc {
		cfg.trace.skipProc(rs)
		return nil, "", errUnusable
	}
	via := cfg.procSelfFD + "/" + strconv.Itoa(fd) + "/" + spelling
	if len(via) > directMax {
		cfg.trace.add(rs.proc, "spelling too long")
		return nil, "", errUnusable
	}
	ep, err := viaStd(opListen, via, false)
	return ep, rs.proc, err
}

// noDPrefix is what R's name starts with when it is made in the socket's
// directory (the form without D).
const noDPrefix = "." + linkPrefix

// withoutD binds when D can be made nowhere (SPEC "D 無しの形"): R, named
// .sockat.XXXXXXXXXX and pointing to the socket's directory (real), is made in
// the socket's directory itself, and the bind goes by .sockat.XXXXXXXXXX/<name>
// relative to that directory's fd (sdir), by the same means as with D. R is
// removed afterwards, so the spelling the kernel keeps names nothing later
// either. errUnusable when sdir could not be opened, the spelling does not
// fit, or R cannot be made.
func withoutD(sdir *os.File, real, name string, thread, proc bool, cfg *config) (endpoint, route, error) {
	if sdir == nil {
		cfg.trace.skipMeans(noDRoutes, "socket directory not opened")
		return nil, "", errUnusable
	}
	spellingLen := len(noDPrefix) + rNameLen + 1 + len(name)
	if spellingLen > directMax {
		cfg.trace.skipMeans(noDRoutes, "spelling too long")
		return nil, "", errUnusable
	}
	fd := int(sdir.Fd())
	if !atSupported && !thread && proc && len(cfg.procSelfFD)+len("/")+len(strconv.Itoa(fd))+len("/")+spellingLen > directMax {
		cfg.trace.skipThread(noDRoutes, cfg)
		cfg.trace.add(routeProcSockdir, "spelling too long")
		return nil, "", errUnusable
	}
	if !atSupported && !thread && !proc {
		cfg.trace.skipThread(noDRoutes, cfg)
		cfg.trace.skipProc(noDRoutes)
		return nil, "", errUnusable
	}
	part, err := createAt(randomPart, func(n string) error { return symlinkat(real, fd, noDPrefix+n) })
	if err != nil {
		cfg.trace.skipMeans(noDRoutes, "R not made")
		return nil, "", errUnusable
	}
	r := noDPrefix + part
	defer unix.Unlinkat(fd, r, 0)
	return byMeans(fd, r+"/"+name, thread, proc, cfg, noDRoutes)
}

// realDir is dir as a real path (symlinks resolved, absolute), for R to point
// to: the kernel then reaches the socket's directory as dir names it now.
//
// dir is made absolute first and then resolved component by component, so
// every component is looked up: on macOS getcwd(3) still reports the path of
// a cwd that was removed, which resolving "." alone would take as real.
func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// held is a D made by makeD, with R in it.
type held struct {
	baseFD  int    // the place D is in
	ownBase bool   // baseFD was opened by makeD (closed on cleanup)
	name    string // D's name in the place
	fd      int    // D
	st      unix.Stat_t
	r       string // R's name in D
}

// makeD makes D, with R in it pointing to real, in the first place it can,
// trying bases in order and then the socket's directory (sdir, unless nil, where D is
// named ".sockat.XXXXXXXXXX"). A place where a step fails is passed over;
// what was made there is removed first, except a directory that failed the
// check, which is not known to be ours. With nextR nil, D is made without R
// (SPEC "D と R" steps 1 and 2 only, for proc-held).
//
// proc says whether /proc can be used, which decides how D is opened (and so
// how its mode is set): O_PATH, then chmod through <procSelfFD>/<fd>, which
// needs no read permission on D (a umask of 0477 leaves 0300); otherwise an fd
// fchmod(2) takes.
func makeD(bases []string, sdir *os.File, real string, proc bool, procSelfFD string,
	nextD, nextR func() (string, error)) (*held, error) {
	type place struct {
		fd     int
		own    bool
		prefix string
	}
	var places []place
	defer func() {
		for _, p := range places {
			if p.own && p.fd >= 0 {
				unix.Close(p.fd)
			}
		}
	}()
	try := func(p place) (*held, error) {
		h, err := makeDIn(p.fd, p.prefix, real, proc, procSelfFD, nextD, nextR)
		if err != nil {
			return nil, err
		}
		h.baseFD, h.ownBase = p.fd, p.own
		return h, nil
	}
	for _, base := range bases {
		fd, err := openDirFD(base)
		if err != nil {
			continue
		}
		p := place{fd: fd, own: true, prefix: linkPrefix}
		if h, err := try(p); err == nil {
			return h, nil
		}
		places = append(places, p)
	}
	if sdir == nil {
		return nil, errUnusable
	}
	return try(place{fd: int(sdir.Fd()), own: false, prefix: "." + linkPrefix})
}

// makeDIn makes D (named prefix + a random part from nextD) in the place
// baseFD, checks it, sets it to 0700, and makes R (named by nextR, unless nil)
// in it.
func makeDIn(baseFD int, prefix, real string, proc bool, procSelfFD string,
	nextD, nextR func() (string, error)) (*held, error) {
	part, err := createAt(nextD, func(n string) error { return mkdirD(baseFD, prefix+n, 0o700) })
	if err != nil {
		return nil, err
	}
	name := prefix + part
	fd, err := unix.Openat(baseFD, name, dOpenFlags, 0)
	if (errors.Is(err, unix.EISDIR) || errors.Is(err, unix.EINVAL)) && dOpenFlagsRetry != 0 {
		// A kernel that does not take O_SEARCH on a directory (macOS).
		fd, err = unix.Openat(baseFD, name, dOpenFlagsRetry, 0)
	}
	if err != nil {
		// D cannot be opened (a umask taking the owner's bits, where it is
		// opened O_SEARCH / O_RDONLY). When the name still looks like the D
		// made (the check of an opened D, by lstat), it is removed: removing
		// with AT_REMOVEDIR takes only an empty directory, so a directory
		// swapped in meanwhile with something in it is not lost.
		var st unix.Stat_t
		if unix.Fstatat(baseFD, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && isOurPrivateDir(&st, uint32(os.Geteuid())) {
			unix.Unlinkat(baseFD, name, unix.AT_REMOVEDIR)
		}
		return nil, err
	}
	h := &held{baseFD: baseFD, name: name, fd: fd}
	if err := unix.Fstat(fd, &h.st); err != nil || !isOurPrivateDir(&h.st, uint32(os.Geteuid())) {
		unix.Close(fd)
		return nil, errUnusable
	}
	if err := setPrivate(baseFD, name, fd, &h.st, proc, procSelfFD); err != nil {
		// D passed the check, so it is ours: removed.
		h.cleanup()
		return nil, err
	}
	if nextR != nil {
		if err := h.makeR(real, nextR); err != nil {
			h.cleanup()
			return nil, err
		}
	}
	return h, nil
}

// mkdirD is mkdirat(2) for D (SPEC "D と R" step 1).
//
// Design rationale: a variable rather than a direct call only so that a test
// (TestADSwappedInBeforeItIsOpenedIsNotUsed, in a child process) can put
// another directory at D's name between the mkdirat and the open, which no
// input of makeDIn can bring about. Nothing else assigns it.
var mkdirD = unix.Mkdirat

// makeR makes R in D, pointing to real, named by nextR (SPEC "D と R" step 3).
func (h *held) makeR(real string, nextR func() (string, error)) error {
	r, err := createAt(nextR, func(n string) error { return symlinkat(real, h.fd, n) })
	if err != nil {
		return err
	}
	h.r = r
	return nil
}

// createAt calls create with names from next until one is not taken
// (EEXIST), linkAttempts at most, and returns the name created.
func createAt(next func() (string, error), create func(name string) error) (string, error) {
	for range linkAttempts {
		name, err := next()
		if err != nil {
			return "", err
		}
		err = create(name)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", err
		}
	}
	return "", errNoFreeName
}

// randomPart is rNameLen random letters and digits: R's name, and D's after
// its prefix.
func randomPart() (string, error) {
	n, err := randomName()
	if err != nil {
		return "", err
	}
	return n[len(linkPrefix):], nil
}

// cleanup removes R (when it was made) and D (only while D's name in the
// place still names the directory held), and closes the fds. Best effort.
func (h *held) cleanup() {
	if h.r != "" {
		unix.Unlinkat(h.fd, h.r, 0)
	}
	var st unix.Stat_t
	if unix.Fstatat(h.baseFD, h.name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Dev == h.st.Dev && st.Ino == h.st.Ino {
		unix.Unlinkat(h.baseFD, h.name, unix.AT_REMOVEDIR)
	}
	unix.Close(h.fd)
	if h.ownBase {
		unix.Close(h.baseFD)
	}
}

// isOurPrivateDir reports whether st (an fstat) is a directory owned by us
// with no permission for group and others: the one created (mkdir 0700 under
// any umask), and not one swapped in for it.
func isOurPrivateDir(st *unix.Stat_t, us uint32) bool {
	return uint32(st.Mode)&unix.S_IFMT == unix.S_IFDIR && st.Uid == us && uint32(st.Mode)&0o077 == 0
}

// openDirFD opens a place for D as openDir does and returns a close-on-exec
// fd of its own.
func openDirFD(dir string) (int, error) {
	f, err := openDir(dir)
	if err != nil {
		return -1, err
	}
	defer f.Close()
	return unix.FcntlInt(f.Fd(), unix.F_DUPFD_CLOEXEC, 0)
}

// trace records the routes tried and found unusable, in order, for the
// Skipped of ListenEx and DialEx (diagnostics only). A nil trace records
// nothing.
type trace struct{ skipped []skipped }

// skipped is a route found unusable and why.
type skipped struct {
	name   route
	reason string
}

func (t *trace) add(r route, reason string) {
	if t != nil {
		t.skipped = append(t.skipped, skipped{r, reason})
	}
}

// skipThread records the thread-cwd means of rs as unusable, where the system
// has it: turned off (the tests), or under a seccomp filter.
func (t *trace) skipThread(rs routes, cfg *config) {
	if atSupported || !hasThreadCwd {
		return
	}
	reason := "seccomp filter"
	if !cfg.threadCwd {
		reason = "thread cwd off"
	}
	t.add(rs.thread, reason)
}

// skipProc records the /proc means of rs as unusable, where the system has it.
func (t *trace) skipProc(rs routes) {
	if !atSupported && hasProc {
		t.add(rs.proc, "/proc unusable")
	}
}

// skipMeans records every means of rs the system has as unusable for reason.
func (t *trace) skipMeans(rs routes, reason string) {
	switch {
	case atSupported:
		t.add(rs.at, reason)
	default:
		if hasThreadCwd {
			t.add(rs.thread, reason)
		}
		if hasProc {
			t.add(rs.proc, reason)
		}
	}
}

// noForkRoute is the reason the fork routes are skipped: Go cannot fork bare.
const noForkRoute = "no fork route in sockat-go"
