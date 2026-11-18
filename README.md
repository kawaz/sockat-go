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

## Why

The net package refuses a path that does not fit `sun_path` with `EINVAL`. The file system has no such limit, but a socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into it quickly. The limit is on the argument passed to `bind(2)` / `connect(2)`, so sockat hands the kernel a short spelling that names the same place.

## How a long path is reached

Handed to the net package as is:

| OS | Length |
|---|---|
| Linux / Android / illumos | up to 107 bytes |
| macOS / FreeBSD / NetBSD / OpenBSD | up to 103 bytes |

A longer path is tried on these routes, in this order per OS, and bound / connected on the first route that can be used.

| OS | Order tried (Listen and Dial alike) |
|---|---|
| Linux / Android (`/proc` usable) | /proc |
| Linux / Android (`/proc` not usable) | relative path → symlink → thread cwd |
| FreeBSD | bindat / connectat |
| macOS | relative path → symlink → thread cwd |
| NetBSD / OpenBSD / illumos / other Unix | relative path → symlink |
| Windows | not supported |

- **/proc**: open the directory `O_PATH` and pass `/proc/self/fd/<fd>/<file name>`. Not usable without `/proc`, or for a file name that does not fit it (over about 90 bytes)
- **bindat / connectat**: pass the file name relative to the directory fd
- **relative path**: when the path relative to the cwd is short enough for the net package, pass that. Only the string is rewritten; the cwd is not changed
- **symlink**: create a short symlink under `TMPDIR` or `/tmp` and pass the path through it: to the socket's directory for Listen, to the socket file for Dial. Created only where no other user can replace it, and removed right away. On Linux with `/proc`, Dial also uses a place others can write to: it creates a directory of its own (mode 0700) there, holds it by its fd, puts the symlink in it and passes `/proc/self/fd/<fd>/s`
- **thread cwd**: a goroutine locked to its OS thread moves that thread's cwd (and no other's) into the directory and passes the file name; the thread is then discarded. Linux uses `unshare(CLONE_FS)` (not under a seccomp filter, such as Docker's default), macOS `__pthread_fchdir`

None of the routes changes the process's cwd, so any goroutine may call them.

### Routes that differ from the specification

| Route | sockat-go | Why |
|---|---|---|
| fork | none | Go's runtime cannot fork bare. Hence no `connect_any` either |
| widened sockaddr (macOS) | none | Handing sockat's own socket to the net package's types, `syscall` refuses an address longer than `sockaddr_un`: `net.FileListener` fails with `EINVAL` for a bound one, and a connected one gets a nil `RemoteAddr()` when the peer is bound in the widened form |
| thread cwd | Linux / macOS only | NetBSD / OpenBSD / illumos have no per-thread working directory |

### Where a path stops being reachable

When a path over the limit gets `ENAMETOOLONG` (numbers are bytes).

| OS | Listen | Dial |
|---|---|---|
| Linux | file name > 107. Under a seccomp filter: the file name does not fit the `/proc` spelling, the relative path > 107, and the file name > 84 or no symlink can be made | file name > 107 and no symlink can be made. Under a seccomp filter: neither the `/proc` spelling nor the relative path fits, and no symlink can be made |
| FreeBSD | file name > 103 | file name > 103 |
| macOS | file name > 103 | file name > 103 and no symlink can be made |
| NetBSD / OpenBSD | relative path > 103, and file name > 80 or no symlink can be made | relative path > 103 and no symlink can be made |
| illumos | relative path > 107, and file name > 84 or no symlink can be made | relative path > 107 and no symlink can be made |

In an ordinary environment where a symlink can be made in `/tmp`, Dial reaches a path whatever the directory depth and the file name length.

## What differs from the net package

- **A path containing a NUL is `EINVAL`, whatever its length.** The net package silently cuts it short there and binds some other path. An abstract socket name on Linux (a leading NUL or `@`) is handed to the net package as is

Only for a long path:

- **The listener does not remove the socket file on Close.** The net package (and a short path) does. The address a long-path listener holds is the spelling handed to the kernel, so do not call `SetUnlinkOnClose(true)` on it; remove the file by the path you passed with `os.Remove` (unlink has no length limit)
- **`Addr()` / `LocalAddr()` / `RemoteAddr()` do not report the path you passed.** They report the spelling handed to the kernel (a relative path, `/proc/self/fd/...`, a path through a symlink that no longer exists, a bare file name). Keep the path you passed
- **A path that does not end with a file name** (`dir/`, `dir/.`, `dir/..`) is `EINVAL`, and nothing is created
- **The directory is looked up first**: the /proc, relative, bindat and thread cwd routes look the directory up and then pass the file name. A rename or a repointed symlink in between is not seen
- **Another goroutine's `os.Chdir`**: if the cwd changes during the relative route, the result is discarded and an error is returned
- **A symlink or directory can be left behind**: if the process dies between creating and removing it, a `sockat.XXXXXXXXXX` stays in the place it was made

## Documentation

- [The sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (routes, safety conditions, errors)
- [docs/decisions/](./docs/decisions/INDEX.md) — design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## License

MIT License, Yoshiaki Kawazu (@kawaz)
