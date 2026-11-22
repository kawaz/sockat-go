# sockat-go reference

> English | [日本語](./REFERENCE-ja.md)

The functions and types are documented on [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go). The language-independent behaviour is defined by the [sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md); sockat-go is its Go implementation. This page holds the Go names and types, the Go forms and the caveats specific to Go, and for what is common to all languages links to the sections of the specification. How to use it is in the [user manual](MANUAL.md).

## Relation to the standard library

- The return values are the standard library's `*net.UnixListener` / `*net.UnixConn` themselves (assignable to `net.Listener` / `net.Conn` as they are)
- An absolute path short enough for the standard library (up to 107 bytes on Linux / Android / illumos, 103 on Apple's systems and the BSDs) is handed to it unchanged, so it behaves exactly as with the standard library
- A relative path is made absolute before it is handled by the binds (`Listen`, `ListenEx`, `ListenUnixgram`, `ListenUnixgramEx`) and resolve, and is handled as it is by the connects (`Dial`, `DialEx`, `DialUnixgram`, `DialUnixgramEx`). See the specification's "[Making a relative path absolute](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#making-a-relative-path-absolute)"
- For a long path too, the socket file is created at the path given
- Errors are the standard library's `*net.OpError`: `errors.Is(err, syscall.ENOENT)` and the like work
- The only dependency is `golang.org/x/sys`. Go: the two supported releases (1.26 and later)
- A long path, and the bind of a relative path, differ from the standard library in some ways (the address the kernel returns, what stays outside the application's places, and so on). See the specification's "[What differs from the standard library for a long path](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-differs-from-the-standard-library-for-a-long-path)" and "[Go-specific caveats](#go-specific-caveats)"

## Functions

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

- There is one `Dial`. The connect of Go's standard library never waits (with a full backlog Linux answers `EAGAIN` at once, macOS `ECONNREFUSED`), so there is no separate `_nonblocking`
- The `Ex` functions return the cleanup and the diagnostics along with the standard library's type. Their error is a `*sockat.RouteError`
- DGRAM's `send_to` and SEQPACKET are open in the SPEC and not made. For SEQPACKET, hand a resolve spelling to the standard library's `"unixpacket"` sockets (the user manual's "[Use SEQPACKET](manual/seqpacket.md)")

## Types

| The SPEC's type | Go's type |
|---|---|
| `Bound` | `*Bound` (`File *SocketFile`, `Spelling`, `Route`, `Base`, `Skipped`) |
| `Connected` / `Report` | The standard library's `*net.UnixConn` and `Report` (`Route`, `Skipped`) |
| `ForBind` | `*ForBind` (`Path`, `Spelling`, `Cleanup()`, `SocketFile()`, diagnostics) |
| `ForConnect` | `*ForConnect` (`Path`, `Spelling`, `Cleanup()`, diagnostics) |
| `SocketFile` | `*SocketFile` (`Remove()`, `Cleanup()`) |
| The failure of `_ex` and resolve | `*RouteError` (`Route`, `Skipped`; `Unwrap` reaches the errno) |
| An argument error | `EINVAL` |

## `Options`

The meaning of the fields and their errors are in the specification's "[`Options`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#options)" and "[Errors](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#errors)".

| SPEC | Go |
|---|---|
| `usable_cross_uid` | `UsableCrossUID bool`. Only the binds (`ListenEx`, `ListenUnixgramEx`, `ResolveForBind`) look at it |
| `base` | `Base string` (`""` for none) |

- The zero value `Options{}` is the default: `ListenEx` / `DialEx` then try the routes `Listen` / `Dial` do, in the same order
- A `Base` of the wrong form (relative, not ending with a name, or containing a NUL) is `EINVAL`, whatever the length of the path

## What the spelling is good for (`Spelling`)

For a long path, the listener's `Addr()`, an accepted connection's `LocalAddr()` and the peer's `RemoteAddr()` are not the path you passed but the bind's spelling. `Bound.Spelling` (and `ForBind.Spelling`, `ForConnect.Spelling`) tells whether that spelling can be used later. The meaning of the values is in the specification's "[Spelling properties](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#spelling-properties-spelling)".

| SPEC | Go |
|---|---|
| `Original` / `CwdRelative` / `Linked` / `Dangling` | `Spelling.Kind`'s `sockat.Original` / `sockat.CwdRelative` / `sockat.Linked` / `sockat.Dangling` |
| `removable` | `Spelling.Removable` |
| `until` (`Process` / `Cleanup`) | `Spelling.Until` (`sockat.UntilProcess` / `sockat.UntilCleanup`) |
| `usable_cross_uid` | `Spelling.UsableCrossUID` |
| `usable_with_private_tmp` | `Spelling.UsableWithPrivateTmp` |

`ExampleSpelling` ([example_long_test.go](../example_long_test.go)):

```go
switch sp := b.Spelling; sp.Kind {
case sockat.Original, sockat.CwdRelative:
	fmt.Println("the path itself")
case sockat.Linked:
	// sp.Until: how long l.Addr() can be followed.
	// sp.UsableCrossUID: a peer of another uid can follow it.
	fmt.Println("Linked; unlinking it removes the socket file:", sp.Removable)
case sockat.Dangling:
	fmt.Println("names nothing: keep path")
}
```

- The fields that mean something only for `Linked` are false or zero for the other kinds
- `Spelling` compares with `==`, and the zero value of `Kind` is not used (a zero value on failure does not read as `Original`). `Kind.String()` and `Until.String()` give the SPEC's names (`"Linked"`, `"Process"` and so on)
- `CwdRelative` comes only when a relative path, made absolute, could take no route and was handed to the standard library as it is (and from a resolve that returned it as it is)
- `ForConnect.Spelling` is one of `Original`, `CwdRelative` and `Linked`

## Diagnostics (`Route`, `Base`, `Skipped`) and `RouteError`

The meaning of the fields is in the specification's "[Diagnostics](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#diagnostics)". They are all for diagnostics and their contents are not a promise: branch on `Spelling`.

`ExampleDialEx` ([example_long_test.go](../example_long_test.go)):

```go
c, rep, err := sockat.DialEx(path, sockat.Options{})
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
	}
	log.Fatal(err)
}
defer c.Close()
```

- On success, `rep.Route` and `rep.Skipped` (`b.Route`, `b.Base` and `b.Skipped` for `ListenEx`) hold the same
- `Skipped` is never nil. `Bound.Base` is `""` when the spelling goes through no B
- `*sockat.RouteError` reaches through `Unwrap` the same `*net.OpError` that `Listen` / `Dial` return, so `errors.Is(err, syscall.ENOENT)` and the like work

## Removing the socket file (`SocketFile`)

What `Remove()` and `Cleanup()` do (remove only while the path names the same file, do nothing after a success, try again after a failure) is in the specification's "[`remove`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#remove)" and "[`remove` and `cleanup`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#remove-and-cleanup)". How to use it is in the user manual's "[Remove the socket file when done](manual/bind.md#remove-the-socket-file-when-done)".

- Call `Remove()` before closing the listener. Write the `defer` of `l.Close` first, and `Remove` runs first (`ExampleListenEx`)
- `Remove()` only removes the file: it does not close the listener
- The sockets of `ListenEx` and `ListenUnixgramEx` do not remove the file on Close (short paths included; `SetUnlinkOnClose(false)`). Only `Remove` removes it
- `Remove()` returns the error of the file side and that of the `Cleanup` joined with `errors.Join`. Neither is called for you

## resolve (`ForBind`, `ForConnect`)

The spellings returned and when they cannot be made are in the specification's "[resolve](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#resolve)" and "[`SocketFile` of `ForBind`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#socketfile-of-forbind)". How to use them is in the user manual's "[Resolve a path only](manual/resolve.md)".

- A listener you made by handing the spelling to `net.ListenUnix` is set `SetUnlinkOnClose(false)` and its file removed by the `SocketFile`'s `Remove` (the standard library's Close unlinks by the spelling; "[Go-specific caveats](#go-specific-caveats)")
- Call `ForBind.SocketFile()` after the bind, before closing the listener. It fails with `ENOENT` before the bind, and with `ENOTSOCK` when what the spelling leads to is not a socket
- The error of a resolve is a `*RouteError` around an `*os.PathError` (`Op` `resolve`, `Path` the path you passed). A length for which `ResolveForBind` returns `ENAMETOOLONG` (one only thread-cwd / at reach) is still reached by `Listen` / `ListenEx`

## What stays in the places, and `Sweep`

A bind of a long path leaves B, D and E outside the application's places (`/tmp/sockat-<uid>` and the like). What stays, when it goes, what `Sweep` removes, and how a long-lived process cleans up are in the specification's "[What stays in the base](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-stays-in-the-base)" and "[`sweep`](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#sweep)". The Go forms:

- `sockat.Sweep()` removes the dead D of places 1 to 3, and `sockat.SweepBase(base)` the dead D in the B given as `Options.Base`. Neither is called for you. Where tests run again and again, call `sockat.Sweep()` once (`ExampleSweep`)
- The default `Listen` and `ListenUnixgram` return no `SocketFile`, so their E stays until the process ends. Link-dir's E is shared by the binds to one directory, so the E grow with the number of directories only. A long-lived process that binds long paths in a new directory again and again uses `ListenEx`, and for each socket no longer needed calls `b.File.Remove()` (the file too) or `b.File.Cleanup()` (the E only)

## Patterns not to use

Why not is in the specification's "[Patterns not to use](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#patterns-not-to-use)". Here are the Go forms and what to do instead. The code that avoids them runs as Example functions (`example_*_test.go`).

| The Go form | Instead |
|---|---|
| `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` without looking at `Spelling` | Keep the path you passed and use it. `b.File.Remove()` (`ExampleListenEx`) |
| Using an old spelling (`ForBind.Path`, `ForConnect.Path`, `l.Addr()`) after its `Cleanup`, or after the process ends | Use a spelling only for as long as `Spelling.Until` says |
| Listening on a relative path, and removing it by that relative path after an `os.Chdir` | `b.File.Remove()`. To keep an absolute path yourself, put `os.Getwd()` in front (not `filepath.Abs`, which folds `..` lexically; `ExampleListenEx_absolutePath`) |
| Counting on `l.Close()` alone to remove the socket file | `b.File.Remove()`, or `os.Remove(path)` with the path you passed |
| Counting on `b.File.Remove()` to close the listener too | Write `defer l.Close()` yourself |
| Calling `b.File.Remove()` after `l.Close()` | Call it before closing the listener (write the `defer` of `l.Close` first) |
| Having a peer of another uid follow a spelling through `/proc`, or one whose `Spelling.UsableCrossUID` is false | Pass `Options{UsableCrossUID: true}` and check the `Spelling` returned (`ExampleOptions_usableCrossUID`) |
| Branching on the strings of `Route`, `Base`, or a `Skipped`'s `Name` / `Reason` | Branch on `Spelling` (`ExampleSpelling`) |

## How a long path is reached

A path longer than the standard library takes tries the routes below from left to right, and the first route that can be used binds / connects. Before the routes, the binds and resolve check whether the path through the real path of the socket's directory fits, and hand that to the standard library if it does (the specification's "[Making a relative path absolute](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#making-a-relative-path-absolute)"). The route names and their order are in the specification's "[Routes for long paths and their order](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#routes-for-long-paths-and-their-order)", how each route and B are built in [SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes.md#routes), and how far they reach in the hub's [reach table](https://github.com/kawaz/sockat/blob/main/pages/reach/README.md).

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
- **at**: binds a socket of our own with FreeBSD's `bindat(2)` and hands it to the standard library's type with `net.FileListener`
- None of the routes changes the working directory of the process or of the calling thread, so any goroutine may call them

### Routes that depart from the SPEC

| Route | sockat-go | Why |
|---|---|---|
| widened (after relative in the connect of Apple's systems) | None (listed in `Skipped` as `no widened route in sockat-go`; the next route is tried) | Handing a socket of our own to the standard library's type leaves `RemoteAddr()` nil, whose `String()` panics, when the peer bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

With no widened and no fork, on Apple's systems, a connect to a path whose absolute or relative form is at most 253 bytes is `ENAMETOOLONG` when relative does not fit and neither thread-cwd-direct (no `__pthread_fchdir`, or a file name over 103 bytes) nor link-file (no B can be made) can be used.

## Go-specific caveats

Where sockat-go takes a form other than the standard library's because of how Go's standard library behaves, and what it inherits from it. The differences common to all languages are in the specification's "[What differs from the standard library for a long path](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-differs-from-the-standard-library-for-a-long-path)".

- **An empty path is `EINVAL`.** The standard library binds an empty path, on Linux, to an abstract name the kernel picks (autobind), and refuses a connect to it with an error that has no errno
- **A path containing a NUL byte is `EINVAL`, whatever its length.** The standard library silently cuts the path at the NUL and binds another path. On Linux / Android, an abstract socket name (a leading NUL or `@`) is handed to the standard library as it is (`Spelling` `Original`). Where there are no abstract sockets (illumos included), a leading NUL is `EINVAL` too
- **The `Err` of an argument error is not a bare `syscall.EINVAL`.** For the argument errors sockat finds itself (an `Options.Base` of the wrong form, a `SweepBase` of the wrong form, a long path that does not end with a name, an empty path, a NUL), the `Err` of the `*net.OpError` / `*os.PathError` is a value that wraps `syscall.EINVAL`. Test it with `errors.Is(err, syscall.EINVAL)` (`== syscall.EINVAL` and a type assertion such as `.(syscall.Errno)` do not match)
- **On illumos, a path that starts with `@` names the file `@...` in the cwd.** The standard library rewrites a leading `@` into an abstract name, but the illumos kernel has no abstract sockets. A bind makes it absolute as any other relative path. Where it is handed to the standard library as a relative path (a connect, and a bind that, made absolute, could take no route), it goes with `./` in front: that listener's `Addr()` is `./@...`, its `Spelling` is `CwdRelative`, and its Close does not remove the file (as the standard library elsewhere leaves a path that starts with `@`; to remove it, use `ListenEx`'s `Remove`). A path of 106 or 107 bytes, which no longer fits with `./`, goes through the routes as a long path
- **The standard library's `UnixListener.Close` unlinks the spelling it keeps.** For the default `Listen`'s listener of a long path, through a link-dir spelling the E on the way is followed and the socket file is removed (as with the standard library). **Through a link-file spelling, the E itself is removed and the socket file stays.** Through a `Dangling` spelling nothing is removed (it is set `SetUnlinkOnClose(false)`). The spelling of a `Listen` on a relative path does not depend on the cwd (except when it was handed to the standard library as a relative path), so Close works as above after a change of the cwd too. The listener of `ListenEx` removes nothing, whatever the spelling, short paths included (`SetUnlinkOnClose(false)`; only `Remove` removes the file)
- **No route for macOS's widened sockaddr.** Why, and what stops being reached: "[Routes that depart from the SPEC](#routes-that-depart-from-the-spec)"

## Specification and design

- [sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (operations, resolve, spelling properties, cleanup, what stays in the base, routes, what differs from the standard library, patterns not to use, errors)
- [Per-language caveats](https://github.com/kawaz/sockat/blob/main/pages/langs.md) — the caveats specific to each language's implementation
- [docs/decisions/](./decisions/INDEX.md) — the design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API
