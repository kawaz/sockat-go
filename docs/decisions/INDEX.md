# Decision Records 一覧

Status は各 DR ファイルの `Status:` 行が正本。

状態は絵文字とラベルだけ。日付・裁定の内訳は各 DR 本文に書く。

- `💭 提案`: Decision がまだ裁定されていない。実装に着手しない (本文の `Status: Proposed`)
- `✅ 実装済`: Decision の全部に実装エビデンスがある
- `🟡 部分実装`: 一部のみ実装
- `⬜ 未実装`: 裁定済みで設計のみ、実装エビデンスなし (着手してよい)
- `🚧 進行中`: 実装の途中
- `N/A`: 実装対象でない (命名・思想・プロセス等)
- `❌ 撤退`: 撤退判断済

| DR | 状態 | 説明 |
|---|---|---|
| [DR-0001](DR-0001-public-api.md) | ✅ 実装済 | 公開 API は `Listen` / `ListenEx` / `Dial` で、標準の具体型と `*net.OpError` を返す (`ListenEx` は discard 関数も) |
| [DR-0002](DR-0002-standard-handles-paths-it-accepts.md) | ✅ 実装済 | 標準が受け付けるパスと Linux の `@` は標準にそのまま渡す。NUL は長さによらず `EINVAL` |
| [DR-0004](DR-0004-thread-cwd-route.md) | ✅ 実装済 | fork の代わりに、スレッドだけ cwd を移す経路 (thread-cwd) を使う |
| [DR-0005](DR-0005-no-widened-sockaddr.md) | ✅ 実装済 | macOS の広げた sockaddr の経路 (widened) は作らず、connect の順では使えない経路として次へ進む (標準の型に渡すとアドレスの取得で落ちる) |
| [DR-0006](DR-0006-own-socket-into-net.md) | ✅ 実装済 | 自前のソケット (FreeBSD の bindat) は標準と同じ設定で作り、`net.FileListener` / `FileConn` で渡す |
| [DR-0007](DR-0007-long-path-listener-keeps-socket-file.md) | ✅ 実装済 | 長いパスの listener は Close でソケットファイルを消さない |
| [DR-0008](DR-0008-dependency-and-go-version.md) | ✅ 実装済 | 依存は `golang.org/x/sys` だけ、Go は支援中の 2 版 |
| [DR-0009](DR-0009-routes-not-exposed.md) | ✅ 実装済 | 経路は公開せず、試験はパッケージ内から `config` を差し替えて強制する |
| [DR-0011](DR-0011-central-route.md) | ✅ 実装済 | 長いパスは自分専用のディレクトリ D と乱数の symlink R を通す。Listen は D を通す経路 (と D 無しの形) だけ。D は 1 回の呼び出しで 1 度だけ作る |
| [DR-0012](DR-0012-listen-at-any-cost.md) | ✅ 実装済 | 何かを失ってでも届く Listen は別パッケージ `atanycost` に置き、失ったもの (`Losses`)・通った経路・飛ばした経路を `Result` で返す |
| [DR-0013](DR-0013-connect-order.md) | ✅ 実装済 | Dial は OS ごとの「connect の順」(書き込みもスレッドも要らない経路を先に) で試し、D を使う経路は D を共有する。proc-held は D に置く |

言語によらない振る舞いの正本は [ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md)。

退避した DR は [archive/INDEX.md](archive/INDEX.md)。
