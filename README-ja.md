# sockat-go

> [English](./README.md) | 日本語

Unix domain socket のパスが長すぎて bind / connect できない。sockat-go は、それを無くす Go のライブラリです。

Go の標準では、長すぎるパスはこんなエラーになります。

```text
listen unix /…/app.sock: bind: invalid argument
dial unix /…/app.sock: connect: invalid argument
```

深い一時ディレクトリ (macOS の `$TMPDIR` はそれだけで 50 バイト前後、`t.TempDir()` がさらに掘る。[golang/go#62614](https://github.com/golang/go/issues/62614)) にソケットを置くと、すぐに当たります。

標準の Listen / Dial を sockat に置き換えるだけで、長いパスでもそのまま動きます。

```go
// l, err := net.Listen("unix", path)
l, err := sockat.Listen(path)

// c, err := net.Dial("unix", path)
c, err := sockat.Dial(path)
```

返ってくるのは標準と同じ型 (`*net.UnixListener` / `*net.UnixConn`) で、短いパスは今までどおり標準にそのまま渡します。長いパスの時だけ、sockat が同じ場所を指す短い道を裏で用意してつなぎます。パスを短くする工夫も、OS ごとの上限も、気にしなくて構いません。

## 入れ方

```sh
go get github.com/kawaz/sockat-go
```

## もっと詳しく

- [利用マニュアル](./docs/MANUAL-ja.md)
- [リファレンス](./docs/REFERENCE-ja.md)
- [技術説明](https://github.com/kawaz/sockat/blob/main/pages/technical-ja.md)

## ライセンス

MIT License, Yoshiaki Kawazu (@kawaz)
