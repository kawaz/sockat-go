# DR-0016: 公開 API は SPEC の関数を Go の net の形に写し、`Ex` は標準の型・情報・error の 3 値で返す

- Status: Accepted
- Date: 2026-11-21

## Context

[ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) の「公開する操作」は、関数 (stream と DGRAM の bind / connect と各 `_ex`、resolve の 2 つ、`sweep` の 2 つ)、`Options` (`usable_cross_uid`・`base`)、返す物 (`Bound`・`Connected`・`Report`・`ForBind`・`ForConnect`)、綴りの性質 `Spelling`、片付けの `remove` / `cleanup` を決め、名前と形は各言語の慣習に任せる。設計の正本はハブの [DR-0017](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0017-resolve-through-persistent-base.md)。

Go の事情:

- 標準は `net.ListenUnix` / `net.DialUnix` / `net.ListenUnixgram` で、具体型 `*net.UnixListener` / `*net.UnixConn` を返す
- 標準の connect は元から待たない (backlog が満杯なら Linux は即 `EAGAIN`、macOS は `ECONNREFUSED`。実測)
- 標準には、bind していない DGRAM のソケットを作る関数も、既にあるソケットを connect する関数も無い (`net.DialUnix("unixgram", laddr, raddr)` が作成・bind・connect を 1 回で行う)
- generics はあるが、標準の型を包む generic な struct は標準の API に例が少ない

## Decision

package は `sockat` 1 つ (子 package を作らない)。

| 関数 | 戻り値 | SPEC |
|---|---|---|
| `Listen(path string)` | `(*net.UnixListener, error)` | `bind` |
| `Dial(path string)` | `(*net.UnixConn, error)` | `connect`<br>`connect_nonblocking` |
| `ListenEx(path string, o Options)` | `(*net.UnixListener, *Bound, error)` | `bind_ex` |
| `DialEx(path string, o Options)` | `(*net.UnixConn, Report, error)` | `connect_ex`<br>`connect_nonblocking_ex` |
| `ListenUnixgram(path string)` | `(*net.UnixConn, error)` | `datagram::bind` |
| `DialUnixgram(path string)` | `(*net.UnixConn, error)` | `datagram::unbound` と `datagram::connect` |
| `ListenUnixgramEx(path string, o Options)` | `(*net.UnixConn, *Bound, error)` | `datagram::bind_ex` |
| `DialUnixgramEx(path string, o Options)` | `(*net.UnixConn, Report, error)` | `datagram::connect_ex` |
| `ResolveForBind(path string, o Options)` | `(*ForBind, error)` | `resolve_for_bind` |
| `ResolveForConnect(path string, o Options)` | `(*ForConnect, error)` | `resolve_for_connect` |
| `Sweep()` | `error` | `sweep` |
| `SweepBase(base string)` | `error` | `sweep_base` |

- 名前は Go の `net` の語彙 (Listen / Dial、Unixgram) に合わせる
- connect は `Dial` 1 つ。SPEC の「標準の connect が元から待たない言語」に当たるので `_nonblocking` を分けない
- `DialUnixgram` は bind していないソケットを作って connect する (標準の `net.DialUnix("unixgram", nil, raddr)` の形)。bind したソケットを長いパスへ connect する・送る人は、`ResolveForConnect` で綴りを作って標準に渡す
- DGRAM の `send_to` / `send_to_ex` と SEQPACKET は SPEC で未確定なので作らない

型:

| 型 | 中身 |
|---|---|
| `Options` | `UsableCrossUID bool`、`Base string` (`""` は指定しない)。ゼロ値が既定 |
| `Bound` | `File *SocketFile`、`Spelling Spelling`、診断の `Route string`・`Base string` (B を通らない時は `""`)・`Skipped []Skip` (nil にしない) |
| `Report` | 診断の `Route`・`Skipped` |
| `Skip` | `Name`・`Reason` |
| `SocketFile` | 非公開の項目だけ。method `Remove() error` と `Cleanup() error` |
| `ForBind` | `Path`、`Spelling`、診断の `Route`・`Base`・`Skipped`。method `Cleanup() error` と `SocketFile() (*SocketFile, error)` |
| `ForConnect` | `Path`、`Spelling` (`Original`・`CwdRelative`・`Linked`。[DR-0020](DR-0020-absolutize-relative-path.md))、診断の `Route`・`Skipped`。method `Cleanup() error` |
| `Spelling` | `Kind SpellingKind` と、`Linked` の時だけ意味を持つ `Removable bool`・`Until Until`・`UsableCrossUID bool`・`UsableWithPrivateTmp bool` (他の `Kind` では false かゼロ値) |
| `SpellingKind` | `Original` (1 から)・`CwdRelative`・`Linked`・`Dangling`。`String()` は SPEC の名前 |
| `Until` | `UntilProcess` (1 から)・`UntilCleanup`。`String()` は `"Process"` / `"Cleanup"` |
| `RouteError` | `Route` (実際に bind / connect して失敗した経路。無ければ `""`)、`Skipped`、`Err`。`Unwrap` で `Err` に届く |

エラー:

| 関数 | エラー |
|---|---|
| `Listen`・`Dial`・`ListenUnixgram`・`DialUnixgram` | 標準と同じ形の `*net.OpError` (`Op` は `listen` / `dial`、`Net` は `unix` / `unixgram`、`Addr` は呼び出し元のパス、`Err` は `*os.SyscallError` 等)。経路の綴りはエラーに出さない |
| 各 `Ex` | `*RouteError`。`Unwrap` で上の `*net.OpError` に届く |
| `ResolveForBind`・`ResolveForConnect` | `*RouteError`。`Err` は `*os.PathError` (`Op` は `resolve`、`Path` は呼び出し元のパス) |
| `(*ForBind).SocketFile`・`Remove`・`Cleanup`・`Sweep`・`SweepBase` | `*os.PathError` (複数あれば `errors.Join`) |
| 引数の誤り (SPEC「エラー」と、「短いパス」の空のパス) | `syscall.EINVAL` を包んだ上の型。sockat が自分で見つけた引数の誤りには内部の目印 (`internal/argerr`) を付ける。目印は `Error()` と `errors.Is` を変えず、利用者は名指しできない。適合試験の CLI はこの目印の付いた物だけを `invalid-argument` にし、システムコールや標準が返した `EINVAL` は `EINVAL` と出す |
| 一般のエラー (relative の途中で cwd が変わった) | errno を持たないエラーを包んだ上の型 |
| `Options.Base` の確かめに外れた | `ENOENT` / `ENOTDIR` / `EPERM` を包んだ上の型。`Ex` では `RouteError.Route` は `""`、`Skipped` は空 |

`SocketFile`:

- `ListenEx`・`ListenUnixgramEx` は bind した直後にソケットのファイルの realpath (カーネルと同じに引く。[DR-0018](DR-0018-route-orders-per-os.md)) と `(st_dev, st_ino)` を覚える。覚える場所はハブの SPEC「[remove](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#remove)」のとおりで、link-dir・thread-cwd・at は経路が E に指させた実ディレクトリ (`bindCall.real`、経路を組む時に引いた物) とファイル名を、経路が bind の結果と一緒に返し (`bound.file`)、元のパスを引き直さない。standard と link-file は bind の後に、標準に渡した綴り (元のパスか絶対パスの候補) と E の指す先の realpath を引く (`bound.lookup`。bind の前に引くと、間に途中の symlink が張り替わった時に bind したのと別のファイルを覚え、`Remove` がそれを消す)。元のパスの最後の要素が既にある symlink の時は辿った先のソケットのファイルを覚え、辿った先がソケットでなければ覚えられない時と同じに扱う。覚えられなければ listener を閉じ、作った E を片付け、ソケットのファイルは消さずにエラーを返す
- 綴りが `CwdRelative` の時 (相対パスを相対のまま標準に渡した時、[DR-0020](DR-0020-absolutize-relative-path.md)) は、何かを引く前と、bind と覚えるための realpath の両方の後でプロセスの cwd の `(st_dev, st_ino)` を比べ、違えば覚えられない時と同じに扱う。絶対の綴りで bind した相対パスは、覚える物が cwd によらないので比べない
- `Remove` はそのパスが今も同じファイルの時だけ消し、その後 `Cleanup` もする。listener は閉じない。既に無い・置き換わっていた時は nil。エラーを返すのは確かめられない時 (`lstat` が `ENOENT` / `ENOTDIR` 以外で失敗) と消すのに失敗した時で、その時も `Cleanup` まで行う
- `Remove`・`Cleanup` は 1 回目だけ働く。`ForBind` と、その `SocketFile()` が返す物は同じ E の片付けを共有する
- doc と利用マニュアルで `Remove` を listener を閉じる前に呼ぶよう勧め、例の `defer` もその順にする

## 理由

- 標準の代わりに置き換えるだけで使えることが責務。Go の利用者が探す名前は Listen / Dial / ListenUnixgram で、戻り値が標準の具体型なら `net.Listener` / `net.Conn` にそのまま代入でき、`AcceptUnix` 等も使える
- `Ex` の 3 値は、第 1 戻り値が標準の型のままで `Listen` から置き換えやすく、stream と DGRAM で同じ `*Bound` を使えて generics が要らない
- 消す口は `Remove` と `Cleanup` の 2 つがあり、`ListenEx` と `ForBind.SocketFile()` が同じ物を返すので、型にして method で持つ。2 つの関数が同じ E を片付けることが型に出る
- `Spelling` を struct にすると `==` で比べられ、`switch s.Kind` で分岐できる。`Kind` と `Until` のゼロ値を使わないので、失敗した時のゼロ値を `Original` と読ませない
- `Options.Base` を `string` にするのは Go の標準の Config 系と同じ (ゼロ値が既定)
- `DialUnixgram` を 1 つにするのは、Go の標準に `unbound()` と `connect(&sock, path)` を別々に写す先が無いため。bind したソケットを長いパスへ connect する形は、resolve の綴りを標準に渡せば足りる

## 却下した案

| 案 | 却下の理由 |
|---|---|
| SPEC の名前どおり `Bind` / `Connect` | Go の `net` の語彙と違い、標準から置き換える時に探しにくい |
| `Bound[S]` (generic な struct に socket を入れる) | 2 種類の socket のためだけに型引数が要り、標準の型を取り出す手間が増える |
| `Ex` の戻り値に `remove`・`cleanup`・`Spelling`・診断を並べる (4 値以上) | 数が増えすぎる |
| 消す口を `func() error` で返す | 口が 2 つになり、`ForBind` の `SocketFile()` も同じ物を返すので、2 つの関数が同じ E を片付けることが型に出ない |
| `Spelling` を sealed interface と型 switch にする | 値の比較ができず、Go の標準に例が少ない |
| `Options.Base` を `*string` にする | 呼び出しが書きにくい |
| `DialUnixgram(laddr, raddr)` で両方の長いパスを受ける | bind の経路 (thread-cwd・at) と connect の経路 (thread-cwd-direct) を 1 つのソケットで行うには自前のソケットが要り、どちらの経路で失敗したかの診断も 2 組になる |
| `net.Listener` / `net.Conn` で返す | 標準と同じ具体型でなくなり、長いパスでだけ別の型になる余地が生まれる |
| `DialContext` を足す | Unix domain の connect は呼び出しの中で終わるので、待つものが無い |

## 判断の出所

- 関数・`Options`・返す物・`Spelling`・片付けの約束: ハブの SPEC と DR-0017
- `Ex` の 3 値・`*SocketFile` の method・`Spelling` の struct・`DialUnixgram` 1 つ: 実装側の案、ハブの答え (2026-11-21、3 言語共通として SPEC に入った)
- 元のパスの最後の要素が symlink の時に辿った先を覚えること: ハブの答え (2026-11-21)
- link-dir・thread-cwd・at は経路が作った実ディレクトリを覚えること: ハブの SPEC「remove」(ハブ 307620d1、2026-11-22)
- `Dial` を 1 つにすること: ハブの裁定 (ハブの DR-0003)
