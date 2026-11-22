# sockat-go user manual

> English | [日本語](./MANUAL-ja.md)

The user manual of sockat-go, a Go library that binds and connects Unix domain sockets at paths longer than `sun_path` allows (104 bytes on macOS and the BSDs, 108 on Linux, the terminating NUL included). The entry point is the [README](../README.md).

The language-independent behaviour is defined by the [sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md); sockat-go is its Go implementation. This manual holds only the Go names and forms and the caveats specific to Go; for what is common to all languages it links to the sections of the specification.

```go
l, err := sockat.Listen("/a/very/deep/directory/.../server.sock") // *net.UnixListener
c, err := sockat.Dial("/a/very/deep/directory/.../server.sock")   // *net.UnixConn
```

## Usage

Use `sockat.Listen` / `sockat.Dial` where you would use the standard `net.ListenUnix` / `net.DialUnix` (`net.Listen("unix", ...)` / `net.Dial("unix", ...)`).

```sh
go get github.com/kawaz/sockat-go
```

- The return values are the standard `*net.UnixListener` / `*net.UnixConn` themselves (assignable to `net.Listener` / `net.Conn` as they are)
- A path short enough for the standard library (up to 107 bytes on Linux / Android / illumos, 103 on Apple's systems and the BSDs) is handed to it unchanged, so it behaves exactly as with the standard library
- For a long path too, the socket file is created at the path given
- Errors are the standard `*net.OpError`: `errors.Is(err, syscall.ENOENT)` and the like work
- The only dependency is `golang.org/x/sys`. Go: the two supported releases (1.26 and later)
- A long path differs from the standard library in some ways (the address the kernel returns, what stays outside the application's places, and so on). Read the specification's "[What differs from the standard for a long path](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-differs-from-the-standard-for-a-long-path)" and this manual's "[Go-specific caveats](#go-specific-caveats)"

### Functions

| Function | Returns | The SPEC's function |
|---|---|---|
| `Listen(path)` | `*net.UnixListener` | `bind` |
| `Dial(path)` | `*net.UnixConn` | `connect`<br>`connect_nonblocking` |
| `ListenEx(path, Options)` | `*net.UnixListener`<br>`*Bound` | `bind_ex` |
| `DialEx(path, Options)` | `*net.UnixConn`<br>`Report` | `connect_ex`<br>`connect_nonblocking_ex` |
| `ListenUnixgram(path)` | `*net.UnixConn` (DGRAM) | `datagram::bind` |
| `DialUnixgram(path)` | `*net.UnixConn` (DGRAM, not bound) | `datagram::unbound` and `datagram::connect` |
| `ListenUnixgramEx(path, Options)` | `*net.UnixConn`<br>`*Bound` | `datagram::bind_ex` |
| `DialUnixgramEx(path, Options)` | `*net.UnixConn`<br>`Report` | `datagram::connect_ex` |
| `ResolveForBind(path, Options)` | `*ForBind` | `resolve_for_bind` |
| `ResolveForConnect(path, Options)` | `*ForConnect` | `resolve_for_connect` |
| `Sweep()` | | `sweep` |
| `SweepBase(base)` | | `sweep_base` |

- There is one `Dial`. Go's standard connect never waits (with a full backlog Linux answers `EAGAIN` at once, macOS `ECONNREFUSED`), so there is no separate `_nonblocking`
- The `Ex` functions return the cleanup and the diagnostics along with the standard type. Their error is a `*sockat.RouteError`
- DGRAM's `send_to` and SEQPACKET are open in the SPEC and not made

| The SPEC's type | Go's type |
|---|---|
| `Bound` | `*Bound` (`File *SocketFile`, `Spelling`, `Route`, `Base`, `Skipped`) |
| `Connected` / `Report` | The standard `*net.UnixConn` and `Report` (`Route`, `Skipped`) |
| `ForBind` | `*ForBind` (`Path`, `Spelling`, `Cleanup()`, `SocketFile()`, diagnostics) |
| `ForConnect` | `*ForConnect` (`Path`, `Cleanup()`, diagnostics) |
| `SocketFile` | `*SocketFile` (`Remove()`, `Cleanup()`) |
| The failure of `_ex` and resolve | `*RouteError` (`Route`, `Skipped`; `Unwrap` reaches the errno) |
| An argument error | `EINVAL` |

### Removing the socket file (`ListenEx` and `SocketFile`)

What `Remove()` and `Cleanup()` do (remove only while the path names the same file, do nothing after a success, try again after a failure) is in the specification's "[`remove`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#remove)" and "[`remove` and `cleanup`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#remove-and-cleanup)". The Go form:

```go
l, b, err := sockat.ListenEx(path, sockat.Options{}) // l is a *net.UnixListener
if err != nil {
	return err
}
defer l.Close()
defer b.File.Remove() // removes the socket file (does not close l); runs before l.Close
```

- Call `Remove()` before closing the listener. Write the `defer` of `l.Close` first, and `Remove` runs first (`ExampleListenEx`)
- The listener of `ListenEx` does not remove the file on Close (short paths included; `SetUnlinkOnClose(false)`). Only `Remove` removes it
- `Remove()` returns the error of the file side and that of the `Cleanup` joined with `errors.Join`. Neither is called for you

### What the spelling is good for (`Spelling`)

For a long path, the listener's `Addr()`, an accepted connection's `LocalAddr()` and the peer's `RemoteAddr()` are not the path you passed but the bind's spelling. `Bound.Spelling` (and `ForBind.Spelling`) tells whether that spelling can be used later. The meaning of the values is in the specification's "[Spelling properties](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#spelling-properties-spelling)".

| SPEC | Go |
|---|---|
| `Original` / `CwdRelative` / `Linked` / `Dangling` | `Spelling.Kind`'s `sockat.Original` / `sockat.CwdRelative` / `sockat.Linked` / `sockat.Dangling` |
| `removable` | `Spelling.Removable` |
| `until` (`Process` / `Cleanup`) | `Spelling.Until` (`sockat.UntilProcess` / `sockat.UntilCleanup`) |
| `usable_cross_uid` | `Spelling.UsableCrossUID` |
| `usable_with_private_tmp` | `Spelling.UsableWithPrivateTmp` |

```go
switch sp := b.Spelling; sp.Kind {
case sockat.Original, sockat.CwdRelative:
	// the path itself
case sockat.Linked:
	// l.Addr() reaches the path for as long as sp.Until says
case sockat.Dangling:
	// l.Addr() names nothing: keep the path
}
```

- The fields that mean something only for `Linked` are false or zero for the other kinds
- `Spelling` compares with `==`, and the zero value of `Kind` is not used (a zero value on failure does not read as `Original`). `Kind.String()` and `Until.String()` give the SPEC's names (`"Linked"`, `"Process"` and so on)

### Diagnostics (`Route`, `Base`, `Skipped`) and `RouteError`

The meaning of the fields is in the specification's "[Diagnostics](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#diagnostics)". They are all for diagnostics and their contents are not a promise: branch on `Spelling`.

```go
c, rep, err := sockat.DialEx(path, sockat.Options{}) // c is a *net.UnixConn
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
	}
	return err
}
log.Printf("dial %s: route %s after %v", path, rep.Route, rep.Skipped)
```

- `Skipped` is never nil. `Bound.Base` is `""` when the spelling goes through no B
- `*sockat.RouteError` reaches through `Unwrap` the same `*net.OpError` that `Listen` / `Dial` return, so `errors.Is(err, syscall.ENOENT)` and the like work

### `Options`

The meaning of the fields and their errors are in the specification's "[`Options`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#options)" and "[Errors](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#errors)".

| SPEC | Go |
|---|---|
| `usable_cross_uid` | `UsableCrossUID bool`. Only the binds (`ListenEx`, `ListenUnixgramEx`, `ResolveForBind`) look at it |
| `base` | `Base string` (`""` for none) |

- The zero value `Options{}` is the default: `ListenEx` / `DialEx` then try the routes `Listen` / `Dial` do, in the same order
- A `Base` of the wrong form (relative, not ending with a name, or containing a NUL) is `EINVAL`, whatever the length of the path

### DGRAM

```go
srv, err := sockat.ListenUnixgram(path) // *net.UnixConn
c, err := sockat.DialUnixgram(path)     // *net.UnixConn (not bound)
```

- `ListenUnixgram` binds by the routes `Listen` takes. As with the standard library, Close does not remove the file
- `DialUnixgram` makes a socket that is not bound and connects it (the shape of the standard `net.DialUnix("unixgram", nil, ...)`), by the routes `Dial` tries
- To send from, or connect, a bound socket to a long path, spell the path with `ResolveForConnect` and hand that to the standard library (`ExampleListenUnixgram`):

```go
to, err := sockat.ResolveForConnect(serverPath, sockat.Options{})
if err != nil {
	return err
}
defer to.Cleanup()
_, err = cl.WriteToUnix(msg, &net.UnixAddr{Name: to.Path, Net: "unixgram"})
```

### resolve (`ResolveForBind` / `ResolveForConnect`)

For handing a spelling to the standard library yourself (to pass a path to another library, to send datagrams to, and so on). The spellings returned and when they cannot be made are in the specification's "[resolve](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#resolve)" and "[`SocketFile` of `ForBind`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#socketfile-of-forbind)".

```go
f, err := sockat.ResolveForBind(path, sockat.Options{})
if err != nil {
	return err // ENAMETOOLONG: a length only Listen's thread-cwd / at reach
}
l, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.Path, Net: "unix"})
if err != nil {
	f.Cleanup()
	return err
}
l.SetUnlinkOnClose(false) // the SocketFile's Remove removes the file
defer l.Close()
sf, err := f.SocketFile() // after the bind, before closing l
if err != nil {
	return err
}
defer sf.Remove()
```

```go
f, err := sockat.ResolveForConnect(path, sockat.Options{})
if err != nil {
	return err
}
defer f.Cleanup()
c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.Path, Net: "unix"}) // as often as you like
```

- A listener you made by handing the spelling to `net.ListenUnix` is set `SetUnlinkOnClose(false)` and its file removed by the `SocketFile`'s `Remove` (the standard Close unlinks by the spelling; "[Go-specific caveats](#go-specific-caveats)")
- `ForBind.SocketFile()` fails with `ENOENT` before the bind, and with `ENOTSOCK` when what the spelling leads to is not a socket
- The error of a resolve is a `*RouteError` around an `*os.PathError` (`Op` `resolve`, `Path` the path you passed)

### What stays in the places, and `Sweep`

A bind of a long path leaves B, D and E outside the application's places (`/tmp/sockat-<uid>` and the like). What stays, when it goes, what `Sweep` removes, and how a long-lived process cleans up are in the specification's "[What stays in the base](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-stays-in-the-base)" and "[`sweep`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#sweep)". The Go forms:

- `sockat.Sweep()` removes the dead D of places 1 to 3, and `sockat.SweepBase(base)` the dead D in the B given as `Options.Base`. Neither is called for you. Where tests run again and again, call `sockat.Sweep()` once
- The default `Listen` and `ListenUnixgram` return no `SocketFile`, so their E stays until the process ends. Link-dir's E is shared by the binds to one directory, so the E grow with the number of directories only. A long-lived process that binds long paths in a new directory again and again uses `ListenEx`, and for each socket no longer needed calls `b.File.Remove()` (the file too) or `b.File.Cleanup()` (the E only)

## Patterns not to use

Why not is in the specification's "[Patterns not to use](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#patterns-not-to-use)". Here are the Go forms and what to do instead. The code that avoids them runs as tests (the Example functions in [example_long_test.go](../example_long_test.go)).

| The Go form | Instead |
|---|---|
| `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` without looking at `Spelling` | Keep the path you passed and use it. `b.File.Remove()` (`ExampleListenEx`) |
| Using an old spelling (`ForBind.Path`, `ForConnect.Path`, `l.Addr()`) after its `Cleanup`, or after the process ends | Use a spelling only for as long as `Spelling.Until` says |
| Listening on a relative path, and removing it by that relative path after an `os.Chdir` | Make the path absolute before the listen by putting `os.Getwd()` in front (not `filepath.Abs`, which folds `..` lexically; `ExampleListenEx_absolutePath`). `b.File.Remove()` |
| Counting on `l.Close()` alone to remove the socket file | `b.File.Remove()`, or `os.Remove(path)` with the path you passed |
| Counting on `b.File.Remove()` to close the listener too | Write `defer l.Close()` yourself |
| Calling `b.File.Remove()` after `l.Close()` | Call it before closing the listener (write the `defer` of `l.Close` first) |
| Having a peer of another uid follow a spelling through `/proc`, or one whose `Spelling.UsableCrossUID` is false | Pass `Options{UsableCrossUID: true}` and check the `Spelling` returned (`ExampleSpelling`) |
| Branching on the strings of `Route`, `Base`, or a `Skipped`'s `Name` / `Reason` | Branch on `Spelling` |

## How a long path is reached

A path longer than the standard library takes tries the routes below from left to right, and the first route that can be used binds / connects. The route names and their order are in the specification's "[Routes for long paths and their order](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#routes-for-long-paths-and-their-order)", how each route and B are built in [SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes.md#routes), and how far they reach in the hub's [reach table](https://github.com/kawaz/sockat/blob/main/pages/reach/README.md).

Bind (`Listen`, `ListenEx`, `ListenUnixgram`, `ListenUnixgramEx`):

| System | Routes |
|---|---|
| Linux / Android | link-dir (spelt through `/proc`, then B's real path), thread-cwd |
| Apple's systems | link-dir, link-file, thread-cwd |
| FreeBSD | link-dir, at |
| NetBSD | link-dir, link-file |
| OpenBSD / illumos | link-dir |
| Other Unix | link-dir, link-file (whether bind follows a symlink is told at run time) |

Connect (`Dial`, `DialEx`, `DialUnixgram`, `DialUnixgramEx`):

| System | Routes |
|---|---|
| Linux / Android (`/proc` usable) | proc-direct, thread-cwd-direct, link-file |
| Linux / Android (`/proc` not usable) | relative, thread-cwd-direct, link-file |
| Apple's systems | relative, widened (not made), thread-cwd-direct, link-file |
| FreeBSD | at-direct, link-file |
| NetBSD / OpenBSD / illumos / other Unix | relative, link-file |
| Windows | Not supported |

resolve: `ResolveForBind` takes link-dir of the bind routes and (on macOS and NetBSD only) link-file; `ResolveForConnect` takes link-file only.

The means in Go:

- **thread-cwd and thread-cwd-direct**: a throwaway goroutine locked to its own OS thread moves that thread's cwd (and no other's). The thread is thrown away with the goroutine after use, without unlocking (the cwd is never moved on the main thread, which the runtime does not throw away). Linux uses `unshare(CLONE_FS)` then `fchdir` (not under a seccomp filter, such as Docker's default); Apple's systems use `__pthread_fchdir`
- **at**: binds a socket of our own with FreeBSD's `bindat(2)` and hands it to the standard type with `net.FileListener`
- None of the routes changes the working directory of the process or of the calling thread, so any goroutine may call them

### Routes that depart from the SPEC

| Route | sockat-go | Why |
|---|---|---|
| widened (after relative in the connect of Apple's systems) | None (listed in `Skipped` as `no widened route in sockat-go`; the next route is tried) | Handing a socket of our own to the standard type leaves `RemoteAddr()` nil, whose `String()` panics, when the peer bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

With no widened and no fork, on Apple's systems, a connect to a path whose absolute or relative form is at most 253 bytes is `ENAMETOOLONG` when relative does not fit and neither thread-cwd-direct (no `__pthread_fchdir`, or a file name over 103 bytes) nor link-file (no B can be made) can be used.

## Go-specific caveats

Where sockat-go takes a form other than the standard library's because of how Go's standard library behaves, and what it inherits from it. The differences common to all languages are in the specification's "[What differs from the standard for a long path](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-differs-from-the-standard-for-a-long-path)".

- **An empty path is `EINVAL`.** The standard library binds an empty path, on Linux, to an abstract name the kernel picks (autobind), and refuses a connect to it with an error that has no errno
- **A path containing a NUL byte is `EINVAL`, whatever its length.** The standard library silently cuts the path at the NUL and binds another path. On Linux / Android, an abstract socket name (a leading NUL or `@`) is handed to the standard library as it is (`Spelling` `Original`). Where there are no abstract sockets (illumos included), a leading NUL is `EINVAL` too
- **The `Err` of an argument error is not a bare `syscall.EINVAL`.** For the argument errors sockat finds itself (an `Options.Base` of the wrong form, a `SweepBase` of the wrong form, a long path that does not end with a name, an empty path, a NUL), the `Err` of the `*net.OpError` / `*os.PathError` is a value that wraps `syscall.EINVAL`. Test it with `errors.Is(err, syscall.EINVAL)` (`== syscall.EINVAL` and a type assertion such as `.(syscall.Errno)` do not match)
- **On illumos, a path that starts with `@` is bound / connected as the file `@...` in the cwd.** The standard library rewrites a leading `@` into an abstract name, but the illumos kernel has no abstract sockets. sockat hands the path to it with `./` in front, so the listener's `Addr()` is `./@...` and `Spelling` is `CwdRelative`. Closing the listener does not remove the file (as the standard library elsewhere leaves a path that starts with `@`; to remove it, use `ListenEx`'s `Remove`). A path of 106 or 107 bytes, which no longer fits with `./`, goes through the routes as a long path
- **The standard `UnixListener.Close` unlinks the spelling it keeps.** For the default `Listen`'s listener of a long path, through a link-dir spelling the E on the way is followed and the socket file is removed (as with the standard library). **Through a link-file spelling, the E itself is removed and the socket file stays.** Through a `Dangling` spelling nothing is removed (it is set `SetUnlinkOnClose(false)`). The listener of `ListenEx` removes nothing, whatever the spelling, short paths included (`SetUnlinkOnClose(false)`; only `Remove` removes the file)
- **No route for macOS's widened sockaddr.** Why, and what stops being reached: "[Routes that depart from the SPEC](#routes-that-depart-from-the-spec)"

## Documentation

- [sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (operations, resolve, spelling properties, cleanup, what stays in the base, routes, what differs from the standard, patterns not to use, errors)
- [Per-language caveats](https://github.com/kawaz/sockat/blob/main/pages/langs.md) — the caveats specific to each language's implementation
- [docs/decisions/](./decisions/INDEX.md) — the design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API
