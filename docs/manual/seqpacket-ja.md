# SEQPACKET を使う

> [English](./seqpacket.md) | 日本語

[利用マニュアル](../MANUAL-ja.md) の一部。

SEQPACKET 専用の関数は無い。[resolve](resolve-ja.md) で作ったパスを、自分で作った SEQPACKET のソケット (`net.ListenUnix("unixpacket", ...)`・`net.DialUnix("unixpacket", ...)`) に渡せばよい。使い方は stream と同じで、`"unix"` を `"unixpacket"` に変えるだけ。

macOS の標準は SEQPACKET のソケットを作れない。
