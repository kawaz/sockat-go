# sockat-go

> English | [日本語](./README-ja.md)

A Go library that binds and connects Unix domain sockets at paths longer than `sun_path` allows (104 bytes on macOS and the BSDs, 108 on Linux, the NUL included).

The language-independent behaviour is specified in [the sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md); sockat-go is its Go implementation.

```go
l, err := sockat.Listen("/a/very/deep/directory/.../server.sock") // *net.UnixListener
c, err := sockat.Dial("/a/very/deep/directory/.../server.sock")   // *net.UnixConn
```

## Usage

Use `sockat.Listen` / `sockat.Dial` where you would use `net.ListenUnix` / `net.DialUnix` (or `net.Listen("unix", ...)` / `net.Dial("unix", ...)`).

```sh
go get github.com/kawaz/sockat-go
```

- They return the net package's own `*net.UnixListener` / `*net.UnixConn` (assignable to `net.Listener` / `net.Conn`)
- A path the net package accepts is handed to it unchanged, so it behaves exactly as with the net package
- For a longer path, the socket file is still created at the path given
- Errors are `*net.OpError`s, as from the net package; `errors.Is(err, syscall.ENOENT)` and the like work
- The only dependency is `golang.org/x/sys`. Go: the two supported releases (1.26 and later)

To remove the socket file later, use `sockat.ListenEx`:

```go
l, discard, err := sockat.ListenEx(path) // l is a *net.UnixListener
if err != nil {
	return err
}
defer discard() // removes the socket file (does not close l)
defer l.Close()
```

- `discard` records the file right after the bind and removes it only while the path still names that same file (`(st_dev, st_ino)`); a socket that replaced it in the meantime is left alone. When the file cannot be recorded, `ListenEx` closes the listener and returns an error (the file is left)
- A replacement in the instant between the check and the removal cannot be told apart (POSIX has no removal by inode)
- The listener from `ListenEx` does not remove the file on Close (short paths included); only `discard` removes it

## Why

The net package refuses a path that does not fit `sun_path` with `EINVAL`. The file system has no such limit, but a socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into it quickly. The limit is on the argument passed to `bind(2)` / `connect(2)`, so sockat hands the kernel a short spelling that names the same place.

## How a long path is reached

Handed to the net package as is:

| OS | Length |
|---|---|
| Linux / Android / illumos | up to 107 bytes |
| macOS / FreeBSD / NetBSD / OpenBSD | up to 103 bytes |

A longer path is bound / connected through a directory D of sockat's own, held by its fd, with a symlink R of a random name in it pointing to the socket's directory: the path passed is `R/<file name>` relative to D (the central route). R and D are removed afterwards.

| OS | Listen | Dial |
|---|---|---|
| Linux / Android | thread cwd → /proc variant | thread cwd → /proc variant → fallback routes |
| macOS | thread cwd | thread cwd → fallback routes |
| FreeBSD | bindat | connectat → fallback route |
| NetBSD / OpenBSD / illumos / other Unix | none (`ENAMETOOLONG`) | fallback routes only |
| Windows | not supported | not supported |

- **Where D goes**: `${TMPDIR:-/tmp}`, failing that `/tmp`, failing that the socket's directory (named `.sockat.XXXXXXXXXX`). A deep place or one others can write to works too (D is followed by its fd, not its name, and a directory swapped in before it is opened is refused by its owner and mode)
- **thread cwd**: a throwaway goroutine locked to its OS thread moves that thread's cwd (and no other's) into D and passes `R/<file name>`; the thread is then discarded (the goroutine ends without unlocking). Linux uses `unshare(CLONE_FS)` (not under a seccomp filter, such as Docker's default), macOS `__pthread_fchdir`
- **/proc variant**: pass `/proc/self/fd/<D's fd>/R/<file name>`, without a thread
- **bindat / connectat**: pass D's fd and `R/<file name>`

The fallback routes of Dial (when the central route cannot be used):

| OS | Order tried |
|---|---|
| Linux / Android (`/proc` usable) | /proc → (per place) symlink or the fd-held form |
| Linux / Android (`/proc` not usable) | relative path → symlink |
| macOS / NetBSD / OpenBSD / illumos / other | relative path → symlink |
| FreeBSD | connectat (the socket's directory fd and the file name) |

- **/proc**: open the socket's directory and pass `/proc/self/fd/<fd>/<file name>`
- **relative path**: when the path relative to the cwd is short enough, pass that. Only the string is rewritten; the cwd is not changed
- **symlink**: create a short symlink to the socket file in `TMPDIR` or `/tmp` where no other user can replace it, pass it, and remove it right away. On Linux with `/proc`, a place others can write to is used too: a directory of sockat's own is created there and held by its fd, the symlink goes in it and `/proc/self/fd/<fd>/s` is passed (the fd-held form)

No route changes the process's cwd or the calling thread's, so any goroutine may call them.

### Routes that differ from the specification

| Route | sockat-go | Why |
|---|---|---|
| fork (bind's last resort, the fork without D, `connect_any`) | none | Go's runtime cannot fork bare. Hence no `connect_any` either |
| widened sockaddr (macOS, Dial's fallback) | none | Handing sockat's own socket to the net package's types, a connected one gets a nil `RemoteAddr()` when the peer is bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

**Without fork, Listen gets `ENAMETOOLONG` for a long path here:**

- **NetBSD / OpenBSD / illumos**: there is no means for the central route (thread cwd, /proc, bindat). Every long-path Listen is `ENAMETOOLONG`
- Linux under a seccomp filter without `/proc`
- macOS where `__pthread_fchdir` cannot be used

### Where a path stops being reachable

When a path over the limit gets `ENAMETOOLONG` (with the directory there and D creatable; numbers are bytes).

| OS | Listen | Dial |
|---|---|---|
| Linux | file name > 96. Under a seccomp filter about 79 (/proc variant), and every long path without `/proc` | file name > 96 and no fallback route (the /proc spelling, a symlink or the fd-held form) can be used |
| macOS | file name > 92 | file name > 92, the relative path > 103 and no symlink can be made |
| FreeBSD | file name > 92 | file name > 103 |
| NetBSD / OpenBSD | every long path | relative path > 103 and no symlink can be made |
| illumos | every long path | relative path > 107 and no symlink can be made |

In an ordinary environment where a symlink can be made in `/tmp`, Dial reaches a path whatever the directory depth and the file name length.

A path longer than `PATH_MAX` (1024 bytes on macOS, 4096 on Linux) cannot be opened by any other system call either; it is outside what sockat handles and gets `ENAMETOOLONG`.

## What differs from the net package

- **A path containing a NUL is `EINVAL`, whatever its length.** The net package silently cuts it short there and binds some other path. An abstract socket name on Linux (a leading NUL or `@`) is handed to the net package as is

Only for a long path:

- **The listener does not remove the socket file on Close.** The net package (and a short path) does. The address a long-path listener holds is the spelling handed to the kernel, so do not call `SetUnlinkOnClose(true)` on it; to remove the file, use `ListenEx`'s `discard`
- **`Addr()` / `LocalAddr()` / `RemoteAddr()` do not report the path you passed.** They report the spelling handed to the kernel, `R/<file name>` (`/proc/self/fd/<fd>/R/<file name>` for the /proc variant). R is gone, so removing or stat-ing that spelling hits nothing, but it cannot be handed to another process to connect to. Keep the path you passed
- **A path that does not end with a file name** (`dir/`, `dir/.`, `dir/..`) is `EINVAL`, and nothing is created
- **The directory is looked up first**: the central route resolves the socket's directory to a real path when it creates R; Dial's fallback /proc, relative and connectat routes look the directory up and then pass the file name. A rename or a repointed symlink in between is not seen
- **Another goroutine's `os.Chdir`**: if the cwd changes during the relative route (Dial's fallback), the result is discarded and an error is returned
- **Leftovers**: if the process dies before removing them, a `sockat.XXXXXXXXXX` (D, a symlink) stays in the place, and a `.sockat.XXXXXXXXXX` (D) in the socket's directory

## Documentation

- [The sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (routes, safety conditions, errors)
- [docs/decisions/](./docs/decisions/INDEX.md) — design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## License

MIT License, Yoshiaki Kawazu (@kawaz)
