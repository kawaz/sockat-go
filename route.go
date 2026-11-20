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

// The order in which the ways of reaching a long socket path are tried. run is
// the only entry point used by the public API; the per-route functions are
// separate so the tests can force each route.

// op is what to do at a path.
type op int

const (
	opListen op = iota // bind and listen, as net.ListenUnix does
	opDial             // connect, as net.DialUnix does
)

// sysName is the system call op comes down to, for error messages.
func (o op) sysName() string {
	if o == opListen {
		return "bind"
	}
	return "connect"
}

// netName is the operation name net.OpError uses for op.
func (o op) netName() string {
	if o == opListen {
		return "listen"
	}
	return "dial"
}

// endpoint is what a route produced: a *net.UnixListener for opListen, a
// *net.UnixConn for opDial.
type endpoint interface{ Close() error }

// route names which way a bind / connect went. The public API does not show
// it; the tests read it to check that a path took the route they force.
type route string

const (
	// The names are SPEC's ("経路の名前"): the means, then what it is about;
	// -direct is the form that passes the file name with no E.
	routeStandard route = "standard" // the path itself, through the standard library
	// The routes of a bind (bind.go), and link-file of a connect.
	routeLinkDir  route = "link-dir"  // <B>/<D>/<E>/<name>, E a symlink to the socket's directory
	routeLinkFile route = "link-file" // <B>/<D>/<E> (a bind), <B>/<E> (a connect), E a symlink to the socket file
	// <E>/<name> relative to D, E removed right after: thread-cwd from a
	// thread whose own cwd is D (Linux, macOS), at by bindat(2) (FreeBSD).
	routeThreadCwd route = "thread-cwd"
	routeAt        route = "at"
	// The routes of a connect only (dial.go).
	routeProcDirect      route = "proc-direct"       // /proc/self/fd/<dirfd>/<name> (Linux)
	routeRelative        route = "relative"          // the path relative to the cwd
	routeWidened         route = "widened"           // SPEC's; sockat-go does not make it (named in skipped, macOS)
	routeAtDirect        route = "at-direct"         // connectat(2) on the socket's directory (FreeBSD)
	routeThreadCwdDirect route = "thread-cwd-direct" // <name> from a thread whose own cwd is the socket's directory
)

// directMax is the longest path the standard library accepts: sun_path less
// the NUL, 107 bytes on Linux and illumos, 103 on macOS and the BSDs (the
// test syscall.SockaddrUnix makes). A path of at most this many bytes is handed
// to the standard library as is, so it alone decides what happens to every
// path it can take. It is also the limit of every other spelling a route
// hands to the kernel.
const directMax = len(unix.RawSockaddrUnix{}.Path) - 1

// errUnusable means that a route cannot be used at all (too long, no /proc,
// no B, D or E, no thread cwd of its own), as
// opposed to a real failure of the bind / connect it made. It never leaves the
// package: it only makes the order move on to the next route.
var errUnusable = errors.New("sockat: route unusable")

// unusable is errUnusable with the reason the route could not be used, which
// the trace records (diagnostics only).
type unusable string

func (u unusable) Error() string        { return "sockat: route unusable: " + string(u) }
func (u unusable) Is(target error) bool { return target == errUnusable }

// reasonOf is the reason an unusable error gives, for the trace.
func reasonOf(err error) string {
	var u unusable
	if errors.As(err, &u) {
		return string(u)
	}
	return "unusable"
}

// errCwdChanged is returned when the cwd changed while binding / connecting
// through a path relative to it.
var errCwdChanged = errors.New("the current directory changed while binding / connecting through a path relative to it; the result was discarded")

// listen binds (and listens) at path (Listen, ListenEx), recording the
// routes found unusable in tr (nil records nothing).
//
// A path the standard library accepts goes to it as is (stdSpelling), before
// anything else is looked at (the environment and Options included), and
// its listener removes the socket file on Close as the net package's does. So
// does an abstract socket name (Linux, Android). A NUL anywhere else, a
// leading one too where the kernel has no abstract names, is EINVAL, whatever
// the length: the standard library would cut the path short there and bind
// some other path; so is an Options.Base of the wrong form (relative, or not
// ending with a name). A longer path takes the bind order (bindLong). The error
// is a *net.OpError for path; the route that bound and failed, if any, is in
// the bound returned.
func (w *world) listen(path string, opts Options, tr *trace) (*net.UnixListener, bound, error) {
	if opts.Base != "" {
		if err := givenShape(opts.Base); err != nil {
			return nil, bound{}, opError(opListen, path, err)
		}
	}
	if abstractName(path) {
		ep, err := viaStd(opListen, path, true)
		if err != nil {
			return nil, bound{route: routeStandard}, err
		}
		return ep.(*net.UnixListener), bound{route: routeStandard, spelling: Spelling{Kind: Original}}, nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, bound{}, opError(opListen, path, fmt.Errorf("socket path must not contain a NUL byte: %w", syscall.EINVAL))
	}
	if s := stdSpelling(path); len(s) <= directMax {
		ep, err := viaStd(opListen, s, s == path)
		if err != nil {
			if s != path {
				err = opError(opListen, path, unwrapOpError(err))
			}
			return nil, bound{route: routeStandard}, err
		}
		l := ep.(*net.UnixListener)
		if s != path {
			l.SetUnlinkOnClose(false)
		}
		kind := Original
		if !isAbs(path) {
			kind = CwdRelative
		}
		return l, bound{route: routeStandard, spelling: Spelling{Kind: kind}}, nil
	}
	ep, b, err := w.bindLong(path, opts, tr)
	if err != nil {
		return nil, b, opError(opListen, path, err)
	}
	return ep.(*net.UnixListener), b, nil
}

// abstractName reports whether path is the name of an abstract socket (a
// leading NUL, or Go's '@' for it) where the kernel has such names (Linux,
// Android). Such a name has no place in the file system, so no route applies
// and the NUL check does not either; the standard library alone decides,
// whatever the length.
func abstractName(path string) bool {
	return abstractNames && (strings.HasPrefix(path, "@") || strings.HasPrefix(path, "\x00"))
}

// stdSpelling is what path is handed to the standard library as, when it is
// short enough: path itself, except where the standard library would read a
// leading '@' as an abstract name that the kernel does not have (illumos).
// There "./" goes in front, so the bind / connect is of the file "@..." in
// the cwd, as the path means on that system. When that no longer fits, the
// path goes through the routes like any other long path.
//
// Design rationale: SPEC hands a short path to the standard library as it is,
// but Go's standard library on illumos rewrites a leading '@' to a NUL and
// the kernel then fails (ENOENT). "./" is the shortest spelling of the same
// relative path that it leaves alone, at the cost of two bytes and of the
// kernel keeping "./@..." (the listener's Addr). Its listener does not
// remove the file on Close, as the net package elsewhere leaves a path that
// starts with '@' (and as a relative spelling must not be unlinked once the
// cwd may have changed). DR-0002.
func stdSpelling(path string) string {
	if stdReadsAtAsAbstract && !abstractNames {
		return asSpelling(path)
	}
	return path
}

// viaStd binds (and listens) or connects through the standard library with
// spelling as the address. A listener removes its address on Close, as the
// net package's do; the caller turns that off where the address is not to be
// unlinked.
//
// direct is true when spelling is the caller's own path; otherwise the error
// is the bare system call error (an *os.SyscallError), for the caller to wrap
// with the caller's path.
func viaStd(o op, spelling string, direct bool) (endpoint, error) {
	addr := &net.UnixAddr{Name: spelling, Net: "unix"}
	var ep endpoint
	var err error
	switch o {
	case opListen:
		var l *net.UnixListener
		if l, err = net.ListenUnix("unix", addr); err == nil {
			ep = l
		}
	case opDial:
		var c *net.UnixConn
		if c, err = net.DialUnix("unix", nil, addr); err == nil {
			ep = c
		}
	}
	if err != nil {
		if direct {
			return nil, err
		}
		return nil, unwrapOpError(err)
	}
	return ep, nil
}

// unwrapOpError takes the system call error out of a net.OpError (whose Addr
// is a spelling, not the caller's path).
func unwrapOpError(err error) error {
	var oe *net.OpError
	if errors.As(err, &oe) {
		return oe.Err
	}
	return err
}

// opError wraps err in the net.OpError the standard library would return for
// path, so a long path fails in the same shape as a short one.
func opError(o op, path string, err error) error {
	var oe *net.OpError
	if errors.As(err, &oe) {
		return err
	}
	return &net.OpError{Op: o.netName(), Net: "unix", Addr: &net.UnixAddr{Name: path, Net: "unix"}, Err: err}
}

// syscallError wraps a system call's errno as os.SyscallError does, keeping
// errors.Is(err, syscall.ENOENT) and the like working.
func syscallError(name string, err error) error {
	if err == nil {
		return nil
	}
	return os.NewSyscallError(name, err)
}

// errNameTooLong is the error for a path that no route reaches:
// ENAMETOOLONG, the error of a plain bind / connect with a path too long for
// sun_path.
func errNameTooLong(o op) error {
	return syscallError(o.sysName(), syscall.ENAMETOOLONG)
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
