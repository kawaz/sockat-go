# DR-0004: スレッドだけ cwd を移す経路 (thread-cwd・thread-cwd-direct) は、Unlock しない使い捨ての goroutine で作る

- Status: Accepted
- Date: 2026-11-18

## Context

SPEC の thread-cwd (bind) と thread-cwd-direct (connect) は、使い捨てのスレッドの cwd だけを D (thread-cwd) かソケットのディレクトリ (thread-cwd-direct) に移し、短い綴りで bind / connect する。sockat は fork しない。Go は runtime がマルチスレッドで、goroutine とスレッドの対応を runtime が決める。スレッドごとに cwd を持たせる手は macOS と Linux にある。

## Decision

- `runtime.LockOSThread` した使い捨ての goroutine で、そのスレッドだけ cwd を移し、標準を呼ぶ。thread-cwd は D に移して `E/<ファイル名>` で、thread-cwd-direct はソケットのディレクトリに移してファイル名で呼ぶ ([DR-0018](DR-0018-route-orders-per-os.md))。スレッドは sockat が起こした goroutine のもので、呼び出し元のスレッドには触らない
  - Linux: `unshare(CLONE_FS)` → `fchdir`。seccomp の filter が掛かっている時 (`prctl(PR_GET_SECCOMP)` が 0 でない) は `unshare` を呼ばない
  - macOS: `__pthread_fchdir` (syscall 349、libSystem の `pthread_fchdir_np` の中身)
- cwd を移しうる所まで進んだ goroutine は Unlock せずに終える。runtime はそのスレッドを捨て、他の goroutine に渡さない。新しいスレッドは locked なスレッドからは作られないので、cwd は他のスレッドに移らない
- 使えないと分かった時 (下記。スレッドの cwd はプロセスのまま) は Unlock してスレッドを runtime に返す (捨てる理由が無い)
- 主スレッド (runtime の m0。package の init が走るスレッドで、Linux ではスレッドグループの leader も) では cwd を移さない。runtime は locked のまま終わった goroutine の主スレッドを捨てずに眠らせ続けるので、移した cwd がプロセスの終わりまで残る (Linux の `/proc/<pid>/cwd` がそのディレクトリを指し続け、ディレクトリが使用中のまま、主スレッドもプログラムから失われる)。使い捨ての goroutine が主スレッドに乗ったら、主スレッドを lock したまま (他の goroutine を乗せないため) もう 1 つの使い捨ての goroutine を起こし、それは主スレッド以外で走る。終わったら主スレッドを Unlock する
- 呼び出し元は goroutine の結果を待ってから E を片付ける (thread-cwd の E は成功でも失敗でもすぐ消す)
- 使えないとみなす時: Linux で seccomp の filter が掛かっている・`prctl` か `unshare` が失敗した、macOS で `__pthread_fchdir` が失敗した (`ENOSYS` に限らず `EACCES`・サンドボックスの `EPERM` 等も。スレッドの cwd は変わらない)。その時は次の経路へ進む (bind の thread-cwd は列の最後なので `ENAMETOOLONG`、connect の thread-cwd-direct は link-file へ)。`unshare` の後の `fchdir` の失敗は本当の失敗として返す
- NetBSD / OpenBSD / illumos にはスレッド単位の cwd が無いので、この経路は無い (列にも並ばない)

実測 (macOS 26.5、Linux の docker arm64 で root と uid 1000、go 1.27):

| 確かめたこと | 結果 |
|---|---|
| 333 バイトのパスに、ファイル名で ListenUnix / DialUnix | 通り、ソケットは元の場所にできる |
| 並行する 8 goroutine の `getcwd` | 一度も変わらない |
| 200 回繰り返した後の OS スレッドの数 | 増えない |
| サーバから見える peer の pid (`LOCAL_PEERPID` / `SO_PEERCRED`) | 呼び出し元の pid |
| docker の既定の seccomp | `unshare` が `EPERM` (filter が掛かっているので、今は `unshare` を呼ぶ前に使えないとみなす) |
| main の goroutine から呼んだ時の使い捨ての goroutine のスレッド (主スレッドに乗る対策が無い形で、GOMAXPROCS 1・2・8、2026-11-22) | 1 回目は毎回主スレッドに乗り (`gettid() == getpid()`)、その後 `/proc/self/cwd`・`/proc/<pid>/cwd` は移した先のまま (`os.Getwd` は元のまま)。macOS でもその後の goroutine は主スレッドに乗らなくなる |

## 理由

- seccomp の filter には `unshare` をプロセスの kill (SIGSYS) で断るもの (systemd の `SystemCallFilter=` の既定等) がある。filter の下では呼ばない (ハブの DR-0005)

- 自分のプロセスのスレッドなので、fork や子プロセスの副作用 (peer の pid が別のプロセスになる、fd の複製) が無い。なので connect も試してよい
- macOS の `pthread_fchdir_np` の wrapper は標準にも x/sys にも無く、libSystem を直接呼ぶには cgo が要る。`syscall.Syscall` で番号を呼ぶ (番号は XNU の syscalls.master で固定)

## 却下した案

| 案 | 却下の理由 |
|---|---|
| 自分の実行ファイルを再 exec した子に bind / connect させる | 利用者のプログラムの初期化 (各 package の init) が子で走る。peer の pid の副作用も fork と同じ |
| UnlockOSThread の前に cwd を戻してスレッドを使い続ける | 戻すのに失敗した時に、他の goroutine が別の cwd で動く。スレッドを捨てる方が確実 |

## 判断の出所

- この経路を使うこと: kawaz 裁定 (HB-Q4) 2026-11-18
- bind・connect の列での位置: ハブの SPEC「bind の順」「connect の順」と DR-0017
- seccomp の filter の下で `unshare` を呼ばないこと: ハブの DR-0005 2026-11-18
