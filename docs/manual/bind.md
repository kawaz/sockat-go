# Bind

> English | [日本語](./bind-ja.md)

Part of the [user manual](../MANUAL.md). The code runs as Example functions in [example_bind_test.go](../../example_bind_test.go) (`path` is the socket's path, of any length).

## Listen

Write `sockat.Listen(path)` instead of `net.Listen("unix", path)`. It returns the standard library's `*net.UnixListener`.

```go
l, err := sockat.Listen(path) // *net.UnixListener
if err != nil {
	log.Fatal(err)
}
defer l.Close()
go func() {
	for {
		c, err := l.Accept()
		if err != nil {
			return // l is closed
		}
		go func() {
			defer c.Close()
			io.Copy(c, c)
		}()
	}
}()
```

## Hand it to `http.Serve`

Any server that takes a `net.Listener` takes it as it is.

```go
l, err := sockat.Listen(path)
if err != nil {
	log.Fatal(err)
}
defer l.Close() // http.Serve returns
go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "hello")
}))
```

## Remove the socket file when done

Bind with `ListenEx`, and call `b.File.Remove()` before closing the listener (with `defer`, write `l.Close` first).

```go
l, b, err := sockat.ListenEx(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer b.File.Remove() // runs before l.Close
```

## Choose the place yourself

Have sockat make the short way for a long path in a directory the application manages (`Options.Base`). `SweepBase` cleans up what earlier processes left there.

```go
if err := sockat.SweepBase(base); err != nil {
	log.Print(err) // what could not be read or removed
}
l, b, err := sockat.ListenEx(path, sockat.Options{Base: base})
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer b.File.Remove()
```

## Let a process of another user connect

Pass `Options.UsableCrossUID`, and check with `Spelling.UsableCrossUID` whether it was met.

```go
l, b, err := sockat.ListenEx(path, sockat.Options{UsableCrossUID: true})
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer b.File.Remove()
if !b.Spelling.UsableCrossUID {
	log.Print("another uid cannot follow l.Addr(): give it path")
}
```

## Clean up what earlier processes left

Where the same program starts again and again (tests and the like), call `Sweep` once. It leaves what live processes use.

```go
if err := sockat.Sweep(); err != nil {
	log.Print(err) // what could not be read or removed
}
```
