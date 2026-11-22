package sockat

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"

	"github.com/kawaz/sockat-go/internal/argerr"
	"golang.org/x/sys/unix"
)

// The order in which the ways of reaching a long socket path are tried:
// listen / bind and dial / connect are the entry points of the public API;
// the per-route functions are separate so the tests can force each route.

// op is what to do at a path, and with which type of socket.
type op int

const (
	opListen     op = iota // bind and listen a stream socket, as net.ListenUnix does
	opDial                 // connect a stream socket, as net.DialUnix does
	opListenGram           // bind a datagram socket, as net.ListenUnixgram does
	opDialGram             // connect a datagram socket, as net.DialUnix("unixgram", nil, ...) does
)

// binds reports whether o is a bind (opListen, opListenGram).
func (o op) binds() bool { return o == opListen || o == opListenGram }

// network is the net package's name of o's socket type.
func (o op) network() string {
	if o == opListenGram || o == opDialGram {
		return "unixgram"
	}
	return "unix"
}

// sysName is the system call op comes down to, for error messages.
func (o op) sysName() string {
	if o.binds() {
		return "bind"
	}
	return "connect"
}

// netName is the operation name net.OpError uses for op.
func (o op) netName() string {
	if o.binds() {
		return "listen"
	}
	return "dial"
}

// endpoint is what a route produced: a *net.UnixListener for opListen, a
// *net.UnixConn for the others.
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

// reasonOf is the reason an unusable error gives, for the trace: with the B
// of a default name found occupied, when it tells one.
func reasonOf(err error) string {
	if o := occupiedIn(err); o != nil {
		var u unusable
		if errors.As(err, &u) {
			return string(u) + "; " + occupiedReason(o)
		}
		return occupiedReason(o)
	}
	var u unusable
	if errors.As(err, &u) {
		return string(u)
	}
	return "unusable"
}

// errEmptyPath is the error of an empty socket path: an invalid argument
// (SPEC "短いパス"), which the net package would otherwise take to bind an
// address of the kernel's choosing (an abstract name on Linux) or report
// with an error of no errno.
var errEmptyPath = argerr.Mark(fmt.Errorf("socket path must not be empty: %w", syscall.EINVAL))

// errNULInPath is the error of a socket path with a NUL other than an
// abstract name's leading one: an invalid argument whatever the length (SPEC
// "短いパス"), which the net package would cut short there.
var errNULInPath = argerr.Mark(fmt.Errorf("socket path must not contain a NUL byte: %w", syscall.EINVAL))

// errInvalidArg is the errno of an argument of the wrong form that has no
// message of its own (a long path not ending with a file name, an
// Options.Base of the wrong form), marked as sockat's own argument error.
var errInvalidArg = argerr.Mark(syscall.EINVAL)

// errCwdChanged is returned when the cwd changed while binding / connecting
// through a path relative to it.
var errCwdChanged = errors.New("the current directory changed while binding / connecting through a path relative to it; the result was discarded")

// eUse is how a bind has link-dir's E (SPEC-routes "link-dir").
type eUse bool

const (
	// eOwn: an E made for the call, which its SocketFile cleans up (ListenEx,
	// ListenUnixgramEx), removed when the bind through it fails.
	eOwn eUse = false
	// eShared: one E per (D, the socket's directory's real path), shared by
	// the default binds (Listen, ListenUnixgram), which return no SocketFile
	// and so never clean it up; nothing removes it (base.sharedE).
	eShared eUse = true
)

// listen is the bind of Listen: a stream socket, no Options, link-dir's E
// shared.
func (w *world) listen(path string, tr *trace) (*net.UnixListener, bound, error) {
	ep, b, err := w.bind(opListen, path, Options{}, tr, eShared)
	if err != nil {
		return nil, b, err
	}
	return ep.(*net.UnixListener), b, nil
}

// bind binds (and listens, for opListen) at path, recording the routes found
// unusable in tr (nil records nothing), with link-dir's E had as e says.
//
// An empty path is EINVAL. An absolute path the standard library accepts goes
// to it as is, before anything else is looked at (the environment and Options
// included), and its listener removes the socket file on Close as the net
// package's does. So does an abstract socket name (Linux, Android). A NUL
// anywhere else, a leading one too where the kernel has no abstract names, is
// EINVAL, whatever the length: the standard library would cut the path short
// there and bind some other path; so is an Options.Base of the wrong form
// (relative, not ending with a name, or with a NUL).
//
// A relative path is bound through an absolute path naming the same place
// (SPEC "相対パスを絶対パスにする"): its first absolute candidate that fits
// (relCandidate) goes to the standard library, so the spelling the kernel
// keeps, which the listener unlinks on Close, does not depend on the cwd.
// Otherwise, as a longer absolute path does, it takes the bind order
// (bindLong); only when no route can be used is it handed to the standard
// library as it is, if that fits (bindAsIs, CwdRelative). A short relative
// path not ending with a file name has no candidate and goes there at once.
// The error is a *net.OpError for path; the route that bound and failed, if
// any, is in the bound returned. A failure of the bind of a candidate, or of
// a route, is returned: nothing else is tried after it.
func (w *world) bind(o op, path string, opts Options, tr *trace, e eUse) (endpoint, bound, error) {
	if opts.Base != "" {
		if err := givenShape(opts.Base); err != nil {
			return nil, bound{}, opError(o, path, err)
		}
	}
	if path == "" {
		return nil, bound{}, opError(o, path, errEmptyPath)
	}
	if abstractName(path) {
		ep, err := viaStd(o, path, true)
		if err != nil {
			return nil, bound{route: routeStandard}, err
		}
		return ep, bound{route: routeStandard, spelling: Spelling{Kind: Original}}, nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, bound{}, opError(o, path, errNULInPath)
	}
	rel := !isAbs(path)
	if !rel && len(path) <= directMax {
		ep, err := viaStd(o, path, true)
		if err != nil {
			return nil, bound{route: routeStandard}, err
		}
		return ep, bound{route: routeStandard, spelling: Spelling{Kind: Original}, lookup: path}, nil
	}
	if rel {
		if dir, name, err := splitPath(path); err != nil {
			if fitsAsIs(path) {
				return bindAsIs(o, path)
			}
		} else if s := relCandidate(dir, name); s != "" {
			return bindCandidate(o, path, s)
		}
	}
	ep, b, err := w.bindLong(o, path, opts, tr, e)
	if errors.Is(err, errUnusable) {
		if rel && fitsAsIs(path) {
			// The route is standard itself, which then is no route passed
			// over (SPEC "相対パスを絶対パスにする" 5).
			tr.dropStandard()
			return bindAsIs(o, path)
		}
		err = errNameTooLong(o)
	}
	if err != nil {
		return nil, b, opError(o, path, err)
	}
	return ep, b, nil
}

// bindCandidate binds through the standard library by s, an absolute
// candidate of path (SPEC "相対パスを絶対パスにする"): Original, nothing made
// in a place. The listener removes s on Close, as the net package's do: s is
// absolute and names the socket file bound. The error, the bind's own, is
// not one to try anything else after.
func bindCandidate(o op, path, s string) (endpoint, bound, error) {
	ep, err := viaStd(o, s, false)
	if err != nil {
		return nil, bound{route: routeStandard}, opError(o, path, err)
	}
	return ep, bound{route: routeStandard, spelling: Spelling{Kind: Original}, lookup: s}, nil
}

// bindAsIs binds the relative path through the standard library as it is
// (stdSpelling): a short one not ending with a file name, whose error the
// standard library gives, and one no absolute candidate fits and no route
// reaches (SPEC "相対パスを絶対パスにする" 5). Its spelling is CwdRelative. A
// listener whose spelling is not the path itself (illumos's "./@...") does
// not remove it on Close (DR-0017).
func bindAsIs(o op, path string) (endpoint, bound, error) {
	s := stdSpelling(path)
	ep, err := viaStd(o, s, s == path)
	if err != nil {
		if s != path {
			err = opError(o, path, unwrapOpError(err))
		}
		return nil, bound{route: routeStandard}, err
	}
	if l, ok := ep.(*net.UnixListener); ok && s != path {
		l.SetUnlinkOnClose(false)
	}
	return ep, bound{route: routeStandard, spelling: Spelling{Kind: CwdRelative}, lookup: path}, nil
}

// abstractName reports whether path is the name of an abstract socket (a
// leading NUL, or Go's '@' for it) where the kernel has such names (Linux,
// Android). Such a name has no place in the file system, so no route applies
// and the NUL check does not either; the standard library alone decides,
// whatever the length.
func abstractName(path string) bool {
	return abstractNames && (strings.HasPrefix(path, "@") || strings.HasPrefix(path, "\x00"))
}

// stdSpelling is what a path handed to the standard library as it is (a
// connect's; a relative one of a bind or a resolve only when no absolute
// spelling can be used) is handed as, when it is short enough: path itself,
// except where the standard library would read a leading '@' as an abstract
// name that the kernel does not have (illumos). There "./" goes in front, so
// the bind / connect is of the file "@..." in the cwd, as the path means on
// that system. When that no longer fits, a connect takes the routes like any
// other long path, and a bind or a resolve, whose routes were found unusable
// already, is ENAMETOOLONG.
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
	addr := &net.UnixAddr{Name: spelling, Net: o.network()}
	var ep endpoint
	var err error
	switch o {
	case opListen:
		var l *net.UnixListener
		if l, err = net.ListenUnix(o.network(), addr); err == nil {
			ep = l
		}
	case opListenGram:
		var c *net.UnixConn
		if c, err = net.ListenUnixgram(o.network(), addr); err == nil {
			ep = c
		}
	case opDial, opDialGram:
		var c *net.UnixConn
		if c, err = net.DialUnix(o.network(), nil, addr); err == nil {
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
	return &net.OpError{Op: o.netName(), Net: o.network(), Addr: &net.UnixAddr{Name: path, Net: o.network()}, Err: err}
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

// dropStandard takes standard out of the routes recorded: for a relative
// path that no route could be used for and that is then handed to the
// standard library as it is, standard is the route taken (SPEC
// "相対パスを絶対パスにする": skipped holds the routes found unusable only).
func (t *trace) dropStandard() {
	if t != nil && len(t.skipped) > 0 && t.skipped[0].name == routeStandard {
		t.skipped = t.skipped[1:]
	}
}
