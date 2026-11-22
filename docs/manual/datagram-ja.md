# DGRAM を使う

> [English](./datagram.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。コードは [example_datagram_test.go](../../example_datagram_test.go) の Example として動かしている (`path`・`serverPath`・`clientPath` はソケットのパスで、長さは問わない)。

## 受け取る・送る

受け取る側は `net.ListenUnixgram` の代わりに `sockat.ListenUnixgram`、送る側は `net.DialUnix("unixgram", nil, ...)` の代わりに `sockat.DialUnixgram` と書く。

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

## 返信を受け取る

送る側も `ListenUnixgram` で bind し、送り先は `ResolveForConnect` の綴りで渡す。

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

受け取った側は、届いたアドレスにそのまま返信できる。

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

## 終わる時にソケットのファイルを消す

`ListenUnixgramEx` で bind し、閉じる前に `b.File.Remove()` を呼ぶ。

```go
srv, b, err := sockat.ListenUnixgramEx(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
defer srv.Close()
defer b.File.Remove() // runs before srv.Close
```
