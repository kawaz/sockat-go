# sockat-go リファレンス

> [English](./REFERENCE.md) | 日本語

関数と型の説明は [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) にある。言語によらない振る舞いの正本は [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) で、sockat-go はその Go 実装。ここには Go での名前と型の対応、Go での形、Go に固有の注意を置き、言語によらない説明は仕様の該当の節へリンクする。使い方は [利用マニュアル](MANUAL-ja.md)。

## 標準との対応

- 戻り値は標準の `*net.UnixListener` / `*net.UnixConn` そのもの (`net.Listener` / `net.Conn` にそのまま代入できる)
- 標準が受け付ける長さの絶対パス (Linux / Android / illumos は 107 バイト、Apple の OS と BSD は 103 バイトまで) は標準にそのまま渡すので、標準と同じに振る舞う
- 相対パスは、bind 系 (`Listen`・`ListenEx`・`ListenUnixgram`・`ListenUnixgramEx`) と resolve では絶対パスにしてから扱い、connect 系 (`Dial`・`DialEx`・`DialUnixgram`・`DialUnixgramEx`) では相対のまま扱う。仕様の「[相対パスを絶対パスにする](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#相対パスを絶対パスにする)」
- 長いパスでも、ソケットのファイルは渡したパスにできる
- エラーは標準と同じ `*net.OpError`。`errors.Is(err, syscall.ENOENT)` 等が効く
- 依存は `golang.org/x/sys` だけ。Go は支援中の 2 版 (1.26 以降)
- 長いパスと相対パスの bind では、標準と違う点がある (カーネルが返すアドレス、アプリの置き場の外に残る物等)。仕様の「[長いパスで標準と違う点](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#長いパスで標準と違う点)」と「[Go に固有の注意](#go-に固有の注意)」

## 関数

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
- DGRAM の `send_to` と SEQPACKET は SPEC で未確定なので無い。SEQPACKET は resolve の綴りを標準の `"unixpacket"` のソケットに渡して使う (利用マニュアルの「[SEQPACKET を使う](manual/seqpacket-ja.md)」)

## 型

| SPEC の型 | Go の型 |
|---|---|
| `Bound` | `*Bound` (`File *SocketFile`、`Spelling`、`Route`、`Base`、`Skipped`) |
| `Connected` / `Report` | 標準の `*net.UnixConn` と `Report` (`Route`、`Skipped`) |
| `ForBind` | `*ForBind` (`Path`、`Spelling`、`Cleanup()`、`SocketFile()`、診断) |
| `ForConnect` | `*ForConnect` (`Path`、`Spelling`、`Cleanup()`、診断) |
| `SocketFile` | `*SocketFile` (`Remove()`、`Cleanup()`) |
| `_ex` と resolve の失敗 | `*RouteError` (`Route`、`Skipped`。`Unwrap` で errno に届く) |
| 引数の誤り | `EINVAL` |

## `Options`

項目の意味とエラーは仕様の「[`Options`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#options)」と「[エラー](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#エラー)」。

| SPEC | Go |
|---|---|
| `usable_cross_uid` | `UsableCrossUID bool`。bind 系 (`ListenEx`・`ListenUnixgramEx`・`ResolveForBind`) だけが見る |
| `base` | `Base string` (`""` は指定しない) |

- ゼロ値の `Options{}` が既定で、`ListenEx` / `DialEx` は `Listen` / `Dial` と同じ経路を同じ順に試す
- `Base` の形の誤り (相対パス、最後が名前でない、NUL を含む) は、パスの長さによらず `EINVAL`

## 綴りの性質 (`Spelling`)

長いパスでは、listener の `Addr()`・accept した接続の `LocalAddr()`・相手の `RemoteAddr()` は渡したパスでなく bind の綴り。`Bound.Spelling` (と `ForBind.Spelling`・`ForConnect.Spelling`) がその綴りを後で使えるかを返す。値の意味は仕様の「[綴りの性質](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#綴りの性質-spelling)」。

| SPEC | Go |
|---|---|
| `Original` / `CwdRelative` / `Linked` / `Dangling` | `Spelling.Kind` の `sockat.Original` / `sockat.CwdRelative` / `sockat.Linked` / `sockat.Dangling` |
| `removable` | `Spelling.Removable` |
| `until` (`Process` / `Cleanup`) | `Spelling.Until` (`sockat.UntilProcess` / `sockat.UntilCleanup`) |
| `usable_cross_uid` | `Spelling.UsableCrossUID` |
| `usable_with_private_tmp` | `Spelling.UsableWithPrivateTmp` |

`ExampleSpelling` ([example_long_test.go](../example_long_test.go)):

```go
switch sp := b.Spelling; sp.Kind {
case sockat.Original, sockat.CwdRelative:
	fmt.Println("the path itself")
case sockat.Linked:
	// sp.Until: how long l.Addr() can be followed.
	// sp.UsableCrossUID: a peer of another uid can follow it.
	fmt.Println("Linked; unlinking it removes the socket file:", sp.Removable)
case sockat.Dangling:
	fmt.Println("names nothing: keep path")
}
```

- `Linked` の時だけ意味を持つ項目は、他の `Kind` では false かゼロ値
- `Spelling` は `==` で比べられ、`Kind` のゼロ値は使わない (失敗した時のゼロ値を `Original` と読ませない)。`Kind.String()` と `Until.String()` は SPEC の名前 (`"Linked"`、`"Process"` 等) を返す
- `CwdRelative` が返るのは、相対パスを絶対パスにしてもどの経路も使えず、相対のまま標準に渡した時 (と、相対のまま返した resolve) だけ
- `ForConnect.Spelling` は `Original`・`CwdRelative`・`Linked` のどれか

## 診断 (`Route`・`Base`・`Skipped`) と `RouteError`

項目の意味は仕様の「[診断](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#診断)」。どれも診断用で中身を約束しないので、分岐は `Spelling` で書く。

`ExampleDialEx` ([example_long_test.go](../example_long_test.go)):

```go
c, rep, err := sockat.DialEx(path, sockat.Options{})
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
	}
	log.Fatal(err)
}
defer c.Close()
```

- 成功した時は `rep.Route`・`rep.Skipped` (`ListenEx` は `b.Route`・`b.Base`・`b.Skipped`) に同じ物が入る
- `Skipped` は nil にならない。`Bound.Base` は綴りが B を通らない時 `""`
- `*sockat.RouteError` は `Unwrap` で `Listen` / `Dial` と同じ `*net.OpError` に届くので、`errors.Is(err, syscall.ENOENT)` 等が効く

## ソケットのファイルを消す (`SocketFile`)

`Remove()` と `Cleanup()` が何をするか (同じファイルの時だけ消す、成功の後は何もしない、失敗の後はやり直す) は仕様の「[`remove`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#remove)」と「[`remove` と `cleanup`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#remove-と-cleanup)」。使い方は利用マニュアルの「[終わる時にソケットのファイルを消す](manual/bind-ja.md#終わる時にソケットのファイルを消す)」。

- `Remove()` は listener を閉じる前に呼ぶ。`defer` は `l.Close` を先に書けば、`Remove` が先に走る (`ExampleListenEx`)
- `Remove()` はファイルを消すだけで、listener を閉じない
- `ListenEx` と `ListenUnixgramEx` のソケットは Close でファイルを消さない (短いパスも。`SetUnlinkOnClose(false)`)。消すのは `Remove` だけ
- `Remove()` はファイルを消す側と `Cleanup` のエラーを `errors.Join` でまとめて返す。どちらも自動では呼ばれない

## resolve (`ForBind`・`ForConnect`)

返す綴りと使えない時は仕様の「[resolve](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#resolve)」と「[`ForBind` の `SocketFile`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#forbind-の-socketfile)」。使い方は利用マニュアルの「[パスだけを解決する](manual/resolve-ja.md)」。

- 自分で `net.ListenUnix` に綴りを渡した listener は、`SetUnlinkOnClose(false)` にして `SocketFile` の `Remove` で消す (標準の Close は綴りで unlink する。「[Go に固有の注意](#go-に固有の注意)」)
- `ForBind.SocketFile()` は bind の後・listener を閉じる前に呼ぶ。エラーは、bind の前なら `ENOENT`、辿った先がソケットでなければ `ENOTSOCK`
- resolve のエラーは `*RouteError` で、中は `*os.PathError` (`Op` は `resolve`、`Path` は渡したパス)。`ResolveForBind` が `ENAMETOOLONG` を返す長さ (thread-cwd・at でしか届かない) でも、`Listen` / `ListenEx` は届く

## 置き場に残る物と `Sweep`

長いパスの bind は、アプリの置き場の外 (`/tmp/sockat-<uid>` 等) に B・D・E を残す。何が残り、いつ消えるか、`Sweep` が何を消すか、長命のプロセスでの片付け方は仕様の「[置き場に残る物](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#置き場に残る物)」と「[`sweep`](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#sweep)」。Go での形:

- `sockat.Sweep()` は置き場 1〜3 の死んだ D を、`sockat.SweepBase(base)` は `Options.Base` に渡した B の中の死んだ D を消す。自動では呼ばれない。試験を何度も回す環境では `sockat.Sweep()` を 1 回呼ぶ (`ExampleSweep`)
- 既定の `Listen` と `ListenUnixgram` は `SocketFile` を返さないので、その E はプロセスが終わるまで残る。link-dir の E は同じディレクトリへの bind で使い回すので、増えるのはディレクトリの数だけ。毎回違うディレクトリへ長いパスで bind する長命のプロセスは `ListenEx` を使い、要らなくなったソケットごとに `b.File.Remove()` (ファイルも消す) か `b.File.Cleanup()` (E だけ) を呼ぶ

## 使ってはいけない書き方

なぜいけないかは仕様の「[使ってはいけない書き方](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#使ってはいけない書き方)」。Go での形と避け方を並べる。避け方のコードは Example 関数 (`example_*_test.go`) として動かしている。

| Go での形 | 代わりに |
|---|---|
| `Spelling` を見ずに `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` | 元のパスを持っておいて使う。`b.File.Remove()` (`ExampleListenEx`) |
| `Cleanup` の後やプロセスが終わった後に、古い綴り (`ForBind.Path`、`ForConnect.Path`、`l.Addr()`) を使う | 綴りは `Spelling.Until` の間だけ使う |
| 相対パスで Listen し、`os.Chdir` の後にその相対パスで `os.Remove` する | `b.File.Remove()`。自分で絶対パスを持つ時は、`os.Getwd()` を前に付ける (`filepath.Abs` は字句で `..` を畳むので使わない。`ExampleListenEx_absolutePath`) |
| `l.Close()` だけでソケットのファイルが消えると当てにする | `b.File.Remove()` か、元のパスで `os.Remove(path)` |
| `b.File.Remove()` で listener も閉じると当てにする | `defer l.Close()` を自分で書く |
| `l.Close()` の後に `b.File.Remove()` を呼ぶ | listener を閉じる前に呼ぶ (`defer` は `l.Close` を先に書く) |
| `/proc` を通る綴りや `Spelling.UsableCrossUID` が false の綴りを、別の uid の相手に辿らせる | `Options{UsableCrossUID: true}` を渡し、結果の `Spelling` を確かめる (`ExampleOptions_usableCrossUID`) |
| `Route`・`Base`・`Skipped` の `Name` / `Reason` の文字列で分岐する | `Spelling` で分岐する (`ExampleSpelling`) |

## 長いパスへの届き方

標準に渡す長さを超えるパスは、下の経路を左から順に試し、最初に使えた経路で bind / connect する。bind と resolve は、経路の前にソケットのディレクトリの実パスで収まるかを見て、収まればそれを標準に渡す (仕様の「[相対パスを絶対パスにする](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#相対パスを絶対パスにする)」)。経路の名前と試す順番は仕様の「[長いパスの経路と試す順番](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#長いパスの経路と試す順番)」、各経路の作り方と B の作り方は [SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md#経路)、どこまで届くかはハブの [届く範囲の表](https://github.com/kawaz/sockat/blob/main/pages/reach/README-ja.md)。

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
- **illumos では `@` で始まるパスを cwd の `@...` のファイルとして扱う**。標準は先頭の `@` を abstract の名前に書き換えるが、illumos のカーネルには abstract socket が無い。bind では他の相対パスと同じに絶対パスにする。相対のまま標準に渡す時 (connect と、bind で絶対パスにしてもどの経路も使えない時) は `./` を付けて渡すので、その listener の `Addr()` は `./@...` を返し、`Spelling` は `CwdRelative`、Close はファイルを消さない (他の OS の標準が `@` で始まるパスを消さないのとそろえる。消すなら `ListenEx` の `Remove`)。`./` を付けて上限を超える 106・107 バイトのパスは長いパスとして経路に回る
- **標準の `UnixListener.Close` は覚えている綴りを unlink する**。既定の `Listen` の長いパスの listener では、link-dir の綴りは途中の E を辿ってソケットのファイルを消す (標準と同じ)。**link-file の綴りは E 自体が消え、ソケットのファイルは残る**。`Dangling` の綴りでは何も消さない (`SetUnlinkOnClose(false)` にしてある)。相対パスの `Listen` の綴りは (相対のまま標準に渡した時を除き) cwd によらないので、後で cwd を変えても Close は上と同じに働く。`ListenEx` の listener はどの綴りでも、短いパスでも消さない (`SetUnlinkOnClose(false)`。消すのは `Remove` だけ)
- **macOS の広げた sockaddr (widened) の経路が無い**。理由と届かなくなる所は「[SPEC から外れる経路](#spec-から外れる経路)」

## 仕様と設計

- [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) — 言語によらない振る舞い (公開する操作、resolve、綴りの性質、片付け、置き場に残る物、経路、標準と違う点、使ってはいけない書き方、エラー)
- [言語ごとの注意](https://github.com/kawaz/sockat/blob/main/pages/langs-ja.md) — 各言語の実装に固有の注意の一覧
- [docs/decisions/](./decisions/INDEX.md) — Go 版の設計の判断 (DR)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API
