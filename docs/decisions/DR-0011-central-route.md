# DR-0011: 長いパスは自分専用のディレクトリ D と乱数の symlink R を通す (中心の経路)

- Status: Accepted
- Date: 2026-11-19

## Context

ハブの [DR-0012](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0012-own-directory-and-thread-cwd.md) が経路を作り直した。カーネルは bind の綴りを覚えて `getsockname` / `getpeername` で返し、上のライブラリや利用者はその綴りで unlink / stat する。`/proc/self/fd/<n>/<名前>`・ファイル名だけ・相対パスの綴りは、後で解決すると無関係なファイルに当たる。Go に fork は無い ([DR-0004](DR-0004-thread-cwd-route.md))。

## Decision

試す順番 (Listen と Dial):

| 順 | Listen | Dial |
|---|---|---|
| 1 | 標準に渡す長さ以下なら標準にそのまま | 同じ |
| 2 | 中心の経路 (Linux / macOS はスレッド cwd、FreeBSD は bindat) | 中心の経路 (Linux / macOS はスレッド cwd、FreeBSD は connectat) |
| 3 | 中心の経路の /proc 版 (`/proc` を使える Linux) | 同左 |
| 4 | `ENAMETOOLONG` | 予備の経路 |
| 5 | | `ENAMETOOLONG` |

Dial の予備の経路:

| OS | 試す順番 |
|---|---|
| Linux (`/proc` を使える) | /proc → (置き場ごとに) 安全なら symlink、安全でなければ fd で掴む形 ([DR-0010](DR-0010-unsafe-base-held-by-fd.md)) |
| Linux (`/proc` を使えない) | 相対パス → symlink |
| macOS | 相対パス → symlink (広げた sockaddr は使わない、[DR-0005](DR-0005-no-widened-sockaddr.md)) |
| FreeBSD | connectat (ソケットのディレクトリの fd とファイル名) |
| NetBSD / OpenBSD / illumos | 相対パス → symlink |

中心の経路 (SPEC のまま):

- 最初にソケットのディレクトリを `stat` で引き、realpath を取る。無ければ `ENOENT`、ディレクトリでなければ `ENOTDIR`、realpath を引けなければそのエラー (長さの判定や D を作るより前。`ENAMETOOLONG` に化けさせない)。realpath は絶対パスにしてから成分ごとに引く (macOS の getcwd は消された cwd の綴りをそのまま返すので、"." だけを引くと実在しないパスを実パスとみなす)。fd が要る経路 (置き場としてのソケットのディレクトリ、Dial の予備の /proc と connectat) のためにそれを開き、開けなければ (O_RDONLY で開く OS で読み取り権限が無い等) その経路は使えないとして次へ
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
- 確認が通った後に 0700 にそろえられなければ (umask 0477 の非 root で開き直しが `EACCES` 等)、その置き場では作れないとみなし、D を片付けて次へ。O_RDONLY で開く OS で開けない時は、確かめられないので触らずに残す
- 片付け: R は作れた時だけ消す。D は確認が通り、名前の `(st_dev, st_ino)` が fd と一致する時だけ消す。確認に落ちた D は中身も含めて触らない

- D をどこにも作れない時、Listen は D 無しの形で試す: ソケットのディレクトリに R (`.sockat.` + 乱数 10 文字、ソケットのディレクトリの realpath を指す) を作り、ソケットのディレクトリの fd を基準に `.sockat.XXXXXXXXXX/<ファイル名>` で、中心の経路と同じ手段の順 (スレッド cwd (FreeBSD は bindat) → /proc 版) に bind する。ソケットのディレクトリを開けない、綴りが収まらない、R を作れない時は `ENAMETOOLONG`。Dial は D 無しの形を使わず予備の経路へ
  - 例: `/proc` の無い Linux で umask 0477 の非 root は D を 0700 にそろえられず (どの置き場でも)、D 無しの形で bind する (試験で確かめた)

## 理由

- 片付けた後は R がどこにも無いので、返る綴り `R/<ファイル名>` を後でどこで解決しても何にも当たらない
- D を fd で掴むので、置き場の安全も長さも問わない
- macOS で D を `O_SEARCH` で開くのは、検索権限だけで開け (umask 0477 で mkdir が 0300 になっても開ける)、`fchmod` と `__pthread_fchdir` が効くから。ソケットのディレクトリを開くのと同じ扱い

## Consequences

- ファイル名の上限は Listen で Linux 96 (/proc 版はおよそ 79)、macOS / FreeBSD 92
- Go には fork が無いので、SPEC の bind の 4 (中心の経路を fork で) と D 無しの fork を作らない。次の環境の Listen は長いパスで `ENAMETOOLONG`: NetBSD / OpenBSD / illumos (中心の経路の手段が無い)、Linux で seccomp の filter があり `/proc` も無い、macOS で `__pthread_fchdir` が使えない。Linux で filter があり `/proc` がある時は、ファイル名およそ 79 バイトまで
- 長いパスの Listen / Dial のたびに D と R を作って消す

## 判断の出所

- 経路と順番: kawaz 裁定 (ハブの DR-0012) 2026-11-19
- fork の無い Go で bind の 4 を作らないこと: kawaz 裁定 (D12-Q3 = a) 2026-11-19
- D をどこにも作れない時の bind を D 無しの形で試すこと: kawaz 裁定 (D12-Q1 = a) 2026-11-19
- macOS の D を `O_SEARCH` で開くこと: 実装側の判断、ハブの SPEC に取り入れ済み (f74cb86b) 2026-11-19
- Linux は `/proc` の有無によらず D を `O_PATH` で開いて確かめること: 実装側の提案、ハブの SPEC に取り入れ済み (b442e602) 2026-11-19
- ソケットのディレクトリは stat で引き、開くのは fd の要る経路だけ: ハブの SPEC (c5f63b69) 2026-11-19
