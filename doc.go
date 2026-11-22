// Package sockat binds and connects Unix domain sockets whose path is longer
// than sun_path allows.
//
// struct sockaddr_un keeps the socket path in a fixed array: 104 bytes on
// macOS and the BSDs, 108 on Linux and illumos, the terminating NUL included.
// The net package refuses anything longer with EINVAL, although the file
// system has no such limit. Deep temporary directories (macOS's $TMPDIR alone
// is about 50 bytes, and t.TempDir() nests further) run into it easily. The
// limit is on the argument passed to bind(2) / connect(2), so sockat hands the
// kernel a short spelling that names the same place.
//
// Use [Listen] and [Dial] where you would use net.ListenUnix / net.DialUnix
// (or net.Listen("unix", ...) / net.Dial("unix", ...)), and [ListenUnixgram]
// and [DialUnixgram] for datagram sockets. They take a path of any length and
// return the net package's own *net.UnixListener and *net.UnixConn. An
// absolute path the net package accepts (103 bytes on macOS and the BSDs, 107
// on Linux and illumos) is handed to it unchanged, so it behaves exactly as
// with the net package. A relative path is made absolute first by the binds
// and the resolves, so that the spelling the kernel keeps does not change
// with the working directory; the connects take it as it is. For a longer
// path the socket file is still created at (or looked up at) the path given.
//
//	l, err := sockat.Listen("/a/very/long/directory/.../server.sock")
//	c, err := sockat.Dial("/a/very/long/directory/.../server.sock")
//
// [ListenEx], [DialEx], [ListenUnixgramEx] and [DialUnixgramEx] do the same
// with [Options], and also report the route taken and the routes skipped, for
// diagnostics; the binds ([Bound]) also report what the spelling the kernel
// keeps is good for ([Spelling]) and give a [SocketFile] that removes the
// socket file. Their error is a [*RouteError]:
//
//	l, b, err := sockat.ListenEx(path, sockat.Options{})
//	defer l.Close()
//	defer b.File.Remove() // before l.Close
//
// [ResolveForBind] and [ResolveForConnect] spell a long path for you to hand
// to the net package yourself. [Sweep] and [SweepBase] remove what processes
// that have ended left in the places.
//
// # The specification
//
// The behaviour common to all languages is defined by the specification,
// https://github.com/kawaz/sockat/blob/main/docs/SPEC.md, and not repeated
// here. Read in it, in particular:
//
//   - Making a relative path absolute: which operations do, and how
//   - What differs from the standard for a long path: the address the kernel
//     returns is the spelling (absolute for a relative path too), state is
//     left outside the application's places, a live D may be removed, a peer
//     with PrivateTmp
//   - Patterns not to use, with the Go forms in the reference and the
//     Example functions of this package
//   - What stays in the base, and sweep: B, D and E, what Sweep removes, and
//     how a long-lived process cleans up with ListenEx
//   - Routes for long paths and their order; how each route is built is in
//     SPEC-routes.md next to it
//
// How to use the package, scene by scene, is in the user manual,
// https://github.com/kawaz/sockat-go/blob/main/docs/MANUAL.md, whose sample
// code is the Example functions. The routes each system tries in Go are
// listed in the reference,
// https://github.com/kawaz/sockat-go/blob/main/docs/REFERENCE.md#how-a-long-path-is-reached.
//
// # Go-specific caveats
//
//   - An empty path is EINVAL. The net package binds it, on Linux, to an
//     abstract name the kernel picks, and fails a connect to it with an
//     error that has no errno.
//   - A path containing a NUL byte is EINVAL, whatever its length (the net
//     package would cut the path short there and bind some other path). An
//     abstract socket name (a leading NUL, or '@', on Linux and Android) is
//     handed to the net package as is. Where the kernel has no abstract
//     sockets (illumos included), a leading NUL is EINVAL too.
//   - On illumos, a path that starts with '@' names the file "@..." in the
//     current directory. The net package would read the '@' as an abstract
//     name, which the kernel there does not have. A bind makes it absolute
//     as any other relative path; where it is handed to the net package as a
//     relative path (a connect, and a bind that could take no route), it
//     goes with "./" in front: that listener's Addr is "./@...", its Close
//     leaves the file (as the net package elsewhere leaves a path that starts
//     with '@'), and a path of 106 or 107 bytes, which no longer fits, is
//     treated as a long path.
//   - net.UnixListener's Close unlinks the address it keeps. For [Listen]'s
//     listener of a long path that is the spelling: through link-dir's the
//     socket file is removed, through link-file's the symlink E is removed
//     and the file stays, and through a Dangling one (thread-cwd, at) nothing
//     is (SetUnlinkOnClose(false)). The listener of [ListenEx] removes
//     nothing on Close, short paths included: use Bound.File's Remove, before
//     closing the listener. A listener you make yourself from
//     [ForBind].Path wants SetUnlinkOnClose(false) and the Remove of
//     [ForBind.SocketFile].
//   - The specification's widened sockaddr (Apple's systems) is not made:
//     handed to the net package, a socket connected that way to a peer bound
//     that way has a nil RemoteAddr, whose String panics.
//   - The thread-cwd routes run on a throwaway goroutine locked to its OS
//     thread, which ends without unlocking so that the runtime throws the
//     thread away (the main thread, which the runtime keeps, never has its
//     cwd moved). None of the routes changes the working directory of the
//     process or of the calling goroutine's thread, and sockat does not fork:
//     every function can be called from any goroutine.
package sockat
