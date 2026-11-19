# DR-0007: 長いパスの listener は Close でソケットファイルを消さない

- Status: Accepted
- Date: 2026-11-18

## Context

Go の標準の listener は Close でソケットファイルを消す (`SetUnlinkOnClose` の既定が true。実測)。消すパスは listener が覚えているアドレスで、長いパスの経路ではそれが綴り (`R/<ファイル名>`、`/proc/self/fd/N/R/<ファイル名>`、もう無い symlink) になる。

## Decision

- 短いパスは標準のまま (Close で消える)
- 長いパスの listener は `SetUnlinkOnClose(false)` にし、消さない
- README の「長いパスで標準と違う点」と `Listen` の doc に書く。長いパスの listener に `SetUnlinkOnClose(true)` を呼ばないことも書く
- 後でファイルを消したい利用者には `ListenEx` の `discard` を出す ([DR-0001](DR-0001-public-api.md))。`ListenEx` の listener は短いパスも `SetUnlinkOnClose(false)` にし、消すのは `discard` だけにする

## 理由

- 綴りを後で unlink すると、その時の fd 番号や cwd で解決され、別のファイルを消しうる
- 短いパスは標準にそのまま渡すのが第一原則 ([DR-0002](DR-0002-standard-handles-paths-it-accepts.md))

## 却下した案

| 案 | 却下の理由 |
|---|---|
| 長さによらず消さない (短いパスも `SetUnlinkOnClose(false)`) | 短いパスに何かを足すことになる |
| Close で元のパスを消す wrapper を返す | 標準と同じ型でなくなる |

## 判断の出所

- 統括の裁定 2026-11-18 (SPEC: 短いパスは標準と同じ、長いパスは消さず README に書く)
