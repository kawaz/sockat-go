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
// # How a long path is reached
//
// A longer path is tried on these routes, in this order, and bound /
// connected on the first route that can be used:
//
//	| System                          | Order tried (Listen and Dial alike) |
//	|---------------------------------|-------------------------------------|
//	| Linux, Android (/proc usable)   | /proc                               |
//	| Linux, Android (no /proc)       | relative path, symlink, thread cwd  |
//	| FreeBSD                         | bindat / connectat                  |
//	| macOS                           | relative path, symlink, thread cwd  |
//	| NetBSD, OpenBSD, illumos, other | relative path, symlink              |
//
// The routes:
//
//   - /proc: /proc/self/fd/<dirfd>/<name>, with the directory opened O_PATH.
//     Not usable without /proc, or for a file name longer than about 90 bytes.
//   - bindat / connectat: the directory fd and the file name.
//   - relative path: the path spelled relative to the working directory, when
//     the net package accepts that. Only the string is rewritten; the cwd is
//     never changed.
//   - symlink: a short symlink under $TMPDIR or failing that /tmp, removed
//     right after the call: for Listen to the socket's directory, for Dial to
//     the socket file. It is created only where no other user can replace it.
//     On Linux with /proc, a Dial also uses a place others can write to: it
//     creates a directory of its own (mode 0700) there, holds it by its fd,
//     puts the symlink in it and passes /proc/self/fd/<fd>/s.
//   - thread cwd: a goroutine locked to its own OS thread moves that thread's
//     working directory (and no other's) into the socket's directory and binds
//     / connects by the file name; the thread is then discarded. Linux uses
//     unshare(CLONE_FS), but not under a seccomp filter (such as Docker's
//     default profile); macOS uses __pthread_fchdir.
//
// None of these changes the working directory of the process, and all of
// them can be used from any goroutine. When no route reaches the path, the
// error is ENAMETOOLONG. The language-independent specification is at
// https://github.com/kawaz/sockat/blob/main/docs/SPEC.md.
//
// # What differs from the net package
//
//   - A path containing a NUL byte is EINVAL, whatever its length (the net
//     package would cut the path short there and bind some other path). An
//     abstract socket name (a leading NUL, or '@', on Linux) is handed to the
//     net package as is.
//   - The listener of a long path does not remove the socket file on Close.
//     Its address is the spelling handed to the kernel, so do not call
//     SetUnlinkOnClose(true) on it; remove the file by the path you passed
//     (os.Remove has no length limit).
//   - For a long path, Addr / LocalAddr / RemoteAddr report the spelling
//     handed to the kernel (a relative path, a /proc/self/fd path, a path
//     through a symlink that no longer exists, a bare file name), not the path
//     you passed. Keep the path you passed.
//   - A long path that does not end with a file name ("dir/", "dir/.",
//     "dir/..") is EINVAL, and nothing is created.
//   - The /proc, relative, bindat and thread cwd routes look the directory up
//     first and then bind / connect by the file name; a rename or a symlink
//     repointed in between is not seen.
//   - The relative route is exposed to another goroutine's os.Chdir: when the
//     cwd changed during the call, the result is discarded and an error is
//     returned.
//   - A temporary symlink or directory sockat.XXXXXXXXXX can be left behind
//     under $TMPDIR or /tmp if the process dies between creating and removing
//     it.
package sockat
