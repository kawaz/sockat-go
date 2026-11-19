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
if res.Has(atanycost.RecreatableSpelling) {
	log.Printf("%s: bound through a shared place (route %s, skipped %v)", path, res.Route, res.Skipped)
}
```

- It tries the routes of `sockat.Listen` and, only on `ENAMETOOLONG`, a symlink in a place (to the socket's directory, `<place>/sockat.XXXXXXXXXX/<file name>`, removed right after the bind). The places are `XDG_RUNTIME_DIR`, `TMPDIR` and `/tmp`: those only you can write to first, then shared ones (a sticky `/tmp` and the like)
- `res.Losses` names what the route taken gave up ([the specification's losses](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md)), empty when nothing was. The names are not ordered and none includes another. More names may be added; read an unknown one only as "something was lost"
- On FreeBSD, `at` can always be used and the symlink's spelling is always longer than at's, so `atanycost.Listen` reaches nothing beyond `sockat.Listen`
- `res.Route` is the name of the route taken, `res.Skipped` the routes tried before it and found unusable (in the order tried) with why. Both are for diagnostics (logs and the like); their contents are not a promise
- On failure the error is an `*atanycost.Error` with `Route` (the route that bound and failed; empty for `ENAMETOOLONG` when every route was unusable) and `Skipped`. It wraps the net package's `*net.OpError`, so `errors.Is(err, syscall.ENOENT)` and the like work

| `Loss` | What is lost | When |
|---|---|---|
| `Respelled` (`respelled`) | the address the kernel keeps is not the path; Close does not remove the file (remove it by the path) | bound through a route other than standard |
| `RecreatableSpelling` (`recreatable-spelling`) | the spelling the kernel keeps goes through a shared place; once another user has made it again, removing the file by that spelling may remove a file in a directory of their choosing (removing by the path is safe) | bound through a symlink in a shared place |
| `CwdRelative` (`cwd-relative`) | the address the kernel keeps is relative to the cwd; removing the file by it after the cwd changed reaches somewhere else (a risk of the standard library itself) | a relative path went through standard |
| `Forked` (`forked`) | a fork's side effects | never: Go has no fork route (kept to match the other implementations) |

How to avoid each loss follows. The code runs as tests (the Example functions in [example_test.go](./example_test.go) and [atanycost/example_test.go](./atanycost/example_test.go)).

### respelled

[The specification's respelled](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#respelled). For a long path `l.Addr()` reports a spelling that is not the path passed, and closing the listener does not remove the socket file. Keep the path passed and remove the file by it, or use `sockat.ListenEx`'s `discard`.

With `atanycost.Listen`, remove by the path passed ([`ExampleListen_respelled`](./atanycost/example_test.go)):

```go
l, res, err := atanycost.Listen(path)
if err != nil {
	log.Fatal(err)
}
defer os.Remove(path) // the path passed, not l.Addr().String()
defer l.Close()
fmt.Println("respelled:", res.Has(atanycost.Respelled))
```

With `sockat`, remove with `ListenEx`'s `discard` ([`ExampleListenEx`](./example_test.go)):

```go
l, discard, err := sockat.ListenEx(path)
if err != nil {
	log.Fatal(err)
}
defer discard() // removes the socket file at path (does not close l)
defer l.Close()
```

### cwd-relative

[The specification's cwd-relative](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#cwd-relative). A relative path goes to the standard library as it is, and the kernel keeps a spelling relative to the cwd; removing the file by that spelling or by the relative path after the cwd changed reaches somewhere else. Make the path absolute with `filepath.Abs` before the Listen, and remove the file by that absolute path ([`ExampleListen_cwdRelative`](./atanycost/example_test.go)):

```go
abs, err := filepath.Abs(path)
if err != nil {
	log.Fatal(err)
}
l, res, err := atanycost.Listen(abs)
if err != nil {
	log.Fatal(err)
}
defer os.Remove(abs)
defer l.Close()
fmt.Println("cwd-relative:", res.Has(atanycost.CwdRelative))
```

### forked

[The specification's forked](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#forked). Never attached: Go has no fork route.

### recreatable-spelling

[The specification's recreatable-spelling](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#recreatable-spelling). Through a symlink in a shared place (`/tmp` and the like), other users can make the spelling the kernel reports again, and removing the file by that spelling may then remove a file in a directory of their choosing. Remove the file by the path passed, not by `l.Addr()`'s spelling, and provide a place only the owner can write to as `XDG_RUNTIME_DIR`, passed by whatever starts the program (it is used before the shared places, and this name is not attached; [`ExampleListen_recreatableSpelling`](./atanycost/example_test.go)):

```go
l, res, err := atanycost.Listen(path)
if err != nil {
	log.Fatal(err)
}
defer os.Remove(path) // the path passed, not l.Addr().String()
defer l.Close()
if res.Has(atanycost.RecreatableSpelling) {
	log.Printf("%s: bound through a shared place", path)
}
```

## Patterns not to use

Ways of using sockat-go that remove an unrelated file or fail to remove the file ([the specification's Patterns not to use](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#patterns-not-to-use)).

| Pattern | Why not | Instead |
|---|---|---|
| `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` | for a long path it is not the path passed ([respelled](#respelled)). Depending on the cwd or a shared place, it may hit another file someone made again ([cwd-relative](#cwd-relative), [recreatable-spelling](#recreatable-spelling)) | `os.Remove(path)` with the path passed, or `ListenEx`'s `discard()` ([respelled](#respelled)) |
| listening on a relative path and calling `os.Remove` with it after an `os.Chdir` | hits a file in another place ([cwd-relative](#cwd-relative)) | make the path absolute with `filepath.Abs` before the Listen ([cwd-relative](#cwd-relative)). `discard()` |
| leaving it to `l.Close()`, or relying on `l.SetUnlinkOnClose(true)` to remove the file | for a long path Close does not remove it, unlike Go's standard library ([respelled](#respelled)) | `discard()`, or `os.Remove(path)` with the path passed ([respelled](#respelled)) |
| not calling `l.Close()` because `discard()` was called | `discard()` only removes the file | write `defer l.Close()` yourself (`ListenEx` in [respelled](#respelled)) |
| branching on `res.Route == "symlink"` or on the `Name` / `Reason` in `res.Skipped` | they are for diagnostics and promise nothing (routes are added and changed) | branch on `res.Losses`, such as `res.Has(atanycost.RecreatableSpelling)` |
| another goroutine reaping children with `waitpid(-1)` | does not occur in Go (no fork route) | none |

## Why

The net package refuses a path that does not fit `sun_path` with `EINVAL`. The file system has no such limit, but a socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into it quickly. The limit is on the argument passed to `bind(2)` / `connect(2)`, so sockat hands the kernel a short spelling that names the same place.

## How a long path is reached

Handed to the net package as is:

| OS | Length |
|---|---|
| Linux / Android / illumos | up to 107 bytes |
| macOS / FreeBSD / NetBSD / OpenBSD | up to 103 bytes |

A longer path is bound / connected through a directory D of sockat's own, held by its fd, with a symlink R of a random name in it pointing to the socket's directory: the path passed is `R/<file name>` relative to D (the routes through D). R and D are removed afterwards. Dial first tries the routes that need no writing and no thread (proc-direct, relative, at-direct), and the routes that make D or start a thread after them ([the specification's connect order](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md)).

Routes are called by [the specification's route names](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md). They are tried from left to right, and the first one that can be used binds / connects.

| OS | Listen | Dial |
|---|---|---|
| Linux / Android (`/proc` usable) | thread-cwd → proc | proc-direct → thread-cwd → proc → proc-held → symlink |
| Linux / Android (`/proc` not usable) | thread-cwd | relative → thread-cwd → symlink |
| macOS | thread-cwd | relative → (no widened) → thread-cwd → symlink |
| FreeBSD | at (bindat) | at-direct → at (connectat) |
| NetBSD / OpenBSD / illumos / other Unix | none (`ENAMETOOLONG`) | relative → symlink |
| Windows | not supported | not supported |

- **Where D goes**: `${TMPDIR:-/tmp}`, failing that `/tmp`, failing that the socket's directory (named `.sockat.XXXXXXXXXX`). A deep place or one others can write to works too (D is followed by its fd, not its name, and a directory swapped in before it is opened is refused by its owner and mode). When D can be made nowhere, Listen tries the form without D (thread-cwd-sockdir → proc-sockdir, at-sockdir on FreeBSD): R (`.sockat.XXXXXXXXXX`) is made in the socket's directory itself and the bind goes by `.sockat.XXXXXXXXXX/<file name>` relative to that directory's fd (file names up to 88 bytes on Linux, 84 on macOS / FreeBSD, about 72 for proc-sockdir)
- **thread-cwd**: a throwaway goroutine locked to its OS thread moves that thread's cwd (and no other's) into D and passes `R/<file name>`; the thread is then discarded (the goroutine ends without unlocking). Linux uses `unshare(CLONE_FS)` (not under a seccomp filter, such as Docker's default), macOS `__pthread_fchdir`
- **proc**: pass `/proc/self/fd/<D's fd>/R/<file name>`, without a thread
- **at**: pass D's fd and `R/<file name>` to `bindat(2)` / `connectat(2)`
- D is made at most once per call and shared by the routes that use it (thread-cwd, proc, proc-held, at). When it can be made nowhere, every route that uses it is passed over (D is not tried again)

The routes of Dial alone (the connecting socket is not bound, so its spelling stays with nobody, and no R is needed):

- **proc-direct**: open the socket's directory and pass `/proc/self/fd/<fd>/<file name>`
- **relative**: when the path relative to the cwd is short enough, pass that. Only the string is rewritten; the cwd is not changed
- **proc-held**: put a symlink `s` to the socket file in D and pass `/proc/self/fd/<D's fd>/s`. The file name is not in the spelling, so any file name is reached
- **symlink**: create a short symlink to the socket file in `TMPDIR` or `/tmp` where no other user can replace it, pass it, and remove it right away
- **at-direct**: pass the socket's directory fd and the file name to `connectat(2)`

No route changes the process's cwd or the calling thread's, so any goroutine may call them.

### Routes that differ from the specification

| Route | sockat-go | Why |
|---|---|---|
| fork (bind's last resort, the fork without D, `connect_any`) | none | Go's runtime cannot fork bare. Hence no `connect_any` either |
| widened (macOS, second in Dial's order) | none (passed over for thread-cwd) | Handing sockat's own socket to the net package's types, a connected one gets a nil `RemoteAddr()` when the peer is bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

**Without fork, Listen gets `ENAMETOOLONG` for a long path here:**

- **NetBSD / OpenBSD / illumos**: there is no means for the routes through D (thread-cwd, proc, at). Every long-path Listen is `ENAMETOOLONG`
- Linux under a seccomp filter without `/proc`
- macOS where `__pthread_fchdir` cannot be used

To reach the path anyway, use `atanycost.Listen` ("Reaching a path at any cost" below).

### Where a path stops being reachable

When a path over the limit gets `ENAMETOOLONG` (with the directory there and D creatable; numbers are bytes).

| OS | Listen | Dial |
|---|---|---|
| Linux (with `/proc`) | file name > 96. Under a seccomp filter about 79 (proc) | reached (proc-held) |
| Linux (without `/proc`) | file name > 96. Under a seccomp filter every long path | thread-cwd cannot be used (file name > 96, or no thread cwd), the relative path > 107 and no symlink can be made |
| macOS | file name > 92 | thread-cwd cannot be used (file name > 92, or no thread cwd), the relative path > 103 and no symlink can be made |
| FreeBSD | file name > 92 | file name > 103 |
| NetBSD / OpenBSD | every long path | relative path > 103 and no symlink can be made |
| illumos | every long path | relative path > 107 and no symlink can be made |

On Linux with `/proc` where D can be made, and outside FreeBSD in an ordinary environment where a symlink can be made in `/tmp`, Dial reaches a path whatever the directory depth and the file name length. When D can be made nowhere, Dial reaches what the routes without D (proc-direct, relative, at-direct, symlink) reach.

A path longer than `PATH_MAX` (1024 bytes on macOS, 4096 on Linux) cannot be opened by any other system call either; it is outside what sockat handles and gets `ENAMETOOLONG`.

## What differs from the net package

- **A path containing a NUL is `EINVAL`, whatever its length.** The net package silently cuts it short there and binds some other path. An abstract socket name on Linux (a leading NUL or `@`) is handed to the net package as is

Only for a long path:

- **The listener does not remove the socket file on Close.** The net package (and a short path) does. The address a long-path listener holds is the spelling handed to the kernel, so do not call `SetUnlinkOnClose(true)` on it; to remove the file, use `ListenEx`'s `discard`
- **`Addr()` / `LocalAddr()` / `RemoteAddr()` do not report the path you passed.** They report the spelling handed to the kernel, `R/<file name>` (`/proc/self/fd/<fd>/R/<file name>` for proc). R is gone, so removing or stat-ing that spelling hits nothing, but it cannot be handed to another process to connect to. Keep the path you passed
- **A path that does not end with a file name** (`dir/`, `dir/.`, `dir/..`) is `EINVAL`, and nothing is created
- **The directory is looked up first**: the routes through D resolve the socket's directory to a real path when they create R; proc-direct, relative and at-direct look the directory up and then pass the file name. A rename or a repointed symlink in between is not seen
- **Another goroutine's `os.Chdir`**: if the cwd changes during relative (Dial), the result is discarded and an error is returned
- **R swapped in the form without D**: in a socket directory others can write to without the sticky bit, R of the form without D (Listen) can be replaced between its creation and the bind, which may then bind in another directory (where the name exists already the bind fails with `EADDRINUSE`; nothing is overwritten)
- **Leftovers**: if the process dies before removing them, a `sockat.XXXXXXXXXX` (D, a symlink) stays in the place, and a `.sockat.XXXXXXXXXX` (D) in the socket's directory

## Documentation

- [The sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (routes, safety conditions, errors)
- [docs/decisions/](./docs/decisions/INDEX.md) — design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## License

MIT License, Yoshiaki Kawazu (@kawaz)
