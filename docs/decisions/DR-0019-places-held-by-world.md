# DR-0019: 置き場の B と D はプロセスの world が fd で持ち続け、閉じない

- Status: Accepted
- Date: 2026-11-21

## Context

[ハブの SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md) の「B の作り方」「実体の置き場」「綴りの形」「別の uid と PrivateTmp」「D と E」が、利用者ごとの置き場 B、(プロセス, B) ごとのディレクトリ D、symlink E の作り方を決める。B は置き場の順 (`/tmp` → `$XDG_RUNTIME_DIR` → `$TMPDIR` → ソケットのディレクトリ、または `Options.Base`) で最初に作れた所で、プロセスは一度持った B を使い続け、D には共有ロックを掛けた fd を持ち続ける。Go で決めることは、それをプロセスのどこに持つか、fd をどう持つか、綴りの長さをいつ見積もるか、`/proc` を通す綴り (procB) をいつ使えるとするか、`Symlinkat` の無い OS での E の作り方。

## Decision

プロセスの状態:

- package の変数 `defaultWorld` (`*world`) が、環境の読み方 (`config`) と、作った B を持つ。公開 API はどれもこれを使う
  - `auto`: 置き場 1〜3 の B (プロセスに 1 つ)
  - `autoCross`: `UsableCrossUID` の B (途中と B 自身が全部 other に x を持つ所。`auto` がそうなら同じ物)
  - `sockdir`: 置き場 4 の B (ソケットのディレクトリの `(st_dev, st_ino)` ごと)
  - `given`: `Options.Base` の B (B の `(st_dev, st_ino)` ごと)
  - `checked`: 途中の確かめが済んだ置き場の実パス (プロセスで 1 度だけ確かめる)
- B と D の fd は `int` で持ち、close-on-exec で、閉じない (`*os.File` にすると finalizer が閉じる)。消されていたと分かった B・D は外して作り直すが、外した方の fd も閉じない (渡した E の `Cleanup` が D の fd で unlink するので、閉じた番号が使い回されると別の所を消す)
- 作れなかった置き場は覚えず、次の呼び出しで試し直す。D を作れなかった B は、その呼び出しの後の経路で作り直さず、別の B も探さない
- B・D が消されたかは、fd の `fstat` の `st_nlink` が 0 か、名前の `lstat` (`AT_SYMLINK_NOFOLLOW`) と fd の `(st_dev, st_ino)` が違うかで見る。macOS (APFS) では消したディレクトリの `st_nlink` が 2 のままなので、名前の確かめを省かない
- `Options.Base` の安全の確かめ (途中・持ち主・他人が書けない) は呼び出しのたびに行う。`UsableCrossUID` のための other の x の判定は、B を初めて持った時点の物を覚えて見直さない

作り方 (SPEC-routes のとおり):

- B: 置き場を realpath → 途中を確かめる → 置き場を `O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC` で開く → `mkdirat(0700)` → `fchmodat(0711)` → 同じフラグで開いて `fstat` (実ディレクトリ・持ち主が実効 uid)。`EEXIST` なら開いて確かめ (他人が書けないことも)、mode は変えない。外れたら同じ置き場の `sockat-<実効 uid>.<乱数 10>` に落とす (`Options.Base` はエラー)。そろえられない・開けない時は作った自分の空のディレクトリを消す
- D: B の fd を基準に乱数 10 文字で `mkdirat(0700)` → `fchmodat(0711)` → 開いて確かめる → `flock(LOCK_SH)` → 名前を引き直して fd と同じ物か確かめ、違えば (間に他のプロセスの `Sweep` が消した) 作り直す (16 回まで)
- E: resolve の E は B ごとのカウンタを 36 進にした名前、thread-cwd・at と既定の connect の E は乱数 10 文字。illumos / Solaris は `golang.org/x/sys/unix` に `Symlinkat` が無いので、D (か B) の実パスから組んだパスで `unix.Symlink` を呼ぶ
- 乱数は英数 62 種の文字で、`crypto/rand` から作る

綴りの長さと procB:

- 綴りが収まらないと先に分かる時は B を作らない。見積もりは置き場の実パスと B の名前、最短の procB (`/proc/<pid>/fd/0`)、最短の E の名前で行い、B を持った後に実際の fd と次の E の名前で確かめ直す。収まらなければ次の置き場には移らず、その経路を使えないとする
- `/proc/<pid>/fd` を引けない時は、procB は収まらないとして見積もる (procB を使えない)
- procB は、`/proc/<pid>/fd/<B の fd>` の `stat` が B の `(st_dev, st_ino)` と同じ時だけ使う。ソケットのディレクトリの B は procB の綴りでだけ使う

`Sweep` / `SweepBase`:

- `Sweep` は置き場 1〜3 の既定の名前の B (`sockat-<uid>`・`sockat`) のうち、あって途中の確かめに通り、自分の物で他人が書けない物だけを見る。B を作らない。`SweepBase` は渡されたパスの B を `Options.Base` と同じに確かめ (形の誤りは `EINVAL`、symlink・他人の物・他人が書ける B や途中は `EPERM`、ディレクトリでなければ `ENOTDIR`)、B か親が無ければ何もしない
- B の直下の、名前が D の形 (英数字 10 文字) のディレクトリを `O_NOFOLLOW` で開き、自分の物で `flock(LOCK_EX | LOCK_NB)` が取れ、名前が同じ物を指し、中が symlink だけなら、symlink ごと消す。B の直下の E と、symlink でない物がある D には触らない

## 理由

- 置き場の B はプロセスをまたいで使い回す物で、プロセスが持つ B と D はプロセスが生きている間ずっと要る (D のロックが生きていることの印)。package の変数に持つのが、Go で 1 プロセスに 1 つの状態を持つ素直な形
- fd を閉じないのは、閉じた番号の使い回しで、procB の綴りや E の片付けが無関係な所に当たるのを防ぐため。消されて外した B・D の fd も、数個がプロセスの間残るだけ
- 見積もりで先に止めると、収まらないパスのために B を作ったり別の置き場に B を作ったりしない。プロセスの B がパスの長さで変わらないので、同じ利用者のプロセスは同じ所に B を見つける
- illumos の代わりの作り方は、D (と B) が自分の 0711 のディレクトリで、途中が他人に付け替えられないことを確かめ済みなので、パスで作っても fd で作るのと同じ所に当たる

## 却下した案

| 案 | 却下の理由 |
|---|---|
| B・D の fd を `*os.File` で持つ | finalizer が閉じうる。閉じた番号が使い回されると procB の綴りが別の所を指す |
| 消された B・D を外した時に fd を閉じる | 渡した E の `Cleanup` が D の fd で unlink するので、番号の使い回しで別の所を消しうる |
| 綴りが収まらない時に次の置き場で B を作る | プロセスの B がパスの長さで変わり、置き場ごとに B と D が増える (ハブの答え) |
| `/proc/<pid>/fd/<B の fd>` を引けるだけで procB を使う | PID の名前空間と `/proc` の mount が食い違うと、別のプロセスの fd を指しうる |

## 判断の出所

- B・D・E の作り方、置き場の順、見積もりで次の置き場に移らないこと、消された B・D を作り直すこと、`Sweep` の対象: ハブの SPEC・SPEC-routes・DR-0017 と、ハブの答え (2026-11-21)
- world に持つこと、fd を `int` で持ち閉じないこと、procB の見積もりで `/proc/<pid>/fd` を引けない時の扱い、illumos の `Symlink`: 実装側の判断 2026-11-21 (illumos の扱いはハブの答えで了承)
