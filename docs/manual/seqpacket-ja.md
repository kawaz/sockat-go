# SEQPACKET を使う

> [English](./seqpacket.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。

SEQPACKET 専用の関数は無い。[resolve](resolve-ja.md) で作ったパスを、自分で作った SEQPACKET のソケット (`net.ListenUnix("unixpacket", ...)`・`net.DialUnix("unixpacket", ...)`) に渡せばよい。使い方は stream と同じで、`"unix"` を `"unixpacket"` に変えるだけ。

- 待ち受ける (bind する) 側: `ResolveForBind` で作ったパスを `net.ListenUnix("unixpacket", ...)` に渡す ([他のライブラリに bind させる](resolve-ja.md#他のライブラリに-bind-させる))
- つなぐ (connect する) 側: `ResolveForConnect` で作ったパスを `net.DialUnix("unixpacket", ...)` に渡す ([何度も connect する](resolve-ja.md#何度も-connect-する))

macOS の標準は SEQPACKET のソケットを作れない。FreeBSD では `Write` (write(2)) が記録の境目を付けず、続けて送った物が 1 つの記録として届く。1 回ずつ記録にするには、`SyscallConn` で `unix.Sendmsg(fd, b, nil, nil, unix.MSG_EOR)` のように `MSG_EOR` を付けて送る。
