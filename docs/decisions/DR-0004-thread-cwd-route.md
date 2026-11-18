# DR-0004: fork の代わりにスレッドだけ cwd を移す経路を最後に試す

- Status: Accepted
- Date: 2026-11-18

## Context

SPEC の最後の経路は、ディレクトリに `fchdir` した子を fork し、子がファイル名で bind / connect するもの。Go は runtime がマルチスレッドで、生の fork は使えない。一方、スレッドごとに cwd を持たせる手が macOS と Linux にある。

## Decision

- `runtime.LockOSThread` した使い捨ての goroutine で、そのスレッドだけ cwd を中心の経路の D ([DR-0011](DR-0011-central-route.md)) に移し、`R/<ファイル名>` で標準を呼ぶ。スレッドは sockat が起こした goroutine のもので、呼び出し元のスレッドには触らない
  - Linux: `unshare(CLONE_FS)` → `fchdir`。seccomp の filter が掛かっている時 (`prctl(PR_GET_SECCOMP)` が 0 でない) は `unshare` を呼ばない
  - macOS: `__pthread_fchdir` (syscall 349、libSystem の `pthread_fchdir_np` の中身)
- goroutine は Unlock せずに終える。runtime はそのスレッドを捨て、他の goroutine に渡さない。新しいスレッドは locked なスレッドからは作られないので、cwd は他のスレッドに移らない
- 中心の経路の最初の手段として、Listen も Dial も試す ([DR-0011](DR-0011-central-route.md))
- 使えないとみなす時: Linux で seccomp の filter が掛かっている・`prctl` か `unshare` が失敗した、macOS で `ENOSYS`。その時は中心の経路の次の手段 (/proc 版) へ進む。`unshare` の後の `fchdir` の失敗は本当の失敗として返す
- NetBSD / OpenBSD / illumos にはスレッド単位の cwd が無いので、この経路は無い

実測 (macOS 26.5、Linux の docker arm64 で root と uid 1000、go 1.27):

| 確かめたこと | 結果 |
|---|---|
| 333 バイトのパスに、ファイル名で ListenUnix / DialUnix | 通り、ソケットは元の場所にできる |
| 並行する 8 goroutine の `getcwd` | 一度も変わらない |
| 200 回繰り返した後の OS スレッドの数 | 増えない |
| サーバから見える peer の pid (`LOCAL_PEERPID` / `SO_PEERCRED`) | 呼び出し元の pid |
| docker の既定の seccomp | `unshare` が `EPERM` (filter が掛かっているので、今は `unshare` を呼ぶ前に使えないとみなす) |

## 理由

- seccomp の filter には `unshare` をプロセスの kill (SIGSYS) で断るもの (systemd の `SystemCallFilter=` の既定等) がある。filter の下では呼ばない (ハブの DR-0005)

- fork の 2 つの副作用 (peer の pid がもう終了した子になる、fd の複製) が無い。なので connect も試してよい
- macOS の `pthread_fchdir_np` の wrapper は標準にも x/sys にも無く、libSystem を直接呼ぶには cgo が要る。`syscall.Syscall` で番号を呼ぶ (番号は XNU の syscalls.master で固定)

## 却下した案

| 案 | 却下の理由 |
|---|---|
| 自分の実行ファイルを再 exec した子に bind / connect させる | 利用者のプログラムの初期化 (各 package の init) が子で走る。peer の pid の副作用も fork と同じ |
| UnlockOSThread の前に cwd を戻してスレッドを使い続ける | 戻すのに失敗した時に、他の goroutine が別の cwd で動く。スレッドを捨てる方が確実 |

## 判断の出所

- この経路を Listen / Dial の最後の手にすること: kawaz 裁定 (HB-Q4) 2026-11-18
- seccomp の filter の下で `unshare` を呼ばないこと: ハブの DR-0005 2026-11-18
