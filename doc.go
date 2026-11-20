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
// return the net package's own *net.UnixListener and *net.UnixConn. A path the
// net package accepts (103 bytes on macOS and the BSDs, 107 on Linux and
// illumos) is handed to it unchanged, so it behaves exactly as with the net
// package. For a longer path the socket file is still created at (or looked
// up at) the path given.
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
// The language-independent specification is at
// https://github.com/kawaz/sockat/blob/main/docs/SPEC.md.
//
// # The places B, D and E
//
// The spelling of a long path goes through B, a directory of this user's
// processes shared across them: the first of /tmp/sockat-<uid>,
// $XDG_RUNTIME_DIR/sockat, $TMPDIR/sockat-<uid> and .sockat.<random> in the
// socket's directory that can be made, or [Options].Base. Every directory on
// the way to the place must be owned by root or the user and not writable by
// others (or sticky); a B already there that fails its checks is passed over
// for sockat-<uid>.<random> in the same place. In B, each process makes one
// directory D of a random name and holds it, with a shared lock, for as long
// as it lives; in D go the symlinks E the spellings go through. B and D are
// not removed by sockat: a dead process's D goes with the system's cleaning
// of its temporary directories, or with [Sweep] (and [SweepBase] for
// Options.Base's B), which removes only directories named as D is (10
// alphanumeric characters) and only when no live process holds them. B in the
// socket's directory and a B fallen back to are removed by neither. B,
// Options.Base's included, is sockat's alone: put nothing else in it.
//
// The SocketFile of ListenEx (and of ForBind.SocketFile) records the socket
// file the path leads to: where its last component is a symlink already
// there (bind follows it on macOS and NetBSD), the file at its end, which
// Remove removes, leaving the symlink.
//
// # How a long path is reached
//
// The socket's directory is looked up first (ENOENT, ENOTDIR). Then the
// routes are tried in this order, the first one that can be used taking the
// call (the names are the specification's); a route that binds or connects
// and fails returns that error, and no later route tries again:
//
//	| System              | Bind                                      |
//	|---------------------|-------------------------------------------|
//	| Linux, Android      | link-dir (via /proc, then B), thread-cwd  |
//	| Apple's systems     | link-dir, link-file, thread-cwd           |
//	| FreeBSD             | link-dir, at                              |
//	| NetBSD              | link-dir, link-file                       |
//	| OpenBSD, illumos    | link-dir                                  |
//	| other Unix          | link-dir, link-file (told at run time)    |
//
//	| System              | Connect                                                    |
//	|---------------------|------------------------------------------------------------|
//	| Linux with /proc    | proc-direct, thread-cwd-direct, link-file                  |
//	| Linux without /proc | relative, thread-cwd-direct, link-file                     |
//	| Apple's systems     | relative, widened (not made), thread-cwd-direct, link-file |
//	| FreeBSD             | at-direct, link-file                                       |
//	| NetBSD, OpenBSD,    | relative, link-file                                        |
//	| illumos, other Unix |                                                            |
//
// A bind leaves its spelling, so whatever later unlinks or looks up the
// address the kernel keeps reaches the path while the spelling lives.
// Link-dir binds by B/D/E/<name>, E a symlink to the socket's directory, B
// spelt /proc/<pid>/fd/<B's fd> (Linux, not with UsableCrossUID) or by its
// real path; unlinking that spelling removes the socket file. Link-file binds
// by B/D/E, E a symlink to the socket file, where bind(2) follows a symlink
// in the last component (macOS and NetBSD do; Linux, FreeBSD, OpenBSD and
// illumos do not and do not try it; elsewhere a failed bind with nothing at
// the path moves on); it reaches a file name of any length, and unlinking the
// spelling removes E, not the file. For a file name no spelling through B
// fits, thread-cwd binds by E/<name> from a throwaway goroutine locked to its
// own OS thread whose cwd (and no other's) is D, through an E removed right
// after (Linux: unshare(CLONE_FS), not under a seccomp filter such as
// Docker's default; macOS: __pthread_fchdir), and at hands the same spelling
// to bindat(2) with D's fd (FreeBSD). The goroutine ends without unlocking,
// so the thread is thrown away.
//
// A connect leaves no spelling with the peer, so the routes that need no
// writing and no thread come first: proc-direct passes
// /proc/self/fd/<dirfd>/<name>, relative the path relative to the cwd,
// at-direct the socket's directory fd to connectat(2). Thread-cwd-direct
// passes the file name from a throwaway thread whose cwd is the socket's
// directory. Link-file makes an E right in B, connects by its real path B/E,
// and removes E right after; a connect makes no D.
//
// sockat does not fork. The specification's widened sockaddr (Apple's
// systems) is not made: handed to the net package, a socket connected that
// way to a peer bound that way has a nil RemoteAddr. None of the routes
// changes the working directory of the process or of the calling thread, and
// all of them can be used from any goroutine. When no route can be used, the
// error is ENAMETOOLONG.
//
// # What differs from the net package
//
//   - A path containing a NUL byte is EINVAL, whatever its length (the net
//     package would cut the path short there and bind some other path). An
//     abstract socket name (a leading NUL, or '@', on Linux and Android) is
//     handed to the net package as is. Where the kernel has no abstract
//     sockets (illumos included), a leading NUL is EINVAL too.
//   - On illumos, a path that starts with '@' names the file "@..." in the
//     current directory. The net package would read the '@' as an abstract
//     name, which the kernel there does not have, so the path is handed to it
//     with "./" in front: the listener's Addr is "./@...", its Close leaves
//     the file (as the net package elsewhere leaves a path that starts with
//     '@'), and a path of 106 or 107 bytes, which no longer fits, is treated
//     as a long path.
//   - For a long path, the listener's Addr (and its accepted connections'
//     LocalAddr, and the peer's RemoteAddr) is the spelling the bind handed to
//     the kernel, not the path you passed. [Spelling] tells what it is good
//     for: do not unlink or stat it without looking.
//   - Closing [Listen]'s listener of a long path unlinks that spelling, as the
//     net package does: through link-dir's the socket file is removed,
//     through link-file's the symlink E is removed and the file stays, and
//     through a Dangling one (thread-cwd, at) nothing is. The listener of
//     [ListenEx] removes nothing on Close, short paths included: use
//     Bound.File's Remove, before closing the listener.
//   - A long path that does not end with a file name ("dir/", "dir/.",
//     "dir/..") is EINVAL, and nothing is created.
//   - The directory is looked up first (resolved to a real path for E); a
//     rename or a symlink repointed in between is not seen.
//   - Dial's relative route is exposed to another goroutine's os.Chdir: when
//     the cwd changed during the call, the result is dropped and an error is
//     returned.
//   - B, D and E stay outside the application's places. A live D may be
//     removed where systemd is older than v242 and in macOS's $TMPDIR; its
//     spellings then name nothing. A peer with a private /tmp (systemd's
//     PrivateTmp) cannot follow a spelling through /tmp:
//     Spelling.UsableWithPrivateTmp tells.
package sockat
