package sockat

import "net"

// ListenUnixgram creates a datagram socket bound to path, like
// net.ListenUnixgram("unixgram", ...) but for a path of any length.
//
// An absolute path that the net package accepts goes to
// net.ListenUnixgram unchanged. A relative path is bound through an absolute
// path naming the same place, as Listen binds it, so that a reply to the
// sender's address arrives whatever the cwd of the replier. A longer path is
// bound by the routes Listen takes, and the spelling the
// kernel keeps for it (the socket's LocalAddr(), and the address the peers
// see as the sender) is the one Listen's would be: a reply sent to it
// arrives for as long as the spelling can be followed (ListenUnixgramEx
// tells which). As with the net package, closing the socket does not remove
// the socket file.
//
// The error is a *net.OpError, as from the net package (Net "unixgram"):
// for example EADDRINUSE when path exists, ENOENT when its directory does
// not, ENAMETOOLONG when no route reaches path, EINVAL for an empty path or a path that
// contains a NUL byte or for a long path that does not end with a file name.
func ListenUnixgram(path string) (*net.UnixConn, error) {
	ep, _, err := defaultWorld.bind(opListenGram, path, Options{}, nil, eShared)
	if err != nil {
		return nil, err
	}
	return ep.(*net.UnixConn), nil
}

// DialUnixgram connects a datagram socket, not bound, to the socket at path,
// like net.DialUnix("unixgram", nil, ...) but for a path of any length. A
// longer path is reached by the routes Dial takes.
//
// To connect a socket that is bound (one from ListenUnixgram, say) to a long
// path, or to send to a long path without connecting, spell the path with
// ResolveForConnect and hand that spelling to the net package.
//
// The error is a *net.OpError, as from the net package (Net "unixgram"):
// for example ENOENT when nothing is at path, ECONNREFUSED when no socket is
// bound to it, ENAMETOOLONG when no route reaches path, EINVAL for an empty path or a path
// that contains a NUL byte or for a long path that does not end with a file
// name.
func DialUnixgram(path string) (*net.UnixConn, error) {
	ep, _, err := defaultWorld.connect(opDialGram, path, Options{}, nil)
	if err != nil {
		return nil, err
	}
	return ep.(*net.UnixConn), nil
}

// ListenUnixgramEx is ListenUnixgram with Options, reporting what the
// spelling the kernel keeps is good for (Bound.Spelling: whether a reply to
// it can arrive, and from whom), a SocketFile that removes the socket file
// and cleans up the spelling, and the routes tried, as ListenEx does. Right
// after the bind it records the socket file for Bound.File's Remove, as
// ListenEx does. Neither Remove nor Cleanup is done for you.
//
// The error is a *RouteError.
func ListenUnixgramEx(path string, opts Options) (*net.UnixConn, *Bound, error) {
	ep, b, err := defaultWorld.bindReported(opListenGram, path, opts)
	if err != nil {
		return nil, nil, err
	}
	return ep.(*net.UnixConn), b, nil
}

// DialUnixgramEx is DialUnixgram, reporting the route taken and the routes
// tried, as DialEx does. A connect leaves no spelling with the peer, so
// there is nothing to clean up.
//
// The error is a *RouteError.
func DialUnixgramEx(path string, opts Options) (*net.UnixConn, Report, error) {
	tr := &trace{}
	ep, r, err := defaultWorld.connect(opDialGram, path, opts, tr)
	if err != nil {
		return nil, Report{}, &RouteError{Route: string(r), Skipped: tr.skips(), Err: err}
	}
	return ep.(*net.UnixConn), Report{Route: string(r), Skipped: tr.skips()}, nil
}
