# DR-0013: Dial は「connect の順」で試し、D は 1 回の呼び出しで 1 度だけ作る

- Status: Accepted
- Date: 2026-11-19

## Context

ハブの [DR-0012](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0012-own-directory-and-thread-cwd.md) と [SPEC の「connect の順」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#connect-の順)が、長いパスの connect を OS ごとの 1 つの列で試すと決めた。書き込み (D・R・symlink を作る) もスレッドも要らない経路を先に、D を作る経路とスレッドを立てる経路をその後にする。connect した側のソケットは bind されないので、connect の綴りは相手に残らず、R を挟まない経路も使える。D ([DR-0011](DR-0011-central-route.md)) は 1 回の呼び出しで 1 度だけ作り、D を使う経路で共有する。

Go に fork は無い ([DR-0004](DR-0004-thread-cwd-route.md)) ので、SPEC の fork-cwd-direct (最終手段を使う `connect_ex` の最後) は作れない ([DR-0014](DR-0014-ex-last-resort-and-diagnostics.md))。macOS の widened は作らない ([DR-0005](DR-0005-no-widened-sockaddr.md))。

## Decision

標準に渡す長さを超えるパスの Dial は、ソケットのディレクトリを引いた後 ([DR-0011](DR-0011-central-route.md))、OS ごとに次の順に試す。全部が使えなければ `ENAMETOOLONG`。

| OS | 試す順番 |
|---|---|
| Linux / Android (`/proc` を使える) | proc-direct → thread-cwd → proc → proc-held → symlink-file |
| Linux / Android (`/proc` を使えない) | relative → thread-cwd → symlink-file |
| macOS | relative → widened (作らない) → thread-cwd → symlink-file |
| FreeBSD | at-direct → at |
| NetBSD / OpenBSD / illumos / その他の Unix | relative → symlink-file |

- `/proc` を使えるかは、ソケットのディレクトリの fd (開けなかった時は `/` の fd) で `/proc/self/fd/<fd>` を引けるかで決める

各経路と、使えないとみなして次へ進む時:

| 経路 | 中身 | 使えない時 |
|---|---|---|
| proc-direct | `/proc/self/fd/<ソケットのディレクトリの fd>/<ファイル名>` | ソケットのディレクトリを開けない<br>`/proc/self/fd/<fd>` を引けない<br>綴りが sockaddr に入らない |
| relative | cwd からの相対パス (文字列を書き直すだけで `chdir` しない) | 元のパスが絶対パスでない<br>相対パスが sockaddr に入らない<br>作った綴りのディレクトリが元と `(st_dev, st_ino)` で一致しない<br>cwd が既に変わっている |
| widened | 作らない | いつも (`no widened route in sockat-go`) |
| thread-cwd | 使い捨てのスレッドの cwd を D に移し、`R/<ファイル名>` ([DR-0004](DR-0004-thread-cwd-route.md)) | 綴りが sockaddr に入らない<br>スレッド cwd が使えない (seccomp の filter、`unshare` / `__pthread_fchdir` が断られた)<br>D をどこにも作れない |
| proc | `/proc/self/fd/<Dfd>/R/<ファイル名>` | 綴りが sockaddr に入らない (D を作る前は 1 桁の fd で見積もり、開いた後に確かめ直す)<br>D をどこにも作れない |
| proc-held | D の中にソケットのファイルを指す `s` を `symlinkat` で作り、`/proc/self/fd/<Dfd>/s` | D をどこにも作れない<br>`s` を作れない |
| at-direct | `connectat(<ソケットのディレクトリの fd>, <ファイル名>)` | ソケットのディレクトリを開けない<br>ファイル名が sockaddr に入らない |
| at | `connectat(Dfd, R/<ファイル名>)` | 綴りが sockaddr に入らない<br>D をどこにも作れない |
| symlink-file | 安全な置き場 (`TMPDIR` が空でない絶対パスの時はそれ、次に `/tmp`) にソケットのファイルを指す `sockat.XXXXXXXXXX` を作って connect し、すぐ消す | どの候補も、綴りが sockaddr に入らない・置き場が安全でない (sticky で持ち主が root か自分、または自分だけが書ける、のどちらでもない)・symlink を作れない |

- D は 1 回の Dial で 1 度だけ、D を使う経路 (thread-cwd・proc・proc-held・at) のうち最初にそこまで来たものが作り、後の経路は同じ D を使う。片付けは Dial の終わりに 1 度だけ。どこにも作れなかったら、後の D を使う経路も「D をどこにも作れない」として飛ばし、作り直さない
- proc の綴りが D を開いた後に入らないと分かった時は、D を片付けずに次の経路へ進む (proc-held が同じ D を使う)
- proc-held は D と R の手順 1・2・5 だけを使い、R を作らない。先に thread-cwd か proc が D を作っていれば、その D に `s` を置く。`s` は作れた時だけ消す。`s` の先と symlink-file の symlink の先はソケットのファイルの綴りそのもの (相対パスなら cwd を前に付ける)
- 経路が実際に connect して失敗したら (`ENOENT`、`ECONNREFUSED`、backlog が満杯等) そのエラーを返し、次の経路も次の置き場も試さない ([DR-0011](DR-0011-central-route.md) と同じ)。relative の途中で cwd が変わったら一般のエラー
- 試して使えなかった経路は、試した順に理由付きで記録する (`DialEx` の `Skipped`、適合試験の CLI の `skipped`)。最初は長いパスの standard。その OS の列に無い経路は入れず、symlink-file の置き場の候補が複数でも 1 件にする。最終手段を使う `DialEx` は、列が全部使えない時に最後に fork-cwd-direct を `no fork route in sockat-go` で入れる

## 理由

- 書き込みもスレッドも要らない経路は速く、relative は D を通す経路より遠くまで届く場面がある (ハブの DR-0012)
- proc-held を D で作るのは、D を fd で掴むので置き場の安全も長さも問わず、置き場の候補にソケットのディレクトリまで入るから。seccomp の filter が掛かり (thread-cwd が使えない)、`TMPDIR` も `/tmp` も安全でなく、ファイル名が proc-direct に収まらない環境 (hyoui の実測) でも届く
- symlink-file は名前で辿るので、他人が差し替えられない安全な置き場だけを使う
- widened を作らない経路として記録して次へ進むのは、SPEC の「その言語で作れない経路は飛ばし (使えない経路として次へ進む)」に従い、Listen が fork-cwd / fork-cwd-sockdir を `no fork route in sockat-go` で記録するのとそろえるため

## Consequences

- Linux (`/proc` あり) の Dial は、D をどこかに作れればファイル名によらず届く (proc-held)
- proc の綴りは proc-direct よりおよそ 11 バイト長い (fd の桁で変わる) ので、proc-direct が収まらないファイル名では proc もまず収まらない。Dial が proc を通るのは、実際にはソケットのディレクトリを開けない時 (Linux は `O_PATH` で開くので、ほぼ無い) くらい
- macOS で失うのは widened だけが届く connect ([DR-0005](DR-0005-no-widened-sockaddr.md))
- 試験は、connect の順を途中の経路から始めて (`dialFrom`) 後の経路を強制し、順そのものは試した経路の記録で確かめる ([DR-0009](DR-0009-routes-not-exposed.md))

## 判断の出所

- connect の順: kawaz 裁定 (ハブの DR-0012、SPEC 12dc157) 2026-11-19
- D を 1 度だけ作って共有し、作れなければ作り直さないこと、proc-held を D の手順 1・2・5 で作ること: ハブの SPEC (12dc157) 2026-11-19
- proc-held (fd で掴む形) を使うこと: kawaz 裁定 (ハブの DR-0008) 2026-11-18
- widened を作らない経路として記録すること: 実装側の判断 (fork の経路の記録にそろえる) 2026-11-19
