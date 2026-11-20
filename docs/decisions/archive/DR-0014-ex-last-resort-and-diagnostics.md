# DR-0014: ListenEx / DialEx は最終手段を使うかを LastResort で受け取り、失ったもの・通った経路・飛ばした経路を返す

- Status: Superseded by DR-0016
- Date: 2026-11-19

## Context

ハブの [DR-0015](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0015-ex-with-last-resort-and-diagnostics.md) が、何かを失ってでも届く操作を別の関数・別のパッケージに置かず、既定の操作ごとに後始末・診断つきの `_ex` を 1 つずつ置くと決めた。`_ex` の引数で選べるのは「最終手段を使うか」の 1 つだけで、既定は使わない。最終手段は bind が symlink-dir (置き場のソケットのディレクトリを指す symlink)、connect が fork-cwd-direct (fork した子がファイル名で connect)。`_ex` は標準の型と一緒に失ったもの (`losses`)・通った経路 (`route`)・飛ばした経路 (`skipped`) を返し、失敗した時もエラーに `route` と `skipped` を付ける。型・項目・引数の形は各言語の慣習で決める。Go には fork が無い ([DR-0004](../DR-0004-thread-cwd-route.md))。

## Decision

公開 API は package `sockat` に置く ([DR-0001](DR-0001-public-api.md))。

```go
type LastResort bool
const (
	NoLastResort  LastResort = false
	UseLastResort LastResort = true
)
type Loss string // Respelled, CwdRelative, Forked, RecreatableSpelling
type Skip struct{ Name, Reason string }
type Result struct {
	Losses  []Loss
	Route   string
	Skipped []Skip
}
func (r Result) Has(l Loss) bool
type RouteError struct {
	Route   string
	Skipped []Skip
	Err     error // *net.OpError
}
func ListenEx(path string, lr LastResort) (l *net.UnixListener, discard func() error, res Result, err error)
func DialEx(path string, lr LastResort) (*net.UnixConn, Result, error)
```

| 項目 | 決定 |
|---|---|
| 最終手段を使うか | `LastResort` (bool を下に持つ型)。呼び出し側は定数 `NoLastResort` / `UseLastResort` で書く (doc と Example もそう書く)。ゼロ値は `NoLastResort` |
| 失うもの | `Loss` は文字列の型で、文字列は SPEC の名前と同じ (`respelled` / `cwd-relative` / `forked` / `recreatable-spelling`)。大小・包含は無い。知らない名前は「何かを失った」とだけ読むと doc に書く。`Forked` は Go では付かないが、名前をそろえるために置く |
| 成功した時 | `Result`。`Losses` と `Skipped` は空でも nil にしない。`Route`・`Skipped`・`Reason` は診断用で中身を約束しない |
| 失敗した時 | いつも `*RouteError` (標準が失敗した短いパスも、経路を試す前の失敗も)。`Unwrap` で `Listen` / `Dial` と同じ `*net.OpError` を返し、`errors.Is` で errno に、`errors.As` で `*net.OpError` に届く。`Route` は実際に bind / connect して失敗した経路 (短いパスは `standard`、経路を試す前の失敗と全部使えなかった `ENAMETOOLONG` は空)。戻り値の `Result` はゼロ値 |
| `ListenEx` の後始末 | `discard` は [DR-0001](DR-0001-public-api.md) のまま (消すだけで listener を閉じない)。listener は短いパスも `SetUnlinkOnClose(false)` |
| `ListenEx` の最終手段 | 既定の経路が全部使えない (`ENAMETOOLONG` で、bind した経路が無い) 時だけ symlink-dir を試す。候補は `XDG_RUNTIME_DIR`・`TMPDIR` (空でない絶対パスの時)・`/tmp`、同じ綴りは 1 回。持ち主だけが書ける置き場 (ディレクトリ、持ち主が実効 uid、`mode & 0o022 == 0`) を順に試し、無ければ共有の置き場 (ディレクトリ、sticky、持ち主が root か実効 uid) を順に。綴り `<置き場>/sockat.XXXXXXXXXX/<ファイル名>` が入らない候補には何も作らない。symlink はソケットのディレクトリの realpath を指し、bind の後すぐ消す。losses は持ち主だけの置き場で `[respelled]`、共有の置き場で `[respelled, recreatable-spelling]`。どの候補も使えなければ symlink-dir を `Skipped` の最後に入れて `ENAMETOOLONG` |
| `DialEx` の最終手段 | fork-cwd-direct は作れないので、既定の connect の順が全部使えない時に `Skipped` の最後に `no fork route in sockat-go` で入れ、`ENAMETOOLONG` を返す。届く所は `Dial` と同じ。`Losses` はいつも空 (connect の綴りは相手に残らない) |
| 既定の操作 | `Listen` / `Dial` は変えない (最終手段を使わず、診断も返さない) |

- 最終手段を使わない `ListenEx` / `DialEx` は `Listen` / `Dial` と同じ経路を同じ順に試す
- 経路は package の中の関数のままで、`_ex` は経路を試した記録 (飛ばした経路と理由) を `Result` / `RouteError` に写す ([DR-0009](../DR-0009-routes-not-exposed.md))
- 適合試験の CLI (`internal/cmd/sockat-conform`) は `ListenEx` / `DialEx` に `NoLastResort` を渡して試す (公開 API で作れるので、経路を渡す内部の口は置かない)

## 理由

- 名前付きの型と定数にすると、呼び出しが `ListenEx(path, sockat.UseLastResort)` と読め、何を選んだかがその場で分かる。裸の `true` は読み手に意味が伝わらない
- 型は bool を下に持つので、ゼロ値が「使わない」になり、SPEC の既定と一致する
- 失うものを名前の文字列の並びにするのは、包含や順を考えずに足せ、文字列にした時に SPEC の名前と同じになるから (ハブの DR-0015)
- 失敗した時の型を 1 つ (`*RouteError`) にすると、利用者は `errors.As` 1 回で経路の記録に届く。中身を `*net.OpError` のままにして、`Listen` / `Dial` から置き換えた時の errno の扱いを変えない
- symlink-dir の置き場の順 (持ち主だけ → 共有) と losses はハブの DR-0015 のまま

## 却下した案

| 案 | 却下の理由 |
|---|---|
| 最終手段の引数を裸の `bool` にする | 呼び出しが `ListenEx(path, true)` になり、何を選んだか読めない |
| functional options (`ListenEx(path, sockat.WithLastResort())`) | 選べるのは 1 つだけと決まっている (ハブの DR-0015 の案 B)。選択肢が増える形を先に作ることになる |
| `LastResort` を中身の見えない struct にして、裸の `true` / `false` を compile で弾く | 定数にできず (package の変数になり、外から書き換えられる)、比較や表示も不便になる。bool の型でも定数で書く例を doc に置けば読みやすさは得られる |
| 何かを失ってでも届く Listen を別パッケージ (`atanycost`) に置く | ハブの DR-0015 の案 A。API が増え、推奨コードと結び付けにくい |
| 失敗した時は `*net.OpError` を返し、経路の記録は別の戻り値にする | 失敗の情報が 2 か所に分かれ、標準の作法 (`error` 1 つ) から外れる |

## Consequences

- bool を下に持つ型なので、裸の `true` / `false` (untyped の定数) も compile が通る。定数で書くことは doc と Example で誘導するだけで、強制はしない

## 判断の出所

- `_ex` の振る舞い (最終手段の有無、losses / route / skipped、失敗時の記録、discard は消すだけ): kawaz 裁定 (ハブの DR-0015) 2026-11-19
- Go の形 (`LastResort` の型と定数、`Result`、`RouteError`、`ListenEx` / `DialEx` の戻り値): 統括の案、kawaz 了承 2026-11-19
- 適合試験の CLI が公開 API を使うこと: 実装側の判断 2026-11-19
