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
| [DR-0002](DR-0002-standard-handles-paths-it-accepts.md) | ✅ 実装済 | 標準が受け付けるパスと Linux / Android の abstract の名前は標準にそのまま渡す (illumos の `@` は `./` を付けて普通のパスに、経路の綴りの先頭の `@` にも `./`)。空のパスとそれ以外の NUL は長さによらず `EINVAL` |
| [DR-0004](DR-0004-thread-cwd-route.md) | ✅ 実装済 | スレッドだけ cwd を移す経路 (thread-cwd・thread-cwd-direct) は、Unlock しない使い捨ての goroutine で作る |
| [DR-0005](DR-0005-no-widened-sockaddr.md) | ✅ 実装済 | Apple の OS の広げた sockaddr の経路 (widened) は作らず、connect の列では使えない経路として次へ進む (標準の型に渡すとアドレスの取得で落ちる) |
| [DR-0006](DR-0006-own-socket-into-net.md) | ✅ 実装済 | 自前のソケット (FreeBSD の bindat / connectat、stream と DGRAM) は標準と同じ設定で作り、`net.FileListener` / `FileConn` で渡す |
| [DR-0008](DR-0008-dependency-and-go-version.md) | ✅ 実装済 | 依存は `golang.org/x/sys` だけ、Go は支援中の 2 版 |
| [DR-0009](DR-0009-routes-not-exposed.md) | ✅ 実装済 | 経路は公開せず、試験はパッケージ内から `config` を差し替えた `world` で強制する。公開 API の試験は試験専用の置き場を使う |
| [DR-0016](DR-0016-public-api-go-shape.md) | ✅ 実装済 | 公開 API は SPEC の関数を Go の net の形に写す (stream・DGRAM・resolve・Sweep)。`Ex` は標準の型・`*Bound` / `Report`・error の 3 値、消す口は `*SocketFile` の `Remove` / `Cleanup`、綴りの性質は `Spelling` |
| [DR-0017](DR-0017-listener-close-by-spelling.md) | ✅ 実装済 | 既定の `Listen` の listener は Close で標準のまま綴りを unlink し、`Dangling` (と illumos の `./@`) だけ消さない。`ListenEx` の listener はどの綴りでも消さない |
| [DR-0018](DR-0018-route-orders-per-os.md) | ✅ 実装済 | 長いパスの経路は OS ごとの 1 つの列 (bind・connect・resolve) で試し、列に並ぶ経路だけを `Skipped` に入れる。ディレクトリは字句で畳まずカーネルと同じに引く。bind が symlink を辿るかは OS の表と実行時の見分け、FreeBSD の connect の link-file は作って CI で確かめる |
| [DR-0019](DR-0019-places-held-by-world.md) | ✅ 実装済 | 置き場の B と D はプロセスの `world` が fd (`int`) で持ち続け、閉じない。綴りの長さは B を作る前に見積もり、procB は `(st_dev, st_ino)` を確かめて使う。`Sweep` は D の形の名前だけを消す。`UsableCrossUID` の判定は初めて持った時の物で、B 自身の other の x が無い時だけ見直す。占拠された既定の名前の B は次の置き場へ。既定の bind の link-dir の E は (D, ディレクトリ) ごとに使い回す |

言語によらない振る舞いの正本は [ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md)。

退避した DR は [archive/INDEX.md](archive/INDEX.md)。
