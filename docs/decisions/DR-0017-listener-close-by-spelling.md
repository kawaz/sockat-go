# DR-0017: 既定の Listen の listener は Close で標準のまま綴りを unlink し、Dangling だけ消さない。ListenEx の listener は消さない

- Status: Accepted
- Date: 2026-11-21

## Context

Go の標準の listener は Close で、覚えているアドレスの綴りを unlink する (`SetUnlinkOnClose` の既定が true。`net/unixsock_posix.go`)。長いパスの listener が覚えているのは bind の綴りで、unlink した時に起きることは綴りで違う:

| 綴り | unlink すると |
|---|---|
| link-dir (`B/D/E/<ファイル名>`、`/proc/<pid>/fd/<B の fd>/D/E/<ファイル名>`) | 途中の E を辿り、元のパスのソケットのファイルが消える |
| link-file (`B/D/E`) | 最後の symlink は辿られず、E が消える。ソケットのファイルは残る |
| `Dangling` (thread-cwd・at の `E/<ファイル名>`) | E は既に無く、プロセスの cwd からの相対で解決される。cwd に同じ名前の物があればそれに当たる |

[ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) の「長いパスで標準と違う点」は、既定の bind の listener を閉じた時は標準と同じにし、`Dangling` の綴りは unlink させず、`_ex` の bind はどの綴りでも閉じた時に消さないと決めている。

## Decision

| 呼び出し | 通った経路 | `SetUnlinkOnClose` | Close で起きること |
|---|---|---|---|
| `Listen` | standard (元の絶対パス、または相対パスや長い絶対パスの絶対パスの候補、[DR-0020](DR-0020-absolutize-relative-path.md)) | 触らない | 標準のまま (綴りは絶対パスで、元のパスのファイルが消える) |
| `Listen` | standard (経路が全部使えず相対のまま渡した相対パス、[DR-0020](DR-0020-absolutize-relative-path.md)) | 触らない (illumos の `./@...` は `false`、[DR-0002](DR-0002-standard-handles-paths-it-accepts.md)) | 標準のまま (`./@...` は何も消さない) |
| `Listen` | link-dir | 触らない | 途中の E を辿り、元のパスのファイルが消える |
| `Listen` | link-file | 触らない | E が消え、ソケットのファイルは残る |
| `Listen` | thread-cwd・at (`Dangling`) | `false` | 何も消さない |
| `ListenEx` | どれも (短いパスも) | `false` | 何も消さない。消すのは `Bound.File` の `Remove` だけ |

- DGRAM (`ListenUnixgram`・`ListenUnixgramEx`) の `*net.UnixConn` は標準が元から Close で消さないので、何もしない
- `Listen` の doc と利用マニュアルの「Go に固有の注意」に、link-file の綴りでは閉じてもファイルが残ることを書く

## 理由

- 既定の `Listen` は標準と同じ仕組みに任せる (SPEC)。link-dir の綴りは綴りが生きている間は元のパスと同じ所を指すので、unlink しても無関係なファイルに当たらない
- `Dangling` の綴りを unlink させると、cwd に同じ名前の物がある時にそれを消す。sockat がその危険を足さない
- `ListenEx` は置き換えを `(st_dev, st_ino)` で見分ける `Remove` を持つ。Close が綴りで消すと、置き換わったファイルを見分けずに消す

## 却下した案

| 案 | 却下の理由 |
|---|---|
| 長いパスの listener は全部 `SetUnlinkOnClose(false)` | link-dir でも Close でファイルが消えず、SPEC の「標準と同じにする」と違う |
| どの listener も `SetUnlinkOnClose` を触らない | `Dangling` の綴りを cwd からの相対で unlink しに行き、cwd の同じ名前の物に当たりうる |
| Close で元のパスを消す wrapper を返す | 標準と同じ型でなくなる ([DR-0016](DR-0016-public-api-go-shape.md)) |

## 判断の出所

- ハブの答え (2026-11-21、3 言語共通として SPEC に入った): 既定の `Listen` は `SetUnlinkOnClose` を触らず `Dangling` だけ `false`、`ListenEx` はどの綴りでも `false`
