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
| [DR-0001](DR-0001-public-api.md) | 🚧 進行中 | 公開 API は `Listen` / `Dial` の 2 つで、標準の具体型と `*net.OpError` を返す |
| [DR-0002](DR-0002-standard-handles-paths-it-accepts.md) | 🚧 進行中 | 標準が受け付けるパスと Linux の `@` は標準にそのまま渡す。NUL は長さによらず `EINVAL` |
| [DR-0003](DR-0003-route-order.md) | 🚧 進行中 | 長いパスの経路と試す順番 (OS × Listen / Dial) |
| [DR-0004](DR-0004-thread-cwd-route.md) | 🚧 進行中 | fork の代わりに、スレッドだけ cwd を移す経路を最後に試す |
| [DR-0005](DR-0005-widened-sockaddr-only-for-dial.md) | 🚧 進行中 | macOS の広げた sockaddr は Dial にだけ使う |
| [DR-0006](DR-0006-own-socket-into-net.md) | 🚧 進行中 | 自前のソケットは標準と同じ設定で作り、`net.FileListener` / `FileConn` で渡す |
| [DR-0007](DR-0007-long-path-listener-keeps-socket-file.md) | 🚧 進行中 | 長いパスの listener は Close でソケットファイルを消さない |
| [DR-0008](DR-0008-dependency-and-go-version.md) | 🚧 進行中 | 依存は `golang.org/x/sys` だけ、Go は支援中の 2 版 |
| [DR-0009](DR-0009-routes-not-exposed.md) | 🚧 進行中 | 経路は公開せず、試験はパッケージ内から `config` を差し替えて強制する |

言語によらない振る舞いの正本は [ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md)。
