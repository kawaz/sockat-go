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
// (or net.Listen("unix", ...) / net.Dial("unix", ...)). They take a path of
// any length and return the net package's own *net.UnixListener and
// *net.UnixConn. A path the net package accepts (103 bytes on macOS and the
// BSDs, 107 on Linux and illumos) is handed to it unchanged, so it behaves
// exactly as with the net package. For a longer path the socket file is still
// created at (or looked up at) the path given.
//
//	l, err := sockat.Listen("/a/very/long/directory/.../server.sock")
//	c, err := sockat.Dial("/a/very/long/directory/.../server.sock")
//
// [ListenEx] and [DialEx] do the same and also report what the route taken
// gave up ([Result]: Losses, and the route and the routes skipped, for
// diagnostics); ListenEx also returns a discard function that removes the
// socket file later. Their second argument says whether they may take the
// last resort, a route that reaches the path where every other one is
// unusable, at a cost listed in Losses. Pass the constants:
//
//	l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
//	c, res, err := sockat.DialEx(path, sockat.NoLastResort)
//
// # How a long path is reached
//
// The routes are tried in this order, the first one that can be used taking
// the call (the names are the specification's):
//
//	| System              | Listen                    | Dial                                                        |
//	|---------------------|---------------------------|-------------------------------------------------------------|
//	| Linux with /proc    | thread-cwd, proc          | proc-direct, thread-cwd, proc, proc-held, symlink-file      |
//	| Linux without /proc | thread-cwd                | relative, thread-cwd, symlink-file                          |
//	| macOS               | symlink-file, thread-cwd  | relative, widened (not made here), thread-cwd, symlink-file |
//	| FreeBSD             | symlink-file, at (bindat) | at-direct, at (connectat)                                   |
//	| NetBSD, OpenBSD     | symlink-file only         | relative, symlink-file                                      |
//	| illumos, other      | symlink-file only         | relative, symlink-file                                      |
//
// A listen goes first through symlink-file, except on Linux: a symlink
// <place>/sockat.XXXXXXXXXX to the socket file, made in $TMPDIR or /tmp where
// no other user can replace it, bound by and removed right after. It reaches
// a path only where bind(2) follows a symlink in the last component (macOS
// does, Linux does not, the others are told at run time: a failed bind with
// nothing at the path moves on to the next route). Its spelling holds no file
// name, so on macOS a listen reaches a file name of any length.
//
// Then a listen goes through a directory D of sockat's own, held by its fd,
// with a symlink R of a random name in it pointing to the socket's directory:
// the kernel is given R/<name> relative to D. R and D are removed afterwards,
// so the spelling the kernel keeps (Addr and the like) names nothing wherever
// it is resolved later. D goes in $TMPDIR (or /tmp), failing that /tmp,
// failing that the socket's directory (as .sockat.XXXXXXXXXX); it is made at
// most once per call. Thread-cwd is a throwaway goroutine locked to its own OS
// thread that moves that thread's working directory (and no other's) into D;
// the goroutine ends without unlocking, so the thread is discarded. Linux uses
// unshare(CLONE_FS), but not under a seccomp filter (such as Docker's default
// profile); macOS uses __pthread_fchdir. Proc passes
// /proc/self/fd/<D>/R/<name>, at passes D's fd to bindat / connectat.
//
// A dial has its own order: a connect leaves no spelling behind (the
// connecting socket is not bound), so the routes that need no writing and no
// thread come first. Proc-direct passes /proc/self/fd/<dirfd>/<name>, relative
// the path relative to the cwd, at-direct the socket's directory fd to
// connectat. Only after them come the routes that make D and R (thread-cwd,
// proc, at) or D alone (proc-held, which passes /proc/self/fd/<D>/s, s a
// symlink in D to the socket file), and last symlink-file, as a listen makes
// it.
//
// Go cannot fork, so the specification's fork routes are not made: they are
// listed in the routes skipped. The specification's widened sockaddr (macOS)
// is not used either. A listen that no route reaches is ENAMETOOLONG; with
// UseLastResort, ListenEx then binds through symlink-dir, a symlink to the
// socket's directory in $XDG_RUNTIME_DIR, $TMPDIR or /tmp (places only the
// user can write to first; a shared one costs RecreatableSpelling). DialEx's
// last resort is a fork, so it reaches what Dial reaches.
//
// None of these changes the working directory of the process or of the
// calling thread, and all of them can be used from any goroutine. When no
// route reaches the path, the error is ENAMETOOLONG. The language-independent
// specification is at https://github.com/kawaz/sockat/blob/main/docs/SPEC.md.
//
// # What differs from the net package
//
//   - A path containing a NUL byte is EINVAL, whatever its length (the net
//     package would cut the path short there and bind some other path). An
//     abstract socket name (a leading NUL, or '@', on Linux) is handed to the
//     net package as is.
//   - The listener of a long path does not remove the socket file on Close.
//     Its address is the spelling handed to the kernel, so do not call
//     SetUnlinkOnClose(true) on it; use [ListenEx]'s discard.
//   - A listener of a long path reports in Addr (and its accepted connections
//     in LocalAddr) the spelling its bind handed to the kernel, not the path
//     you passed: R/<name>, /proc/self/fd/<fd>/R/<name>, or a symlink
//     <place>/sockat.XXXXXXXXXX that is gone. So does RemoteAddr on a
//     connection to such a listener, whichever way the dial went (the kernel
//     reports what the peer bound, not what the dial was given). It usually
//     names nothing, but other users can see it (Linux's /proc/net/unix) and,
//     where they can write (a shared place, a cwd others can write to), make
//     the name again: do not stat or unlink that spelling, nor hand it to
//     another process. Keep the path you passed.
//   - A long path that does not end with a file name ("dir/", "dir/.",
//     "dir/..") is EINVAL, and nothing is created.
//   - The directory is looked up first (resolved to a real path for R); a
//     rename or a symlink repointed in between is not seen.
//   - Dial's relative route is exposed to another goroutine's os.Chdir: when
//     the cwd changed during the call, the result is discarded and an error is
//     returned.
//   - If the process dies before removing them, sockat.XXXXXXXXXX (D, a
//     symlink) can be left in $TMPDIR or /tmp, and .sockat.XXXXXXXXXX (D) in
//     the socket's directory.
package sockat
