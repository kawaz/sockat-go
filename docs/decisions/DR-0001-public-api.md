# DR-0001: 公開 API は Listen / Dial の 2 つで、標準の具体型を返す

- Status: Accepted
- Date: 2026-11-18

## Context

[ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) は bind / connect の操作 (bind、connect、connect_nonblocking、connect_any 系) を決め、名前は言語の慣習に合わせてよいとしている。Go の標準は `net.ListenUnix` / `net.DialUnix` で、Dial は元から待たない (backlog が満杯なら Linux は即 `EAGAIN`、macOS は `ECONNREFUSED`。実測)。Go に fork の経路は無い ([DR-0004](DR-0004-thread-cwd-route.md))。

## Decision

| 関数 | 戻り値 | 対応する標準 |
|---|---|---|
| `sockat.Listen(path string)` | `(*net.UnixListener, error)` | `net.ListenUnix("unix", ...)` |
| `sockat.Dial(path string)` | `(*net.UnixConn, error)` | `net.DialUnix("unix", nil, ...)` |
| `sockat.ListenEx(path string)` | `(*net.UnixListener, discard func() error, error)` | (SPEC の後始末つきの bind) |

- 名前は Go の `net` の語彙 (Listen / Dial) に合わせる
- 戻り値は具体型。`net.Listener` / `net.Conn` にそのまま代入でき、`AcceptUnix` 等も使える
- Dial は 1 つ。SPEC の「標準の connect が元から待たない言語」に当たるので `_nonblocking` を分けない
- `connect_any` 系は出さない (fork の経路が無い言語は出さない、SPEC)
- `ListenEx` は bind の直後にソケットのファイルの realpath と `(st_dev, st_ino)` を覚える (覚えられなければ listener を閉じ、ファイルは消さずにエラー)。`discard` はそのパスが今も同じファイルの時だけ消す。listener は閉じない (利用者が閉じる)。既に無い (確かめた後に消えていた時も)・別のファイルに置き換わっていて消さない時は nil。エラーを返すのは、確かめられない時 (lstat が ENOENT / ENOTDIR 以外で失敗、EACCES 等) と消そうとして失敗した時 (消せたか分からないのを nil に見せない)。2 回目以降は何もしない。listener は短いパスも `SetUnlinkOnClose(false)` (消すのは discard だけ)
  - 型を足さず関数を返すのは、`context.WithCancel` と同じ形で、listener を標準の型のまま渡せるから
- エラーは標準と同じ形の `*net.OpError` (`Op` は `listen` / `dial`、`Addr` は呼び出し元のパス、`Err` は `*os.SyscallError`)。`errors.Is(err, syscall.ENOENT)` 等が効く。経路が渡した綴りはエラーに出さない

## 理由

- 標準の代わりに置き換えるだけで使えることが責務。Go の利用者が探す名前は Listen / Dial
- interface で返すと `*net.UnixListener` への型 assertion が要り、長いパスでだけ別の型になる余地が生まれる

## 却下した案

| 案 | 却下の理由 |
|---|---|
| SPEC の名前どおり `Bind` / `Connect` | Go の `net` の語彙と違い、標準から置き換える時に探しにくい |
| `net.Listener` / `net.Conn` で返し、長いパスでは Close で元のパスを消す wrapper にする | 標準と同じ型でなくなる。[DR-0007](DR-0007-long-path-listener-keeps-socket-file.md) |
| `DialContext` を足す | Unix domain の connect は呼び出しの中で終わるので、待つものが無い |

## 未確定

- `ListenEx` で bind は通ったのにファイルを覚えられない (realpath / lstat が失敗する) 分岐は試験していない。その状況を試験で作れない (compile と通常の分岐のみ確かめた)

## 判断の出所

- `ListenEx` の形 (関数を返す、discard は消すだけ): ハブ D12-Q4 の統括推し、kawaz の裁定待ち 2026-11-19 (名前は仮)

- 名前と戻り値の型: 実装側の判断 2026-11-18
- Dial を 1 つにすること: ハブの裁定 (HB-Q2) 2026-11-18
