package sockat

import "net"

// Listen creates a socket bound to path and listening on it, like
// net.ListenUnix("unix", ...) but for a path of any length.
//
// A path that the net package accepts goes to net.ListenUnix unchanged, so it
// behaves exactly as with the net package (including the removal of the
// socket file when the listener is closed). A longer path is reached by the
// routes the system has (see the package documentation); the socket file is
// created at path, and is not removed when the listener is closed.
//
// The error is a *net.OpError, as from the net package: for example
// EADDRINUSE when path exists, ENOENT when its directory does not,
// ENAMETOOLONG when no route reaches path, EINVAL for a path that contains a
// NUL byte or for a long path that does not end with a file name.
func Listen(path string) (*net.UnixListener, error) {
	ep, _, err := run(opListen, path)
	if err != nil {
		return nil, err
	}
	return ep.(*net.UnixListener), nil
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
// ENAMETOOLONG when no route reaches path, EINVAL for a path that contains a
// NUL byte or for a long path that does not end with a file name.
func Dial(path string) (*net.UnixConn, error) {
	ep, _, err := run(opDial, path)
	if err != nil {
		return nil, err
	}
	return ep.(*net.UnixConn), nil
}
