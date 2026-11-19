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

## ハブへの報告 (統括が持つ)

- `ListenEx` / `DialEx` の Go の形 ([DR-0014](../decisions/DR-0014-ex-last-resort-and-diagnostics.md)) と、ハブの `just conformance` で go の列が全部一致したこと

## 残り

- ListenEx で bind は通ったのにファイルを覚えられない分岐は試験で作れず未試験 (DR-0001)
- D を作った直後から開くまでの間に、group / other の権限付きのディレクトリ (中に `s` や別のファイルがある) にすり替えられた時、それを使わず中身にも触らないことの試験が無い (ハブの SPEC「実装が確かめる試験の観点」、「D と R」の「確認に落ちた D は他人の物とみなし、中身も含めて触らない」)。Dial の作り直しで proc-held の専用ディレクトリの試験を消した時に、この観点の試験も一緒に消えた。今あるのは別 uid の空の D (`makeDIn` 単体) と `isOurPrivateDir` の判定だけ。`makeDIn` の経路で足す
