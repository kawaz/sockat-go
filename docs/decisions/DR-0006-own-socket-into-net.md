# DR-0006: 自前のソケットは標準と同じ設定で作り、net パッケージに渡す

- Status: Accepted
- Date: 2026-11-18

## Context

at と at-direct (FreeBSD の `bindat` / `connectat`) は、標準では作れない呼び出しで自前のソケットに bind / connect する。戻り値は標準の型 ([DR-0016](DR-0016-public-api-go-shape.md))。stream と DGRAM の両方がこの経路を通る。

## Decision

- ソケットは net パッケージと同じ設定で作る: close-on-exec と非ブロッキング (`SOCK_CLOEXEC | SOCK_NONBLOCK`)。型は呼び出しに合わせる (`Listen` 系と `Dial` は `SOCK_STREAM`、`ListenUnixgram` 系と `DialUnixgram` 系は `SOCK_DGRAM`)
- stream の listen の backlog は net パッケージと同じ値 (FreeBSD は `kern.ipc.soacceptqueue`、65535 で頭打ち、取れなければ `SOMAXCONN`)
- `net.FileListener` (stream の bind) / `net.FileConn` (DGRAM の bind と、stream・DGRAM の connect) で標準の型にする (net パッケージが複製を取り、poller に載せる)。元の fd は閉じる
- それ以外の経路 (link-dir・link-file・thread-cwd、proc-direct・relative・thread-cwd-direct・link-file) は綴りを標準 (`net.ListenUnix` / `net.ListenUnixgram` / `net.DialUnix`) に渡す

## 理由

- 標準の代わりに置き換えるだけで使えるよう、ソケットの設定を標準に揃える (SPEC)
- 綴りを標準に渡せる経路は、ソケットの設定も backlog も標準そのものになる
- 自前のソケットを `net.FileListener` / `FileConn` に渡す時、アドレスが `sockaddr_un` に収まる必要がある (`bindat` の綴りは `E/<ファイル名>`、`connectat` はファイル名だけなので収まる。収まらない広げた sockaddr は使わない、[DR-0005](DR-0005-no-widened-sockaddr.md))

## 判断の出所

- 実装側の判断 2026-11-18
