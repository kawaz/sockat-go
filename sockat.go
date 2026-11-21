package sockat

import "net"

// Listen creates a socket bound to path and listening on it, like
// net.ListenUnix("unix", ...) but for a path of any length.
//
// A path that the net package accepts goes to net.ListenUnix unchanged, so it
// behaves exactly as with the net package (including the removal of the
// socket file when the listener is closed). A longer path is reached by the
// routes the system has (see the package documentation); the socket file is
// created at path, and the spelling the kernel keeps for it (the listener's
// Addr()) goes through a directory of this user's processes in /tmp (or
// $XDG_RUNTIME_DIR, $TMPDIR, the socket's directory), left there for as long
// as the process lives; the listens to one directory share the symlink to
// it in that spelling. Closing the listener unlinks that spelling as the net
// package does: through a spelling that names the socket's directory the
// socket file is removed, through one that names the socket file itself the
// symlink is removed and the file stays, and through a spelling that names
// nothing, nothing is. ListenEx tells which, and removes the file reliably.
//
// The error is a *net.OpError, as from the net package: for example
// EADDRINUSE when path exists, ENOENT when its directory does not,
// ENAMETOOLONG when no route reaches path, EINVAL for an empty path or a path that contains a
// NUL byte or for a long path that does not end with a file name.
func Listen(path string) (*net.UnixListener, error) {
	l, _, err := defaultWorld.listen(path, nil)
	return l, err
}

// Dial connects to the socket at path, like net.DialUnix("unix", nil, ...)
// but for a path of any length.
//
// A path that the net package accepts goes to net.DialUnix unchanged. A
// longer path is reached by the routes the system has (see the package
// documentation). Like the net package's, the connect does not wait for room
// in a full backlog: Linux answers EAGAIN, macOS ECONNREFUSED.
//
// The error is a *net.OpError, as from the net package: for example ENOENT
// when nothing is at path, ECONNREFUSED when nobody listens on it,
// ENAMETOOLONG when no route reaches path, EINVAL for an empty path or a path that contains a
// NUL byte or for a long path that does not end with a file name.
func Dial(path string) (*net.UnixConn, error) {
	c, _, err := defaultWorld.dial(path, Options{}, nil)
	return c, err
}

// ListenEx is Listen with Options, reporting what the spelling the kernel
// keeps is good for (Bound.Spelling), a SocketFile that removes the socket
// file and cleans up the spelling, and the routes tried.
//
// Right after the bind it records the socket file (its real path and
// (st_dev, st_ino)) for Bound.File's Remove. A long path bound through the
// real directory a route resolved before the bind is recorded in that
// directory, so a symlink on the way changed in the meantime does not make
// it record another file. A symlink at the last component of the path
// (which bind(2) follows on macOS and NetBSD) is followed to the socket it
// leads to: that is what Remove removes, and the symlink stays.
// When the file cannot be recorded (a symlink there leads to no socket, for
// example), the listener is closed, the file is left, and an error is
// returned; so too for a relative path when the cwd changes (another
// goroutine's os.Chdir) between the start of the call and the record after
// the bind.
//
// The listener does not remove the socket file on Close, short paths
// included: the removal is Bound.File's alone. Neither Remove nor Cleanup is
// done for you.
//
// The error is a *RouteError.
func ListenEx(path string, opts Options) (*net.UnixListener, *Bound, error) {
	return defaultWorld.listenEx(path, opts)
}

// DialEx is Dial, reporting the route taken and the routes tried. The
// connection is the same as Dial's; a connect leaves no spelling with the
// peer, so there is nothing to clean up.
//
// The error is a *RouteError.
func DialEx(path string, opts Options) (*net.UnixConn, Report, error) {
	tr := &trace{}
	c, r, err := defaultWorld.dial(path, opts, tr)
	if err != nil {
		return nil, Report{}, &RouteError{Route: string(r), Skipped: tr.skips(), Err: err}
	}
	return c, Report{Route: string(r), Skipped: tr.skips()}, nil
}
