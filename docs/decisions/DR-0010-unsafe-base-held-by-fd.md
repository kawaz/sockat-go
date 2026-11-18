# DR-0010: /proc を使える Linux の Dial は、安全でない置き場に自分専用のディレクトリを作り fd で掴んで使う

- Status: Accepted
- Date: 2026-11-18

## Context

ハブの [DR-0008](https://github.com/kawaz/sockat/blob/main/docs/decisions/DR-0008-unsafe-base-held-by-fd.md) が SPEC に「fd で掴む形」を足した。Linux で seccomp の filter が掛かり (スレッド cwd の経路が使えない、[DR-0004](DR-0004-thread-cwd-route.md))、`TMPDIR` も `/tmp` も安全な置き場でなく、ファイル名が `/proc` の綴りに収まらないと、connect が `ENAMETOOLONG` になる (hyoui の実測)。

## Decision

- `/proc` を使える Linux / Android の Dial だけ、symlink の経路で安全でない置き場の候補を飛ばさず、fd で掴む形で使う (`heldLinkRoute`)
  1. 置き場に mode 0700 のディレクトリ `sockat.XXXXXXXXXX` を作る (`EEXIST` で名前を引き直し、16 回で打ち切る)
  2. `O_PATH | O_DIRECTORY | O_NOFOLLOW` で開き (読み取り権限が無くても開ける)、`fstat` で「ディレクトリ・持ち主が実効 uid・group と other に権限が無い」を確かめ、`chmod("/proc/self/fd/<fd>", 0700)` で持ち主のビットをそろえる (O_PATH の fd に `fchmod` はできない。umask 0277 / 0477 等で mkdir が 0500 / 0300 になっても毎回落ちて残骸が溜まらないように)
  3. fd の中に `symlinkat` でソケットのファイル (元のパス、相対パスなら cwd を前に付ける) を指す `s` を作る
  4. `/proc/self/fd/<fd>/s` を `net.DialUnix` に渡す
  5. 片付け: 自分が `symlinkat` に成功した `s` だけを `unlinkat(fd, "s")` で消す。ディレクトリは手順 2 の確認が通っていて、名前を `lstat` した `(st_dev, st_ino)` が fd と一致する時だけ `rmdir`。確認に落ちたディレクトリ (差し替えられたもの) は中身も含めて触らない。fd を閉じる
- 試す順: `TMPDIR` が安全なら symlink → 安全でなければ fd で掴む形 → `/tmp` も同じ → スレッド cwd
- `/proc` を使えるかは、置き場に何かを作る前に確かめる (置き場を開いた fd で `/proc/self/fd/<fd>` を引く)。使えなければ安全でない置き場には何も作らない (SPEC の「fd で掴む形が無い OS」と同じ扱い)
- 使えないとみなして次の候補へ: `/proc` を使えない、ディレクトリを作れない・開けない、`fstat` の確認が通らない、`symlinkat` が失敗した。作った物は片付けてから進む。開けなかったディレクトリは、自分の物と確かめられないので消さない
- Listen は使わない。綴り (`/proc/self/fd/<fd>/s/<ファイル名>`) が /proc の経路の綴りより長く、収まるなら /proc の経路が先に通っている
- macOS と他の Unix は今まで通り、安全でない置き場には何も作らない

## 理由

- 名前でなく fd で辿るので、sticky でない置き場で他人がディレクトリを rename して差し替えても影響を受けない
- 作ってから開くまでのすり替えは `fstat` の確認で弾く
- connect の綴りはファイル名の長さによらず収まる

## 判断の出所

- kawaz 裁定 (ハブの DR-0008) 2026-11-18
- /proc を作る前に確かめること、fstat の確認を緩めて持ち主のビットをそろえること、O_PATH で開いて /proc 経由で chmod すること、片付けを自分が作った物だけにすること: 統括の裁定 (ハブ b6f511a、1875978、e673b93) 2026-11-18
