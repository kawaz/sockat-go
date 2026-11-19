---
title: v0.3.0 (中心の経路、ListenEx / DialEx、Listen の symlink-file、適合試験の CLI) の release 待ちと残り
status: open
category: task
created: 2026-11-19
---

# v0.3.0 (中心の経路、ListenEx / DialEx、Listen の symlink-file、適合試験の CLI) の release 待ちと残り

main@origin は v0.2.0 の後の README の 1 commit まで。それより後の commit (DR-0011〜0015 の実装、ListenEx / DialEx、適合試験の CLI) はローカルの main にあり、push していない。VERSION は 0.2.0 のまま。

## 待っているもの (ハブの統括 → kawaz)

- release の OK。出す時は `just bump-version minor` (0.3.0) → `just push`

## 残り

- ListenEx で bind は通ったのにファイルを覚えられない分岐は試験で作れず未試験 (DR-0001)
