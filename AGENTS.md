# AGENTS.md — sockat-go の作業ルール

sockat-go は、`sun_path` の上限を超えるパスでも Unix domain socket を bind / connect できる Go のライブラリ (公開 module `github.com/kawaz/sockat-go`)。短いパスは標準の `net` にそのまま渡し、長いパスだけを OS ごとの経路で短い綴りにして渡す。

言語によらない振る舞いの正本はハブの [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) (kawaz/sockat) で、sockat-go はその Go 実装。

## 必読

- [docs/decisions/](docs/decisions/INDEX.md) — Go 版の設計 (公開 API、中心の経路と試す順番、スレッド cwd、macOS の広げた sockaddr を使わない理由、長いパスの listener)。経路に手を入れる前に読む
- [README-ja.md](README-ja.md) — 利用者に約束している振る舞い (標準と違う点、どこから届かなくなるか)

## 構成

| パス | 中身 |
|---|---|
| `doc.go` / `sockat.go` | package doc と公開 API (`Listen` / `ListenEx` / `Dial`) |
| `route.go` | `run` (短いパスの判定、NUL、abstract)、`viaStd`、エラーの形、`config` |
| `central.go` | 長いパスの入口 (`runLong`) と中心の経路 (D と R、手段の順、片付け) |
| `posix.go` | Dial の予備の経路 (相対パス → symlink / fd で掴む形、FreeBSD 以外) |
| `os_*.go` | OS ごとの Dial の予備の経路 (`fallbackDial`)、ディレクトリと D の開き方、スレッド cwd の `threadChdir`、bindat / connectat |
| `threadcwd*.go` | スレッドだけ cwd を D に移す手段 (使い捨ての goroutine) |
| `raw.go` | 自前のソケットを `net.FileListener` / `FileConn` に渡す (FreeBSD の bindat) |
| `anyroute.go` / `atanycost/` / `internal/anyhook/` | 何かを失ってでも届く Listen (`atanycost.Listen`、置き場の symlink、失ったものの段階)。経路は `sockat` の中にあり、`internal/anyhook` で渡す |
| `path.go` | パスの分割・相対パスの計算 (ファイルシステムに触らない) |
| `*_test.go` | 経路を強制するパッケージ内の試験 |

## 守ること

- **振る舞いを変える時は、先にハブの SPEC を直す。** sockat-go の docs と実装はその後に追従させる
- **標準が受け付けるパスには何も足さない。** `net.ListenUnix` / `net.DialUnix` をそのまま呼ぶ (DR-0002。例外は NUL の `EINVAL`)
- **経路の本当の失敗は次の経路でやり直さない。** 次へ進むのは経路自体が使えない時だけ (DR-0011)
- **bind は中心の経路だけ。** 綴りが後で無関係なファイルに当たる経路 (/proc の D を通さない形、相対パス、ファイル名だけ) を bind に使わない (DR-0011)
- **スレッド cwd の goroutine は `UnlockOSThread` しない。** cwd が変わったスレッドは goroutine と一緒に捨てる (DR-0004)
- **長いパスの listener は `SetUnlinkOnClose(false)`。** 覚えているアドレスは綴りなので (DR-0007)
- **経路は公開 API に出さない。** 試験はパッケージ内で各経路の関数を呼ぶ (DR-0009)
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
