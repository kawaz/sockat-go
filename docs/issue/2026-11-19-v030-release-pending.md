---
title: v0.3.0 (中心の経路、ListenEx、atanycost、適合試験の CLI) の release 待ちと残り
status: open
category: task
created: 2026-11-19
---

# v0.3.0 (中心の経路、ListenEx、atanycost、適合試験の CLI) の release 待ちと残り

main@origin は v0.2.0 の後の README の 1 commit まで。それより後の commit (DR-0011〜0012 の実装、ListenEx、atanycost、適合試験の CLI) はローカルの main にあり、push していない。VERSION は 0.2.0 のまま。

## 待っているもの (ハブの統括 → kawaz)

- any 系を `_ex` に統合し、`_ex` の引数で経路の選び方 (受け入れる失うもの等) を指定する案を議論中。決まれば `atanycost` パッケージは無くなり、`ListenEx` の形が変わる見込み。決まるまで公開 API の形に手を入れない
- release の OK。出す時は `just bump-version minor` (0.3.0) → `just push`

## 残り

- Dial の予備の経路を SPEC の列の形に作り直し、skipped にも予備の経路を記録する。ただし connect の試す順番そのもの (書き込みもスレッドも要らない経路を先に、の案) の kawaz 裁定の後に行う (ハブの統括の指示 2026-11-19)。今の食い違い: Linux の `/proc` がある時、SPEC は proc-direct → proc-held → symlink だが、実装 (`posixFallbackDial`) は置き場ごとに「安全なら symlink、安全でなければ proc-held」と交互に試す。予備の経路の skipped も記録していない (trace は Listen と D を通す経路だけ)。
- skipped に fork-sockdir が入る場面 (D をどこにも作れず、-sockdir の綴りも収まらない) の試験が無い
- ListenEx で bind は通ったのにファイルを覚えられない分岐は試験で作れず未試験 (DR-0001)
