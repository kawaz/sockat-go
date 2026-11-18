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
- `discard` returns an error when it cannot check the file (no permission, for example) or the removal fails; nil when the file is gone or was replaced
- A replacement in the instant between the check and the removal cannot be told apart (POSIX has no removal by inode)
- The listener from `ListenEx` does not remove the file on Close (short paths included); only `discard` removes it

## Reaching a path at any cost (`atanycost`)

Only where you need the path reached even when `sockat.Listen` gives up with `ENAMETOOLONG` (the environments above, or long file names), use `atanycost.Listen` from a package of its own. Importing it is the opt-in.

```go
import "github.com/kawaz/sockat-go/atanycost"

l, res, err := atanycost.Listen(path) // l is a *net.UnixListener
if err != nil {
	return err
}
if res.Level == atanycost.SharedSymlink {
	log.Printf("%s: bound through a shared place (route %s, skipped %v)", path, res.Route, res.Skipped)
}
```

- It tries the routes of `sockat.Listen` and, only on `ENAMETOOLONG`, a symlink in a place (to the socket's directory, `<place>/sockat.XXXXXXXXXX/<file name>`, removed right after the bind). The places are `XDG_RUNTIME_DIR`, `TMPDIR` and `/tmp`: those only you can write to first, then shared ones (a sticky `/tmp` and the like)
- `res.Level` is what the route taken gave up ([the specification's loss levels](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md)). The values are not ordered (`Forked` and `SharedSymlink` each include `Respelled` but not each other). More values may be added; read an unknown one only as "not the standard library (something was lost, what is not known)"
- `res.Route` is the name of the route taken, `res.Skipped` the routes tried before it and found unusable (in the order tried) with why. Both are for diagnostics (logs and the like); their contents are not a promise

| `Level` | What is lost |
|---|---|
| `None` | nothing (the standard library) |
| `Respelled` | the address the kernel keeps is not the path; Close does not remove the file |
| `Forked` | does not occur: Go has no fork route (kept to match the other implementations) |
| `SharedSymlink` | `Respelled`, and other users can make the spelling again (removing the file by that spelling may remove a file in a directory of their choosing) |

## Why

The net package refuses a path that does not fit `sun_path` with `EINVAL`. The file system has no such limit, but a socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into it quickly. The limit is on the argument passed to `bind(2)` / `connect(2)`, so sockat hands the kernel a short spelling that names the same place.

## How a long path is reached

Handed to the net package as is:

| OS | Length |
|---|---|
| Linux / Android / illumos | up to 107 bytes |
| macOS / FreeBSD / NetBSD / OpenBSD | up to 103 bytes |

A longer path is bound / connected through a directory D of sockat's own, held by its fd, with a symlink R of a random name in it pointing to the socket's directory: the path passed is `R/<file name>` relative to D (the central route). R and D are removed afterwards.

Routes are called by [the specification's route names](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md).

| OS | Listen | Dial |
|---|---|---|
| Linux / Android | thread-cwd → proc | thread-cwd → proc → fallback routes |
| macOS | thread-cwd | thread-cwd → fallback routes |
| FreeBSD | at (bindat) | at (connectat) → fallback route |
| NetBSD / OpenBSD / illumos / other Unix | none (`ENAMETOOLONG`) | fallback routes only |
| Windows | not supported | not supported |

- **Where D goes**: `${TMPDIR:-/tmp}`, failing that `/tmp`, failing that the socket's directory (named `.sockat.XXXXXXXXXX`). A deep place or one others can write to works too (D is followed by its fd, not its name, and a directory swapped in before it is opened is refused by its owner and mode). When D can be made nowhere, Listen tries the form without D (thread-cwd-sockdir → proc-sockdir, at-sockdir on FreeBSD): R (`.sockat.XXXXXXXXXX`) is made in the socket's directory itself and the bind goes by `.sockat.XXXXXXXXXX/<file name>` relative to that directory's fd (file names up to 88 bytes on Linux, 84 on macOS / FreeBSD, about 72 for proc-sockdir)
- **thread-cwd**: a throwaway goroutine locked to its OS thread moves that thread's cwd (and no other's) into D and passes `R/<file name>`; the thread is then discarded (the goroutine ends without unlocking). Linux uses `unshare(CLONE_FS)` (not under a seccomp filter, such as Docker's default), macOS `__pthread_fchdir`
- **proc**: pass `/proc/self/fd/<D's fd>/R/<file name>`, without a thread
- **at**: pass D's fd and `R/<file name>` to `bindat(2)` / `connectat(2)`

The fallback routes of Dial (when the central route cannot be used):

| OS | Order tried |
|---|---|
| Linux / Android (`/proc` usable) | proc-direct → (per place) symlink or proc-held |
| Linux / Android (`/proc` not usable) | relative → symlink |
| macOS / NetBSD / OpenBSD / illumos / other | relative → symlink |
| FreeBSD | at-direct |

- **proc-direct**: open the socket's directory and pass `/proc/self/fd/<fd>/<file name>`
- **relative**: when the path relative to the cwd is short enough, pass that. Only the string is rewritten; the cwd is not changed
- **symlink**: create a short symlink to the socket file in `TMPDIR` or `/tmp` where no other user can replace it, pass it, and remove it right away
- **proc-held**: on Linux with `/proc`, in a place others can write to, create a directory of sockat's own, hold it by its fd, put the symlink in it and pass `/proc/self/fd/<fd>/s`
- **at-direct**: pass the socket's directory fd and the file name to `connectat(2)`

No route changes the process's cwd or the calling thread's, so any goroutine may call them.

### Routes that differ from the specification

| Route | sockat-go | Why |
|---|---|---|
| fork (bind's last resort, the fork without D, `connect_any`) | none | Go's runtime cannot fork bare. Hence no `connect_any` either |
| widened (macOS, Dial's fallback) | none | Handing sockat's own socket to the net package's types, a connected one gets a nil `RemoteAddr()` when the peer is bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

**Without fork, Listen gets `ENAMETOOLONG` for a long path here:**

- **NetBSD / OpenBSD / illumos**: there is no means for the routes through D (thread-cwd, proc, at). Every long-path Listen is `ENAMETOOLONG`
- Linux under a seccomp filter without `/proc`
- macOS where `__pthread_fchdir` cannot be used

To reach the path anyway, use `atanycost.Listen` ("Reaching a path at any cost" below).

### Where a path stops being reachable

When a path over the limit gets `ENAMETOOLONG` (with the directory there and D creatable; numbers are bytes).

| OS | Listen | Dial |
|---|---|---|
| Linux | file name > 96. Under a seccomp filter about 79 (proc), and every long path without `/proc` | file name > 96 and no fallback route (proc-direct, symlink or proc-held) can be used |
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
- **`Addr()` / `LocalAddr()` / `RemoteAddr()` do not report the path you passed.** They report the spelling handed to the kernel, `R/<file name>` (`/proc/self/fd/<fd>/R/<file name>` for proc). R is gone, so removing or stat-ing that spelling hits nothing, but it cannot be handed to another process to connect to. Keep the path you passed
- **A path that does not end with a file name** (`dir/`, `dir/.`, `dir/..`) is `EINVAL`, and nothing is created
- **The directory is looked up first**: the central route resolves the socket's directory to a real path when it creates R; Dial's fallback /proc, relative and connectat routes look the directory up and then pass the file name. A rename or a repointed symlink in between is not seen
- **Another goroutine's `os.Chdir`**: if the cwd changes during the relative route (Dial's fallback), the result is discarded and an error is returned
- **R swapped in the form without D**: in a socket directory others can write to without the sticky bit, R of the form without D (Listen) can be replaced between its creation and the bind, which may then bind in another directory (where the name exists already the bind fails with `EADDRINUSE`; nothing is overwritten)
- **Leftovers**: if the process dies before removing them, a `sockat.XXXXXXXXXX` (D, a symlink) stays in the place, and a `.sockat.XXXXXXXXXX` (D) in the socket's directory

## Documentation

- [The sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (routes, safety conditions, errors)
- [docs/decisions/](./docs/decisions/INDEX.md) — design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## License

MIT License, Yoshiaki Kawazu (@kawaz)
