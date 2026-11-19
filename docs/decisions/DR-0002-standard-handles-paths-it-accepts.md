# DR-0002: 標準が受け付けるパスと abstract の名前は標準にそのまま渡す。それ以外の NUL は長さによらず EINVAL

- Status: Accepted
- Date: 2026-11-18

## Context

Go の標準 (`syscall.SockaddrUnix`) は、macOS / BSD で 103 バイト、Linux / illumos で 107 バイトまでのパスを受け付ける。Linux / Android / illumos / Solaris の標準は、先頭の `@` を NUL に書き換え、先頭が NUL のパスを abstract socket の名前として渡す (`syscall_linux.go` / `syscall_solaris.go` の `SockaddrUnix.sockaddr`)。abstract namespace を持つカーネルは Linux / Android だけで、illumos のカーネルは abstract の名前を受け付けない (bind が `ENOENT`)。NUL を含むパスは黙って NUL で切り詰め、別のパスに bind する (`a\0b` → `a`。macOS と Linux で実測)。

## Decision

- 標準が受け付ける長さ (`len(unix.RawSockaddrUnix{}.Path) - 1`) 以内は `net.ListenUnix` / `net.DialUnix` をそのまま呼ぶ。何も足さない (環境変数も読まない)
- Linux / Android で abstract socket の名前 (先頭が NUL、または Go の記法の `@`) は、長さによらず標準にそのまま渡す。NUL の検査もしない
- abstract namespace の無い OS では、先頭が NUL のパスも長さによらず `EINVAL`。`@` で始まるパスは普通の相対パス
- illumos / Solaris では、`@` で始まるパスを `./` を前に付けて標準に渡す。`./` を付けて標準が受け付ける長さを超える時は、長いパスとして経路に回す
- 経路が作る綴り (相対パス、ファイル名だけ) が `@` で始まる時は `./` を前に付ける
- それ以外で NUL を含むパスは、長さによらず何も作らずに `EINVAL`

## 理由

- 標準が受け付けるパスは標準に完全に任せる (SPEC)
- abstract socket にはファイルの経路が無い。`@` の扱いは Go の `net` の記法で、長いパスの経路に回すと abstract でなくファイルのソケットになる
- abstract namespace の無いカーネルに abstract の名前を渡しても bind / connect は通らない。SPEC はそこでの先頭の NUL を引数の誤り、`@` を普通のパスとする
- illumos の標準は `@` を abstract に書き換えるので、そのまま渡すと cwd の `@...` のファイルでなく、存在しない abstract の名前に bind / connect する。`./@...` は同じ cwd の同じファイルを指す綴りで、標準が書き換えない最短の形
- 綴りの `@` を abstract と読まれると、ファイルでなく abstract の名前空間に bind / connect してしまう
- NUL で切り詰めると、利用者が渡したのと別のパスにソケットができる (ハブの裁定)

illumos で `./` を付けると、SPEC の「標準にそのまま渡す。何も足さない」と次の点が違う。どれも、そのまま渡すと bind / connect 自体が通らないことと引き換え:

- カーネルが覚える綴りと listener の `Addr()` が `./@...` になる。同じ cwd の同じファイルを指すので、失うものは他の相対パスと同じ `cwd-relative`
- 106・107 バイトの `@...` は `./` を付けると標準に入らず、経路に回る。illumos の Listen には経路が無いので `ENAMETOOLONG`、Dial は経路 (symlink-file 等) で届く
- listener の Close は `./@...` のファイルを消す (標準が普通のパスに対してするのと同じ)。macOS / BSD の標準は `@` で始まるアドレスのファイルを消さない

## 却下した案

| 案 | 却下の理由 |
|---|---|
| NUL を含む短いパスは標準に任せる | 標準は別のパスに bind する。長いパスと振る舞いが割れる |
| illumos の `@...` を自前のソケットで `@...` のまま bind / connect する | x/sys/unix の `SockaddrUnix` も同じ書き換えをするので sockaddr を手で組んで bind を呼ぶことになる。listener の `Addr()` が `@...` になり、それを標準に渡すと abstract と読まれる。標準の listener の Close も `@` で始まるアドレスのファイルを消さない |
| illumos の `@...` を絶対パスにして標準に渡す | `./` より長くなり、標準に入らなくなる範囲が広がる。相対パスの意味 (cwd 基準) も失う |

## 判断の出所

- 先頭以外の NUL を長さによらず引数の誤りにし、abstract (先頭が NUL、Go の `@`) は標準に渡すこと: kawaz 裁定 (HB-Q5、ハブの DR-0006) 2026-11-18
- abstract namespace の無い OS で先頭の NUL を引数の誤り、`@` を普通のパスとすること: ハブの SPEC「短いパス」
