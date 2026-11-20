# AGENTS.md — sockat-go の作業ルール

sockat-go は、`sun_path` の上限を超えるパスでも Unix domain socket を bind / connect できる Go のライブラリ (公開 module `github.com/kawaz/sockat-go`)。短いパスは標準の `net` にそのまま渡し、長いパスだけを OS ごとの経路で短い綴りにして渡す。

言語によらない振る舞いの正本はハブの [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) (kawaz/sockat) で、sockat-go はその Go 実装。

## 必読

- [docs/decisions/](docs/decisions/INDEX.md) — Go 版の設計 (公開 API の形、listener の Close、OS ごとの経路の列、置き場の B・D の持ち方、スレッド cwd、広げた sockaddr を使わない理由、試験の置き場)。経路に手を入れる前に読む
- [README-ja.md](README-ja.md) — 利用者に約束している振る舞い (Spelling、片付け、置き場に残る物、標準と違う点、どこから届かなくなるか)
- ハブの [DR-0017](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0017-resolve-through-persistent-base.md)・[SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md)・[SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md) — 置き場と経路の作り方の正本

## 構成

| パス | 中身 |
|---|---|
| `doc.go` / `sockat.go` | package doc と公開 API の `Listen` / `Dial` / `ListenEx` / `DialEx` |
| `datagram.go` | 公開 API の `ListenUnixgram` / `DialUnixgram` と各 `Ex` |
| `resolve.go` | 公開 API の `ResolveForBind` / `ResolveForConnect` と `ForBind` (`SocketFile`、`Cleanup`) / `ForConnect` |
| `sweep.go` | 公開 API の `Sweep` / `SweepBase` (B の中の死んだ D を消す) |
| `options.go` / `spelling.go` | `Options`・`Bound`・`Report`・`Skip`・`RouteError` と `Spelling`・`SpellingKind`・`Until` |
| `socketfile.go` | `SocketFile` (bind した直後に覚える、`Remove`・`Cleanup`) と E の片付けの共有 |
| `route.go` | 入口 (`bind`・`connect`: 短いパス、NUL、abstract、illumos の `@`)、`viaStd`、エラーの形、経路の名前、試した経路の記録 |
| `world.go` | `config` (環境の読み方) と `world` (プロセスが持つ B)、`defaultWorld` |
| `base.go` | B (置き場の順、途中の確かめ、作る・確かめる、`UsableCrossUID` の B、`Options.Base`、消された B の作り直し) |
| `dir.go` | D (作る・flock・作り直す) と E (resolve の E、呼び出しごとの E) |
| `link.go` | B を通る綴り (procB と実パス、長さの見積もり)、`Spelling` と診断の `Base` の値 |
| `bind.go` | bind の列 (link-dir・link-file・thread-cwd / at) と `ListenEx` の覚え方 |
| `dial.go` | connect の列を回す `dialCall`、thread-cwd-direct と既定の connect の link-file |
| `posix.go` | connect の relative (FreeBSD 以外) |
| `os_*.go` | OS ごとの connect の列 (`dialSteps`) とその OS だけの経路 (proc-direct、widened、at-direct)、`/proc` を使えるか、ディレクトリの開き方、スレッド cwd の `threadChdir`、bindat / connectat、bind が symlink を辿るかの表 |
| `threadcwd*.go` | スレッドだけ cwd を移す手段 (使い捨ての goroutine) |
| `raw.go` | 自前のソケットを `net.FileListener` / `FileConn` に渡す (FreeBSD の bindat / connectat、stream と DGRAM) |
| `path.go` / `fileid.go` / `names.go` | パスの分割・相対パスの計算、`(st_dev, st_ino)` と realpath、乱数の名前 |
| `abstract_*.go` / `stdat_*.go` / `at_none.go` / `symlinkat*.go` | build tag ごとの定数と関数 (abstract の名前、標準が `@` を読むか、at の有無、illumos の `Symlinkat` の代わり) |
| `internal/cmd/sockat-conform` | 適合試験の CLI (ハブの conformance.md。`ListenEx` / `DialEx` に `Options` を渡す) |
| `*_test.go` | 経路を強制するパッケージ内の試験 (`main_test.go` の `TestMain` が試験専用の置き場を用意する)。`example_long_test.go` は公開 API だけを使う外の試験で、README の推奨コードを動かす |

## 守ること

- **振る舞いを変える時は、先にハブの SPEC を直す。** sockat-go の docs と実装はその後に追従させる
- **標準が受け付けるパスには何も足さない。** `net.ListenUnix` / `net.ListenUnixgram` / `net.DialUnix` をそのまま呼ぶ (DR-0002。例外は NUL の `EINVAL` と illumos の `@`)
- **経路の本当の失敗は次の経路でやり直さない。** 次へ進むのは経路自体が使えない時だけ。失敗した時はその呼び出しで作った E を消してから返す (DR-0018)
- **bind は link-dir・link-file・thread-cwd / at だけ。** 綴りが後で無関係なファイルに当たる経路 (proc-direct、relative、at-direct) と widened を bind に使わない。link-file は bind が symlink を辿らないと実測した OS では試さない (DR-0018)
- **`Skipped` にはその呼び出しの列に並ぶ経路だけを入れる。** Linux の connect の行は `/proc` を使えるかで呼び出しの初めに 1 度だけ選ぶ (DR-0018)
- **既定の connect は D を作らず、E を残さない。** B の直下の E は connect の後すぐ消す (DR-0018)
- **B と D の fd は `int` で持ち、閉じない。** 消されて外した B・D の fd も閉じない (DR-0019)
- **綴りが収まらないと見積もった時は B を作らず、次の置き場にも移らない。** プロセスの B はパスの長さで変わらない (DR-0019)
- **スレッド cwd の goroutine は `UnlockOSThread` しない。** cwd が変わったスレッドは goroutine と一緒に捨てる (DR-0004)
- **既定の `Listen` は `SetUnlinkOnClose` を触らず、`Dangling` と illumos の `./@` だけ `false`。`ListenEx` 系はどれも `false`** (DR-0017)
- **経路は公開 API に出さない。** 試験はパッケージ内で `config` を差し替えた `world` で各経路を強制する (DR-0009)
- **試験は本物の `/tmp/sockat-<uid>` を使わない。** 公開 API の試験も `TestMain` が向けた試験専用の置き場を使う (DR-0009)
- 公開 API・doc・エラーの文言は英語。DR と日本語の docs は日本語

## 試験

- `just test` (macOS)、`just test-linux` (docker で Linux。スレッド cwd が使えない既定の seccomp と、使える seccomp=unconfined の両方)。経路は OS で違うので、変更したら両方で回す
- 一時ディレクトリはランダム名で作り、試験の終わりに消す。長さを決めたいので、基本は `/tmp` の下に作る
- cwd のようにプロセス全体に効く設定が要る試験は、試験バイナリを子プロセスで起こして中で行う。試験は並行して走るので、同じプロセスで変えない
- 各試験の直前に「何を確かめ、なぜそうなるべきか」をコメントで書く。DR の番号だけで済ませない

## VCS と push

- jj colocate。commit は `jj commit -m "..." <files...>` でパスを指定する
- push は `just push` だけ (lint・試験・翻訳ペアの鮮度・version の上げ忘れを検査してから push する)
- README は日本語版 (`README-ja.md`) が正本。英語版は日本語版を直した同じ変更で直す
- 試験以外の `.go` / `go.mod` / `go.sum` を変えたら `just bump-version` で `VERSION` を上げる。main に `VERSION` が push されると、CI を通してから release workflow が tag と GitHub Release を作る (tag を手で打たない)
