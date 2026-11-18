# DR-0002: 標準が受け付けるパスと abstract の名前は標準にそのまま渡す。それ以外の NUL は長さによらず EINVAL

- Status: Accepted
- Date: 2026-11-18

## Context

Go の標準 (`syscall.SockaddrUnix`) は、macOS / BSD で 103 バイト、Linux / illumos で 107 バイトまでのパスを受け付ける。Linux / illumos では先頭の `@` を abstract socket の名前とみなす。NUL を含むパスは黙って NUL で切り詰め、別のパスに bind する (`a\0b` → `a`。macOS と Linux で実測)。

## Decision

- 標準が受け付ける長さ (`len(unix.RawSockaddrUnix{}.Path) - 1`) 以内は `net.ListenUnix` / `net.DialUnix` をそのまま呼ぶ。何も足さない (環境変数も読まない)
- Linux / illumos で abstract socket の名前 (先頭が NUL、または Go の記法の `@`) は、長さによらず標準にそのまま渡す。NUL の検査もしない
- 経路が作る綴り (相対パス、ファイル名だけ) が `@` で始まる時は `./` を前に付ける
- それ以外で NUL を含むパスは、長さによらず何も作らずに `EINVAL`

## 理由

- 標準が受け付けるパスは標準に完全に任せる (SPEC)
- abstract socket にはファイルの経路が無い。`@` の扱いは Go の `net` の記法で、長いパスの経路に回すと abstract でなくファイルのソケットになる
- 綴りの `@` を abstract と読まれると、ファイルでなく abstract の名前空間に bind / connect してしまう
- NUL で切り詰めると、利用者が渡したのと別のパスにソケットができる (ハブの裁定)

## 却下した案

| 案 | 却下の理由 |
|---|---|
| NUL を含む短いパスは標準に任せる | 標準は別のパスに bind する。長いパスと振る舞いが割れる |

## 判断の出所

- 先頭以外の NUL を長さによらず引数の誤りにし、abstract (先頭が NUL、Go の `@`) は標準に渡すこと: kawaz 裁定 (HB-Q5、ハブの DR-0006) 2026-11-18
