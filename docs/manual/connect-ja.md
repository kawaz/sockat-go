# connect する

> [English](./connect.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。コードは [example_connect_test.go](../../example_connect_test.go) の Example として動かしている (`path` はソケットのパスで、長さは問わない)。

## つなぐ

`net.Dial("unix", path)` の代わりに `sockat.Dial(path)` と書く。返るのは標準ライブラリの `*net.UnixConn`。

```go
c, err := sockat.Dial(path) // *net.UnixConn
if err != nil {
	log.Fatal(err)
}
defer c.Close()
fmt.Fprintln(c, "ping")
```

## HTTP クライアントからつなぐ

`http.Transport` の `DialContext` で `sockat.Dial` を呼ぶ。URL のホスト名は何でもよい。

```go
client := &http.Client{Transport: &http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return sockat.Dial(path)
	},
}}
res, err := client.Get("http://unix/")
if err != nil {
	log.Fatal(err)
}
defer res.Body.Close()
```

## 同じパスに何度もつなぐ

短い綴りを 1 度だけ作って使い回す。[resolve](resolve-ja.md#何度も-connect-する) を使う。
