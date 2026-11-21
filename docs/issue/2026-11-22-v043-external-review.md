---
title: v0.4.3 — 外部レビューの残り (bind の前の realpath、CLI の EINVAL、macOS oldstable の主スレッド)
status: resolved
close_reason: implemented
category: task
created: 2026-11-22
---

# v0.4.3 — 外部レビューの残り (bind の前の realpath、CLI の EINVAL、macOS oldstable の主スレッド)

ハブの外部レビュー (ハブ docs/research/2026-11-21-external-review-carbon.md) の go 向けの指摘のうち、当たった物は main に入っている (VERSION 0.4.2 の commit 85ff704 まで push 済み)。v0.4.2 の Release は、統括の指示 (1-6 を確かめ直してから出す) で tag が出る前に cancel した。tag v0.4.2 は無く、v0.4.2 は出さずに v0.4.3 として出す (main@origin の VERSION が 0.4.2 なので、試験以外の .go を変えると just push の版の検査で 0.4.2 より上が要る)。

## 残り (worker が作業中、2026-11-22)

1. 1-6 の確かめ直し: ex 系 (bind.go の bindReported) が、覚えるための realpath を bind の前に引き、(st_dev, st_ino) を bind の後に読む。間に元のパスの途中の symlink が A→B に張り替わると、bind は B にでき、A の別のソケットを覚えて Remove が消しうる (sockat-ts で再現済み)。試験で確かめ、当たれば realpath も bind の後に引く
2. 2-7: ハブの決定 (a)。sockat が出す引数の誤りに内部の目印を付け、試験用 CLI はそれだけを invalid-argument にし、OS の EINVAL は EINVAL のまま出す。公開のエラーの形は変えない
3. v0.4.2 の commit の CI で、macOS (Go oldstable) だけ TestThreadCwdFromTheMainGoroutineLeavesTheMainThreadAsItWas が落ちた (`before=true after=false`。主スレッドを失う)。外部レビュー 1-7 の直し (使い捨ての goroutine が主スレッドに乗ったら別の goroutine にやらせる) が oldstable の macOS で効いていない

## 出し方

3 件を直したら、just ci・just test-linux・BSD と illumos の compile・ハブの just conformance (全言語) を通し、`just bump-version patch` (0.4.3) → `just push` → Monitor で `just watch`。CI が全部通って tag が出たら、run の URL と版をハブの統括 (session 64eabc3d-e5db-421f-925d-659927ded2c3) に知らせる (パッチ版は各言語に任されている、PB-Q2)。

## 結果

v0.4.3 を 2026-11-21 に公開した (tag v0.4.3 = e0ee21fb、Release の run 44920374876 は 11 job とも success、macOS の Go oldstable の主スレッドの試験も通過、proxy.golang.org に掲載)。残った窓 (経路を組んだ時点の realpath) はハブ 307620d1 の決定で [2026-11-22-v044-remove-records-route-dir.md](../2026-11-22-v044-remove-records-route-dir.md) に回した。
