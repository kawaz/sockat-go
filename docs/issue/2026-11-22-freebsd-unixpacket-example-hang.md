---
title: FreeBSD の CI で、resolve の綴りに unixpacket で bind・connect する Example が 10 分止まった
status: open
category: bug
created: 2026-11-22
---

# FreeBSD の CI で、resolve の綴りに unixpacket で bind・connect する Example が 10 分止まった

## 観測

commit 119ced3 (後で消した `example_seqpacket_test.go` を含む) の CI の FreeBSD VM の job で、`ExampleResolveForBind_seqpacket` が止まり、`go test` の 10 分の打ち切り (`panic: test timed out after 10m0s`) で落ちた (run 44989123263、2026-11-22)。Linux (docker、seccomp 2 通り)・NetBSD・OpenBSD・illumos では同じ Example が通った。

打ち切りの時のスタック:

- 待ち受ける側の goroutine は `l.AcceptUnix()` で止まっていた (Example の 48 行目)
- つなぐ側は `net.DialUnix("unixpacket", ...)` と 2 回の `Write` を終え、1 回目の `c.Read` で止まっていた (90 行目)

つまり connect と送信は通ったのに、listener の accept が返らなかった。

Example の形: 長いパス (`longDir` の下) を `ResolveForBind` で綴って `net.ListenUnix("unixpacket", ...)` し、`SetUnlinkOnClose(false)`。`ResolveForConnect` の綴りで `net.DialUnix("unixpacket", ...)` し、すぐ `Cleanup`。FreeBSD では bind の綴りは link-dir (`B/D/E/<name>`)、connect の綴りは link-file (`B/E`、ソケットのファイルへの symlink)。

## 未確認の仮説

- (a) Go の FreeBSD の unixpacket の accept の問題で、sockat と関係なく短いパスでも起きる
- (b) connect の綴りが bind したソケットと別の物に届いている (sockat の綴りの問題)。connect が通っているので、何かの listen 中のソケットには届いている
- (c) VM の環境の問題

切り分け: 同じ流れを、(1) 標準だけで短いパス、(2) sockat の resolve で短いパス (Original)、(3) 長いパス、で FreeBSD で流し、どこから止まるかを見る。止まった時に `getpeername` / connect した側の `getsockname`、bind したファイルと E の指す先を出す。手元に FreeBSD が無いので CI で流す必要がある (打ち切りを短くした試験で)。

## 今の扱い

SEQPACKET のページは kawaz の指示 (2026-11-22) で短い案内だけにし、この Example は消した。なので CI は通っている。stream の Example と試験は FreeBSD で通っている。調べるかどうかは kawaz に確かめる。
