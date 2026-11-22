---
title: FreeBSD の CI で、resolve の綴りに unixpacket で bind・connect する Example が 10 分止まった
status: resolved
category: bug
created: 2026-11-22
close_reason: documented
---

# FreeBSD の CI で、resolve の綴りに unixpacket で bind・connect する Example が 10 分止まった

## 観測

commit 119ced3 (後で消した `example_seqpacket_test.go` を含む) の CI の FreeBSD VM の job で、`ExampleResolveForBind_seqpacket` が止まり、`go test` の 10 分の打ち切り (`panic: test timed out after 10m0s`) で落ちた (run 44989123263、2026-11-22)。Linux (docker、seccomp 2 通り)・NetBSD・OpenBSD・illumos では同じ Example が通った。

打ち切りの時のスタック:

- 待ち受ける側の goroutine は `l.AcceptUnix()` で止まっていた (Example の 48 行目)
- つなぐ側は `net.DialUnix("unixpacket", ...)` と 2 回の `Write` を終え、1 回目の `c.Read` で止まっていた (90 行目)

つまり connect と送信は通ったのに、listener の accept が返らなかった (と読んだが、読み違い。「原因」の節)。

Example の形: 長いパス (`longDir` の下) を `ResolveForBind` で綴って `net.ListenUnix("unixpacket", ...)` し、`SetUnlinkOnClose(false)`。`ResolveForConnect` の綴りで `net.DialUnix("unixpacket", ...)` し、すぐ `Cleanup`。FreeBSD では bind の綴りは link-dir (`B/D/E/<name>`)、connect の綴りは link-file (`B/E`、ソケットのファイルへの symlink)。

## 調べる前の仮説

- (a) Go の FreeBSD の unixpacket の accept の問題で、sockat と関係なく短いパスでも起きる
- (b) connect の綴りが bind したソケットと別の物に届いている (sockat の綴りの問題)。connect が通っているので、何かの listen 中のソケットには届いている
- (c) VM の環境の問題

切り分け: 同じ流れを、(1) 標準だけで短いパス、(2) sockat の resolve で短いパス (Original)、(3) 長いパス、で FreeBSD で流し、どこから止まるかを見る。止まった時に `getpeername` / connect した側の `getsockname`、bind したファイルと E の指す先を出す。手元に FreeBSD が無いので CI で流す必要がある (打ち切りを短くした試験で)。

## 原因 (確認済み)

sockat の綴りとは関係ない。FreeBSD の SEQPACKET は、送る側が `MSG_EOR` を付けた所にだけ記録の境目を付け、`write(2)` では境目を付けられない。Go の `UnixConn.Write` は `write(2)` なので、`Write("ab")`・`Write("cde")` は 1 つの記録 `"abcde"` として届く。

- 一次資料: FreeBSD 15.1 の socket(2) の「SEQUENCED PACKET SOCKET TYPE」(`lib/libsys/socket.2`): "Record boundaries are set by the sender with the MSG_EOR flag of send(2) or sendmsg(2). There is no possibility to set a record boundary with write(2)."。カーネル (`sys/kern/uipc_usrreq.c` の `uipc_sosend_stream_or_seqpacket`) も `flags & MSG_EOR` の時だけ `M_EOR` を付け、受ける側 (`uipc_soreceive_stream_or_seqpacket`) は `M_EOR` まで続けて読む
- Go の標準 (`net`、`internal/poll`) は `MSG_EOR` をどこでも付けない (go1.27 の src を grep)
- Linux・NetBSD・OpenBSD・illumos は送るたびに 1 つの記録にする (下の表)

Example で起きたこと: 待ち受ける側の接続の goroutine は 1 回目の `Read` で `"abcde"` を受けて返し、2 回目の `Read` で待つ。つなぐ側は 1 回目の `Read` で `"abcde"` を受け、2 回目の `Read` で待つ。両方が相手を待って止まった。打ち切りのスタックの `example_seqpacket_test.go:56` の goroutine (接続ごとの `c.Read`) は accept の後にしか作られないので、accept は 1 度返っている。`AcceptUnix` で止まっていた goroutine は 2 つ目の接続を待つ accept のループで、止まりとは関係ない。「accept が返らなかった」は読み違い。

仮説の判定:

- (a) Go / FreeBSD 側: 該当 (accept ではなく記録の境目)。標準だけ・短いパスでも同じ
- (b) connect の綴りが別の物に届く: 否定。全段で送った物が待ち受けた側から返った
- (c) symlink を通した connect と SEQPACKET: 否定。標準だけ・自分で張った symlink の段も同じ結果
- (d) VM の環境: 否定。どの段・どの順でも決まって起きる、FreeBSD の仕様どおりの挙動

## 切り分けの結果

CI の run [45020145995](https://github.com/kawaz/sockat-go/actions/runs/45020145995) (FreeBSD 15.1、go1.27.4)。2 つの記録 `"ab"`・`"cde"` を `UnixConn.Write` で送り、返ってくるのを期限付きで読む。各組み合わせを 10 回。順は「書いてから accept」「accept してから書く」「同時 (Example と同じ)」の 3 通り。

| 段 | bind / connect の綴り | FreeBSD | Linux (docker 2 通り)・NetBSD・OpenBSD・illumos |
|---|---|---|---|
| (1) 標準だけ・短いパス | 同じ絶対パス | 3 通りとも 1 回目で `["abcde"]` | 3 通り × 10 回とも `["ab" "cde"]` |
| (2) 標準だけ・symlink を通した connect | 実パス / 自分で張った symlink | 同上 | 同上 |
| (3) resolve・短いパス | Original / Original | 同上 | 同上 |
| (4) resolve・長いパス | link-dir (`B/D/E/server.sock`) / link-file (`B/E`) | 同上 | 同上 |
| 対照: `"unix"` (stream) の (1)〜(4) | 同上 | 全部届く (`"abcde"` で 5 byte) | 同上 |

FreeBSD では、つなぐ側の `getpeername` も、受けた接続の `LocalAddr` も bind した綴り (長いパスでは `B/D/E/server.sock`) で、別のソケットには届いていない。

`MSG_EOR` を付けて送る (`SyscallConn` から `unix.Sendmsg(fd, p, nil, nil, unix.MSG_EOR)`) と、FreeBSD でも 2 つの記録が 2 回の `Read` で 1 つずつ届く (回帰の試験 `TestSeqpacketThroughResolvedSpellingsKeepsRecords` が全 OS で通る。CI の run [45020527543](https://github.com/kawaz/sockat-go/actions/runs/45020527543))。

## 直した物と残した物

- sockat の実装は直さない。原因は sockat の責務 (パスを綴る) の外で、resolve の綴りは標準のパスと同じ listener に届いている
- 回帰の試験 `seqpacket_test.go` の `TestSeqpacketThroughResolvedSpellingsKeepsRecords`: 標準だけ・短いパス (対照)、resolve・短いパス、resolve・長いパスで、`unixpacket` に bind・connect し、`MSG_EOR` を付けて送った 2 つの記録が 1 つずつ返ることを、accept・read・write に期限を付けて確かめる
- SEQPACKET のページ (`docs/manual/seqpacket-ja.md`) に「FreeBSD では `Write` で記録の境目が付かない、`MSG_EOR` を付けて送る」を足すかは統括が決める (このページは kawaz の指示で短い案内にしてある)

## 今の扱い

SEQPACKET のページは kawaz の指示 (2026-11-22) で短い案内だけにし、この Example は消した。stream の Example と試験は FreeBSD で通っている。
