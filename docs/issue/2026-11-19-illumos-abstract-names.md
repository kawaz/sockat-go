---
title: illumos / Solaris で先頭が NUL や @ のパスを abstract として標準に渡している (SPEC では abstract の無い OS)
status: open
category: bug
created: 2026-11-19
---

# illumos / Solaris で先頭が NUL や @ のパスを abstract として標準に渡している (SPEC では abstract の無い OS)

## 現象

`abstract_on.go` が `linux || android || illumos || solaris` で `abstractNames = true` にしている。Go の標準 (`syscall_solaris.go` の `SockaddrUnix.sockaddr`) が illumos でも先頭の `@` と NUL を abstract の形に書き換えるのに合わせたもの (DR-0002 も「Linux / illumos で abstract は標準にそのまま渡す」と書いている)。ところが illumos のカーネルには abstract namespace が無く、標準に渡すと `ENOENT` になる (CI の illumos VM で、abstract の名前の試験が `bind: no such file or directory` で落ちた。v0.3.0 の CI、2026-11-19)。

ハブの SPEC (「短いパス」) は「abstract namespace の無い OS では、先頭が NUL のパスも長さによらず引数の誤り」。

| 入力 (illumos) | SPEC | 今の実装 |
|---|---|---|
| `"\x00s.sock"` | `EINVAL` (引数の誤り) | 標準へ渡して `ENOENT` |
| 短い `"@x"` | 普通のパスとして標準へ | 標準へ (Go の標準が NUL に書き換えて `ENOENT`) |
| 長い `"@..."` | 普通のパスとして経路へ (illumos は手段が無いので `ENAMETOOLONG`) | 標準へ (`EINVAL`) |

## 直し方 (v0.3.1 で)

- `abstractNames` を Linux / Android だけにする。illumos / Solaris では先頭 NUL を長さによらず `EINVAL`、`@` は普通のパスとして扱う
- 短い `"@x"` を標準に渡すと Go の標準が NUL に書き換えてしまう。普通のパスとして bind させる手 (例: `./@x` にする、自前のソケット) が要るかを SPEC と照らして決める
- DR-0002 と README の「abstract」の記述を直す
- 試験: `TestALeadingNULIsEINVALWhereThereAreNoAbstractNames` を illumos でも走らせる (v0.3.0 では実装が SPEC と違うので、この issue を理由に illumos では飛ばしている)

## 経緯の要点

v0.3.0 は試験の直しだけで出す (試験以外の .go を変えると版の検査で 0.3.1 になるため)。この修正は v0.3.0 の直後に v0.3.1 として入れる。
