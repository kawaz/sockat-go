# DR-0005: macOS の広げた sockaddr の経路は使わない

- Status: Accepted
- Date: 2026-11-18

## Context

SPEC は macOS の connect の順で、relative の次に 253 バイトまで入る広げた sockaddr の経路 (widened) を試す (bind には使わない)。標準 (`net.ListenUnix` / `net.DialUnix`) はこの形を作れないので、Go では自前のソケットに bind / connect して `net.FileListener` / `net.FileConn` で標準の型にすることになる ([DR-0006](DR-0006-own-socket-into-net.md))。どちらも中で `getsockname(2)` / `getpeername(2)` を読み、`syscall` パッケージは `sockaddr_un` より長いアドレスを `EINVAL` で断る。

## Decision

- macOS でも広げた sockaddr は使わない。どの経路 (relative・thread-cwd・symlink-file) も普通の sockaddr だけを使う
- connect の順 ([DR-0013](DR-0013-connect-order.md)) の widened は、作らない経路として試した経路の記録に `no widened route in sockat-go` で入れ、次の thread-cwd へ進む

## 理由

実測 (macOS 26.5、go 1.27):

| 広げた形で | 結果 |
|---|---|
| bind して `net.FileListener` | `getsockname: invalid argument` で渡せない (bind 自体は通り、ソケットファイルはできる) |
| 広げた形で bind した相手に connect して `net.FileConn` | 渡せるが、`RemoteAddr()` が nil の interface になる (`getpeername` の `EINVAL` を net パッケージが無視する)。`.String()` で panic する |

- 標準の `DialUnix` は `getpeername` が失敗すると渡した綴りを相手のアドレスにするので nil にならない。広げた経路だけがアドレスの取得で落ちうるのは、SPEC の「カーネルが返すアドレスは長いパスでも取得で落ちない」に反する
- 失うのは、relative が収まらず、thread-cwd が使えず (ファイル名が 92 バイトを超えるか `__pthread_fchdir` が使えない)、`TMPDIR` にも `/tmp` にも symlink-file の symlink を作れない時の、元のパスか相対パスが 253 バイト以内の connect だけ。普通の環境では Dial は symlink-file でどこにでも届く

## Consequences

- macOS の Dial は、上の「失う」場面で `ENAMETOOLONG`。SPEC の connect 系の「どこから届かなくなるか」(元のパスも相対パスも 253 を超える) より手前で届かなくなる

## 判断の出所

- 実装側の判断 2026-11-18 (統括に共有済み)
