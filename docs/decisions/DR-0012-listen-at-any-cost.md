# DR-0012: 何かを失ってでも届く Listen は別パッケージ atanycost に置く

- Status: Accepted
- Date: 2026-11-19

## Context

ハブの [DR-0013](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0013-any-means-reach-at-any-cost.md) が any 系を決めた: 既定の経路が全部使えない時に何かを失ってでも届かせる opt-in で、既定の API に出さず、通った経路で失ったものの名前の並び (「失うもの」) を返す。`bind_any` は既定の bind が `ENAMETOOLONG` になる所で、最後に置き場の symlink を試す。Go に fork は無い ([DR-0004](DR-0004-thread-cwd-route.md)) ので、Go の既定の Listen が `ENAMETOOLONG` になる所は他の言語より広い ([DR-0011](DR-0011-central-route.md))。

## Decision

| 項目 | 決定 |
|---|---|
| 置き場 | 別パッケージ `github.com/kawaz/sockat-go/atanycost`。import することが opt-in |
| 関数 | `atanycost.Listen(path string) (*net.UnixListener, atanycost.Result, error)` |
| 戻り値 | 標準の `*net.UnixListener` と `Result{Losses []Loss; Route string; Skipped []Skip}`、`Skip{Name, Reason string}`、`Result.Has(Loss)`。Route は通った経路の名前 (SPEC の名前)、Skipped は Route より前に試して使えなかった経路の並び (試した順) と理由 (短い文字列)。Route・Skipped・Reason は診断用で中身を約束しない |
| 失敗 | `*atanycost.Error{Route; Skipped; Err}`。Route は実際に bind して失敗した経路 (全部使えなかった `ENAMETOOLONG` では空)。`Unwrap` で `*net.OpError` を返し、`errors.Is` で errno に届く |
| 失うもの | `type Loss string`: `Respelled` (`respelled`) / `RecreatableSpelling` (`recreatable-spelling`) / `CwdRelative` (`cwd-relative`) / `Forked` (`forked`、Go では付かない)。文字列は SPEC の名前と同じ。大小・包含は無い。名前は将来増えうるので、知らない名前は「何かを失った」とだけ読むと doc に書く |
| 中身 | `sockat.Listen` と同じ経路を試し、`ENAMETOOLONG` の時だけ置き場の symlink (ソケットのディレクトリの realpath を指す、綴り `<置き場>/sockat.XXXXXXXXXX/<ファイル名>`、bind の後すぐ消す)。候補は `XDG_RUNTIME_DIR`・`TMPDIR` (絶対パスの時)・`/tmp`、同じ綴りは 1 回。持ち主だけが書ける置き場 (ディレクトリ、持ち主が実効 uid、`mode & 0o022 == 0`) を先に、無ければ共有の置き場 (ディレクトリ、sticky、持ち主が root か実効 uid) を順に |
| 付く名前 | `standard` は空 (相対パスなら `[cwd-relative]`)、D を通す経路と -sockdir は `[respelled]`、持ち主だけが書ける置き場の symlink は `[respelled]`、共有の置き場の symlink は `[respelled, recreatable-spelling]` |

- 経路は親パッケージ `sockat` の非公開の関数にあり、`internal/anyhook` の関数の変数を通して `atanycost` に渡す (親パッケージの `init` が入れる)
- FreeBSD では symlink が届かせる範囲が無い: symlink の綴り `<置き場>/sockat.XXXXXXXXXX/<名前>` は at の綴り `R/<名前>` より必ず長く、at は FreeBSD で常に使える (symlink が効くのは、長さでなく手段が使えない時だけ)

## 理由

- Go で opt-in を表す慣習は「import するパッケージを分ける」(`net/http/httputil` 等)。build tag は利用者のビルド全体に要り、ライブラリの opt-in に向かない
- 名前 `atanycost.Listen` は「どんな犠牲を払ってでも」が読め、既定の `sockat.Listen` と並べて違いが分かる
- 失うものと経路の情報は `Result` の struct にまとめ、後でフィールドを足せる形にする
- `Loss` を文字列の型にするのは、文字列にした時に SPEC の名前と同じになり、知らない名前も値として持てるから
- 経路を `sockat` から export すると既定の API に出てしまう。`internal` の変数で渡せば、外からは `atanycost` 以外で使えない

## 却下した案

| 案 | 却下の理由 |
|---|---|
| `sockat.ListenAny` を親パッケージに置く | 既定の API に出る (SPEC は使う人が選んで有効にする仕組みの後ろに置く) |
| build tag (`sockat_any`) の後ろに置く | 利用者のビルド全体に tag が要り、依存の中で使うと伝わらない |
| 経路の実装を `internal/` に移し、両方のパッケージから呼ぶ | 試験が経路を直接呼ぶ形 ([DR-0009](DR-0009-routes-not-exposed.md)) を大きく組み替えることになる。渡すのは関数 1 つで足りる |

## 未確定

- 後始末つきの形 (`atanycost.ListenEx`) を置くか: ハブの DR-0013 の未確定

## 判断の出所

- any 系の振る舞い: kawaz 裁定 (ハブ D12-Q3、DR-0013) 2026-11-19
- 戻り値の失うもの・通った経路・飛ばした経路の並び、失敗時のエラーの形: kawaz 裁定 (ハブ e0357cff、f00f3217) 2026-11-19
- `cwd-relative`: kawaz 裁定 (D13-Q1、ハブ 7ab4e31e) 2026-11-19
- パッケージ・型・値の名前: 実装側の仮の判断 2026-11-19 (3 言語の形を見比べてから確定)
