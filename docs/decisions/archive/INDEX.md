# 退避した Decision Records

| DR | タイトル | 一言 | 置き換え先 |
|---|---|---|---|
| [DR-0003](DR-0003-route-order.md) | 長いパスの経路と試す順番 | bind も相対パス・symlink・/proc を使っていた頃の順番 | [DR-0011](../DR-0011-central-route.md) |
| [DR-0010](DR-0010-unsafe-base-held-by-fd.md) | /proc を使える Linux の Dial は、安全でない置き場に自分専用のディレクトリを作り fd で掴んで使う | 予備の経路の symlink を置き場ごとに試し、安全でない置き場では fd で掴む形にしていた頃の決まり | [DR-0013](../DR-0013-connect-order.md) |
| [DR-0012](DR-0012-listen-at-any-cost.md) | 何かを失ってでも届く Listen は別パッケージ atanycost に置く | 最終手段の Listen を別パッケージ `atanycost` に置いていた頃の決まり | [DR-0014](../DR-0014-ex-last-resort-and-diagnostics.md) |
