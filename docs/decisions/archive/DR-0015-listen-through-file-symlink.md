# DR-0015: Listen は bind が symlink を辿る OS で standard の次に symlink-file を試す

- Status: Superseded by DR-0018
- Date: 2026-11-19

## Context

ハブの [DR-0016](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0016-bind-through-file-symlink.md) が、bind が最後の要素の symlink を辿る OS では、まだ無い元のパスを指す symlink を短い置き場に作ってそれに bind すると決めた (symlink-file)。綴りにファイル名が出ないので、ファイル名の上限が無くなる。ハブの実測 (2026-11-19) では、辿るのは macOS と NetBSD、辿らないのは Linux・FreeBSD・OpenBSD・illumos (`EADDRINUSE` を返して何も作らない)。ハブの SPEC は、辿らないと実測した OS では symlink-file を試さず、どちらとも分からない OS では実行時に見分けると決めた。同じ DR が経路の名前を「手段 + 何についてか」にそろえた (fork-cwd・fork-cwd-sockdir・fork-cwd-direct・symlink-file・symlink-dir)。

## Decision

- Listen の順は standard → symlink-file → D を通す経路 ([DR-0011](DR-0011-central-route.md))。symlink-file を試すのは、辿ると実測した OS (macOS、NetBSD) と実測していない OS (その他)。辿らないと実測した OS (Linux / Android、FreeBSD、OpenBSD、illumos) では試さず、`skipped` にも入れない (`listenTriesSymlinkFile`)
- 置き場は Dial の symlink-file と同じ候補と条件 ([DR-0013](DR-0013-connect-order.md)): `TMPDIR` (空でない絶対パスの時)、`/tmp` の順。綴り `<置き場>/sockat.XXXXXXXXXX` が入らない・置き場が安全でない・symlink を作れない候補には何も作らず次の候補へ
- symlink の先は元のパスのソケットのファイルの綴り (相対パスなら cwd を前に付ける)。その綴りで bind し、成功でも失敗でもすぐ消す
- bind が失敗したら元のパスを `lstat` する。物があれば bind のエラー (`EADDRINUSE` 等) を返し、次の経路は試さない。無いか確かめられなければ、カーネルが辿らなかったとみなして symlink-file を使えないとし、次の経路へ進む (`skipped` に `bind did not follow the symlink` で入る)。次の候補の置き場は試さない
- losses は `respelled` だけ。listener のアドレスは symlink の綴りなので `SetUnlinkOnClose(false)` にし ([DR-0007](DR-0007-long-path-listener-keeps-socket-file.md))、`ListenEx` は bind の前に引いた元のパスの realpath と bind の直後の `(st_dev, st_ino)` を覚える ([DR-0001](DR-0001-public-api.md))
- 経路の名前は SPEC の新しい名前で記録する。Go に無い fork の経路は bind の skipped に fork-cwd / fork-cwd-sockdir、DialEx の最終手段に fork-cwd-direct の名前で入る

## 理由

- 辿らないと実測した OS で試さないのは、長いパスの Listen のたびに symlink を作って断られるだけになるため (ハブの SPEC)
- 実測していない OS で実行時に見分けるのは、辿らない時も bind 全体を落とさず次の経路へ進めるため (ハブの DR-0016)
- 「物がある」の時だけ返すのは、辿らないカーネルの `EADDRINUSE` (symlink がそこにあるから) と、元のパスに物がある本当の `EADDRINUSE` を見分ける手段が元のパスの `lstat` だけだから。本当の失敗を見落としても、次の経路で同じ失敗になって返る
- 辿らないと分かった時に次の置き場を試さないのは、置き場を変えてもカーネルの振る舞いは変わらないから
- その綴りで後で unlink しても、最後の要素の symlink は辿られず置き場の中の項目が消えるだけなので、`recreatable-spelling` は付かない (ハブの DR-0016)

## Consequences

- macOS の Listen はファイル名の上限が無くなる (`TMPDIR` か `/tmp` に安全な置き場がある時)。200 バイトのファイル名で Listen と Dial が通ることを試験で確かめた
- macOS で D を通す経路に進むのは、symlink-file の置き場が無い時 (`TMPDIR` も `/tmp` も長すぎるか安全でない) だけになる
- NetBSD の Listen は symlink-file で届く (D を通す経路の手段が無いので、置き場が無ければ `ENAMETOOLONG`)。OpenBSD・illumos の長いパスの Listen は、手段も symlink-file も無いので `ENAMETOOLONG` (最終手段の symlink-dir は使える)。FreeBSD は at だけ
- 試験は bind が辿るかを試験側で生のソケットを使って確かめ (`bindFollowsSymlink`)、実測した OS では辿るか (macOS・NetBSD は辿る、Linux・FreeBSD・OpenBSD・illumos は辿らない) と `listenTriesSymlinkFile` がそれに合うことを固定し (カーネルが変わったら CI で分かる)、他の OS では見分けた結果に合わせて期待を決める

## 判断の出所

- symlink-file の bind と経路の名前: kawaz 裁定 (ハブの DR-0016) 2026-11-19
- 置き場を Dial の symlink-file と同じにすること、bind が失敗した時 (エラーを問わない) に元のパスを lstat すること: ハブの SPEC (b413a5a、8e218c6) 2026-11-19
- 辿らないと分かった時に次の置き場を試さないこと: 実装側の判断 2026-11-19
- 辿らないと実測した OS (Linux / Android、FreeBSD、OpenBSD、illumos) で試さないこと: ハブの SPEC (bcc8d9b) 2026-11-19
