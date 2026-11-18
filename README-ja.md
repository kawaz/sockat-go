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

後でソケットのファイルを消したい時は `sockat.ListenEx` を使う:

```go
l, discard, err := sockat.ListenEx(path) // l は *net.UnixListener
if err != nil {
	return err
}
defer discard() // ソケットのファイルを消す (l は閉じない)
defer l.Close()
```

- `discard` は bind した直後にファイルを覚えておき、そのパスが今も同じファイル (`(st_dev, st_ino)`) の時だけ消す。間に他のソケットに置き換わっていたら消さない。覚えられなければ `ListenEx` は listener を閉じてエラーを返す (ファイルは消さない)
- `discard` がエラーを返すのは、ファイルを確かめられない時 (権限が無い等) と消すのに失敗した時。既に無い・置き換わっていた時は nil
- 確かめてから消すまでの一瞬に置き換えられると見分けられない (POSIX に inode を指定して消す手段が無い)
- `ListenEx` の listener は Close でファイルを消さない (短いパスも)。消すのは `discard` だけ

## 何かを失ってでも届かせる (`atanycost`)

`sockat.Listen` が `ENAMETOOLONG` になる所 (上の環境や長いファイル名) でも届かせたい時だけ、別パッケージの `atanycost.Listen` を使う。import することが opt-in。

```go
import "github.com/kawaz/sockat-go/atanycost"

l, res, err := atanycost.Listen(path) // l は *net.UnixListener
if err != nil {
	return err
}
if res.Level == atanycost.SharedSymlink {
	log.Printf("%s: bound through a shared place (route %s, skipped %v)", path, res.Route, res.Skipped)
}
```

- `sockat.Listen` と同じ経路を試し、`ENAMETOOLONG` の時だけ置き場の symlink (ソケットのディレクトリを指す、`<置き場>/sockat.XXXXXXXXXX/<ファイル名>`、bind の後すぐ消す) を試す。置き場は `XDG_RUNTIME_DIR`・`TMPDIR`・`/tmp` のうち、自分だけが書ける所を先に、無ければ共有の所 (sticky の `/tmp` 等)
- `res.Level` は通った経路で失ったもの ([SPEC の「失ったものの段階」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#失ったものの段階))。値に大小は無い (`Forked` と `SharedSymlink` はどちらも `Respelled` を含むが互いを含まない)。値は将来増えうる。知らない値は「標準ではない (何かを失った、中身は分からない)」とだけ読む
- FreeBSD では at が常に使え、symlink の綴りは at の綴りより必ず長いので、`atanycost.Listen` が `sockat.Listen` より遠くに届くことは無い
- `res.Route` は通った経路の名前、`res.Skipped` はそれより前に試して使えなかった経路の並び (試した順) と理由。どちらも診断用 (ログ等) で、中身は約束しない

| `Level` | 失ったもの |
|---|---|
| `None` | 無し (標準) |
| `Respelled` | カーネルが覚えるアドレスが元のパスでない。Close でファイルが消えない |
| `Forked` | Go には fork の経路が無いので来ない (他の言語と値をそろえるため) |
| `SharedSymlink` | `Respelled` に加え、他の利用者が綴りを作り直せる (覚えている綴りで消すと、相手の選んだディレクトリのファイルを消させうる) |

## なぜ要るか

標準の `net` は `sun_path` に収まらないパスを `EINVAL` で断る。ファイルシステムには上限が無いのに、深い一時ディレクトリ (macOS の `$TMPDIR` はそれだけで 50 バイト前後、`t.TempDir()` がさらに掘る。[golang/go#62614](https://github.com/golang/go/issues/62614)) にソケットを置くとすぐに当たる。上限が効くのは `bind(2)` / `connect(2)` に渡す引数の長さなので、sockat は同じ場所を指す短い綴りでカーネルに渡す。

## 長いパスへの届き方

標準にそのまま渡す長さ:

| OS | 長さ |
|---|---|
| Linux / Android / illumos | 107 バイトまで |
| macOS / FreeBSD / NetBSD / OpenBSD | 103 バイトまで |

それを超えるパスは、自分専用のディレクトリ D を作って fd で掴み、その中にソケットのディレクトリを指す乱数の名前の symlink R を作り、D を基準に `R/<ファイル名>` で bind / connect する (中心の経路)。終わったら R と D を消す。

経路は [SPEC の「経路の名前」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#経路の名前)で呼ぶ。

| OS | Listen | Dial |
|---|---|---|
| Linux / Android | thread-cwd → proc | thread-cwd → proc → 予備の経路 |
| macOS | thread-cwd | thread-cwd → 予備の経路 |
| FreeBSD | at (bindat) | at (connectat) → 予備の経路 |
| NetBSD / OpenBSD / illumos / その他の Unix | なし (`ENAMETOOLONG`) | 予備の経路だけ |
| Windows | 対象外 | 対象外 |

- **D の置き場**: `${TMPDIR:-/tmp}`、作れなければ `/tmp`、それも作れなければソケットのディレクトリ (名前は `.sockat.XXXXXXXXXX`)。置き場が深くても、他人が書ける所でも使える (D を名前でなく fd で辿り、作ってから開くまでのすり替えは持ち主と権限で弾く)。D をどこにも作れない時、Listen は D 無しの形 (thread-cwd-sockdir → proc-sockdir、FreeBSD は at-sockdir) で試す: ソケットのディレクトリに R (`.sockat.XXXXXXXXXX`) を作り、そのディレクトリの fd を基準に `.sockat.XXXXXXXXXX/<ファイル名>` で bind する (ファイル名は Linux 88、macOS / FreeBSD 84 まで、proc-sockdir はおよそ 72)
- **thread-cwd**: OS スレッドに固定した使い捨ての goroutine で、そのスレッドだけ cwd を D に移し、`R/<ファイル名>` で渡す。スレッドは使い終わったら捨てる (Unlock せずに goroutine を終える)。Linux は `unshare(CLONE_FS)` (seccomp の filter が掛かった環境では使わない。docker の既定等)、macOS は `__pthread_fchdir`
- **proc**: `/proc/self/fd/<D の fd>/R/<ファイル名>` で渡す。スレッドを使わない
- **at**: `bindat(2)` / `connectat(2)` に D の fd と `R/<ファイル名>` を渡す

Dial の予備の経路 (中心の経路が使えない時):

| OS | 試す順番 |
|---|---|
| Linux / Android (`/proc` を使える) | proc-direct → (置き場ごとに) symlink か proc-held |
| Linux / Android (`/proc` を使えない) | relative → symlink |
| macOS / NetBSD / OpenBSD / illumos / その他 | relative → symlink |
| FreeBSD | at-direct |

- **proc-direct**: ソケットのディレクトリを開き、`/proc/self/fd/<fd>/<ファイル名>` で渡す
- **relative**: cwd からの相対パスが渡せる長さなら、それで渡す。文字列を書き直すだけで、cwd は変えない
- **symlink**: `TMPDIR` か `/tmp` の、他人が差し替えられない置き場に、ソケットのファイルを指す短い symlink を作って渡し、すぐ消す
- **proc-held**: `/proc` を使える Linux で、他人が書ける置き場に自分専用のディレクトリを作って fd で掴み、その中に symlink を置いて `/proc/self/fd/<fd>/s` で渡す
- **at-direct**: `connectat(2)` にソケットのディレクトリの fd とファイル名を渡す

どの経路もプロセスの cwd も呼び出し元のスレッドの cwd も変えないので、どの goroutine から呼んでもよい。

### SPEC から外れる経路

| 経路 | Go 版 | 理由 |
|---|---|---|
| fork (bind の最後の手、D 無しの fork、`connect_any` 系) | なし | Go の runtime では生の fork を使えない。そのため `connect_any` 系も無い |
| widened (macOS の Dial の予備) | なし | 自前のソケットを標準の型に渡すと、相手が広げた形で bind している時に `RemoteAddr()` が nil になる (`syscall` が `sockaddr_un` より長いアドレスを断る) |

**fork が無いので、次の環境では長いパスの Listen が `ENAMETOOLONG` になる:**

- **NetBSD / OpenBSD / illumos**: D を通す経路の手段 (thread-cwd、proc、at) が無い。長いパスの Listen は全部 `ENAMETOOLONG`
- Linux で seccomp の filter が掛かり `/proc` も無い環境
- macOS で `__pthread_fchdir` が使えない環境

それでも届かせたい時は `atanycost.Listen` を使う (下の「何かを失ってでも届かせる」)。

### どこから届かなくなるか

上限を超えるパスが `ENAMETOOLONG` になる時 (ディレクトリがあり、D を作れる前提。数字はバイト)。

| OS | Listen | Dial |
|---|---|---|
| Linux | ファイル名 > 96。seccomp の filter が掛かった環境ではおよそ 79 (proc)、`/proc` も無ければ全部 | ファイル名 > 96 で、予備の経路 (proc-direct、symlink、proc-held) も使えない時 |
| macOS | ファイル名 > 92 | ファイル名 > 92 で、相対パス > 103、symlink も使えない時 |
| FreeBSD | ファイル名 > 92 | ファイル名 > 103 |
| NetBSD / OpenBSD | 全部 | 相対パス > 103 で symlink も使えない時 |
| illumos | 全部 | 相対パス > 107 で symlink も使えない時 |

`/tmp` に symlink を作れる普通の環境では、Dial はディレクトリの深さにもファイル名の長さにもよらず届く。

パス全体が `PATH_MAX` (macOS 1024、Linux 4096 バイト) を超えるパスは、他のシステムコールでも開けないので sockat の責務外で、`ENAMETOOLONG` になる。

## 標準と違う点

- **NUL を含むパスは長さによらず `EINVAL`**。標準は NUL で黙って切り詰め、別のパスに bind する。先頭が NUL か `@` の Linux の abstract socket の名前は、標準にそのまま渡す

長いパスでだけ違う点:

- **listener の Close でソケットファイルを消さない**。標準 (と短いパス) は消す。長いパスの listener が覚えているアドレスはカーネルに渡した綴りなので、`SetUnlinkOnClose(true)` を呼ばない。消すなら `ListenEx` の `discard` を使う
- **`Addr()` / `LocalAddr()` / `RemoteAddr()` は渡したパスを返さない**。カーネルに渡した綴り `R/<ファイル名>` (proc は `/proc/self/fd/<fd>/R/<ファイル名>`) を返す。R はもう無いので、その綴りを消したり stat したりしても何にも当たらないが、他のプロセスに渡して繋がせる用途には使えない。元のパスは自分で持っておく
- **名前で終わらないパス** (`dir/`、`dir/.`、`dir/..`) は何も作らずに `EINVAL`
- **ディレクトリを先に引く**: 中心の経路は R を作る時にソケットのディレクトリを realpath で引く。予備の経路の /proc・相対パス・connectat はディレクトリを引いてからファイル名で渡す。その間の rename や symlink の張り替えは反映されない
- **他の goroutine の `os.Chdir`**: 相対パスの経路 (Dial の予備) の途中で cwd が変わったら、結果を捨ててエラーを返す
- **D 無しの形の差し替え**: sticky でなく他人も書けるソケットのディレクトリでは、D 無しの形 (Listen) の R を作ってから bind するまでに R を差し替えられ、別のディレクトリに bind しうる (同じ名前が既にあれば `EADDRINUSE` で、上書きはしない)
- **残骸**: 消す前にプロセスが死ぬと、`sockat.XXXXXXXXXX` (D、symlink) が置き場に、`.sockat.XXXXXXXXXX` (D) がソケットのディレクトリに残る

## ドキュメント

- [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) — 言語によらない振る舞い (経路、安全条件、エラー)
- [docs/decisions/](./docs/decisions/INDEX.md) — Go 版の設計の判断 (DR)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## ライセンス

MIT License, Yoshiaki Kawazu (@kawaz)
