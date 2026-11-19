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
	// The names are SPEC's ("経路の名前"): the means, then what it is about.
	routeStandard route = "standard" // the path itself, through the standard library
	// A symlink <place>/sockat.XXXXXXXXXX to the socket file: a dial, and a
	// listen where bind(2) follows it (not Linux).
	routeSymlinkFile route = "symlink-file"
	// Through D: R/<name> relative to D.
	routeThreadCwd route = "thread-cwd" // a thread whose own cwd is D (Linux, macOS)
	routeProc      route = "proc"       // /proc/self/fd/<D>/R/<name> (Linux)
	routeAt        route = "at"         // bindat(2) / connectat(2) on D (FreeBSD)
	routeForkCwd   route = "fork-cwd"   // SPEC's; Go has no fork route (named in skipped)
	// A bind's form without D: .sockat.R/<name> relative to the socket's directory.
	routeThreadCwdSockdir route = "thread-cwd-sockdir"
	routeProcSockdir      route = "proc-sockdir"
	routeAtSockdir        route = "at-sockdir"
	routeForkCwdSockdir   route = "fork-cwd-sockdir" // SPEC's; Go has no fork route
	// ListenEx's last resort: a symlink <place>/sockat.XXXXXXXXXX to the
	// socket's directory, the bind by <place>/sockat.XXXXXXXXXX/<name>.
	routeSymlinkDir route = "symlink-dir"
	// The routes of a dial only (the connect order).
	routeProcDirect route = "proc-direct" // /proc/self/fd/<dirfd>/<name> (Linux)
	routeProcHeld   route = "proc-held"   // /proc/self/fd/<D>/s, s a symlink to the file (Linux)
	routeRelative   route = "relative"    // the path relative to the cwd
	routeWidened    route = "widened"     // SPEC's; sockat-go does not make it (named in skipped, macOS)
	routeAtDirect   route = "at-direct"   // connectat(2) on the socket's directory (FreeBSD)
	// DialEx's last resort, SPEC's; Go has no fork route (named in skipped).
	routeForkCwdDirect route = "fork-cwd-direct"
)

// directMax is the longest path the standard library accepts: sun_path less
// the NUL, 107 bytes on Linux and illumos, 103 on macOS and the BSDs (the
// test syscall.SockaddrUnix makes). A path of at most this many bytes is handed
// to the standard library as is, so it alone decides what happens to every
// path it can take. It is also the limit of every other spelling a route
// hands to the kernel.
const directMax = len(unix.RawSockaddrUnix{}.Path) - 1

// errUnusable means that a route cannot be used at all (too long, no /proc,
// no safe place for a symlink, no thread cwd of its own), as
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

// run binds (and listens) or connects to path, trying the routes in order.
//
// A path the standard library accepts goes to it as is (stdSpelling), before
// anything else is looked at (the environment included). So does an abstract
// socket name (Linux, Android). A NUL anywhere else, a leading one too where
// the kernel has no abstract names, is EINVAL, whatever the length: the
// standard library would cut the path short there and bind / connect some
// other path.
func run(o op, path string) (endpoint, route, error) {
	return runTraced(o, path, nil, nil)
}

// runWith is run with the environment's inputs given as cfg (the tests pass
// their own); nil reads them from the environment, and only for a long path.
func runWith(o op, path string, cfg *config) (endpoint, route, error) {
	return runTraced(o, path, cfg, nil)
}

// runTraced is runWith recording the routes found unusable in tr (ListenEx,
// DialEx), in place of cfg's own trace, unless tr is nil.
func runTraced(o op, path string, cfg *config, tr *trace) (endpoint, route, error) {
	if abstractName(path) {
		ep, err := viaStd(o, path, true)
		return ep, routeStandard, err
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, "", opError(o, path, fmt.Errorf("socket path must not contain a NUL byte: %w", syscall.EINVAL))
	}
	if s := stdSpelling(path); len(s) <= directMax {
		ep, err := viaStd(o, s, s == path)
		if err != nil && s != path {
			err = opError(o, path, unwrapOpError(err))
		}
		return ep, routeStandard, err
	}
	if cfg == nil {
		cfg = configFromEnv()
	}
	if tr != nil {
		c := *cfg
		c.trace = tr
		cfg = &c
	}
	ep, r, err := runLong(o, path, cfg)
	if err != nil {
		return nil, r, opError(o, path, err)
	}
	return ep, r, nil
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
// spelling as the address.
//
// direct is true when spelling is the caller's own path. Otherwise the
// listener must not remove its "path" on Close: that is the spelling (a
// /proc/self/fd path, a relative path, a symlink that is gone, a bare file
// name), which by then may name some other file.
//
// The error of a spelling is the bare system call error (an *os.SyscallError)
// so the caller can wrap it with the caller's path.
func viaStd(o op, spelling string, direct bool) (endpoint, error) {
	addr := &net.UnixAddr{Name: spelling, Net: "unix"}
	var ep endpoint
	var err error
	switch o {
	case opListen:
		var l *net.UnixListener
		if l, err = net.ListenUnix("unix", addr); err == nil {
			if !direct {
				l.SetUnlinkOnClose(false)
			}
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

// config is what the route order reads from the environment, made explicit so
// the tests can vary it without touching the process environment.
type config struct {
	// dBases is where D may go, in the order tried: ${TMPDIR:-/tmp} (a
	// relative one too), then /tmp. The socket's directory comes after them.
	dBases []string
	// tmpBases is where the temporary symlink of symlink-file (a dial's, and
	// a listen's where bind(2) follows it) may go, in the order tried: TMPDIR
	// when it is set to an absolute path, then /tmp.
	tmpBases []string
	// procSelfFD is where the kernel lists the fds of the process (Linux).
	// The tests point it elsewhere to stand in for a system without /proc.
	procSelfFD string
	// dirBases is where the symlink of symlink-dir (ListenEx's last resort)
	// may go, in the order tried within each kind of place: XDG_RUNTIME_DIR,
	// TMPDIR (when set to absolute paths), then /tmp.
	dirBases []string
	// trace, when set (ListenEx, DialEx), records the routes found unusable.
	trace *trace
	// threadCwd is whether to try the thread-cwd route. Always true outside
	// the tests, which turn it off to stand in for a system that refuses it
	// (a seccomp filter on unshare(2), for example).
	threadCwd bool
}

func configFromEnv() *config {
	tmpdir, _ := os.LookupEnv("TMPDIR")
	return configFromTmpdir(tmpdir)
}

// configFromTmpdir is the configuration for a TMPDIR of tmpdir: TMPDIR, then
// /tmp. An unset or empty TMPDIR leaves /tmp alone; a relative one is left out
// (the link's place would depend on the cwd); one that is /tmp itself is tried
// once.
func configFromTmpdir(tmpdir string) *config {
	var bases []string
	if isAbs(tmpdir) && !sameComponents(tmpdir, "/tmp") {
		bases = append(bases, tmpdir)
	}
	bases = append(bases, "/tmp")
	dBases := []string{"/tmp"}
	if tmpdir != "" && !sameComponents(tmpdir, "/tmp") {
		dBases = []string{tmpdir, "/tmp"}
	}
	runtimeDir, _ := os.LookupEnv("XDG_RUNTIME_DIR")
	return &config{dBases: dBases, tmpBases: bases, dirBases: dirBasesFrom(runtimeDir, tmpdir),
		procSelfFD: "/proc/self/fd", threadCwd: true}
}

// dirBasesFrom is symlink-dir's places for the symlink: XDG_RUNTIME_DIR and
// TMPDIR when absolute, then /tmp, each spelling once.
func dirBasesFrom(runtimeDir, tmpdir string) []string {
	var bases []string
	for _, b := range []string{runtimeDir, tmpdir, "/tmp"} {
		if !isAbs(b) {
			continue
		}
		dup := false
		for _, seen := range bases {
			if sameComponents(seen, b) {
				dup = true
			}
		}
		if !dup {
			bases = append(bases, b)
		}
	}
	return bases
}

// sameComponents reports whether a and b are the same spelling component by
// component ("/tmp/" and "/tmp" are; "/private/tmp" and "/tmp" are not, even
// where they name the same directory, because their lengths differ).
func sameComponents(a, b string) bool {
	ca, cb := components(a), components(b)
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}
