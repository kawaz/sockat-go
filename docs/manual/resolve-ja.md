# パスだけを解決する (resolve)

> [English](./resolve.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。コードは [example_resolve_test.go](../../example_resolve_test.go) の Example として動かしている (`path` はソケットのパスで、長さは問わない)。

パスを受け取るライブラリに長いパスを渡す時に、同じ所を指す短いパス (綴り) を作って、それを渡す。

## 他のライブラリに bind させる

`ResolveForBind` の `f.Path` で bind させ、bind の後・listener を閉じる前に `f.SocketFile()` を受け取っておく。消すのはその `Remove`。

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

## 何度も connect する

`ResolveForConnect` の `f.Path` は、`Cleanup` するまで何度でも使える。他のライブラリに渡して connect させる時も同じ。

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
