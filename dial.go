package sockat

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// The connect order of a long path (SPEC "connect の順"): the routes that need
// no writing and no thread first, then thread-cwd-direct, which starts a
// thread, then link-file, which writes an E in B. The order is the system's
// (dialSteps, in the os_*.go files):
//
//	| system                 | order                                                      |
//	|------------------------|------------------------------------------------------------|
//	| Linux with /proc       | proc-direct, thread-cwd-direct, link-file                  |
//	| Linux without /proc    | relative, thread-cwd-direct, link-file                     |
//	| macOS                  | relative, widened (not made), thread-cwd-direct, link-file |
//	| FreeBSD                | at-direct, link-file                                       |
//	| NetBSD, OpenBSD, other | relative, link-file                                        |
//
// A connect leaves no spelling with the peer (the connecting socket is not
// bound), so no route of a connect makes D or keeps an E: link-file's E, in B
// itself, is unlinked right after the connect. A route that cannot be used is
// passed over (recorded in the trace); the first route that connects decides,
// its failure included.

// dialCall is one dial of a long path.
type dialCall struct {
	w    *world
	o    op     // opDial or opDialGram (a connect)
	path string // as the caller spelt it
	dir  string // its directory part, as the caller spelt it
	name string // the file name
	// sdir is the socket's directory, opened for the routes that need its fd
	// (proc-direct, at-direct, thread-cwd-direct); nil when it could not be
	// opened.
	sdir *os.File
	opts Options
}

// dialStep is one route of the connect order: its name and how it is tried.
// try returns an unusable error when the route cannot be used here, and
// otherwise the outcome of the connect it made.
type dialStep struct {
	route route
	try   func(*dialCall) (endpoint, error)
}

// notRouted is an error of a route that is not the route's failure but the
// call's (Options.Base's B could not be made): it is returned with no route
// (SPEC-routes "B の作り方").
type notRouted struct{ error }

func (e notRouted) Unwrap() error { return e.error }

// dial is connect for a stream socket (Dial, DialEx).
func (w *world) dial(path string, opts Options, tr *trace) (*net.UnixConn, route, error) {
	ep, r, err := w.connect(opDial, path, opts, tr)
	if err != nil {
		return nil, r, err
	}
	return ep.(*net.UnixConn), r, nil
}

// connect connects to path (o: opDial, opDialGram), recording the routes
// found unusable in tr (nil records nothing).
//
// An Options.Base of the wrong form (relative, or not ending with a name) is
// EINVAL whatever the length of the path. A path the standard library accepts
// goes to it as is (stdSpelling), before anything else is looked at (the
// environment included). So does an abstract socket name (Linux, Android). A
// NUL anywhere else, a leading one too where the kernel has no abstract
// names, is EINVAL, whatever the length. A longer path takes the connect
// order (dialLong). The error is a *net.OpError for path; the route that
// connected and failed, if any, is returned with it.
func (w *world) connect(o op, path string, opts Options, tr *trace) (endpoint, route, error) {
	if opts.Base != "" {
		if err := givenShape(opts.Base); err != nil {
			return nil, "", opError(o, path, err)
		}
	}
	if abstractName(path) {
		ep, err := viaStd(o, path, true)
		if err != nil {
			return nil, routeStandard, err
		}
		return ep, routeStandard, nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, "", opError(o, path, fmt.Errorf("socket path must not contain a NUL byte: %w", syscall.EINVAL))
	}
	if s := stdSpelling(path); len(s) <= directMax {
		ep, err := viaStd(o, s, s == path)
		if err != nil {
			if s != path {
				err = opError(o, path, unwrapOpError(err))
			}
			return nil, routeStandard, err
		}
		return ep, routeStandard, nil
	}
	ep, r, err := w.dialLong(o, path, opts, tr)
	if err != nil {
		return nil, r, opError(o, path, err)
	}
	return ep, r, nil
}

// dialLong connects to path, longer than directMax, by the first route of the
// connect order that can be used.
//
// The socket's directory is looked up first (stat, then its real path), so a
// missing one is ENOENT, one that is not a directory ENOTDIR, and a failure to
// resolve it its own error, before anything else is decided or made; then
// Options.Base is checked, making nothing. The directory is then opened for
// the routes that need its fd; when it cannot be opened (no read permission
// where directories are opened O_RDONLY), those routes cannot be used.
// ENAMETOOLONG when no route can be used.
func (w *world) dialLong(o op, path string, opts Options, tr *trace) (endpoint, route, error) {
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
	if opts.Base != "" {
		if err := w.checkGiven(opts.Base, false); err != nil {
			return nil, "", err
		}
	}
	tr.add(routeStandard, "path too long")
	sdir, err := openDir(dir)
	if err != nil {
		sdir = nil
	} else {
		defer sdir.Close()
	}
	c := &dialCall{w: w, o: o, path: path, dir: dir, name: name, sdir: sdir, opts: opts}
	for _, s := range dialSteps(procUsable(sdir, w.cfg.proc)) {
		ep, err := s.try(c)
		if errors.Is(err, errUnusable) {
			tr.add(s.route, reasonOf(err))
			continue
		}
		var nr notRouted
		if errors.As(err, &nr) {
			return nil, "", nr.error
		}
		return ep, s.route, err
	}
	return nil, "", errNameTooLong(o)
}

// threadCwdDirect connects by the file name from a thread whose own cwd is
// the socket's directory (inThreadCwd: Linux, macOS), with no D and no E: the
// spelling stays with nobody.
func (c *dialCall) threadCwdDirect() (endpoint, error) {
	if len(c.name) > directMax {
		return nil, unusable("spelling too long")
	}
	if c.sdir == nil {
		return nil, unusable("socket directory not opened")
	}
	if !c.w.cfg.threadCwd {
		return nil, unusable("thread cwd off")
	}
	if !threadCwdPossible() {
		return nil, unusable("seccomp filter")
	}
	ep, err := inThreadCwd(c.o, int(c.sdir.Fd()), c.name)
	if errors.Is(err, errUnusable) {
		return nil, unusable("thread cwd refused")
	}
	return ep, err
}

// linkFile connects by <B>/<E>, E a symlink of a random name right in B to
// the socket file (the path, made absolute against the cwd), B spelt by its
// real path (SPEC-routes "link-file", the default connect's), and unlinks E
// right after, whatever the outcome. No D is made. B is Options.Base's when
// given (made here when it is not there yet, unless the spelling is
// estimated not to fit through it; an error making it is the call's), otherwise the process's B of the places 1 to 3 (made here when the
// process has none yet, and kept): the socket's directory, whose real-path
// spelling is longer than the path, is no place for it.
func (c *dialCall) linkFile() (endpoint, error) {
	target, ok := absTarget(c.path)
	if !ok {
		return nil, unusable("cwd unreadable")
	}
	fits := func(_, breal string) bool { return linkConnectLen(breal) <= directMax }
	var b *base
	var err error
	if c.opts.Base != "" {
		if b, err = c.w.givenBase(c.opts.Base, fits); errors.Is(err, errUnusable) {
			return nil, err
		} else if err != nil {
			return nil, notRouted{err}
		}
	} else if b, err = c.w.autoBase(false, fits); err != nil {
		return nil, err
	}
	if linkConnectLen(b.real) > directMax {
		return nil, errSpellingTooLong
	}
	name, err := createAt(c.w.cfg.random, func(n string) error { return symlinkat(target, b.fd, b.real, n) })
	if err != nil {
		return nil, unusable("E not made")
	}
	ep, err := viaStd(c.o, b.real+"/"+name, false)
	unix.Unlinkat(b.fd, name, 0)
	return ep, err
}

// linkConnectLen is the length of the default connect's link-file spelling
// <B's real path>/<E>.
func linkConnectLen(breal string) int {
	return len(breal) + 1 + linkRandomLen
}
