# Use SEQPACKET

> English | [日本語](./seqpacket-ja.md)

Part of the [user manual](../MANUAL.md). The code runs as Example functions in [example_seqpacket_test.go](../../example_seqpacket_test.go) (`path` is the socket's path, of any length).

There are no functions for SEQPACKET: hand a path made by [resolve](resolve.md) to the standard `"unixpacket"` sockets. The systems where this works are Linux, FreeBSD, NetBSD, OpenBSD and illumos (the standard library on macOS cannot make SEQPACKET sockets).

## Listen

Listen at `f.Path` of `ResolveForBind`. Keep the listener from removing the file when closed, and leave that to [`Remove`](#remove-the-socket-file-when-done).

```go
f, err := sockat.ResolveForBind(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
l, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: f.Path, Net: "unixpacket"})
if err != nil {
	f.Cleanup()
	log.Fatal(err)
}
l.SetUnlinkOnClose(false) // the file is SocketFile's to remove
defer l.Close()
go func() {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return // l is closed
		}
		go func() {
			defer c.Close()
			buf := make([]byte, 1024)
			for {
				n, err := c.Read(buf) // one message
				if err != nil {
					return
				}
				c.Write(buf[:n])
			}
		}()
	}
}()
```

## Connect

Connect to `to.Path` of `ResolveForConnect`. The connection does not need the spelling, so `Cleanup` it right away.

```go
to, err := sockat.ResolveForConnect(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
c, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: to.Path, Net: "unixpacket"})
to.Cleanup() // the connection does not need the spelling
if err != nil {
	log.Fatal(err)
}
defer c.Close()
```

## Remove the socket file when done

Take `f.SocketFile()` after the listen, before closing the listener, and remove the file with its `Remove` (with `defer`, write `l.Close` first).

```go
sf, err := f.SocketFile() // after the bind, before l.Close
if err != nil {
	log.Fatal(err)
}
defer sf.Remove() // removes the file, then cleans up f's symlink
```
