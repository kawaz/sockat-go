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
	routeDirect route = "direct" // the path itself, through the standard library
	// The central route: R/<name> relative to D.
	routeThreadCwd route = "thread-cwd" // a thread whose own cwd is D (Linux, macOS)
	routeProcD     route = "proc-d"     // /proc/self/fd/<D>/R/<name> (Linux)
	routeAt        route = "at"         // bindat(2) / connectat(2) on D (FreeBSD)
	// The fallback routes of a dial.
	routeProc        route = "proc"         // /proc/self/fd/<dirfd>/<name> (Linux)
	routeRelative    route = "relative"     // the path relative to the cwd
	routeSymlinkFile route = "symlink-file" // through a symlink to the file
	routeHeldLink    route = "held-link"    // a symlink in a directory of our own held by its fd (Linux)
	routeConnectAt   route = "connectat"    // connectat(2) on the socket's directory (FreeBSD)
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

// errCwdChanged is returned when the cwd changed while binding / connecting
// through a path relative to it.
var errCwdChanged = errors.New("the current directory changed while binding / connecting through a path relative to it; the result was discarded")

// run binds (and listens) or connects to path, trying the routes in order.
//
// A path the standard library accepts goes to it as is, before anything else
// is looked at (the environment included). So does an abstract socket name.
// A NUL anywhere else is EINVAL, whatever the length: the standard library
// would cut the path short there and bind / connect some other path.
func run(o op, path string) (endpoint, route, error) {
	if abstractName(path) {
		ep, err := viaStd(o, path, true)
		return ep, routeDirect, err
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, "", opError(o, path, fmt.Errorf("socket path must not contain a NUL byte: %w", syscall.EINVAL))
	}
	if len(path) <= directMax {
		ep, err := viaStd(o, path, true)
		return ep, routeDirect, err
	}
	ep, r, err := runLong(o, path, configFromEnv())
	if err != nil {
		return nil, "", opError(o, path, err)
	}
	return ep, r, nil
}

// abstractName reports whether the standard library reads path as the name
// of an abstract socket (a leading NUL, or Go's '@' for it, on Linux and
// illumos). Such a name has no place in the file system, so no route applies
// and the NUL check does not either; the standard library alone decides,
// whatever the length.
func abstractName(path string) bool {
	return abstractNames && (strings.HasPrefix(path, "@") || strings.HasPrefix(path, "\x00"))
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
	// tmpBases is where the temporary symlink of a dial's fallback may go, in
	// the order tried: TMPDIR when it is set to an absolute path, then /tmp.
	tmpBases []string
	// procSelfFD is where the kernel lists the fds of the process (Linux).
	// The tests point it elsewhere to stand in for a system without /proc.
	procSelfFD string
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
	return &config{dBases: dBases, tmpBases: bases, procSelfFD: "/proc/self/fd", threadCwd: true}
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
