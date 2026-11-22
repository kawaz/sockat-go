# Resolve a path only (resolve)

> English | [日本語](./resolve-ja.md)

Part of the [user manual](../MANUAL.md). The code runs as Example functions in [example_resolve_test.go](../../example_resolve_test.go) (`path` is the socket's path, of any length).

To hand a long path to a library that takes a path, make a short path (spelling) that names the same place, and hand it that.

## Let another library bind

Have it bind `f.Path` of `ResolveForBind`, and take `f.SocketFile()` after the bind, before closing the listener. Its `Remove` removes the file.

```go
f, err := sockat.ResolveForBind(path, sockat.Options{})
if err != nil {
	log.Fatal(err) // ENAMETOOLONG where only Listen reaches
}
l, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.Path, Net: "unix"})
if err != nil {
	f.Cleanup()
	log.Fatal(err)
}
l.SetUnlinkOnClose(false) // the file is SocketFile's to remove
defer l.Close()
sf, err := f.SocketFile() // after the bind, before l.Close
if err != nil {
	log.Fatal(err)
}
defer sf.Remove() // removes the file, then cleans up f's symlink
```

## Connect many times

`f.Path` of `ResolveForConnect` can be used any number of times until `Cleanup`. The same goes for handing it to another library to connect.

```go
f, err := sockat.ResolveForConnect(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
defer f.Cleanup()
for range 2 {
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.Path, Net: "unix"})
	if err != nil {
		log.Fatal(err)
	}
	c.Close()
}
```
