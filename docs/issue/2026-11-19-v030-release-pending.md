---
title: v0.3.0 (中心の経路、ListenEx、atanycost、適合試験の CLI) の release 待ちと残り
status: open
category: task
created: 2026-11-19
---

# v0.3.0 (中心の経路、ListenEx、atanycost、適合試験の CLI) の release 待ちと残り

main@origin は v0.2.0 の後の README の 1 commit まで。それより後の commit (DR-0011〜0013 の実装、ListenEx、atanycost、適合試験の CLI) はローカルの main にあり、push していない。VERSION は 0.2.0 のまま。

## 待っているもの (ハブの統括 → kawaz)

- release の OK。出す時は `just bump-version minor` (0.3.0) → `just push`

## 次にやる (ハブの統括の依頼 2026-11-19、ハブ origin/main = 40a4b54c)

Dial を新しい connect の順に作り直した後に着手する。実装・試験・README (日英)・DR を合わせて commit まで (push / VERSION は待ち)。読むもの: ハブの DR-0015、DR-0016、SPEC。

- DR-0015: `atanycost` パッケージを無くし、既定の操作ごとに `_ex` (`ListenEx`、`DialEx`) を置く。引数は「最終手段を使うか」1 つだけ (既定は使わない)。Listen の最終手段は symlink-dir (持ち主だけの置き場 → 共有の置き場、今の atanycost の symlink)。Dial の最終手段は fork-cwd-direct (Go には fork が無いので理由付きで skipped に入って次へ)。戻り値は標準の型と losses・route・skipped、`ListenEx` は加えて discard。失敗時はエラーに route・skipped (`errors.Is` で errno)。型・項目・引数の形は Go の慣習で決め、案をハブに報告する
- DR-0016: 経路の名前を変える (fork → fork-cwd、fork-sockdir → fork-cwd-sockdir、fork-direct → fork-cwd-direct、Dial の symlink → symlink-file、atanycost の symlink → symlink-dir)。Listen は Linux / Android 以外で standard の次に symlink-file を試す (失敗時に元のパスに物があればそのエラー、無いか確かめられなければ次へ)。macOS ではファイル名の上限が無くなる
- SPEC の proc: 綴りが収まらない時も D を片付けず、後の経路と共有する (D は 1 回だけ作る)
- 適合試験の CLI も新しい名前で出す。終わったらハブの `just conformance` を流して報告する

## 残り

- ListenEx で bind は通ったのにファイルを覚えられない分岐は試験で作れず未試験 (DR-0001)
- D を作った直後から開くまでの間に、group / other の権限付きのディレクトリ (中に `s` や別のファイルがある) にすり替えられた時、それを使わず中身にも触らないことの試験が無い (ハブの SPEC「実装が確かめる試験の観点」、「D と R」の「確認に落ちた D は他人の物とみなし、中身も含めて触らない」)。Dial の作り直しで proc-held の専用ディレクトリの試験を消した時に、この観点の試験も一緒に消えた。今あるのは別 uid の空の D (`makeDIn` 単体) と `isOurPrivateDir` の判定だけ。_ex の統合が終わった後に `makeDIn` の経路で足す
