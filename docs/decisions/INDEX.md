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
| [DR-0004](DR-0004-thread-cwd-route.md) | ✅ 実装済 | fork の代わりに、スレッドだけ cwd を移す経路を最後に試す |
| [DR-0005](DR-0005-no-widened-sockaddr.md) | ✅ 実装済 | macOS の広げた sockaddr の経路は使わない (標準の型に渡すとアドレスの取得で落ちる) |
| [DR-0006](DR-0006-own-socket-into-net.md) | ✅ 実装済 | 自前のソケット (FreeBSD の bindat) は標準と同じ設定で作り、`net.FileListener` / `FileConn` で渡す |
| [DR-0007](DR-0007-long-path-listener-keeps-socket-file.md) | ✅ 実装済 | 長いパスの listener は Close でソケットファイルを消さない |
| [DR-0008](DR-0008-dependency-and-go-version.md) | ✅ 実装済 | 依存は `golang.org/x/sys` だけ、Go は支援中の 2 版 |
| [DR-0009](DR-0009-routes-not-exposed.md) | ✅ 実装済 | 経路は公開せず、試験はパッケージ内から `config` を差し替えて強制する |
| [DR-0010](DR-0010-unsafe-base-held-by-fd.md) | ✅ 実装済 | `/proc` を使える Linux の Dial の予備の経路は、安全でない置き場に 0700 のディレクトリを作り fd で掴んで symlink を置く |
| [DR-0011](DR-0011-central-route.md) | ✅ 実装済 | 長いパスは自分専用のディレクトリ D と乱数の symlink R を通す (中心の経路)。Listen は中心の経路だけ、Dial は予備の経路も |
| [DR-0012](DR-0012-listen-at-any-cost.md) | ✅ 実装済 | 何かを失ってでも届く Listen は別パッケージ `atanycost` に置き、失ったものの段階 (`Loss`) を `Result` で返す |

言語によらない振る舞いの正本は [ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md)。

退避した DR は [archive/INDEX.md](archive/INDEX.md)。
