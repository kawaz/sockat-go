# sockat-go

> English | [日本語](./README-ja.md)

Your Unix domain socket path is too long to bind / connect. sockat-go is a Go library that makes that go away.

With Go's standard library, a path that is too long ends in errors like these:

```text
listen unix /…/app.sock: bind: invalid argument
dial unix /…/app.sock: connect: invalid argument
```

A socket in a deep temporary directory (macOS's `$TMPDIR` alone is about 50 bytes, and `t.TempDir()` nests further; [golang/go#62614](https://github.com/golang/go/issues/62614)) runs into them at once.

Replace the standard library's Listen / Dial with sockat's, and long paths just work.

```go
// l, err := net.Listen("unix", path)
l, err := sockat.Listen(path)

// c, err := net.Dial("unix", path)
c, err := sockat.Dial(path)
```

You get back the same types as the standard library (`*net.UnixListener` / `*net.UnixConn`), and short paths go to the standard library unchanged, as before. Only for a long path does sockat quietly set up a short way to the same place and connect through it. You don't need to shorten paths yourself or care about each OS's limit.

## Installation

```sh
go get github.com/kawaz/sockat-go
```

## More

- [User manual](./docs/MANUAL.md)
- [Reference](./docs/REFERENCE.md)
- [How it works](https://github.com/kawaz/sockat/blob/main/pages/technical.md)

## License

MIT License, Yoshiaki Kawazu (@kawaz)
