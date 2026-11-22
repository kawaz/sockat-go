# sockat-go 利用マニュアル

> [English](./MANUAL.md) | 日本語

sockat-go (`sun_path` の上限を超えるパスでも Unix domain socket を bind / connect できる Go のライブラリ) の利用マニュアル。上限は NUL 込みで macOS と BSD は 104 バイト、Linux は 108 バイト。入口は [README](../README-ja.md)。

言語によらない振る舞いの正本は [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) で、sockat-go はその Go 実装。このマニュアルには Go での名前と形、Go に固有の注意だけを書き、言語によらない説明は仕様の該当の節へリンクする。

```go
l, err := sockat.Listen("/とても/深い/ディレクトリ/.../server.sock") // *net.UnixListener
c, err := sockat.Dial("/とても/深い/ディレクトリ/.../server.sock")   // *net.UnixConn
```

## 使い方

標準の `net.ListenUnix` / `net.DialUnix` (`net.Listen("unix", ...)` / `net.Dial("unix", ...)`) の代わりに `sockat.Listen` / `sockat.Dial` を使えばよい。

```sh
go get github.com/kawaz/sockat-go
```

- 戻り値は標準の `*net.UnixListener` / `*net.UnixConn` そのもの (`net.Listener` / `net.Conn` にそのまま代入できる)
- 標準が受け付ける長さのパス (Linux / Android / illumos は 107 バイト、Apple の OS と BSD は 103 バイトまで) は標準にそのまま渡すので、標準と同じに振る舞う
- 長いパスでも、ソケットファイルは渡したパスにできる
- エラーは標準と同じ `*net.OpError`。`errors.Is(err, syscall.ENOENT)` 等が効く
- 依存は `golang.org/x/sys` だけ。Go は支援中の 2 版 (1.26 以降)
- 長いパスでは標準と違う点がある (カーネルが返すアドレス、アプリの置き場の外に残る物等)。仕様の「[長いパスで標準と違う点](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#長いパスで標準と違う点)」と、このマニュアルの「[Go に固有の注意](#go-に固有の注意)」を読む

### 関数

| 関数 | 返す物 | SPEC の関数 |
|---|---|---|
| `Listen(path)` | `*net.UnixListener` | `bind` |
| `Dial(path)` | `*net.UnixConn` | `connect`<br>`connect_nonblocking` |
| `ListenEx(path, Options)` | `*net.UnixListener`<br>`*Bound` | `bind_ex` |
| `DialEx(path, Options)` | `*net.UnixConn`<br>`Report` | `connect_ex`<br>`connect_nonblocking_ex` |
| `ListenUnixgram(path)` | `*net.UnixConn` (DGRAM) | `datagram::bind` |
| `DialUnixgram(path)` | `*net.UnixConn` (DGRAM、bind していない) | `datagram::unbound` と `datagram::connect` |
| `ListenUnixgramEx(path, Options)` | `*net.UnixConn`<br>`*Bound` | `datagram::bind_ex` |
| `DialUnixgramEx(path, Options)` | `*net.UnixConn`<br>`Report` | `datagram::connect_ex` |
| `ResolveForBind(path, Options)` | `*ForBind` | `resolve_for_bind` |
| `ResolveForConnect(path, Options)` | `*ForConnect` | `resolve_for_connect` |
| `Sweep()` | | `sweep` |
| `SweepBase(base)` | | `sweep_base` |

- `Dial` は 1 つ。Go の標準の connect は元から待たない (backlog が満杯なら Linux は即 `EAGAIN`、macOS は `ECONNREFUSED`) ので、`_nonblocking` を分けない
- `ListenEx` 等の `Ex` は、標準の型と一緒に後始末と診断を返す。失敗は `*sockat.RouteError`
- DGRAM の `send_to` と SEQPACKET は SPEC で未確定なので無い

| SPEC の型 | Go の型 |
|---|---|
| `Bound` | `*Bound` (`File *SocketFile`、`Spelling`、`Route`、`Base`、`Skipped`) |
| `Connected` / `Report` | 標準の `*net.UnixConn` と `Report` (`Route`、`Skipped`) |
| `ForBind` | `*ForBind` (`Path`、`Spelling`、`Cleanup()`、`SocketFile()`、診断) |
| `ForConnect` | `*ForConnect` (`Path`、`Cleanup()`、診断) |
| `SocketFile` | `*SocketFile` (`Remove()`、`Cleanup()`) |
| `_ex` と resolve の失敗 | `*RouteError` (`Route`、`Skipped`。`Unwrap` で errno に届く) |
| 引数の誤り | `EINVAL` |

### ソケットのファイルを消す (`ListenEx` と `SocketFile`)

`Remove()` と `Cleanup()` が何をするか (同じファイルの時だけ消す、成功の後は何もしない、失敗の後はやり直す) は仕様の「[`remove`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#remove)」と「[`remove` と `cleanup`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#remove-と-cleanup)」。Go での形:

```go
l, b, err := sockat.ListenEx(path, sockat.Options{}) // l は *net.UnixListener
if err != nil {
	return err
}
defer l.Close()
defer b.File.Remove() // ソケットのファイルを消す (l は閉じない)。l.Close より先に走る
```

- `Remove()` は listener を閉じる前に呼ぶ。`defer` は `l.Close` を先に書けば、`Remove` が先に走る (`ExampleListenEx`)
- `ListenEx` の listener は Close でファイルを消さない (短いパスも。`SetUnlinkOnClose(false)`)。消すのは `Remove` だけ
- `Remove()` はファイルを消す側と `Cleanup` のエラーを `errors.Join` でまとめて返す。どちらも自動では呼ばれない

### 綴りの性質 (`Spelling`)

長いパスでは、listener の `Addr()`・accept した接続の `LocalAddr()`・相手の `RemoteAddr()` は渡したパスでなく bind の綴り。`Bound.Spelling` (と `ForBind.Spelling`) がその綴りを後で使えるかを返す。値の意味は仕様の「[綴りの性質](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#綴りの性質-spelling)」。

| SPEC | Go |
|---|---|
| `Original` / `CwdRelative` / `Linked` / `Dangling` | `Spelling.Kind` の `sockat.Original` / `sockat.CwdRelative` / `sockat.Linked` / `sockat.Dangling` |
| `removable` | `Spelling.Removable` |
| `until` (`Process` / `Cleanup`) | `Spelling.Until` (`sockat.UntilProcess` / `sockat.UntilCleanup`) |
| `usable_cross_uid` | `Spelling.UsableCrossUID` |
| `usable_with_private_tmp` | `Spelling.UsableWithPrivateTmp` |

```go
switch sp := b.Spelling; sp.Kind {
case sockat.Original, sockat.CwdRelative:
	// 元のパスそのもの
case sockat.Linked:
	// sp.Until の間は l.Addr() で元のパスに届く
case sockat.Dangling:
	// l.Addr() はどこにも届かない。元のパスを持っておく
}
```

- `Linked` の時だけ意味を持つ項目は、他の `Kind` では false かゼロ値
- `Spelling` は `==` で比べられ、`Kind` のゼロ値は使わない (失敗した時のゼロ値を `Original` と読ませない)。`Kind.String()` と `Until.String()` は SPEC の名前 (`"Linked"`、`"Process"` 等) を返す

### 診断 (`Route`・`Base`・`Skipped`) と `RouteError`

項目の意味は仕様の「[診断](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#診断)」。どれも診断用で中身を約束しないので、分岐は `Spelling` で書く。

```go
c, rep, err := sockat.DialEx(path, sockat.Options{}) // c は *net.UnixConn
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
	}
	return err
}
log.Printf("dial %s: route %s after %v", path, rep.Route, rep.Skipped)
```

- `Skipped` は nil にならない。`Bound.Base` は綴りが B を通らない時 `""`
- `*sockat.RouteError` は `Unwrap` で `Listen` / `Dial` と同じ `*net.OpError` に届くので、`errors.Is(err, syscall.ENOENT)` 等が効く

### `Options`

項目の意味とエラーは仕様の「[`Options`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#options)」と「[エラー](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#エラー)」。

| SPEC | Go |
|---|---|
| `usable_cross_uid` | `UsableCrossUID bool`。bind 系 (`ListenEx`・`ListenUnixgramEx`・`ResolveForBind`) だけが見る |
| `base` | `Base string` (`""` は指定しない) |

- ゼロ値の `Options{}` が既定で、`ListenEx` / `DialEx` は `Listen` / `Dial` と同じ経路を同じ順に試す
- `Base` の形の誤り (相対パス、最後が名前でない、NUL を含む) は、パスの長さによらず `EINVAL`

### DGRAM

```go
srv, err := sockat.ListenUnixgram(path) // *net.UnixConn
c, err := sockat.DialUnixgram(path)     // *net.UnixConn (bind していない)
```

- `ListenUnixgram` は `Listen` と同じ経路で bind する。Close でファイルを消さないのは標準と同じ
- `DialUnixgram` は bind していないソケットを作って connect する (標準の `net.DialUnix("unixgram", nil, ...)` の形)。`Dial` と同じ経路を試す
- bind したソケットから長いパスへ送る・connect する時は、`ResolveForConnect` で綴りを作って標準に渡す (`ExampleListenUnixgram`):

```go
to, err := sockat.ResolveForConnect(serverPath, sockat.Options{})
if err != nil {
	return err
}
defer to.Cleanup()
_, err = cl.WriteToUnix(msg, &net.UnixAddr{Name: to.Path, Net: "unixgram"})
```

### resolve (`ResolveForBind` / `ResolveForConnect`)

綴りを自分で標準に渡したい時 (他のライブラリにパスを渡す、DGRAM の送り先にする等) に使う。返す綴りと使えない時は仕様の「[resolve](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#resolve)」と「[`ForBind` の `SocketFile`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#forbind-の-socketfile)」。

```go
f, err := sockat.ResolveForBind(path, sockat.Options{})
if err != nil {
	return err // ENAMETOOLONG: Listen の thread-cwd / at でしか届かない長さ
}
l, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.Path, Net: "unix"})
if err != nil {
	f.Cleanup()
	return err
}
l.SetUnlinkOnClose(false) // 消すのは SocketFile の Remove
defer l.Close()
sf, err := f.SocketFile() // bind の後・l を閉じる前に呼ぶ
if err != nil {
	return err
}
defer sf.Remove()
```

```go
f, err := sockat.ResolveForConnect(path, sockat.Options{})
if err != nil {
	return err
}
defer f.Cleanup()
c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.Path, Net: "unix"}) // 何度でも
```

- 自分で `net.ListenUnix` に綴りを渡した listener は、`SetUnlinkOnClose(false)` にして `SocketFile` の `Remove` で消す (標準の Close は綴りで unlink する。「[Go に固有の注意](#go-に固有の注意)」)
- `ForBind.SocketFile()` のエラーは、bind の前なら `ENOENT`、辿った先がソケットでなければ `ENOTSOCK`
- resolve のエラーは `*RouteError` で、中は `*os.PathError` (`Op` は `resolve`、`Path` は渡したパス)

### 置き場に残る物と `Sweep`

長いパスの bind は、アプリの置き場の外 (`/tmp/sockat-<uid>` 等) に B・D・E を残す。何が残り、いつ消えるか、`Sweep` が何を消すか、長命のプロセスでの片付け方は仕様の「[置き場に残る物](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#置き場に残る物)」と「[`sweep`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#sweep)」。Go での形:

- `sockat.Sweep()` は置き場 1〜3 の死んだ D を、`sockat.SweepBase(base)` は `Options.Base` に渡した B の中の死んだ D を消す。自動では呼ばれない。試験を何度も回す環境では `sockat.Sweep()` を 1 回呼ぶ
- 既定の `Listen` と `ListenUnixgram` は `SocketFile` を返さないので、その E はプロセスが終わるまで残る。link-dir の E は同じディレクトリへの bind で使い回すので、増えるのはディレクトリの数だけ。毎回違うディレクトリへ長いパスで bind する長命のプロセスは `ListenEx` を使い、要らなくなったソケットごとに `b.File.Remove()` (ファイルも消す) か `b.File.Cleanup()` (E だけ) を呼ぶ

## 使ってはいけない書き方

なぜいけないかは仕様の「[使ってはいけない書き方](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#使ってはいけない書き方)」。Go での形と避け方を並べる。避け方のコードは試験 ([example_long_test.go](../example_long_test.go) の Example 関数) として動かしている。

| Go での形 | 代わりに |
|---|---|
| `Spelling` を見ずに `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` | 元のパスを持っておいて使う。`b.File.Remove()` (`ExampleListenEx`) |
| `Cleanup` の後やプロセスが終わった後に、古い綴り (`ForBind.Path`、`ForConnect.Path`、`l.Addr()`) を使う | 綴りは `Spelling.Until` の間だけ使う |
| 相対パスで Listen し、`os.Chdir` の後にその相対パスで `os.Remove` する | Listen の前に `os.Getwd()` を前に付けて絶対パスにする (`filepath.Abs` は字句で `..` を畳むので使わない。`ExampleListenEx_absolutePath`)。`b.File.Remove()` |
| `l.Close()` だけでソケットのファイルが消えると当てにする | `b.File.Remove()` か、元のパスで `os.Remove(path)` |
| `b.File.Remove()` で listener も閉じると当てにする | `defer l.Close()` を自分で書く |
| `l.Close()` の後に `b.File.Remove()` を呼ぶ | listener を閉じる前に呼ぶ (`defer` は `l.Close` を先に書く) |
| `/proc` を通る綴りや `Spelling.UsableCrossUID` が false の綴りを、別の uid の相手に辿らせる | `Options{UsableCrossUID: true}` を渡し、結果の `Spelling` を確かめる (`ExampleSpelling`) |
| `Route`・`Base`・`Skipped` の `Name` / `Reason` の文字列で分岐する | `Spelling` で分岐する |

## 長いパスへの届き方

標準に渡す長さを超えるパスは、下の経路を左から順に試し、最初に使えた経路で bind / connect する。経路の名前と試す順番は仕様の「[長いパスの経路と試す順番](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#長いパスの経路と試す順番)」、各経路の作り方と B の作り方は [SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md#経路)、どこまで届くかはハブの [届く範囲の表](https://github.com/kawaz/sockat/blob/main/pages/reach/README-ja.md)。

bind (`Listen`・`ListenEx`・`ListenUnixgram`・`ListenUnixgramEx`):

| OS | 経路 |
|---|---|
| Linux / Android | link-dir (`/proc` を通す綴り → B の実パス) → thread-cwd |
| Apple の OS | link-dir → link-file → thread-cwd |
| FreeBSD | link-dir → at |
| NetBSD | link-dir → link-file |
| OpenBSD / illumos | link-dir |
| その他の Unix | link-dir → link-file (bind が symlink を辿るかを実行時に見分ける) |

connect (`Dial`・`DialEx`・`DialUnixgram`・`DialUnixgramEx`):

| OS | 経路 |
|---|---|
| Linux / Android (`/proc` を使える) | proc-direct → thread-cwd-direct → link-file |
| Linux / Android (`/proc` を使えない) | relative → thread-cwd-direct → link-file |
| Apple の OS | relative → widened (作らない) → thread-cwd-direct → link-file |
| FreeBSD | at-direct → link-file |
| NetBSD / OpenBSD / illumos / その他の Unix | relative → link-file |
| Windows | 対象外 |

resolve: `ResolveForBind` は bind の列の link-dir と (macOS・NetBSD だけ) link-file、`ResolveForConnect` は link-file だけ。

Go での手段:

- **thread-cwd・thread-cwd-direct**: OS スレッドに固定した使い捨ての goroutine で、そのスレッドだけ cwd を移す。使い終わったスレッドは Unlock せずに goroutine ごと捨てる (runtime が捨てない主スレッドでは cwd を移さない)。Linux は `unshare(CLONE_FS)` → `fchdir` (seccomp の filter が掛かった環境では使わない。docker の既定等)、Apple の OS は `__pthread_fchdir`
- **at**: FreeBSD の `bindat(2)` で自前のソケットを bind し、`net.FileListener` で標準の型に渡す
- どの経路もプロセスの cwd も呼び出し元のスレッドの cwd も変えないので、どの goroutine から呼んでもよい

### SPEC から外れる経路

| 経路 | Go 版 | 理由 |
|---|---|---|
| widened (Apple の OS の connect の relative の次) | なし (`Skipped` に `no widened route in sockat-go` で入り、次へ進む) | 自前のソケットを標準の型に渡すと、相手が広げた形で bind している時に `RemoteAddr()` が nil になり `String()` で落ちる (`syscall` が `sockaddr_un` より長いアドレスを断る) |

widened が無く fork もしないので、Apple の OS で relative に収まらず、thread-cwd-direct も (`__pthread_fchdir` が無いかファイル名が 103 バイトを超える) link-file も (B を作れない) 使えない時、元のパスか相対パスが 253 バイト以内の connect は `ENAMETOOLONG` になる。

## Go に固有の注意

Go の標準ライブラリの振る舞いに合わせて、sockat-go が標準と違う形にしている所と、標準から引き継ぐ所。言語によらない違いは仕様の「[長いパスで標準と違う点](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#長いパスで標準と違う点)」。

- **空のパスは `EINVAL`**。標準は空のパスの bind を、Linux ではカーネルが選ぶ abstract の名前に bind し (autobind)、connect は errno の無いエラーで断る
- **NUL を含むパスは長さによらず `EINVAL`**。標準は NUL で黙って切り詰め、別のパスに bind する。Linux / Android では、先頭が NUL か `@` の abstract socket の名前を標準にそのまま渡す (`Spelling` は `Original`)。abstract socket の無い OS (illumos を含む) では先頭の NUL も `EINVAL`
- **引数の誤りの `Err` はむき出しの `syscall.EINVAL` でない**。sockat が自分で見つけた引数の誤り (`Options.Base` の形の誤り、`SweepBase` の形の誤り、名前で終わらない長いパス、空のパス、NUL) の `*net.OpError` / `*os.PathError` の `Err` は、`syscall.EINVAL` を包んだ値。`errors.Is(err, syscall.EINVAL)` で見る (`== syscall.EINVAL` や `.(syscall.Errno)` の型の主張では一致しない)
- **illumos では `@` で始まるパスを cwd の `@...` のファイルとして bind / connect する**。標準は先頭の `@` を abstract の名前に書き換えるが、illumos のカーネルには abstract socket が無い。sockat は `./` を付けて標準に渡すので、listener の `Addr()` は `./@...` を返し、`Spelling` は `CwdRelative`。listener の Close はファイルを消さない (他の OS の標準が `@` で始まるパスを消さないのとそろえる。消すなら `ListenEx` の `Remove`)。`./` を付けて上限を超える 106・107 バイトのパスは長いパスとして経路に回る
- **標準の `UnixListener.Close` は覚えている綴りを unlink する**。既定の `Listen` の長いパスの listener では、link-dir の綴りは途中の E を辿ってソケットのファイルを消す (標準と同じ)。**link-file の綴りは E 自体が消え、ソケットのファイルは残る**。`Dangling` の綴りでは何も消さない (`SetUnlinkOnClose(false)` にしてある)。`ListenEx` の listener はどの綴りでも、短いパスでも消さない (`SetUnlinkOnClose(false)`。消すのは `Remove` だけ)
- **macOS の広げた sockaddr (widened) の経路が無い**。理由と届かなくなる所は「[SPEC から外れる経路](#spec-から外れる経路)」

## ドキュメント

- [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) — 言語によらない振る舞い (公開する操作、resolve、綴りの性質、片付け、置き場に残る物、経路、標準と違う点、使ってはいけない書き方、エラー)
- [言語ごとの注意](https://github.com/kawaz/sockat/blob/main/pages/langs-ja.md) — 各言語の実装に固有の注意の一覧
- [docs/decisions/](./decisions/INDEX.md) — Go 版の設計の判断 (DR)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API
