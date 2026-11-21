# DR-0002: 標準が受け付けるパスと abstract の名前は標準にそのまま渡す。空のパスとそれ以外の NUL は長さによらず EINVAL

- Status: Accepted
- Date: 2026-11-18

## Context

Go の標準 (`syscall.SockaddrUnix`) は、macOS / BSD で 103 バイト、Linux / illumos で 107 バイトまでのパスを受け付ける。Linux / Android / illumos / Solaris の標準は、先頭の `@` を NUL に書き換え、先頭が NUL のパスを abstract socket の名前として渡す (`syscall_linux.go` / `syscall_solaris.go` の `SockaddrUnix.sockaddr`)。abstract namespace を持つカーネルは Linux / Android だけで、illumos のカーネルは abstract の名前を受け付けない (bind が `ENOENT`)。NUL を含むパスは黙って NUL で切り詰め、別のパスに bind する (`a\0b` → `a`。macOS と Linux で実測)。空のパスは、Linux では bind がカーネルの選ぶ abstract の名前に bind し (autobind)、macOS では bind が `EINVAL`、connect はどちらでも errno の無い `missing address` になる (実測 2026-11-21)。

## Decision

- 空のパスは、何も見ずに `EINVAL` (各関数のエラーの型で包む)
- 標準が受け付ける長さ (`len(unix.RawSockaddrUnix{}.Path) - 1`) 以内は `net.ListenUnix` / `net.ListenUnixgram` / `net.DialUnix` をそのまま呼ぶ。何も足さない (環境変数も読まない)
- Linux / Android で abstract socket の名前 (先頭が NUL、または Go の記法の `@`) は、長さによらず標準にそのまま渡す。NUL の検査もしない
- abstract namespace の無い OS では、先頭が NUL のパスも長さによらず `EINVAL`。`@` で始まるパスは普通の相対パス
- illumos / Solaris では、`@` で始まるパスを `./` を前に付けて標準に渡す。`./` を付けて標準が受け付ける長さを超える時は、長いパスとして経路に回す
- 経路が作る綴り (relative の相対パス、thread-cwd-direct のファイル名) が `@` で始まる時は `./` を前に付ける
- `ListenEx` 等と resolve の `Spelling` は、abstract の名前では `Original`、illumos の `./@...` では `CwdRelative`
- それ以外で NUL を含むパスは、長さによらず何も作らずに `EINVAL`

## 理由

- 標準が受け付けるパスは標準に完全に任せる (SPEC)
- abstract socket にはファイルの経路が無い。`@` の扱いは Go の `net` の記法で、長いパスの経路に回すと abstract でなくファイルのソケットになる
- abstract namespace の無いカーネルに abstract の名前を渡しても bind / connect は通らない。SPEC はそこでの先頭の NUL を引数の誤り、`@` を普通のパスとする
- illumos の標準は `@` を abstract に書き換えるので、そのまま渡すと cwd の `@...` のファイルでなく、存在しない abstract の名前に bind / connect する。`./@...` は同じ cwd の同じファイルを指す綴りで、標準が書き換えない最短の形
- 綴りの `@` を abstract と読まれると、ファイルでなく abstract の名前空間に bind / connect してしまう
- NUL で切り詰めると、利用者が渡したのと別のパスにソケットができる (ハブの裁定)
- 空のパスを標準に渡すと、Linux では利用者の知らない abstract の名前に bind し、OS で振る舞いが割れる。resolve は空の綴りを返してしまう (SPEC「短いパス」の空のパスは引数の誤り)

illumos で `./` を付けると、SPEC の「標準にそのまま渡す。何も足さない」と次の点が違う。どれも、そのまま渡すと bind / connect 自体が通らないことと引き換え:

- カーネルが覚える綴りと listener の `Addr()` が `./@...` になる。同じ cwd の同じファイルを指すので、`Spelling` は他の相対パスと同じ `CwdRelative`
- 106・107 バイトの `@...` は `./` を付けると標準に入らず、経路に回る (Listen は link-dir、Dial は relative → link-file、[DR-0018](DR-0018-route-orders-per-os.md))
- listener の Close は `./@...` のファイルを消さない (`SetUnlinkOnClose(false)`、[DR-0017](DR-0017-listener-close-by-spelling.md))。他の OS の Go の標準が `@` で始まるアドレスのファイルを Close で消さない (`net/unixsock_posix.go` の `ln.path[0] != '@'`) のとそろえ、相対の綴りで消して間に変わった cwd の別のファイルを消す危険を sockat が足さないため (ハブの統括の判断 2026-11-19)

## 却下した案

| 案 | 却下の理由 |
|---|---|
| NUL を含む短いパスは標準に任せる | 標準は別のパスに bind する。長いパスと振る舞いが割れる |
| illumos の `@...` を自前のソケットで `@...` のまま bind / connect する | x/sys/unix の `SockaddrUnix` も同じ書き換えをするので sockaddr を手で組んで bind を呼ぶことになる。listener の `Addr()` が `@...` になり、それを標準に渡すと abstract と読まれる。標準の listener の Close も `@` で始まるアドレスのファイルを消さない |
| illumos の `@...` を絶対パスにして標準に渡す | `./` より長くなり、標準に入らなくなる範囲が広がる。相対パスの意味 (cwd 基準) も失う |

## 判断の出所

- 先頭以外の NUL を長さによらず引数の誤りにし、abstract (先頭が NUL、Go の `@`) は標準に渡すこと: kawaz 裁定 (HB-Q5、ハブの DR-0006) 2026-11-18
- abstract namespace の無い OS で先頭の NUL を引数の誤り、`@` を普通のパスとすること: ハブの SPEC「短いパス」
- 空のパスを引数の誤りとすること: ハブの SPEC「短いパス」(ハブ 80d6eec4、sockat-ts のレビュー)
