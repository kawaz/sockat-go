# DR-0005: macOS の広げた sockaddr の経路は使わない

- Status: Accepted
- Date: 2026-11-18

## Context

SPEC は macOS で、253 バイトまで入る広げた sockaddr の経路を bind / connect の両方に使う。標準 (`net.ListenUnix` / `net.DialUnix`) はこの形を作れないので、Go では自前のソケットに bind / connect して `net.FileListener` / `net.FileConn` で標準の型にすることになる ([DR-0006](DR-0006-own-socket-into-net.md))。どちらも中で `getsockname(2)` / `getpeername(2)` を読み、`syscall` パッケージは `sockaddr_un` より長いアドレスを `EINVAL` で断る。

## Decision

- macOS でも広げた sockaddr は使わない。経路の中 (相対パス・symlink・スレッド cwd) でも普通の sockaddr だけを使う
- macOS の順は Listen / Dial とも 相対パス → symlink → スレッド cwd

## 理由

実測 (macOS 26.5、go 1.27):

| 広げた形で | 結果 |
|---|---|
| bind して `net.FileListener` | `getsockname: invalid argument` で渡せない (bind 自体は通り、ソケットファイルはできる) |
| 広げた形で bind した相手に connect して `net.FileConn` | 渡せるが、`RemoteAddr()` が nil の interface になる (`getpeername` の `EINVAL` を net パッケージが無視する)。`.String()` で panic する |

- 標準の `DialUnix` は `getpeername` が失敗すると渡した綴りを相手のアドレスにするので nil にならない。広げた経路だけがアドレスの取得で落ちうるのは、SPEC の「カーネルが返すアドレスは長いパスでも取得で落ちない」に反する
- 失うのは、`/tmp` にも `TMPDIR` にも symlink を作れず、ファイル名が 104〜253 バイトの時だけ。普通の環境では Dial は symlink の経路でどこにでも届く

## Consequences

- macOS の Listen / Dial は、ファイル名が 103 バイトを超えると、symlink の経路で届く場合 (Dial) を除いて `ENAMETOOLONG`。SPEC の Rust 版の値 (253) より短い

## 判断の出所

- 実装側の判断 2026-11-18 (統括に共有済み)
