---
title: v0.4.4 — remove が覚える場所を経路が作った実ディレクトリにする、README に Err の型の注意
status: open
category: task
created: 2026-11-22
---

# v0.4.4 — remove が覚える場所を経路が作った実ディレクトリにする、README に Err の型の注意

ハブの統括の決定 (2026-11-22、ハブ 307620d1 で SPEC の「remove」に入った)。v0.4.3 の後の版で入れる。

## 1. 覚える場所 (SPEC「remove」)

ListenEx・ListenUnixgramEx が覚えるソケットのファイルの場所:

- link-dir・thread-cwd・at: 経路がソケットを作った実ディレクトリ (E が指す realpath、thread-cwd / at は移った D の先) とファイル名。元のパスを引き直さない
- standard と link-file: bind の後に元のパスの realpath

v0.4.3 は realpath を bind の後に引く形にしたが、長いパスの経路は経路を組んだ時点の realpath の下に bind するので、その後 bind の後の realpath までに途中の symlink が張り替わると、まだ覚える物と bind した物が食い違う。経路が知っている実ディレクトリを覚えればこの窓が閉じる。試験は v0.4.3 の TestListenExRecordsTheFileBoundThroughASymlinkRetargetedBeforeTheBind の形 (bindEx の継ぎ目で途中の symlink を張り替える) で、張り替える時点を「経路を組んだ後・bind の後の realpath の前」にずらして確かめる。

## 2. README の Go に固有の注意に 1 行 (日英)

sockat が出す引数の誤り (Options.Base の形の誤り、SweepBase の形の誤り、名前で終わらない長いパス等) の `*os.PathError.Err` / `*net.OpError.Err` は、むき出しの `syscall.EINVAL` でなく、それを包んだ値。`errors.Is(err, syscall.EINVAL)` で見る (`== syscall.EINVAL` や型の主張では一致しない)。

## 出し方

just ci・just test-linux・BSD と illumos の compile・ハブの just conformance (全言語) を通し、`just bump-version patch` → `just push` → Monitor で `just watch`。tag が出たら run の URL と版をハブの統括 (session 64eabc3d-e5db-421f-925d-659927ded2c3) に知らせる。
