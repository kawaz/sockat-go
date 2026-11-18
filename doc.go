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
// To remove the socket file later, use [ListenEx], which also returns a
// discard function.
//
// # How a long path is reached
//
// A longer path goes through a directory D of sockat's own, held by its fd,
// with a symlink R of a random name in it pointing to the socket's directory:
// the kernel is given R/<name> relative to D. R and D are removed afterwards,
// so the spelling the kernel keeps (Addr and the like) names nothing wherever
// it is resolved later. D goes in $TMPDIR (or /tmp), failing that /tmp,
// failing that the socket's directory (as .sockat.XXXXXXXXXX). The means of
// making the call relative to D:
//
//	| System          | Listen                     | Dial                                       |
//	|-----------------|----------------------------|--------------------------------------------|
//	| Linux, Android  | thread cwd, /proc variant  | thread cwd, /proc variant, fallback routes |
//	| macOS           | thread cwd                 | thread cwd, fallback routes                |
//	| FreeBSD         | bindat                     | connectat, fallback route                  |
//	| NetBSD, OpenBSD | none (ENAMETOOLONG)        | fallback routes                            |
//	| illumos, other  | none (ENAMETOOLONG)        | fallback routes                            |
//
// Thread cwd is a throwaway goroutine locked to its own OS thread that moves
// that thread's working directory (and no other's) into D; the goroutine ends
// without unlocking, so the thread is discarded. Linux uses unshare(CLONE_FS),
// but not under a seccomp filter (such as Docker's default profile); macOS
// uses __pthread_fchdir. The /proc variant passes /proc/self/fd/<D>/R/<name>.
//
// The fallback routes of Dial are, on Linux with /proc, /proc/self/fd/<dirfd>/<name>
// and then a symlink to the socket file (in a safe place, or held by the fd
// of a directory of sockat's own); elsewhere the path relative to the cwd and
// then the symlink; on FreeBSD connectat on the socket's directory. Go cannot
// fork, so there is no fork route: a Listen that the central route cannot
// make is ENAMETOOLONG.
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
//   - For a long path, Addr / LocalAddr / RemoteAddr report the spelling
//     handed to the kernel (R/<name>, or /proc/self/fd/<fd>/R/<name>), not the
//     path you passed. R is gone, so nothing is hit through it, but it cannot
//     be handed to another process either. Keep the path you passed.
//   - A long path that does not end with a file name ("dir/", "dir/.",
//     "dir/..") is EINVAL, and nothing is created.
//   - The directory is looked up first (resolved to a real path for R); a
//     rename or a symlink repointed in between is not seen.
//   - The relative route (Dial's fallback) is exposed to another goroutine's
//     os.Chdir: when the cwd changed during the call, the result is discarded
//     and an error is returned.
//   - If the process dies before removing them, sockat.XXXXXXXXXX (D, a
//     symlink) can be left in $TMPDIR or /tmp, and .sockat.XXXXXXXXXX (D) in
//     the socket's directory.
package sockat
