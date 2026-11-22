# Use DGRAM

> English | [日本語](./datagram-ja.md)

Part of the [user manual](../MANUAL.md). The code runs as Example functions in [example_datagram_test.go](../../example_datagram_test.go) (`path`, `serverPath` and `clientPath` are sockets' paths, of any length).

## Receive and send

On the receiving side write `sockat.ListenUnixgram` instead of `net.ListenUnixgram`, and on the sending side `sockat.DialUnixgram` instead of `net.DialUnix("unixgram", nil, ...)`.

```go
srv, err := sockat.ListenUnixgram(path) // *net.UnixConn
if err != nil {
	log.Fatal(err)
}
defer srv.Close()

c, err := sockat.DialUnixgram(path) // *net.UnixConn, not bound
if err != nil {
	log.Fatal(err)
}
defer c.Close()
if _, err := c.Write([]byte("ping")); err != nil {
	log.Fatal(err)
}
```

## Get a reply

Bind the sending side too with `ListenUnixgram`, and give the destination as the spelling of `ResolveForConnect`.

```go
cl, err := sockat.ListenUnixgram(clientPath)
if err != nil {
	log.Fatal(err)
}
defer cl.Close()
to, err := sockat.ResolveForConnect(serverPath, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
defer to.Cleanup()
if _, err := cl.WriteToUnix([]byte("ping"), &net.UnixAddr{Name: to.Path, Net: "unixgram"}); err != nil {
	log.Fatal(err)
}
```

The receiving side replies to the address the datagram came from, as it is.

```go
buf := make([]byte, 16)
_, from, err := srv.ReadFromUnix(buf)
if err != nil {
	log.Fatal(err)
}
if _, err := srv.WriteToUnix([]byte("pong"), from); err != nil {
	log.Fatal(err)
}
```

## Remove the socket file when done

Bind with `ListenUnixgramEx`, and call `b.File.Remove()` before closing.

```go
srv, b, err := sockat.ListenUnixgramEx(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
defer srv.Close()
defer b.File.Remove() // runs before srv.Close
```
