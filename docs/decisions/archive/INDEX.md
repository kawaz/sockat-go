# 退避した Decision Records

| DR | タイトル | 一言 | 置き換え先 |
|---|---|---|---|
| [DR-0001](DR-0001-public-api.md) | 公開 API は Listen / Dial と、後始末・診断つきの ListenEx / DialEx で、標準の具体型を返す | `ListenEx` が discard 関数と `Result` を返していた頃の公開 API | [DR-0016](../DR-0016-public-api-go-shape.md) |
| [DR-0003](DR-0003-route-order.md) | 長いパスの経路と試す順番 | bind も相対パス・symlink・/proc を使っていた頃の順番 | [DR-0018](../DR-0018-route-orders-per-os.md) |
| [DR-0007](DR-0007-long-path-listener-keeps-socket-file.md) | 長いパスの listener は Close でソケットファイルを消さない | 長いパスの listener を全部 `SetUnlinkOnClose(false)` にしていた頃の決まり | [DR-0017](../DR-0017-listener-close-by-spelling.md) |
| [DR-0010](DR-0010-unsafe-base-held-by-fd.md) | /proc を使える Linux の Dial は、安全でない置き場に自分専用のディレクトリを作り fd で掴んで使う | 予備の経路の symlink を置き場ごとに試し、安全でない置き場では fd で掴む形にしていた頃の決まり | [DR-0018](../DR-0018-route-orders-per-os.md) |
| [DR-0011](DR-0011-central-route.md) | 長いパスは自分専用のディレクトリ D と乱数の symlink R を通す | 呼び出しごとに D と R を作って消していた頃の経路 | [DR-0018](../DR-0018-route-orders-per-os.md)<br>[DR-0019](../DR-0019-places-held-by-world.md) |
| [DR-0012](DR-0012-listen-at-any-cost.md) | 何かを失ってでも届く Listen は別パッケージ atanycost に置く | 最終手段の Listen を別パッケージに置いていた頃の決まり | [DR-0016](../DR-0016-public-api-go-shape.md) |
| [DR-0013](DR-0013-connect-order.md) | Dial は「connect の順」で試し、D は 1 回の呼び出しで 1 度だけ作る | connect が D と R を作る経路と proc-held を持っていた頃の順 | [DR-0018](../DR-0018-route-orders-per-os.md) |
| [DR-0014](DR-0014-ex-last-resort-and-diagnostics.md) | ListenEx / DialEx は最終手段を使うかを LastResort で受け取り、失ったもの・通った経路・飛ばした経路を返す | 最終手段と失うものの名前を返していた頃の `Ex` | [DR-0016](../DR-0016-public-api-go-shape.md) |
| [DR-0015](DR-0015-listen-through-file-symlink.md) | Listen は bind が symlink を辿る OS で standard の次に symlink-file を試す | 置き場の symlink を bind の後すぐ消していた頃の経路 | [DR-0018](../DR-0018-route-orders-per-os.md) |
