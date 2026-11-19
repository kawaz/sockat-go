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
l, discard, res, err := sockat.ListenEx(path, sockat.NoLastResort) // l is a *net.UnixListener
if err != nil {
	return err
}
defer l.Close()
defer discard() // removes the socket file (does not close l)
```

- `discard` records the file right after the bind and removes it only while the path still names that same file (`(st_dev, st_ino)`). When the file cannot be recorded, `ListenEx` closes the listener and returns an error (the file is left)
- **Call `discard` before closing the listener** (with the `defer`s above, `discard` runs first). A replacement is told apart only while the listener is open: the kernel holds the socket file meanwhile, so whatever another process removes and makes again there has another `(st_dev, st_ino)`, and `discard` leaves it. After the close, a file removed and made again gets the same numbers on a file system that reuses them at once (Linux's ext4, the BSDs' UFS / FFS), and `discard` may remove it (as `os.Remove` of the path would)
- `discard` returns an error when it cannot check the file (no permission, for example) or the removal fails; nil when the file is gone or was replaced
- A replacement in the instant between the check and the removal cannot be told apart (POSIX has no removal by inode)
- The listener from `ListenEx` does not remove the file on Close (short paths included); only `discard` removes it

## What was lost and which route, and the last resort (`ListenEx` / `DialEx`)

`sockat.ListenEx` and `sockat.DialEx` try the routes of `Listen` / `Dial` in the same order, and return a `sockat.Result` beside the net package's type. Their second argument says whether they may take the last resort; write it with the constants, not a bare `true` / `false`.

```go
l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("%s: route %q, skipped %v", path, re.Route, re.Skipped)
	}
	return err
}
defer l.Close()
defer discard()
if res.Has(sockat.RecreatableSpelling) {
	log.Printf("%s: bound through a shared place (route %s, skipped %v)", path, res.Route, res.Skipped)
}

c, res, err := sockat.DialEx(path, sockat.NoLastResort) // c is a *net.UnixConn
```

- `res.Losses` is the names of what the route taken gave up ([the specification's What is lost](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-is-lost)); empty when nothing was. The names are not ordered and none includes another. More names may be added: read an unknown one only as "something was lost". Branch on these
- `res.Route` is the name of the route taken, `res.Skipped` the routes tried before it and found unusable (in the order tried), with why. Both are for diagnostics (logs and the like) and promise nothing
- A failure is a `*sockat.RouteError` with `Route` (the route that bound / connected and failed; empty for `ENAMETOOLONG` when every route was unusable, and for a failure before any route, such as a missing directory) and `Skipped`. It wraps the `*net.OpError` of `Listen` / `Dial`, so `errors.Is(err, syscall.ENOENT)` and the like work
- The last resort is tried only where every default route is unusable (`ENAMETOOLONG`)
  - `ListenEx`: symlink-dir, a symlink in a place to the socket's directory (`<place>/sockat.XXXXXXXXXX/<file name>`, removed right after the bind). The places are `XDG_RUNTIME_DIR`, `TMPDIR` and `/tmp`: those only you can write to first, then shared ones (a sticky `/tmp` and the like)
  - `DialEx`: fork-cwd-direct (a forked child connects). Go has no fork, so it only goes into `res.Skipped`, and `DialEx` reaches what `Dial` reaches
- On FreeBSD at can always be used and symlink-dir's spelling is always longer than at's, so the last resort never reaches further than `Listen`

| `Loss` | What is lost | When |
|---|---|---|
| `Respelled` (`respelled`) | the address the kernel keeps is not the path. Close does not remove the file (remove it with `discard` or by the path) | bound through a route other than the standard library |
| `CwdRelative` (`cwd-relative`) | the spelling the kernel keeps is relative to the cwd. Removing by it after changing the cwd hits another place (a risk of the standard library itself) | a relative path through standard |
| `Forked` (`forked`) | a fork's side effects | never in Go, which has no fork route (here so the names match the other implementations) |
| `RecreatableSpelling` (`recreatable-spelling`) | the spelling the kernel keeps goes through a shared place. Once another user has made it again, removing by it may remove a file in a directory of their choosing (removing with `discard` or by the path is safe) | `ListenEx`'s last resort went through symlink-dir in a shared place |

`DialEx`'s `Losses` is always empty (a connect leaves no spelling behind with the peer).

How to avoid each loss follows. The code runs as tests (the Example functions in [example_test.go](./example_test.go)).

### respelled

[The specification's respelled](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#respelled). For a long path `l.Addr()` reports a spelling that is not the path passed, and closing the listener does not remove the socket file. Remove it with `ListenEx`'s `discard` (or keep the path passed and call `os.Remove(path)`; [`ExampleListenEx_respelled`](./example_test.go)):

```go
l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer discard() // not os.Remove(l.Addr().String())
fmt.Println("respelled:", res.Has(sockat.Respelled))
```

### cwd-relative

[The specification's cwd-relative](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#cwd-relative). A relative path goes to the net package as is, and the kernel keeps a spelling relative to the cwd. Removing by that spelling or by the relative path after the cwd has changed hits another place. Make the path absolute with `filepath.Abs` before the Listen, and remove the file with `discard` (which records even a relative path against the cwd of the bind) or by the absolute path ([`ExampleListenEx_cwdRelative`](./example_test.go)):

```go
abs, err := filepath.Abs(path)
if err != nil {
	log.Fatal(err)
}
l, discard, res, err := sockat.ListenEx(abs, sockat.UseLastResort)
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer discard()
fmt.Println("cwd-relative:", res.Has(sockat.CwdRelative))
```

### forked

[The specification's forked](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#forked). Go has no fork route, so it never occurs.

### recreatable-spelling

[The specification's recreatable-spelling](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#recreatable-spelling). When `ListenEx`'s last resort goes through symlink-dir in a shared place (`/tmp` and the like), other users can make the spelling the kernel reports again, and removing the file by it may remove a file in a directory of their choosing. Remove it with `discard`, not by `l.Addr()`, and provide a place only the owner can write to as `XDG_RUNTIME_DIR` (passed by whatever starts the program; it is tried before the shared places, and this name does not occur; [`ExampleListenEx_recreatableSpelling`](./example_test.go)):

```go
l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer discard() // not os.Remove(l.Addr().String())
if res.Has(sockat.RecreatableSpelling) {
	log.Printf("%s: bound through a shared place", path)
}
```

## Patterns not to use

Ways of using sockat-go that remove an unrelated file or fail to remove the file ([the specification's Patterns not to use](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#patterns-not-to-use)).

| Pattern | Why not | Instead |
|---|---|---|
| `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` | for a long path it is not the path passed ([respelled](#respelled)). Depending on the cwd or a shared place, it may hit another file someone made again ([cwd-relative](#cwd-relative), [recreatable-spelling](#recreatable-spelling)) | `ListenEx`'s `discard()`, or `os.Remove(path)` with the path passed ([respelled](#respelled)) |
| listening on a relative path and calling `os.Remove` with it after an `os.Chdir` | hits a file in another place ([cwd-relative](#cwd-relative)) | make the path absolute with `filepath.Abs` before the Listen ([cwd-relative](#cwd-relative)). `discard()` |
| leaving it to `l.Close()`, or relying on `l.SetUnlinkOnClose(true)` to remove the file | for a long path Close does not remove it, unlike Go's standard library ([respelled](#respelled)) | `discard()`, or `os.Remove(path)` with the path passed ([respelled](#respelled)) |
| not calling `l.Close()` because `discard()` was called | `discard()` only removes the file | write `defer l.Close()` yourself ([respelled](#respelled)) |
| branching on `res.Route == "symlink-dir"` or on the `Name` / `Reason` in `res.Skipped` | they are for diagnostics and promise nothing (routes are added and changed) | branch on `res.Losses`, such as `res.Has(sockat.RecreatableSpelling)` |
| another goroutine reaping children with `waitpid(-1)` | does not occur in Go (no fork route) | none |

## Why

The net package refuses a path that does not fit `sun_path` with `EINVAL`. The file system has no such limit, but a socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into it quickly. The limit is on the argument passed to `bind(2)` / `connect(2)`, so sockat hands the kernel a short spelling that names the same place.

## How a long path is reached

Handed to the net package as is:

| OS | Length |
|---|---|
| Linux / Android / illumos | up to 107 bytes |
| macOS / FreeBSD / NetBSD / OpenBSD | up to 103 bytes |

A longer path goes through the routes that make a short spelling ([the specification's Route names](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#route-names)), tried from left to right; the first one that can be used binds / connects. Listen and Dial have orders of their own.

| OS | Listen | Dial |
|---|---|---|
| Linux / Android (`/proc` usable) | thread-cwd → proc | proc-direct → thread-cwd → proc → proc-held → symlink-file |
| Linux / Android (`/proc` not usable) | thread-cwd | relative → thread-cwd → symlink-file |
| macOS | symlink-file → thread-cwd | relative → (no widened) → thread-cwd → symlink-file |
| FreeBSD | at (bindat) | at-direct → at (connectat) |
| NetBSD / other Unix | symlink-file only | relative → symlink-file |
| OpenBSD / illumos | none | relative → symlink-file |
| Windows | not supported | not supported |

Listen:

- **symlink-file** (where the bind follows a symlink in the last component): create a short symlink `sockat.XXXXXXXXXX` to the socket file at the path passed, in `TMPDIR` or `/tmp` where no other user can replace it, bind by it, and remove it right away. The socket is made at the path, and since the file name is not in the spelling, a file name of any length is reached. macOS and NetBSD were measured to follow it; Linux / Android, FreeBSD, OpenBSD and illumos were measured not to, and do not try it. On a system not measured it is told at run time: when the bind fails with nothing at the path, the next route is tried (with something there, that error is returned)
- **The routes through D**: make a directory D of sockat's own, held by its fd, with a symlink R of a random name in it pointing to the socket's directory, and bind by `R/<file name>` relative to D. R and D are removed afterwards
  - **thread-cwd**: a throwaway goroutine locked to its OS thread moves that thread's cwd (and no other's) into D and passes `R/<file name>`; the thread is then discarded (the goroutine ends without unlocking). Linux uses `unshare(CLONE_FS)` (not under a seccomp filter, such as Docker's default), macOS `__pthread_fchdir`
  - **proc**: pass `/proc/self/fd/<D's fd>/R/<file name>`, without a thread
  - **at**: pass D's fd and `R/<file name>` to `bindat(2)`
- **Where D goes**: `${TMPDIR:-/tmp}`, failing that `/tmp`, failing that the socket's directory (named `.sockat.XXXXXXXXXX`). A deep place or one others can write to works too (D is followed by its fd, not its name, and a directory swapped in before it is opened is refused by its owner and mode). When D can be made nowhere, the form without D is tried (thread-cwd-sockdir → proc-sockdir, at-sockdir on FreeBSD): R (`.sockat.XXXXXXXXXX`) is made in the socket's directory itself and the bind goes by `.sockat.XXXXXXXXXX/<file name>` relative to that directory's fd (file names up to 88 bytes on Linux, 84 on macOS / FreeBSD, about 72 for proc-sockdir)

Dial: the connecting socket is not bound, so its spelling stays with nobody, and the routes that need no writing and no thread come first; the routes that make D and R or start a thread come after them ([the specification's Connect order](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#connect-order)):

- **proc-direct**: open the socket's directory and pass `/proc/self/fd/<fd>/<file name>`
- **relative**: when the path relative to the cwd is short enough, pass that. Only the string is rewritten; the cwd is not changed
- **at-direct**: pass the socket's directory fd and the file name to `connectat(2)`
- **thread-cwd / proc / at**: make D and R and pass them, as Listen does
- **proc-held**: put a symlink `s` to the socket file in D and pass `/proc/self/fd/<D's fd>/s` (no R). The file name is not in the spelling, so any file name is reached
- **symlink-file**: pass a symlink in a place, as Listen does (a connect follows a symlink, so it works on every system)

D is made at most once per call and shared by the routes that use it. When it can be made nowhere, every route that uses it is passed over (D is not tried again).

No route changes the process's cwd or the calling thread's, so any goroutine may call them.

### Routes that differ from the specification

| Route | sockat-go | Why |
|---|---|---|
| fork-cwd and fork-cwd-sockdir (bind), fork-cwd-direct (a connect taking the last resort) | none (listed in `Skipped` as `no fork route in sockat-go`, and the next route is tried) | Go's runtime cannot fork bare |
| widened (macOS, second in Dial's order) | none (passed over for thread-cwd) | Handing sockat's own socket to the net package's types, a connected one gets a nil `RemoteAddr()` when the peer is bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

**Without fork, Listen gets `ENAMETOOLONG` for a long path here:**

- **OpenBSD / illumos**: there is no means for the routes through D (thread-cwd, proc, at), and the bind does not follow a symlink
- **NetBSD** with no place for symlink-file (`TMPDIR`, `/tmp`)
- Linux under a seccomp filter without `/proc`
- macOS where `__pthread_fchdir` cannot be used and there is no place for symlink-file (`TMPDIR`, `/tmp`)

To reach the path anyway, use `ListenEx(path, sockat.UseLastResort)` (the last resort above).

### Where a path stops being reachable

When a path over the limit gets `ENAMETOOLONG` (with the directory there and D creatable; numbers are bytes).

| OS | Listen | Dial |
|---|---|---|
| Linux (with `/proc`) | file name > 96. Under a seccomp filter about 79 (proc) | reached (proc-held) |
| Linux (without `/proc`) | file name > 96. Under a seccomp filter every long path | thread-cwd cannot be used (file name > 96, or no thread cwd), the relative path > 107 and symlink-file cannot be used |
| macOS | reached (symlink-file). With no place for symlink-file, file name > 92 | thread-cwd cannot be used (file name > 92, or no thread cwd), the relative path > 103 and symlink-file cannot be used |
| FreeBSD | file name > 92 | file name > 103 |
| NetBSD | reached (symlink-file). With no place for symlink-file, every long path | relative path > 103 and symlink-file cannot be used |
| OpenBSD | every long path | relative path > 103 and symlink-file cannot be used |
| illumos | every long path | relative path > 107 and symlink-file cannot be used |

- On Linux with `/proc` where D can be made, and outside FreeBSD in an ordinary environment where a symlink can be made in `/tmp`, Dial reaches a path whatever the directory depth and the file name length. When D can be made nowhere, Dial reaches what the routes without D (proc-direct, relative, at-direct, symlink-file) reach
- A path longer than `PATH_MAX` (1024 bytes on macOS, 4096 on Linux) cannot be opened by any other system call either; it is outside what sockat handles and gets `ENAMETOOLONG`

## What differs from the net package

- **A path containing a NUL is `EINVAL`, whatever its length.** The net package silently cuts it short there and binds some other path. On Linux and Android, an abstract socket name (a leading NUL or `@`) is handed to the net package as is. Where there are no abstract sockets (illumos included), a leading NUL is `EINVAL` too
- **On illumos, a path that starts with `@` is bound / dialled as the file `@...` in the cwd.** The net package rewrites a leading `@` into an abstract name, but the illumos kernel has no abstract sockets. sockat hands the net package the path with `./` in front, so the listener's `Addr()` returns `./@...` and the loss is `cwd-relative`, as for any other relative path. The listener's Close leaves the file (as the net package elsewhere leaves a path that starts with `@`; to remove it, use `ListenEx`'s `discard`). A path of 106 or 107 bytes, which no longer fits once `./` is added, is treated as a long path (Listen gets `ENAMETOOLONG`)

Only for a long path:

- **The listener does not remove the socket file on Close.** The net package (and a short path) does. The address a long-path listener holds is the spelling handed to the kernel, so do not call `SetUnlinkOnClose(true)` on it; to remove the file, use `ListenEx`'s `discard`
- **`Addr()` of a long-path listener (and `LocalAddr()` of the connections it accepts) does not report the path you passed.** It reports the spelling its bind handed to the kernel: `R/<file name>` (`/proc/self/fd/<fd>/R/<file name>` for proc, `<place>/sockat.XXXXXXXXXX` for symlink-file). `RemoteAddr()` of a connection to such a listener reports the same spelling, whichever route the Dial took (the kernel reports what the peer bound). R and the symlink are gone, so it usually names nothing, but the spelling can be seen by other users (Linux's `/proc/net/unix`). Where others can write (a shared place for symlink-file, a cwd others can write to for R), once the same name is made again, stat-ing that spelling reaches their file, and unlinking an R spelling (`R/<file name>`) may remove the file of the same name in the directory they chose. Do not unlink or stat that spelling, nor hand it to another process to connect to. Keep the path you passed (to remove the file, use `ListenEx`'s `discard`)
- **A path that does not end with a file name** (`dir/`, `dir/.`, `dir/..`) is `EINVAL`, and nothing is created
- **The directory is looked up first**: the routes through D resolve the socket's directory to a real path when they create R; proc-direct, relative and at-direct look the directory up and then pass the file name. A rename or a repointed symlink in between is not seen. symlink-file is as the net package
- **Another goroutine's `os.Chdir`**: if the cwd changes during relative (Dial), the result is discarded and an error is returned. If the cwd changes between `ListenEx`'s resolving of a relative path's real path and the bind (short paths too), it closes the listener, leaves the file, and returns an error (the file recorded and the file bound may differ, so it returns no `discard`)
- **R swapped in the form without D**: in a socket directory others can write to without the sticky bit, R of the form without D (Listen) can be replaced between its creation and the bind, which may then bind in another directory (where the name exists already the bind fails with `EADDRINUSE`; nothing is overwritten)
- **Leftovers**: if the process dies before removing them, a `sockat.XXXXXXXXXX` (D, a symlink) stays in the place, and a `.sockat.XXXXXXXXXX` (D) in the socket's directory

## Documentation

- [The sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (routes, safety conditions, errors)
- [docs/decisions/](./docs/decisions/INDEX.md) — design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## License

MIT License, Yoshiaki Kawazu (@kawaz)
