# DR-0011: 長いパスは自分専用のディレクトリ D と乱数の symlink R を通す

- Status: Accepted
- Date: 2026-11-19

## Context

ハブの [DR-0012](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0012-own-directory-and-thread-cwd.md) が経路を作り直した。カーネルは bind の綴りを覚えて `getsockname` / `getpeername` で返し、上のライブラリや利用者はその綴りで unlink / stat する。`/proc/self/fd/<n>/<名前>`・ファイル名だけ・相対パスの綴りは、後で解決すると無関係なファイルに当たる。Go に fork は無い ([DR-0004](DR-0004-thread-cwd-route.md))。

## Decision

Listen の試す順番:

| 順 | Listen |
|---|---|
| 1 | 標準に渡す長さ以下なら標準にそのまま (standard) |
| 2 | symlink-file (bind が symlink を辿らないと実測した Linux / Android・FreeBSD・OpenBSD・illumos 以外、[DR-0015](DR-0015-listen-through-file-symlink.md)) |
| 3 | D を通す経路 (Linux / macOS は thread-cwd、FreeBSD は at (bindat)) |
| 4 | proc (`/proc` を使える Linux) |
| 5 | `ENAMETOOLONG` (`ListenEx` が最終手段を使う時は symlink-dir、[DR-0014](DR-0014-ex-last-resort-and-diagnostics.md)) |

- SPEC の 5 番目の fork-cwd と D 無しの fork-cwd-sockdir は Go に無いので、`skipped` に `no fork route in sockat-go` で入れて次へ進む

Dial は標準に渡す長さを超えると、OS ごとの「connect の順」で試す ([DR-0013](DR-0013-connect-order.md))。その中の thread-cwd・proc・at (connectat) と proc-held が D を使う。

D を通す経路 (SPEC のまま):

- 最初にソケットのディレクトリを `stat` で引き、realpath を取る。無ければ `ENOENT`、ディレクトリでなければ `ENOTDIR`、realpath を引けなければそのエラー (長さの判定や D を作るより前。`ENAMETOOLONG` に化けさせない)。realpath は絶対パスにしてから成分ごとに引く (macOS の getcwd は消された cwd の綴りをそのまま返すので、"." だけを引くと実在しないパスを実パスとみなす)。fd が要る経路 (置き場としてのソケットのディレクトリ、Dial の proc-direct と at-direct) のためにそれを開き、開けなければ (O_RDONLY で開く OS で読み取り権限が無い等) その経路は使えないとして次へ
- 長さ (`R/<ファイル名>` = 11 + ファイル名が sockaddr に入るか) と手段を使えるか (seccomp の filter、`/proc`) を先に確かめ、使えなければ何も作らない
- 置き場 `${TMPDIR:-/tmp}` (相対パスも可) → `/tmp` → ソケットのディレクトリ (名前は `.sockat.`) の順に、`mkdirat` で 0700 の D を作る
- D を開いて `fstat` で「ディレクトリ・持ち主が実効 uid・group と other に権限が無い」を確かめ、0700 にそろえる
  | OS | D の開き方 | 0700 にそろえる |
  |---|---|---|
  | Linux (`/proc` を使える) | `O_PATH \| O_DIRECTORY \| O_NOFOLLOW` | `chmod("/proc/self/fd/<fd>")` |
  | Linux (`/proc` を使えない) | `O_PATH \| O_DIRECTORY \| O_NOFOLLOW` | 名前で `O_RDONLY \| O_DIRECTORY \| O_NOFOLLOW` で開き直し、`(st_dev, st_ino)` が最初の fd と同じことを確かめて `fchmod` |
  | FreeBSD 等 | `O_RDONLY \| O_DIRECTORY \| O_NOFOLLOW` | `fchmod` |
  | macOS | `O_SEARCH \| O_DIRECTORY \| O_NOFOLLOW` (カーネルが `EISDIR` / `EINVAL` を返したら `O_RDONLY`) | `fchmod` |
- D の中に、ソケットのディレクトリの realpath を指す乱数 10 文字の R を `symlinkat` で作る
- D の fd を基準に `R/<ファイル名>` で bind / connect する
- 作った直後の D を開けない時 (macOS の `O_SEARCH` で umask が持ち主の検索権限を削った等) は、名前を `fstatat(AT_SYMLINK_NOFOLLOW)` で引いて確認と同じ条件なら `unlinkat(AT_REMOVEDIR)` で消し (空のディレクトリしか消えない)、次の置き場へ (kawaz 裁定 D12-Q5)
- 確認が通った後に 0700 にそろえられなければ (umask 0477 の非 root で開き直しが `EACCES` 等)、その置き場では作れないとみなし、D を片付けて次へ。
- 片付け: R は作れた時だけ消す。D は確認が通り、名前の `(st_dev, st_ino)` が fd と一致する時だけ消す。確認に落ちた D は中身も含めて触らない
- D は 1 回の呼び出しで 1 度だけ作り、D を使う手段 (Listen の thread-cwd → proc、Dial の connect の順の中の D を使う経路) で共有する。どこにも作れなかったら作り直さない。proc の綴りが D を開いた後に入らない時も、D を片付けずに後の経路へ進む。Listen では D を使う後の経路 (fork-cwd) が Go に無いので、D は D を通す経路を試し終えた所で片付ける

- D をどこにも作れない時、Listen は D 無しの形で試す: ソケットのディレクトリに R (`.sockat.` + 乱数 10 文字、ソケットのディレクトリの realpath を指す) を作り、ソケットのディレクトリの fd を基準に `.sockat.XXXXXXXXXX/<ファイル名>` で、D を通す経路と同じ手段の順 (thread-cwd-sockdir (FreeBSD は at-sockdir) → proc-sockdir) に bind する。ソケットのディレクトリを開けない、綴りが収まらない、R を作れない時は `ENAMETOOLONG`。Dial は D 無しの形を使わず、connect の順の次の経路へ進む ([DR-0013](DR-0013-connect-order.md))
  - 例: `/proc` の無い Linux で umask 0477 の非 root は D を 0700 にそろえられず (どの置き場でも)、D 無しの形で bind する (試験で確かめた)

## 理由

- 片付けた後は R がどこにも無いので、返る綴り `R/<ファイル名>` を後でどこで解決しても何にも当たらない
- D を fd で掴むので、置き場の安全も長さも問わない
- macOS で D を `O_SEARCH` で開くのは、検索権限だけで開け (umask 0477 で mkdir が 0300 になっても開ける)、`fchmod` と `__pthread_fchdir` が効くから。ソケットのディレクトリを開くのと同じ扱い

## Consequences

- ファイル名の上限は、D を通す経路で Linux 96 (/proc 版はおよそ 79)、macOS / FreeBSD 92。macOS の Listen は symlink-file でファイル名の上限が無い ([DR-0015](DR-0015-listen-through-file-symlink.md))
- Go には fork が無いので、SPEC の bind の fork-cwd と D 無しの fork-cwd-sockdir を作らない。次の環境の Listen は長いパスで `ENAMETOOLONG`: NetBSD / OpenBSD / illumos で bind が symlink を辿らない時 (D を通す経路の手段が無い)、Linux で seccomp の filter があり `/proc` も無い、macOS で `__pthread_fchdir` が使えず symlink-file の置き場も無い。Linux で filter があり `/proc` がある時は、ファイル名およそ 79 バイトまで
- D を通す経路で届く長いパスの Listen / Dial は、そのたびに D と R を作って消す

## 判断の出所

- 経路と順番: kawaz 裁定 (ハブの DR-0012) 2026-11-19
- fork の無い Go で bind の fork-cwd を作らないこと: kawaz 裁定 (D12-Q3 = a) 2026-11-19
- D をどこにも作れない時の bind を D 無しの形で試すこと: kawaz 裁定 (D12-Q1 = a) 2026-11-19
- macOS の D を `O_SEARCH` で開くこと: 実装側の判断、ハブの SPEC に取り入れ済み (f74cb86b) 2026-11-19
- Linux は `/proc` の有無によらず D を `O_PATH` で開いて確かめること: 実装側の提案、ハブの SPEC に取り入れ済み (b442e602) 2026-11-19
- ソケットのディレクトリは stat で引き、開くのは fd の要る経路だけ: ハブの SPEC (c5f63b69) 2026-11-19
