# DR-0020: bind と resolve は相対パスを絶対パスの候補にしてから扱い、connect 系は相対のまま渡す

- Status: Accepted
- Date: 2026-11-22

## Context

[ハブの DR-0019](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0019-absolutize-relative-path-on-bind.md) と [SPEC の「相対パスを絶対パスにする」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#相対パスを絶対パスにする)・[SPEC-routes の「絶対パスの候補の作り方」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md#絶対パスの候補の作り方) が、bind 系と resolve で相対パスを絶対パスの候補 (実パスの候補、`$PWD` の候補) にしてから標準に渡すこと、収まらなければ経路に進むこと、どの経路も使えない時だけ相対のまま標準に渡すこと (`CwdRelative`) を決める。標準に渡す長さを超える絶対パスも、経路の前に実パスの候補を見る。connect 系は変えない。Go で決めることは、候補の作り方、PATH_MAX を超える cwd の見分け方、`ListenEx` の記録と cwd の見張りの持ち方、`Options.Base` の確かめとの順、`ForConnect` の綴りの性質の持ち方。

## Decision

対象と振る舞い:

| 呼び出し | 相対パス | 標準に渡す長さを超える絶対パス |
|---|---|---|
| `Listen`・`ListenEx`・`ListenUnixgram`・`ListenUnixgramEx` | 候補が収まれば標準 (`Original`)、無ければ経路、経路が全部使えなければ相対のまま標準 (`CwdRelative`) か `ENAMETOOLONG` | 実パスの候補が収まれば標準 (`Original`)、無ければ経路 |
| `ResolveForBind`・`ResolveForConnect` | 同じ (綴りを返す) | 同じ |
| `Dial`・`DialEx`・`DialUnixgram`・`DialUnixgramEx` | 相対のまま (短ければ標準、長ければ経路) | 経路 |

候補の作り方 (`absolute.go`):

- 実パスの候補: ソケットのディレクトリを `realDir` (getcwd の値に `/` と相対パスのディレクトリをそのままつなぎ、`filepath.EvalSymlinks` で要素ごとに辿る。字句で畳まない) で実パスにし、`/` とファイル名をつなぐ。長い絶対パスは、経路のために引いたソケットのディレクトリの実パス (`bindCall.real`) を使う
- `$PWD` の候補: `os.Getenv("PWD")` が絶対パスで、`stat` の `(st_dev, st_ino)` が `.` と同じ時だけ使う (connect の relative の `cwdSpellings` と同じ確かめ)。`$PWD` と相対パスのディレクトリを `/` でつないで `filepath.Clean` で畳み、畳んだディレクトリの `(st_dev, st_ino)` が相対パスのディレクトリ (cwd から引いた物) と同じ時だけ候補にする
- 実パスの候補を先に見て、収まればそれを使う。候補を作れない (realpath・`stat` の失敗、`$PWD` が確かめに外れる) のはエラーにせず、その候補が無いだけ
- 名前で終わらない短い相対パス (`dir/`・`..` 等) は候補を作らず、相対のまま標準に渡す (標準のエラーをそのまま返す)。resolve はそのまま返して `CwdRelative`

PATH_MAX を超える cwd:

- 経路に進んだ相対パスで、ソケットのディレクトリの `realDir` が `ENAMETOOLONG`・`ERANGE`・`EINVAL` (FreeBSD では `ENOMEM` も) で失敗したら (`pastPathMax`)、エラーにせず、経路が使えない物として扱う (短ければ相対のまま標準、長ければ `ENAMETOOLONG` で `Route` は無し。どちらも `Skipped` は空)。SPEC は realpath(3) の `ENAMETOOLONG` と `ERANGE` を挙げるが、Go の `realDir` は realpath(3) でなく getcwd と `filepath.EvalSymlinks` で、`syscall.Getwd` は PATH_MAX を超える cwd で Linux は `ENAMETOOLONG`、macOS は `EINVAL` を返す (Go 1.27 で実測 2026-11-22。Linux は約 4200 バイト、macOS は 1027 バイトから)。`ERANGE` は POSIX の getcwd(3) の「バッファが足りない」で、他の OS (未実測) のために入れる。`syscall.Getwd` のバッファは空でないので、`EINVAL` は他の理由では出ない。FreeBSD の `syscall.Getwd` は `ENOMEM` を返す (CI で実測 2026-11-22、約 1000 バイトの cwd から)。FreeBSD の `__getcwd(2)` は渡されたバッファの中でパスを組み、収まらないと `ENOMEM` を返すため。`ENOMEM` は FreeBSD でだけパスの長さのエラーとみなす (他の OS では長さと関係ない)。NetBSD・OpenBSD・illumos は CI でこの試験が通っている
- SPEC は realpath が成功して PATH_MAX を超える実パスを返す時 (Linux の glibc) も、その stat の `ENAMETOOLONG` で 5 に進むとする。Go の `realDir` はそういうパスを返さない: `syscall.Getwd` は PATH_MAX (NUL を含む) のバッファで、超えればエラー。`filepath.EvalSymlinks` は普通の要素を足すたびにそれまでの全体を `lstat` し (超えれば `ENAMETOOLONG`)、`..` は末尾を削るだけなので、返すのはいつも `lstat` が通った綴りかその前の部分。Linux で cwd を 3803〜4309 バイトに刻み、ディレクトリ部分を 1・99・299 バイトと `<299 バイト>/../../..` にして実測した (2026-11-22): 成功は最長 4087 バイトで全部 `stat` でき、全体が 4096 を超える物は全部 `ENAMETOOLONG` (getcwd か lstat)。そのため、返した実パスを stat し直す手順は置かない
- その時、既定の `Listen`・`ListenUnixgram` は相対のまま bind できるが、`ListenEx` 系はソケットのファイルの実パスを引けず覚えられないので、覚えられない時と同じにエラーを返す (listener を閉じ、ソケットのファイルは残す)
- それ以外の失敗 (`ENOENT`・`ENOTDIR`・`EACCES` 等) はそのエラーを返す

順と診断:

- 長い絶対パスは、ソケットのディレクトリを引いた後 (`ENOENT`・`ENOTDIR` が先に決まる)、`Options.Base` の確かめの前に実パスの候補を見る。候補は B を使わないので、短い絶対パスと同じく `Options.Base` の中身を見ない (形の検査は呼び出しの初めに行う)。候補が収まらず経路に進んだ時は `Options.Base` を確かめ、外れたらそのエラーを返す (相対パスでも相対のまま渡す場面に落とさない)
- 候補を標準に渡した時の `Skipped` は空。経路に進んだ時は `standard` を先頭に入れる (理由は絶対パスなら `path too long`、相対パスなら `no absolute path fits`)
- 経路が全部使えず相対のまま標準に渡した時は、`Route` が `standard`、`Skipped` は経路だけ (`standard` を除く)。PATH_MAX を超えて経路を試さなかった時は `Skipped` は空
- 候補や経路で実際に bind して失敗したら、そのエラーを返し、もう 1 つの候補・経路・相対パスで試し直さない

記録と片付け:

- `ListenEx` 系は、standard と link-file のソケットのファイルを bind の後に引く。引く場所は `bound.lookup` に持つ: standard は標準に渡した綴り (元のパスか候補)、link-file は E の指す先 (相対パスなら getcwd を前に付けた物)。link-dir・thread-cwd・at は経路が作った実ディレクトリ (`bound.file`)
- cwd を呼び出しの初めと記録の後で比べるのは、綴りが `CwdRelative` の時だけ。絶対の綴りで bind した時は比べない (記録が cwd によらない)
- `ForBind` の `SocketFile` の cwd の比較も `CwdRelative` の時だけ。比べる cwd は resolve の初めに読む
- 既定の `Listen` で候補を標準に渡した時は `SetUnlinkOnClose` を触らない (綴りが絶対パスで、Close の unlink が bind したファイルに当たる)。illumos の `./@...` (`stdSpelling`) は相対のまま標準に渡す時 (connect 系と、bind の名前で終わらない短いパス・経路が全部使えない時) だけ付け、その listener は Close で消さない ([DR-0017](DR-0017-listener-close-by-spelling.md))

`ForConnect`:

- `Spelling` を足す (`Original`・`CwdRelative`・`Linked`)。`Linked` は link-file の綴りの性質で、`Removable` は false、procB なら `Until` が `UntilProcess`。`UsableCrossUID` 等は綴りの実際の性質 (`ResolveForConnect` は `Options.UsableCrossUID` を見ない)

## 理由

- 相対の綴りを残すと、Go の `UnixListener.Close` が cwd の変わった後に別のファイルを unlink し、DGRAM の返信が届かない (ハブの DR-0019)。既定の `Listen` の利用者は `Spelling` を見ないので、絶対にすれば何もしなくても事故が起きない
- `realDir` を使うのは、`filepath.Abs` が `$PWD` から始めて `Clean` で `sym/..` を畳み、カーネルと別のディレクトリを引くから ([DR-0018](DR-0018-route-orders-per-os.md))。実パスの候補は OS に引かせた結果なので確かめが要らない
- `$PWD` の候補は字句で畳むのが目的 (symlink を通った短い綴りで、`../` を残さない) なので `filepath.Clean` で畳み、`(st_dev, st_ino)` の確かめで場所が変わる畳み方 (`a/sym/..`) を除く。畳んだ結果が同じディレクトリでも、`.` と違う `$PWD` は使わない (SPEC の手順。`$PWD` は受け継いだ値で、誰でも書ける)
- PATH_MAX を超える cwd では、どの経路もソケットのディレクトリの実パスが要るので使えない。エラーにすると、標準なら届く短い相対パスが sockat で届かなくなる
- 記録を綴りから引くのは、相対パスを引き直すと、bind の後に cwd が変わった時に新しい cwd の同じ名前の別のファイルを記録してしまうから

## 却下した案

| 案 | 却下の理由 |
|---|---|
| `filepath.Abs` で候補を作る | `$PWD` から始めて確かめずに畳むので、`a/sym/..` で別のディレクトリになり、`.` と違う `$PWD` も使ってしまう |
| `$PWD` の候補を確かめずに使う | `a/sym/..` が別のディレクトリになる (ハブの DR-0019 の案 E) |
| 長い絶対パスの実パスの候補を `Options.Base` の確かめの後に見る | 候補は B を使わないのに、`Options.Base` の確かめに外れると標準で届く所がエラーになる。短い絶対パスでも `Options.Base` の中身は見ない |
| `Options.Base` の確かめに外れた相対パスを相対のまま標準に渡す | `Options.Base` のエラーは経路が使えないことではない。利用者が指定した置き場の誤りを黙って隠す |
| `getcwd` の失敗を全部「経路が使えない」にする | cwd が消された時の `ENOENT` 等まで黙って相対のまま渡すことになる (SPEC はそのエラーを返す) |
| `ListenEx` で相対パスなら綴りによらず cwd を比べる | 絶対の綴りで bind した後に cwd が変わると、正しく記録できるのにエラーになる |

## 判断の出所

- 相対パスを絶対にする操作、候補の 2 つと順、5 の場面、`ForConnect` の `Spelling`、長い絶対パスの実パスの候補: kawaz の裁定 (ハブの DR-0019、2026-11-22)
- 名前で終わらない短い相対パスを相対のまま渡すこと (resolve はそのまま返して `CwdRelative`)、パスの長さのせいで realpath を引けない時 (`ENAMETOOLONG`・`ERANGE`) に 5 に進み `Skipped` を空にすること、5 の診断、その時 `bind_ex` がエラーになること、候補を標準に渡す時は `Options.base` を確かめないこと: ハブの SPEC「相対パスを絶対パスにする」(ハブ 3813928・42fa79c・153b551、2026-11-22)
- `pastPathMax` に `EINVAL` を入れること、`bound.lookup`: 実装側の判断 2026-11-22
