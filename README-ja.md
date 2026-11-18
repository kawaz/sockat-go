# sockat-go

> [English](./README.md) | 日本語

`sun_path` の上限 (NUL 込みで macOS と BSD は 104 バイト、Linux は 108 バイト) を超えるパスでも、Unix domain socket を bind / connect できる Go のライブラリ。

言語によらない振る舞いの正本は [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) で、sockat-go はその Go 実装。

```go
l, err := sockat.Listen("/とても/深い/ディレクトリ/.../server.sock") // *net.UnixListener
c, err := sockat.Dial("/とても/深い/ディレクトリ/.../server.sock")   // *net.UnixConn
```

## 使い方

標準の `net.ListenUnix` / `net.DialUnix` (`net.Listen("unix", ...)` / `net.Dial("unix", ...)`) の代わりに `sockat.Listen` / `sockat.Dial` を使えばよい。

```sh
go get github.com/kawaz/sockat-go
```

- 戻り値は標準の `*net.UnixListener` / `*net.UnixConn` そのもの (`net.Listener` / `net.Conn` にそのまま代入できる)
- 標準が受け付ける長さのパスは標準にそのまま渡すので、標準と同じに振る舞う
- 長いパスでも、ソケットファイルは渡したパスにできる
- エラーは標準と同じ `*net.OpError`。`errors.Is(err, syscall.ENOENT)` 等が効く
- 依存は `golang.org/x/sys` だけ。Go は支援中の 2 版 (1.26 以降)

## なぜ要るか

標準の `net` は `sun_path` に収まらないパスを `EINVAL` で断る。ファイルシステムには上限が無いのに、深い一時ディレクトリ (macOS の `$TMPDIR` はそれだけで 50 バイト前後、`t.TempDir()` がさらに掘る。[golang/go#62614](https://github.com/golang/go/issues/62614)) にソケットを置くとすぐに当たる。上限が効くのは `bind(2)` / `connect(2)` に渡す引数の長さなので、sockat は同じ場所を指す短い綴りでカーネルに渡す。

## 長いパスへの届き方

標準にそのまま渡す長さ:

| OS | 長さ |
|---|---|
| Linux / Android / illumos | 107 バイトまで |
| macOS / FreeBSD / NetBSD / OpenBSD | 103 バイトまで |

それを超えるパスは、OS ごとに次の順に試し、最初に使えた経路で bind / connect する。

| OS | 試す順番 (Listen / Dial 共通) |
|---|---|
| Linux / Android (`/proc` を使える) | /proc |
| Linux / Android (`/proc` を使えない) | 相対パス → symlink → スレッド cwd |
| FreeBSD | bindat / connectat |
| macOS | 相対パス → symlink → スレッド cwd |
| NetBSD / OpenBSD / illumos / その他の Unix | 相対パス → symlink |
| Windows | 対象外 |

- **/proc**: ディレクトリを `O_PATH` で開き、`/proc/self/fd/<fd>/<ファイル名>` で渡す。`/proc` が無い時と、ファイル名がこれに収まらない時 (およそ 90 バイト超) は使えない
- **bindat / connectat**: ディレクトリの fd を基準にファイル名で渡す
- **相対パス**: cwd からの相対パスが標準に渡せる長さなら、それで渡す。文字列を書き直すだけで、cwd は変えない
- **symlink**: `TMPDIR` か `/tmp` に短い symlink を作り、それを通して渡す。Listen はソケットのディレクトリを、Dial はソケットのファイルを指す。他人が差し替えられない置き場にだけ作り、すぐ消す。`/proc` を使える Linux の Dial は、他人が書ける置き場でも、そこに自分専用のディレクトリ (0700) を作って fd で掴み、その中に symlink を置いて `/proc/self/fd/<fd>/s` で渡す
- **スレッド cwd**: OS スレッドに固定した goroutine で、そのスレッドだけ cwd をディレクトリに移し、ファイル名で渡す。スレッドは使い終わったら捨てる。Linux は `unshare(CLONE_FS)` (seccomp の filter が掛かった環境では使わない。docker の既定等)、macOS は `__pthread_fchdir`

どの経路もプロセスの cwd を変えないので、どの goroutine から呼んでもよい。

### SPEC から外れる経路

| 経路 | Go 版 | 理由 |
|---|---|---|
| fork | なし | Go の runtime では生の fork を使えない。そのため `connect_any` 系も無い |
| 広げた sockaddr (macOS) | なし | 自前のソケットを標準の型に渡すと、`sockaddr_un` より長いアドレスを `syscall` が断る。bind したものは `net.FileListener` が `EINVAL`、connect したものは相手が広げた形の時に `RemoteAddr()` が nil になる |
| スレッド cwd | Linux / macOS だけ | NetBSD / OpenBSD / illumos にはスレッド単位の cwd が無い |

### どこから届かなくなるか

上限を超えるパスが `ENAMETOOLONG` になる時 (数字はバイト)。

| OS | Listen | Dial |
|---|---|---|
| Linux | ファイル名 > 107。seccomp の filter が掛かった環境では、ファイル名が `/proc` の綴りに収まらず、相対パス > 107 で、ファイル名 > 84 か symlink が使えない時 | ファイル名 > 107 で symlink も使えない。seccomp の filter が掛かった環境では、`/proc` の綴りにも相対パスにも収まらず、symlink も使えない時 |
| FreeBSD | ファイル名 > 103 | ファイル名 > 103 |
| macOS | ファイル名 > 103 | ファイル名 > 103 で symlink も使えない |
| NetBSD / OpenBSD | 相対パス > 103 で、ファイル名 > 80 か symlink が使えない | 相対パス > 103 で symlink も使えない |
| illumos | 相対パス > 107 で、ファイル名 > 84 か symlink が使えない | 相対パス > 107 で symlink も使えない |

`/tmp` に symlink を作れる普通の環境では、Dial はディレクトリの深さにもファイル名の長さにもよらず届く。

## 標準と違う点

- **NUL を含むパスは長さによらず `EINVAL`**。標準は NUL で黙って切り詰め、別のパスに bind する。先頭が NUL か `@` の Linux の abstract socket の名前は、標準にそのまま渡す

長いパスでだけ違う点:

- **listener の Close でソケットファイルを消さない**。標準 (と短いパス) は消す。長いパスの listener が覚えているアドレスはカーネルに渡した綴りなので、`SetUnlinkOnClose(true)` を呼ばない。消す時は渡したパスで `os.Remove` する (unlink に長さの上限は無い)
- **`Addr()` / `LocalAddr()` / `RemoteAddr()` は渡したパスを返さない**。カーネルに渡した綴り (相対パス、`/proc/self/fd/...`、もう無い symlink を通したパス、ファイル名だけ) を返す。元のパスは自分で持っておく
- **名前で終わらないパス** (`dir/`、`dir/.`、`dir/..`) は何も作らずに `EINVAL`
- **ディレクトリを先に引く**: /proc・相対パス・bindat・スレッド cwd は、ディレクトリを引いてからファイル名で渡す。その間の rename や symlink の張り替えは反映されない
- **他の goroutine の `os.Chdir`**: 相対パスの経路の途中で cwd が変わったら、結果を捨ててエラーを返す
- **symlink が残りうる**: symlink やディレクトリを作ってから消すまでにプロセスが死ぬと、`sockat.XXXXXXXXXX` が置き場に残る

## ドキュメント

- [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) — 言語によらない振る舞い (経路、安全条件、エラー)
- [docs/decisions/](./docs/decisions/INDEX.md) — Go 版の設計の判断 (DR)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## ライセンス

MIT License, Yoshiaki Kawazu (@kawaz)
