# DR-0018: 長いパスの経路は OS ごとの 1 つの列で試し、列に並ぶ経路だけを Skipped に入れる

- Status: Accepted
- Date: 2026-11-21

## Context

[ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) の「bind の順」「connect の順」と [SPEC-routes](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md) が経路と試す順を決める。経路の名前は SPEC の「経路の名前」(standard、link-dir、link-file、thread-cwd、at、proc-direct、relative、widened、at-direct、thread-cwd-direct)。Go で決めることは、OS を build tag でどう分けるか、bind が最後の要素の symlink を辿るかをどう持つか、Linux の connect の行をどう選ぶか、SPEC で未確定の経路を作るか。

## Decision

bind (`Listen`・`ListenEx`・`ListenUnixgram`・`ListenUnixgramEx`) は standard の後に次の列:

| OS (build tag) | 列 |
|---|---|
| Linux / Android (`linux`・`android`) | link-dir (procB → 実パス) → thread-cwd |
| Apple の OS (`darwin`・`ios`) | link-dir → link-file → thread-cwd |
| FreeBSD | link-dir → at |
| NetBSD | link-dir → link-file |
| OpenBSD / illumos / Solaris | link-dir |
| その他の Unix | link-dir → link-file (実行時に見分ける) |

connect (`Dial`・`DialEx`・`DialUnixgram`・`DialUnixgramEx`) は standard の後に次の列:

| OS | 列 |
|---|---|
| Linux / Android (`/proc` を使える) | proc-direct → thread-cwd-direct → link-file |
| Linux / Android (`/proc` を使えない) | relative → thread-cwd-direct → link-file |
| Apple の OS | relative → widened → thread-cwd-direct → link-file |
| FreeBSD | at-direct → link-file |
| NetBSD / OpenBSD / illumos / その他の Unix | relative → link-file |

resolve: `ResolveForBind` は link-dir と、bind が symlink を辿ると実測した OS (`followsYes`: Apple の OS・NetBSD) だけ link-file。`ResolveForConnect` は link-file だけ (procB → 実パス)。

- bind が最後の要素の symlink を辿るかは OS ごとの定数 `bindFollowsHere` で持つ: 辿らない (`followsNo`) は Linux / Android・FreeBSD・OpenBSD・illumos・Solaris、辿る (`followsYes`) は Apple の OS・NetBSD、他は分からない (`followsUnknown`)。`followsNo` の OS の列には link-file を入れない。`followsUnknown` の OS では link-file の bind が失敗した時に元のパスを `lstat` し、物があればそのエラーを返し、無ければ使えないとして次へ進む
- `Skipped` に入るのは、その呼び出しの列に並ぶ経路だけ (列に無い経路を「その OS に無い」として入れない)。Linux の connect の行は、呼び出しの初めに 1 度だけ `/proc` を使えるか (`procUsable`) で選び、選ばなかった行の経路は試しも入れもしない
- widened は Apple の OS の列に置き、いつも使えない経路として `Skipped` に `no widened route in sockat-go` で入れる ([DR-0005](DR-0005-no-widened-sockaddr.md))
- FreeBSD の connect の link-file (SPEC で未確定) は作る。connect は最後の要素の symlink を辿る (SPEC-routes「link-file」)。publish の時の CI の FreeBSD の VM で確かめ、結果をハブに知らせる
- 経路が実際に bind / connect して失敗したら、そのエラーを返して次の経路を試さない。その時、その呼び出しで作った E は返す前に消す。次へ進むのは経路自体が使えない時 (綴りが収まらない、B・D・E を作れない、手段が無い、`/proc` を使えない、link-file の bind で元のパスに物が無い、relative で cwd が変わっている) だけ
- 長いパスは、経路を試す前にソケットのディレクトリを `stat` で引き、realpath を取る (無ければ `ENOENT`、ディレクトリでなければ `ENOTDIR`、realpath を引けなければそのエラー)。続いて `Options.Base` を確かめる (何も作らない)。どちらも `Skipped` に何も入れない
- ソケットのディレクトリの fd が要る経路 (proc-direct、at-direct、thread-cwd-direct) のために、Linux / Android は `O_PATH | O_DIRECTORY`、Apple の OS は `O_SEARCH` (カーネルが `EISDIR` / `EINVAL` を返したら `O_RDONLY`)、他は `O_RDONLY | O_DIRECTORY` で開く。開けなければその経路は使えない
- `/proc` を使えるか (`procUsable`) は、ソケットのディレクトリの fd (開けなければ `/` を開いた fd) について、`<proc>/self/fd/<fd>` の `stat` が fd の `fstat` と同じ `(st_dev, st_ino)` かで決める
- thread-cwd・thread-cwd-direct の手段は使い捨ての goroutine ([DR-0004](DR-0004-thread-cwd-route.md))、at・at-direct は `bindat` / `connectat` を自前のソケットで呼ぶ ([DR-0006](DR-0006-own-socket-into-net.md))
- thread-cwd・at の D は、`UsableCrossUID` の時も、それを見ない普段の B (置き場 1〜4 の順、`Options.Base` があればその B) に作る

## 理由

- 列を OS (と Linux の `/proc`) だけで決めると、同じ呼び出しは同じ列を通り、`Skipped` を言語どうしで比べられる (ハブの適合試験)
- bind が symlink を辿るかは実測した OS では分かっているので、辿らない OS で link-file を試して E を作り消す手間を省く。分からない OS は実行時に見分けるしかない
- `/proc` が見えても PID の名前空間と `/proc` の mount が食い違う環境では、`/proc/self/fd/<fd>` が別の物を指しうる。`(st_dev, st_ino)` を比べれば無関係なディレクトリに connect しない
- `Dangling` の綴りは誰にも辿れないので、D を置く B が他人に辿れるかは関係ない

## 却下した案

| 案 | 却下の理由 |
|---|---|
| `/proc` を使えない Linux の connect で proc-direct を `Skipped` に入れてから relative へ進む | SPEC の connect の表は Linux を `/proc` を使えるかで 2 行に分けており、使えない行に proc-direct は無い (ハブの答え) |
| その OS に手段の無い経路 (NetBSD 等の thread-cwd、FreeBSD 以外の at) も `Skipped` に入れる | 列に並ばない経路まで入れると、OS ごとに `Skipped` が膨らみ、言語どうしの比べ方が割れる |
| `/proc` を使えるかを、`/proc/self/fd/<fd>` を引けるかだけで決める | PID の名前空間と `/proc` の mount の食い違いで、引けても別の物を指しうる |
| FreeBSD の connect の link-file を作らない | connect は最後の symlink をいつも辿るので、作らない理由が無い。at-direct (103 まで) を超えるファイル名に届く |

## 判断の出所

- 列と経路: ハブの SPEC「bind の順」「connect の順」と DR-0017
- `Skipped` に列の経路だけを入れること、`/proc` を `(st_dev, st_ino)` で確かめること、`UsableCrossUID` の時の thread-cwd・at の D の置き場、FreeBSD の connect の link-file を作って CI で確かめること: ハブの答え (2026-11-21、3 言語共通として SPEC に入った)
- bind が symlink を辿るかの実測: ハブの findings (2026-11-19)
