package sockat

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

// The central route (SPEC "中心の経路"): a directory D of our own, held by its
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
// resolve it its own error, before anything else is decided or made. It is then opened for the
// routes that need its fd (the socket's directory as a place for D, Dial's
// /proc and connectat fallbacks); when it cannot be opened (no read
// permission where directories are opened O_RDONLY), sdir is nil and those
// routes cannot be used. Then the central route; when it cannot be used, a
// listen ends with ENAMETOOLONG (Go has no fork route, the last resort of the
// SPEC) and a dial goes on to the system's fallback routes.
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
	if _, err := realDir(dir); err != nil {
		return nil, "", err
	}
	sdir, err := openDir(dir)
	if err != nil {
		sdir = nil
	} else {
		defer sdir.Close()
	}
	ep, r, err := centralRoute(o, dir, sdir, name, cfg)
	if !errors.Is(err, errUnusable) {
		return ep, r, err
	}
	if o == opListen {
		return nil, "", errNameTooLong(o)
	}
	ep, r, err = fallbackDial(path, dir, name, sdir, cfg)
	if errors.Is(err, errUnusable) {
		return nil, "", errNameTooLong(o)
	}
	return ep, r, err
}

// rNameLen is the length of R's name, so R/<name> is rNameLen+1+len(name).
const rNameLen = 10

// centralRoute binds / connects name inside dir (opened as sdir) through
// R/<name> relative to a fresh D, by the first means the system has:
//
//	| means                     | system              | route          |
//	|---------------------------|---------------------|----------------|
//	| bindat / connectat on D   | FreeBSD             | routeAt        |
//	| a thread whose cwd is D   | Linux, macOS        | routeThreadCwd |
//	| /proc/self/fd/<D>/R/<name>| Linux with /proc    | routeProcD     |
//
// What can be told without making anything (the length, a seccomp filter,
// /proc) is checked first; nothing is made when no means can be used.
// errUnusable when the route cannot be used (nothing left behind).
func centralRoute(o op, dir string, sdir *os.File, name string, cfg *config) (endpoint, route, error) {
	spellingLen := rNameLen + 1 + len(name)
	if spellingLen > directMax {
		return nil, "", errUnusable
	}
	thread := cfg.threadCwd && threadCwdPossible()
	proc := procFDUsableFor(sdir, cfg.procSelfFD)
	if !atSupported && !thread && !proc {
		return nil, "", errUnusable
	}
	// With the /proc variant as the only means, its spelling must fit even
	// with a one-digit fd (the shortest it can be); D's real fd is checked
	// again once D is made.
	if !atSupported && !thread && len(cfg.procSelfFD)+len("/0/")+spellingLen > directMax {
		return nil, "", errUnusable
	}
	real, err := realDir(dir)
	if err != nil {
		return nil, "", errUnusable
	}
	d, err := makeD(cfg.dBases, sdir, real, proc, cfg.procSelfFD, randomPart, randomPart)
	if err != nil {
		return nil, "", errUnusable
	}
	defer d.cleanup()
	spelling := d.r + "/" + name

	if atSupported {
		ep, err := atOp(o, d.fd, spelling)
		return ep, routeAt, err
	}
	if thread {
		ep, err := inThreadCwd(o, d.fd, spelling)
		if !errors.Is(err, errUnusable) {
			return ep, routeThreadCwd, err
		}
	}
	if proc {
		via := cfg.procSelfFD + "/" + strconv.Itoa(d.fd) + "/" + spelling
		if len(via) <= directMax {
			ep, err := viaStd(o, via, false)
			return ep, routeProcD, err
		}
	}
	return nil, "", errUnusable
}

// realDir is dir as a real path (symlinks resolved, absolute), for R to point
// to: the kernel then reaches the socket's directory as dir names it now.
func realDir(dir string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Abs(real)
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
// check, which is not known to be ours.
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
// baseFD, checks it, sets it to 0700, and makes R (named by nextR) in it.
func makeDIn(baseFD int, prefix, real string, proc bool, procSelfFD string,
	nextD, nextR func() (string, error)) (*held, error) {
	part, err := createAt(nextD, func(n string) error { return unix.Mkdirat(baseFD, prefix+n, 0o700) })
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
		// What is at the name now is not known to be ours: left alone.
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
	r, err := createAt(nextR, func(n string) error { return symlinkat(real, fd, n) })
	if err != nil {
		h.cleanup()
		return nil, err
	}
	h.r = r
	return h, nil
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
