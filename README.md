# sockat-go

> English | [日本語](./README-ja.md)

A Go library that binds and connects Unix domain sockets at paths longer than `sun_path` allows (104 bytes on macOS and the BSDs, 108 on Linux, the terminating NUL included).

The language-independent behaviour is defined by the [sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md); sockat-go is its Go implementation.

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
- A path short enough for the standard library is handed to it unchanged, so it behaves exactly as with the standard library
- For a long path too, the socket file is created at the path given
- Errors are the standard `*net.OpError`: `errors.Is(err, syscall.ENOENT)` and the like work
- The only dependency is `golang.org/x/sys`. Go: the two supported releases (1.26 and later)
- A bind of a long path leaves D and E outside the application's own places (`/tmp/sockat-<uid>` and the like), so that the spelling the kernel keeps can still be followed later. They go with the system's cleaning of its temporary directories, or with `sockat.Sweep()`. Removing the D of a live process by hand only makes the spelling unfollowable; the socket at the path passed keeps working. Where tests run again and again, one call of `sockat.Sweep()` keeps dead D from piling up ([What stays in the places, and `Sweep`](#what-stays-in-the-places-and-sweep))

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

### Removing the socket file (`ListenEx` and `SocketFile`)

```go
l, b, err := sockat.ListenEx(path, sockat.Options{}) // l is a *net.UnixListener
if err != nil {
	return err
}
defer l.Close()
defer b.File.Remove() // removes the socket file (does not close l); runs before l.Close
```

- `b.File` (a `*sockat.SocketFile`) records the socket file's real path and `(st_dev, st_ino)` right after the bind, and `Remove()` removes it only while the path still names that same file. When the file cannot be recorded, `ListenEx` closes the listener, cleans up the spelling's E, leaves the file, and returns an error
- When the last component of the path is a symlink already there (on macOS and NetBSD the standard bind follows it and makes the socket at its end), what is recorded is the socket file it leads to, and `Remove()` removes that (not your symlink). When it leads to something that is not a socket, the file counts as one that cannot be recorded
- **Call `Remove()` before closing the listener** (with the `defer` order above, `Remove` runs first). A replacement can be told apart only while the listener is open: the kernel then holds the socket file, so one removed and made again by another process has another `(st_dev, st_ino)` and `Remove` leaves it. One removed and made again after the close may get the same numbers on file systems that reuse them at once (Linux's ext4, the BSDs' UFS / FFS), and `Remove` may remove it
- `Remove()` also does `Cleanup()` after removing the file. `Cleanup()` only cleans up the E (symlink) the spelling goes through; it does not remove the file. After a success, calling either again does nothing; after a failure, the next call tries again (when `Remove` removed the file but its `Cleanup` failed, the next `Remove` leaves the file side alone and tries the `Cleanup` again; a retried file side checks again that the path names the file bound). Concurrent calls remove one at a time. Neither is called for you
- `Remove()` returns an error when the file cannot be checked (no permission, say) or the removal fails. When the file is already gone or has been replaced it returns nil. On an error it still does the `Cleanup`
- A replacement in the instant between the check and the removal cannot be told apart (POSIX has no removal by inode)
- The listener of `ListenEx` does not remove the file on Close (short paths included). Only `Remove` removes it

### What the spelling is good for (`Spelling`)

For a long path, the address the kernel keeps (the listener's `Addr()`, an accepted connection's `LocalAddr()`, the peer's `RemoteAddr()`) is not the path you passed but the spelling handed to the kernel at the bind. `Bound.Spelling` (and `ForBind.Spelling`) tells whether that spelling can be used later ([the SPEC's "Spelling properties"](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#spelling-properties-spelling)). Branch on it.

| `Kind` | Spelling | Route |
|---|---|---|
| `Original` | The absolute path itself (an abstract name too) | standard |
| `CwdRelative` | The relative path itself: after a change of the cwd it names another place (a risk the standard library has of its own) | standard |
| `Linked` | Through B, D and E of the place, naming the same place as the path | link-dir, link-file |
| `Dangling` | Through an E already cleaned up: it names nothing | thread-cwd, at |

The fields that mean something only for `Linked` (false or zero for the other kinds):

| Field | Meaning |
|---|---|
| `Removable` | Whether unlinking the spelling removes the socket file. True for link-dir (the E on the way is followed), false for link-file (the last symlink is not followed, and the E is removed) |
| `Until` | How long the spelling can be followed. `UntilProcess` (a spelling through `/proc/<pid>/fd/<B's fd>`: not after the process ends), `UntilCleanup` (a spelling through B's real path: until `Cleanup`) |
| `UsableCrossUID` | Whether a peer of another uid can follow it. False for a spelling through `/proc`. For a real-path spelling, true when B and every directory on the way to it have search permission for others (B in `/tmp`) |
| `UsableWithPrivateTmp` | Whether a peer can follow it when either side has a `/tmp` of its own (systemd's `PrivateTmp` and the like). True for a spelling through `/proc`. For a real-path spelling, false when B's place or the spelling goes through `/tmp` or `/var/tmp` |

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

`Spelling` compares with `==`, and the zero value of `Kind` is not used (a zero value on failure does not read as `Original`). `Kind.String()` and `Until.String()` give the SPEC's names (`"Linked"`, `"Process"` and so on).

### Diagnostics (`Route`, `Base`, `Skipped`) and `RouteError`

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

- `Route` is the name of the route taken, `Skipped` the routes tried before it and found unusable (in the order tried) with the reasons. For a long path it includes `standard`. `Skipped` is never nil
- `Bound.Base` is the B the spelling goes through (`proc`, `tmp`, `runtime`, `tmpdir`, `sockdir`, `given`), `""` when the spelling goes through no B (standard, thread-cwd, at)
- All of them are for diagnostics (logs and the like), and their contents are not a promise. Branch on `Spelling`
- The error of a failure is a `*sockat.RouteError`, with `Route` (the route that actually bound / connected and failed; empty for `ENAMETOOLONG`, when every route was unusable, and for a failure before any route, such as a missing directory) and `Skipped`. `Unwrap` reaches the same `*net.OpError` that `Listen` / `Dial` return, so `errors.Is(err, syscall.ENOENT)` and the like work

### `Options`

| Field | Meaning |
|---|---|
| `UsableCrossUID bool` | Asks for a spelling a peer of another uid can follow too. Only the places where B and every directory on the way to it have search permission for others (`/tmp` and the like) are used, and no spelling through `/proc`. When that cannot be met, the bind does not fall back to a spelling that does not meet it but goes on to thread-cwd / at (`Dangling`). `Spelling.UsableCrossUID` tells whether it was met. Only the binds (`ListenEx`, `ListenUnixgramEx`, `ResolveForBind`) look at it |
| `Base string` | The path of B itself (a place the application manages, such as `/tmp/app-1000/sockat`), `""` for none. B is made there, and a check that fails is an error rather than a fall back to the automatic places. The connects use it as B's place too. B is sockat's alone: put nothing else in it |

The errors of `Base`:

- Relative, or not ending with a name (`dir/`, `..`, `/`): `EINVAL`, whatever the length of the path
- Checked only for a long path (a short path goes to the standard library, which does not look at it):
  - A directory on the way is missing: `ENOENT`
  - B's place is neither a directory nor a symlink: `ENOTDIR`
  - B is a symlink, others can write to it, it is someone else's, or a directory on the way can be replaced by others: `EPERM`
  - With `UsableCrossUID` of a bind, a directory on the way or B itself has no search permission for others: `EPERM`

The zero value `Options{}` is the default: `ListenEx` / `DialEx` then try the routes `Listen` / `Dial` do, in the same order.

### DGRAM

```go
srv, err := sockat.ListenUnixgram(path) // *net.UnixConn
c, err := sockat.DialUnixgram(path)     // *net.UnixConn (not bound)
```

- `ListenUnixgram` binds by the routes `Listen` takes. The spelling the kernel keeps (`LocalAddr()`, the sender seen by a peer) is `Listen`'s, and a reply to it arrives while the spelling is `Linked`. As with the standard library, Close does not remove the file
- `DialUnixgram` makes a socket that is not bound and connects it (the shape of the standard `net.DialUnix("unixgram", nil, ...)`), by the routes `Dial` tries
- To send from, or connect, a bound socket to a long path, spell the path with `ResolveForConnect` and hand that to the standard library:

```go
to, err := sockat.ResolveForConnect(serverPath, sockat.Options{})
if err != nil {
	return err
}
defer to.Cleanup()
_, err = cl.WriteToUnix(msg, &net.UnixAddr{Name: to.Path, Net: "unixgram"})
```

### resolve (`ResolveForBind` / `ResolveForConnect`)

To hand a spelling to the standard library yourself (to pass a path to another library, to send datagrams to, and so on), use resolve, which spells a long path shorter. The spelling is just a path, so it can go to the standard library for any socket type and operation.

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

| | `ResolveForBind` | `ResolveForConnect` |
|---|---|---|
| Spelling | link-dir's `B/D/E/<name>` first, then link-file's `B/D/E` only where bind is measured to follow a symlink (macOS, NetBSD) | link-file's `B/D/E`, whatever the length of the file name |
| Returns | `*ForBind` (`Path`, `Spelling`, `Cleanup()`, `SocketFile()`, diagnostics) | `*ForConnect` (`Path`, `Cleanup()`, diagnostics) |
| When it cannot | `ENAMETOOLONG` (a length only thread-cwd / at reach) | `ENAMETOOLONG` (no B can be made, for example) |

- A path no longer than the standard library takes is returned as it is (`Spelling` `Original` / `CwdRelative`)
- `ForBind.SocketFile()` follows the spelling when called and returns a `*SocketFile` that has recorded the socket file (where the last component of the path is a symlink, the file it leads to, as with `ListenEx`). It is an error before the bind (`ENOENT`), when what the spelling leads to is not a socket (`ENOTSOCK`), and, for a `CwdRelative` spelling, when the cwd is not the one of the resolve. The `Cleanup` of the `SocketFile` it returns cleans up the same E as the `ForBind`'s `Cleanup` (whichever succeeds first removes it, and both then do nothing)
- `ForConnect` gives no `SocketFile` (the type rules out removing the wrong file). It does not look at `UsableCrossUID` (the spelling of a connect stays with no peer)
- The error is a `*RouteError` around an `*os.PathError` (`Op` `resolve`, `Path` the path you passed)
- Do not use a spelling after its cleanup

### What stays in the places, and `Sweep`

The spelling of a long path goes through a directory D per process and a symlink E inside a place B per user. B, D and E stay outside the application's places (`/tmp` and the like) ([the SPEC's "What stays in the base"](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#what-stays-in-the-base)).

| | B | D | E |
|---|---|---|---|
| Where, and the name | The first that can be made of `/tmp/sockat-<uid>`, `$XDG_RUNTIME_DIR/sockat`, `$TMPDIR/sockat-<uid>`, `.sockat.<random>` in the socket's directory, in this order. `Options.Base` when given | 10 random characters, in B | resolve's E and thread-cwd's / at's E in D; the default connect's E right in B |
| Made | Per user, shared across processes | One per (process, B). Only the binds and `ResolveForConnect` make it | Per resolve. Per call for thread-cwd / at and the default connect |
| While alive | Not removed | The process keeps an fd on it with a shared lock (flock) | resolve's E stays until `Cleanup`. A per-call E is removed within the call |
| Cleanup | Not by sockat (the application manages `Options.Base`'s B) | Not by sockat. The D of a process that has ended goes with the system's cleaning of its temporary directories. `Sweep`, when called, removes the D no longer locked | `Cleanup` (`Remove`) |

- The default `Listen` returns no `SocketFile`, so its E stays until the process ends and goes with D in the system's cleaning. If the cleanup matters to you, use `ListenEx` or resolve
- The default `Dial` leaves neither D nor E in the process (the E right in B is removed right after the connect)
- B (one given as `Options.Base` too) is sockat's alone: put nothing else in it
- `Sweep()` removes the dead D in those of `/tmp/sockat-<uid>`, `$XDG_RUNTIME_DIR/sockat` and `$TMPDIR/sockat-<uid>` that exist and pass the checks. It makes no B. `SweepBase(base)` removes the dead D in the B given as `Options.Base` (called by the application that manages that B). They remove only directories named as D is (10 alphanumeric characters), and touch neither the D of a live process, a D that holds anything but symlinks, nor the E right in B. Neither is called for you. sockat itself never removes dead D (not at a resolve, nor at the start or end of a process)
- A long-lived process that binds long paths again and again (a daemon making a socket per session, say) piles up the E of the default `Listen` until it ends. Use `ListenEx` and clean up each socket no longer needed with `Remove()` (the file too) or `Cleanup()` (the E only)
- A failure to clean up an E within a call changes neither whether the call succeeds nor the error it returns
- **B in the socket's directory (`.sockat.<random>`) and a B fallen back to when the check failed (`sockat-<uid>.<random>`) are removed neither by `Sweep` nor by the system's cleaning.** Having random names, they are not reused by the next process and may pile up one per process. B in the socket's directory stays in your directory

The system's cleaning of its temporary directories ([the hub's research](https://github.com/kawaz/sockat/blob/main/docs/research/2026-11-20-tmp-cleaners-and-persistent-base.md), Japanese):

| Place | Dead D | Live D |
|---|---|---|
| Linux's `/tmp` | systemd-tmpfiles removes it after 10 days | A locked D is not removed (systemd v242 and later). **Before v242 (RHEL 8 and the like) it may be removed after 10 days** |
| `$XDG_RUNTIME_DIR` | Stays until logout and reboot | Same |
| macOS's `/tmp` | Stays until reboot | Not removed |
| macOS's `$TMPDIR` | dirhelper may remove it after 3 days | **Same (may be removed)** |
| FreeBSD, NetBSD, OpenBSD, illumos | By default not removed, or removed at reboot | Same |

When a live D is removed, its spellings can no longer be followed, and that is all: they do not reach an unrelated file.

## Patterns not to use

Ways of using sockat that remove an unrelated file or fail to remove the file ([the SPEC's "Patterns not to use"](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#patterns-not-to-use)). The code that avoids them runs as tests (the Example functions in [example_long_test.go](./example_long_test.go)).

| Pattern | Why not | Instead |
|---|---|---|
| `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` without looking at `Spelling` | For a long path it is not the path you passed. A link-file spelling only removes the E and leaves the file, and a `Dangling` spelling names nothing | Keep the path you passed and use it. `b.File.Remove()` (`ExampleListenEx`) |
| Using an old spelling (`ForBind.Path`, `ForConnect.Path`, `l.Addr()`) after its `Cleanup`, or after the process ends | Once cleaned up, B in `/tmp` can be made again under the same name by someone else. An `UntilProcess` spelling cannot be followed after the process ends | Use a spelling only for as long as `Spelling.Until` says |
| Listening on a relative path, and removing it by that relative path after an `os.Chdir` | It hits a file somewhere else (`CwdRelative`) | Make the path absolute with `filepath.Abs` before the listen (`ExampleListenEx_absolutePath`). `b.File.Remove()` |
| Counting on `l.Close()` alone to remove the socket file | For a long path, whether it does depends on the spelling ("[What differs from the standard library](#what-differs-from-the-standard-library)"). The listener of `ListenEx` never does | `b.File.Remove()`, or `os.Remove(path)` with the path you passed |
| Counting on `b.File.Remove()` to close the listener too | `Remove` only removes the file | Write `defer l.Close()` yourself |
| Calling `b.File.Remove()` after `l.Close()` | Another file made again after the close may not be told apart | Call it before closing the listener (write the `defer` of `l.Close` first) |
| Having a peer of another uid follow a spelling through `/proc`, or one whose `Spelling.UsableCrossUID` is false | It cannot | Pass `Options{UsableCrossUID: true}` and check the `Spelling` returned (`ExampleSpelling`) |
| Branching on the strings of `Route`, `Base`, or a `Skipped`'s `Name` / `Reason` | They are for diagnostics, and their contents are not a promise (routes are added and changed) | Branch on `Spelling` |

## Why

The standard `net` package refuses a path that does not fit `sun_path` with `EINVAL`. The file system has no such limit, yet a socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into it at once. The limit is on the length of the argument passed to `bind(2)` / `connect(2)`, so sockat hands the kernel a short spelling that names the same place.

## How a long path is reached

The length handed to the standard library as it is:

| System | Length |
|---|---|
| Linux / Android / illumos | Up to 107 bytes |
| Apple's systems / FreeBSD / NetBSD / OpenBSD | Up to 103 bytes |

A longer path has its socket directory looked up first (`ENOENT` when missing, `ENOTDIR` when not a directory); then the routes that make a short spelling ([the SPEC's "Route names"](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md#route-names)) are tried from left to right, and the first route that can be used binds / connects. When a route actually binds / connects and fails, that error is returned, and the next route does not try again.

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

resolve: `ResolveForBind` takes link-dir of the bind routes and (on macOS and NetBSD only) link-file; `ResolveForConnect` takes link-file only (on Linux spelt through `/proc`, then B's real path).

The routes of a bind (they leave their spelling, so while it lives, a library above that removes by `l.Addr()` and the replies of DGRAM work as with the path itself):

- **link-dir**: makes an E in D pointing to the socket's directory (its real path) and binds by `B/D/E/<name>`. B is spelt `/proc/<pid>/fd/<B's fd>` (on Linux with `/proc` usable, without `UsableCrossUID`) or by its real path. E stays until `Cleanup`. `Spelling` is `Linked` (`Removable`)
- **link-file**: makes an E in D pointing to the socket file and binds by `B/D/E`. Usable only where bind follows a symlink in the last component (measured to on macOS and NetBSD; measured not to on Linux / Android, FreeBSD, OpenBSD and illumos, which do not try it; elsewhere a failed bind with nothing at the path moves on to the next route). Its spelling holds no file name, so it reaches a file name of any length. `Spelling` is `Linked` (not `Removable`)
- **thread-cwd**: a throwaway goroutine locked to its own OS thread moves that thread's cwd (and no other's) into D and binds by `E/<name>`, through an E removed right after. The thread is thrown away after use (the goroutine ends without unlocking). Linux uses `unshare(CLONE_FS)` then `fchdir` (not under a seccomp filter, such as Docker's default); Apple's systems use `__pthread_fchdir`. `Spelling` is `Dangling`
- **at**: thread-cwd's spelling, handed to `bindat(2)` with D's fd (FreeBSD). `Spelling` is `Dangling`

The routes of a connect (the connecting socket is not bound and leaves no spelling with the peer, so the routes that need no writing and no thread come first):

- **proc-direct**: opens the socket's directory and passes `/proc/self/fd/<fd>/<name>`
- **relative**: passes the path relative to the cwd when it is short enough. It rewrites the string only, and does not change the cwd
- **at-direct**: passes the socket's directory fd and the file name to `connectat(2)`
- **thread-cwd-direct**: moves the cwd of a throwaway thread into the socket's directory and passes the file name. It makes neither D nor E
- **link-file**: makes an E of a random name right in B pointing to the socket file, passes `B/E` by B's real path, and removes the E right after. It makes no D. It reaches a file name of any length

How B is made ([the SPEC-routes' "Building B"](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes.md#building-b)):

- The place is made a real path, and the directories from the root down to it are checked (each owned by root or by you, and not writable by others or sticky). A place that fails is not used
- The place is opened `O_NOFOLLOW` and held by its fd; B is made by `mkdirat(0700)`, set to 0711, opened and checked to be a real directory of yours. One already there is checked the same way (and that others cannot write to it); when it fails, B falls back to `sockat-<uid>.<random>` in the same place
- `$XDG_RUNTIME_DIR` and `$TMPDIR` are not used when empty or relative
- A process keeps using the first B it could make (the length of the path does not change it). When a spelling is known beforehand not to fit, no B is made and the route is passed over as unusable
- B in the socket's directory is used only for a spelling through `/proc` (its real-path spelling is longer than the path). Where there is no `/proc`, it serves only as the place of thread-cwd's / at's D
- A B or D held but since removed is made again by the next call

None of the routes changes the working directory of the process or of the calling thread, so any goroutine may call them.

### Routes that depart from the SPEC

| Route | sockat-go | Why |
|---|---|---|
| widened (after relative in the connect of Apple's systems) | None (listed in `Skipped` as `no widened route in sockat-go`; the next route is tried) | Handing a socket of our own to the standard type leaves `RemoteAddr()` nil, whose `String()` panics, when the peer bound in the widened form (`syscall` refuses an address longer than `sockaddr_un`) |

Without widened, on Apple's systems, a connect to a path whose absolute or relative form is at most 253 bytes is `ENAMETOOLONG` when relative does not fit and neither thread-cwd-direct (no `__pthread_fchdir`, or a file name over 103 bytes) nor link-file (no B can be made) can be used. As the SPEC says, no route forks.

### Where a path stops being reached

The file name lengths reached in the usual environment (the thread cwd and `/proc` usable, the places the system's defaults and writable, no `Options`), as estimated by the hub's [reach table](https://github.com/kawaz/sockat/blob/main/pages/reach/README.md) (bytes; the directory is assumed deeper than the standard length):

| System | `Listen` / `ListenEx` | `ResolveForBind` | `Dial` / `ResolveForConnect` |
|---|---|---|---|
| Linux / Android | Up to 96 (thread-cwd). Under a seccomp filter, what link-dir reaches (about 76) | About 76 | Up to 255 |
| macOS | Up to 255 (link-file) | Up to 255 | Up to 255 |
| FreeBSD | Up to 92 (at) | About 72 | Up to 255 (link-file is unconfirmed on FreeBSD; at-direct reaches 103) |
| NetBSD | Up to 255 (link-file) | Up to 255 | Up to 255 |
| OpenBSD | About 72 | About 72 | Up to 255 |
| illumos | About 76 | About 76 | Up to 255 |

- The file name link-dir reaches is the length that fits the sockaddr (107 on Linux and illumos, 103 elsewhere) − 15 − the length of B's real path (16 for `/tmp/sockat-1000`, 23 for macOS's `/private/tmp/sockat-501`). Through `/proc` it is about 73 (it varies with the digits of the pid)
- Where no B can be made (no place writable, say), the routes through B (link-dir, link-file) cannot be used. A connect then reaches what the routes without B (proc-direct, relative, at-direct, thread-cwd-direct) reach
- A path longer than `PATH_MAX` as a whole (1024 bytes on macOS, 4096 on Linux) cannot be opened by other system calls either; it is outside sockat's job and is `ENAMETOOLONG`

## What differs from the standard library

- **A path containing a NUL byte is `EINVAL`, whatever its length.** The standard library silently cuts the path at the NUL and binds another path. On Linux / Android, an abstract socket name (a leading NUL or `@`) is handed to the standard library as it is (`Spelling` `Original`). Where there are no abstract sockets (illumos included), a leading NUL is `EINVAL` too
- **On illumos, a path that starts with `@` is bound / connected as the file `@...` in the cwd.** The standard library rewrites a leading `@` into an abstract name, but the illumos kernel has no abstract sockets. sockat hands the path to it with `./` in front, so the listener's `Addr()` is `./@...` and `Spelling` is `CwdRelative`. Closing the listener does not remove the file (as the standard library elsewhere leaves a path that starts with `@`; to remove it, use `ListenEx`'s `Remove`). A path of 106 or 107 bytes, which no longer fits with `./`, goes through the routes as a long path

Only for a long path:

- **The listener's `Addr()` (and an accepted connection's `LocalAddr()`, the peer's `RemoteAddr()`) is not the path you passed but the bind's spelling.** `Spelling` tells whether it can be used later. The address shown by `ss -x`, `lsof`, `/proc/net/unix` and the like is the spelling too (a link-file spelling holds no file name)
- **What closing the default `Listen`'s listener removes depends on the spelling.** Go's `UnixListener.Close` unlinks the spelling it keeps. Through a link-dir spelling, the E on the way is followed and the socket file is removed (as with the standard library). **Through a link-file spelling, the E itself is removed and the socket file stays.** Through a `Dangling` spelling nothing is removed (it is set `SetUnlinkOnClose(false)`). The listener of `ListenEx` removes nothing, whatever the spelling
- **A path that does not end with a file name** (`dir/`, `dir/.`, `dir/..`) is `EINVAL`, and nothing is created
- **The directory is looked up first**: the E of link-dir and of thread-cwd / at points to the socket's directory's real path, and proc-direct, at-direct and thread-cwd-direct look the directory up before passing the file name. A rename or a symlink repointed in between is not seen
- **Another goroutine's `os.Chdir`**: when the cwd changes during relative (a connect), the result is dropped and an error is returned. A relative `ListenEx` (short paths too) whose cwd changes between the resolving of the real path and the bind closes the listener, leaves the file, and returns an error
- **State piles up outside the application's places**: B, D and E stay in `/tmp`, `$XDG_RUNTIME_DIR`, `$TMPDIR` (and the socket's directory) ("[What stays in the places, and `Sweep`](#what-stays-in-the-places-and-sweep)")
- **Where a live D may be removed**: Linux with systemd older than v242, and B in macOS's `$TMPDIR`. Its spellings can then no longer be followed, and that is all
- **A peer with `PrivateTmp`**: when the peer's `/tmp` is a private one, it cannot follow a spelling through `/tmp`. On Linux the spelling through `/proc` is taken first, so this rarely happens. `Spelling.UsableWithPrivateTmp` tells whether it can

## Documentation

- [sockat specification](https://github.com/kawaz/sockat/blob/main/docs/SPEC.md) — the language-independent behaviour (operations, resolve, spelling properties, cleanup, routes, errors)
- [docs/decisions/](./docs/decisions/INDEX.md) — the design decisions of the Go implementation (DRs, in Japanese)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## License

MIT License, Yoshiaki Kawazu (@kawaz)
