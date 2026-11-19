---
title: v0.3.0 (中心の経路、ListenEx / DialEx、Listen の symlink-file、適合試験の CLI) の release 待ちと残り
status: open
category: task
created: 2026-11-19
---

# v0.3.0 (中心の経路、ListenEx / DialEx、Listen の symlink-file、適合試験の CLI) の release 待ちと残り

main@origin は v0.2.0 の後の README の 1 commit まで。それより後の commit (DR-0011〜0015 の実装、ListenEx / DialEx、適合試験の CLI) はローカルの main にあり、push していない。VERSION は 0.2.0 のまま。

## 待っているもの (ハブの統括 → kawaz)

- release は kawaz の OK が出た (2026-11-19) が、ハブの裁定待ち DS-Q1 (`discard()` の同一性。listener を閉じた後にソケットのファイルが置き換えられると、ext4 等は同じ inode 番号を渡すので discard が置き換え先を消す) が決まるまで止める (ハブの統括の指示 2026-11-19)。Go の試験は listener を開いたまま置き換えるので通るが、同じ穴がある。裁定が出たらそれを入れてから `just bump-version minor` (0.3.0) → `just push` → Monitor で `just watch`。tag が出たら、CI の各 OS の結果と BSD 系 / illumos で bind が symlink を辿ったかの判定 (TestWhetherABindFollowsASymlinkIsAsObserved) をハブの統括 (25352bb6) に知らせる

## 残り

- ListenEx で bind は通ったのにファイルを覚えられない分岐は試験で作れず未試験 (DR-0001)
