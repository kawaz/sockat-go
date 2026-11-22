# SEQPACKET を使う

> [English](./seqpacket.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。コードは [example_seqpacket_test.go](../../example_seqpacket_test.go) の Example として動かしている (`path` はソケットのパスで、長さは問わない)。

SEQPACKET 用の関数は無いので、[resolve](resolve-ja.md) で作ったパスを標準の `"unixpacket"` のソケットに渡す。使える OS は Linux・FreeBSD・NetBSD・OpenBSD・illumos (macOS の標準は SEQPACKET のソケットを作れない)。

## 待ち受ける

`ResolveForBind` の `f.Path` で listen する。listener は閉じてもファイルを消さないようにし、消すのは [`Remove`](#終わる時にソケットのファイルを消す) に任せる。

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

## つなぐ

`ResolveForConnect` の `to.Path` で connect する。つないだ後は綴りが要らないので、すぐ `Cleanup` してよい。

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

## 終わる時にソケットのファイルを消す

listen の後・listener を閉じる前に `f.SocketFile()` を受け取り、その `Remove` で消す (`defer` は `l.Close` を先に書く)。

```go
sf, err := f.SocketFile() // after the bind, before l.Close
if err != nil {
	log.Fatal(err)
}
defer sf.Remove() // removes the file, then cleans up f's symlink
```
