# DR-0005: macOS の広げた sockaddr は Dial にだけ使う

- Status: Accepted
- Date: 2026-11-18

## Context

SPEC は macOS で、253 バイトまで入る広げた sockaddr の経路を bind / connect の両方に使う。Go では自前のソケットを `net.FileListener` / `net.FileConn` で標準の型にする ([DR-0006](DR-0006-own-socket-into-net.md))。`net.FileListener` はソケットのアドレスを `getsockname(2)` で読み、`syscall` パッケージは `sockaddr_un` より長いアドレスを `EINVAL` で断る。

## Decision

- 広げた sockaddr は Dial にだけ使う (相対パス・symlink・スレッド cwd の各経路の中でも)
- Listen では使えない経路として飛ばす

## 理由

- 広げた形で bind したソケットは `net.FileListener` に渡せない (`getsockname: invalid argument`。bind 自体は通ってソケットファイルはできる。実測)
- connect したソケットは自分側が未 bind なので `getsockname` が短く、`net.FileConn` に渡せる (相手のアドレスの読み取りのエラーは net パッケージが無視する)

## Consequences

- macOS の Listen は、ファイル名が 103 バイトを超えると `ENAMETOOLONG` (symlink の経路の綴り `/tmp/sockat.XXXXXXXXXX/<ファイル名>` は 80 バイトのファイル名まで)。SPEC の Rust 版の値 (253) より短い

## 判断の出所

- 実装側の判断 2026-11-18 (統括に共有済み)
