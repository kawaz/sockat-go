# bind する

> [English](./bind.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。コードは [example_bind_test.go](../../example_bind_test.go) の Example として動かしている (`path` はソケットのパスで、長さは問わない)。

## 待ち受ける

`net.Listen("unix", path)` の代わりに `sockat.Listen(path)` と書く。返るのは標準の `*net.UnixListener`。

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

## `http.Serve` に渡す

`net.Listener` を受け取るサーバーにはそのまま渡せる。

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

## 終わる時にソケットのファイルを消す

`ListenEx` で bind し、listener を閉じる前に `b.File.Remove()` を呼ぶ (`defer` は `l.Close` を先に書く)。

```go
l, b, err := sockat.ListenEx(path, sockat.Options{})
if err != nil {
	log.Fatal(err)
}
defer l.Close()
defer b.File.Remove() // runs before l.Close
```

## 置き場を自分で決める

長いパスのために sockat が作る短い道の置き場を、アプリが管理するディレクトリにする (`Options.Base`)。前のプロセスが残した物は `SweepBase` で片付ける。

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

## 別のユーザーのプロセスにつながせる

`Options.UsableCrossUID` を渡し、満たせたかを `Spelling.UsableCrossUID` で確かめる。

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

## 前のプロセスが残した物を片付ける

同じプログラムを何度も起こす所 (試験等) では、`Sweep` を 1 回呼ぶ。生きているプロセスの物は消さない。

```go
if err := sockat.Sweep(); err != nil {
	log.Print(err) // what could not be read or removed
}
```
