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
l, discard, res, err := sockat.ListenEx(path, sockat.NoLastResort) // l は *net.UnixListener
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

## 失ったものと経路を知る、最終手段を使う (`ListenEx` / `DialEx`)

`sockat.ListenEx` と `sockat.DialEx` は、`Listen` / `Dial` と同じ経路を同じ順に試し、標準の型と一緒に `sockat.Result` を返す。2 つ目の引数は最終手段を使うか。裸の `true` / `false` でなく定数で書く。

```go
l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("%s: route %q, skipped %v", path, re.Route, re.Skipped)
	}
	return err
}
defer discard()
defer l.Close()
if res.Has(sockat.RecreatableSpelling) {
	log.Printf("%s: bound through a shared place (route %s, skipped %v)", path, res.Route, res.Skipped)
}

c, res, err := sockat.DialEx(path, sockat.NoLastResort) // c は *net.UnixConn
```

- `res.Losses` は通った経路で失ったものの名前の並び ([SPEC の「失うもの」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#失うもの))。何も失わなければ空。名前に大小や包含は無い。名前は将来増えうるので、知らない名前は「何かを失った」とだけ読む。分岐はこれで書く
- `res.Route` は通った経路の名前、`res.Skipped` はそれより前に試して使えなかった経路の並び (試した順) と理由。どちらも診断用 (ログ等) で、中身は約束しない
- 失敗した時のエラーは `*sockat.RouteError` で、`Route` (実際に bind / connect して失敗した経路。全部使えなかった `ENAMETOOLONG` と、ディレクトリが無い等の経路を試す前の失敗では空) と `Skipped` を持つ。中は `Listen` / `Dial` と同じ `*net.OpError` で、`errors.Is(err, syscall.ENOENT)` 等が効く
- 最終手段は、既定の経路が全部使えない (`ENAMETOOLONG`) 時だけ試す
  - `ListenEx`: 置き場の symlink-dir (ソケットのディレクトリを指す symlink、`<置き場>/sockat.XXXXXXXXXX/<ファイル名>`、bind の後すぐ消す)。置き場は `XDG_RUNTIME_DIR`・`TMPDIR`・`/tmp` のうち、自分だけが書ける所を先に、無ければ共有の所 (sticky の `/tmp` 等)
  - `DialEx`: fork-cwd-direct (fork した子が connect する)。Go には fork が無いので `res.Skipped` に入るだけで、届く所は `Dial` と同じ
- FreeBSD では at が常に使え、symlink-dir の綴りは at の綴りより必ず長いので、最終手段が `Listen` より遠くに届くことは無い

| `Loss` | 失ったもの | 付く時 |
|---|---|---|
| `Respelled` (`respelled`) | カーネルが覚えるアドレスが元のパスでない。Close でファイルが消えない (`discard` か元のパスで消す) | 標準以外の経路で bind した |
| `CwdRelative` (`cwd-relative`) | カーネルが覚える綴りが cwd からの相対パス。cwd を変えた後にその綴りで消すと別の場所に当たる (標準が元から持つ危険) | 相対パスで standard を通った |
| `Forked` (`forked`) | fork の副作用 | Go には fork の経路が無いので付かない (他の言語と名前をそろえるため) |
| `RecreatableSpelling` (`recreatable-spelling`) | カーネルが覚える綴りが共有の置き場を通る。他の利用者が綴りを作り直した後にその綴りで消すと、相手の選んだディレクトリのファイルを消しうる (`discard` か元のパスで消せば当たらない) | `ListenEx` の最終手段で共有の置き場の symlink-dir を通った |

`DialEx` の `Losses` はいつも空 (connect の綴りは相手に残らない)。

失うものごとの避け方を下に置く。コードは試験 ([example_test.go](./example_test.go) の Example 関数) として動かしている。

### respelled

[SPEC の respelled](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#respelled)。長いパスでは `l.Addr()` が渡したパスでない綴りを返し、listener を閉じてもソケットのファイルは消えない。`ListenEx` の `discard` で消す (元のパスを持っておいて `os.Remove(path)` でもよい。[`ExampleListenEx_respelled`](./example_test.go)):

```go
l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
if err != nil {
	log.Fatal(err)
}
defer discard() // not os.Remove(l.Addr().String())
defer l.Close()
fmt.Println("respelled:", res.Has(sockat.Respelled))
```

### cwd-relative

[SPEC の cwd-relative](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#cwd-relative)。相対パスはそのまま標準に渡り、カーネルは cwd からの相対の綴りを覚える。cwd を変えた後にその綴りや相対パスで消すと別の場所に当たる。Listen の前に `filepath.Abs` で絶対パスにする。消すのは `discard` (相対パスでも bind の時の cwd で覚えている) かその絶対パス ([`ExampleListenEx_cwdRelative`](./example_test.go)):

```go
abs, err := filepath.Abs(path)
if err != nil {
	log.Fatal(err)
}
l, discard, res, err := sockat.ListenEx(abs, sockat.UseLastResort)
if err != nil {
	log.Fatal(err)
}
defer discard()
defer l.Close()
fmt.Println("cwd-relative:", res.Has(sockat.CwdRelative))
```

### forked

[SPEC の forked](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#forked)。Go には fork の経路が無いので付かない。

### recreatable-spelling

[SPEC の recreatable-spelling](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#recreatable-spelling)。`ListenEx` の最終手段で共有の置き場 (`/tmp` 等) の symlink-dir を通ると、カーネルが返す綴りを他の利用者が作り直せ、その綴りで消すと相手の選んだディレクトリのファイルを消しうる。`l.Addr()` の綴りで消さず `discard` で消し、持ち主だけが書ける置き場を `XDG_RUNTIME_DIR` で用意しておく (プログラムを起動する側で渡す。共有の置き場より先に使われ、この名前は付かない。[`ExampleListenEx_recreatableSpelling`](./example_test.go)):

```go
l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
if err != nil {
	log.Fatal(err)
}
defer discard() // not os.Remove(l.Addr().String())
defer l.Close()
if res.Has(sockat.RecreatableSpelling) {
	log.Printf("%s: bound through a shared place", path)
}
```

## 使ってはいけない書き方

無関係なファイルを消したり、ファイルを消し損ねたりする書き方 ([SPEC の「使ってはいけない書き方」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#使ってはいけない書き方))。

| 書き方 | なぜいけないか | 代わりに |
|---|---|---|
| `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` | 長いパスでは渡したパスでない ([respelled](#respelled))。cwd や共有の置き場しだいで、作り直された別のファイルに当たりうる ([cwd-relative](#cwd-relative)、[recreatable-spelling](#recreatable-spelling)) | `ListenEx` の `discard()`、または元のパスで `os.Remove(path)` ([respelled](#respelled)) |
| 相対パスで Listen し、`os.Chdir` の後にその相対パスで `os.Remove` する | 別の場所のファイルに当たる ([cwd-relative](#cwd-relative)) | Listen の前に `filepath.Abs` で絶対パスにする ([cwd-relative](#cwd-relative))。`discard()` |
| `l.Close()` だけで済ませる、`l.SetUnlinkOnClose(true)` で消えると当てにする | 長いパスでは、Go の標準と違い Close で消えない ([respelled](#respelled)) | `discard()` か、元のパスで `os.Remove(path)` ([respelled](#respelled)) |
| `discard()` の後に listener が閉じていると思って `l.Close()` を呼ばない | `discard()` はファイルを消すだけ | `defer l.Close()` を自分で書く ([respelled](#respelled)) |
| `res.Route == "symlink-dir"` や `res.Skipped` の `Name` / `Reason` で分岐する | 診断用で、中身を約束しない (経路は増えたり変わったりする) | `res.Has(sockat.RecreatableSpelling)` 等、`res.Losses` で分岐する |
| 他の goroutine が `waitpid(-1)` で子を刈る | Go では起きない (fork の経路が無い) | なし |

## なぜ要るか

標準の `net` は `sun_path` に収まらないパスを `EINVAL` で断る。ファイルシステムには上限が無いのに、深い一時ディレクトリ (macOS の `$TMPDIR` はそれだけで 50 バイト前後、`t.TempDir()` がさらに掘る。[golang/go#62614](https://github.com/golang/go/issues/62614)) にソケットを置くとすぐに当たる。上限が効くのは `bind(2)` / `connect(2)` に渡す引数の長さなので、sockat は同じ場所を指す短い綴りでカーネルに渡す。

## 長いパスへの届き方

標準にそのまま渡す長さ:

| OS | 長さ |
|---|---|
| Linux / Android / illumos | 107 バイトまで |
| macOS / FreeBSD / NetBSD / OpenBSD | 103 バイトまで |

それを超えるパスは、短い綴りを作る経路 ([SPEC の「経路の名前」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#経路の名前)) を左から順に試し、最初に使えた経路で bind / connect する。Listen と Dial で順が違う。

| OS | Listen | Dial |
|---|---|---|
| Linux / Android (`/proc` を使える) | thread-cwd → proc | proc-direct → thread-cwd → proc → proc-held → symlink-file |
| Linux / Android (`/proc` を使えない) | thread-cwd | relative → thread-cwd → symlink-file |
| macOS | symlink-file → thread-cwd | relative → (widened は無い) → thread-cwd → symlink-file |
| FreeBSD | symlink-file → at (bindat) | at-direct → at (connectat) |
| NetBSD / OpenBSD / illumos / その他の Unix | symlink-file だけ | relative → symlink-file |
| Windows | 対象外 | 対象外 |

Listen:

- **symlink-file** (Linux / Android 以外): `TMPDIR` か `/tmp` の、他人が差し替えられない置き場に、元のパスのソケットのファイルを指す短い symlink `sockat.XXXXXXXXXX` を作ってその綴りで bind し、すぐ消す。bind が最後の要素の symlink を辿る OS (macOS) では元のパスにソケットができ、綴りにファイル名が出ないのでファイル名の長さによらず届く。辿らない OS では bind が失敗するので、元のパスに物が無ければ次の経路へ進む (物があればそのエラー)。Linux は辿らないので試さない。FreeBSD・NetBSD・OpenBSD・illumos は辿るか未確認で、実行時に見分ける
- **D を通す経路**: 自分専用のディレクトリ D を作って fd で掴み、その中にソケットのディレクトリを指す乱数の名前の symlink R を作り、D を基準に `R/<ファイル名>` で bind する。終わったら R と D を消す
  - **thread-cwd**: OS スレッドに固定した使い捨ての goroutine で、そのスレッドだけ cwd を D に移し、`R/<ファイル名>` で渡す。スレッドは使い終わったら捨てる (Unlock せずに goroutine を終える)。Linux は `unshare(CLONE_FS)` (seccomp の filter が掛かった環境では使わない。docker の既定等)、macOS は `__pthread_fchdir`
  - **proc**: `/proc/self/fd/<D の fd>/R/<ファイル名>` で渡す。スレッドを使わない
  - **at**: `bindat(2)` に D の fd と `R/<ファイル名>` を渡す
- **D の置き場**: `${TMPDIR:-/tmp}`、作れなければ `/tmp`、それも作れなければソケットのディレクトリ (名前は `.sockat.XXXXXXXXXX`)。置き場が深くても、他人が書ける所でも使える (D を名前でなく fd で辿り、作ってから開くまでのすり替えは持ち主と権限で弾く)。D をどこにも作れない時は D 無しの形 (thread-cwd-sockdir → proc-sockdir、FreeBSD は at-sockdir) で試す: ソケットのディレクトリに R (`.sockat.XXXXXXXXXX`) を作り、そのディレクトリの fd を基準に `.sockat.XXXXXXXXXX/<ファイル名>` で bind する (ファイル名は Linux 88、macOS / FreeBSD 84 まで、proc-sockdir はおよそ 72)

Dial は、connect した側のソケットが bind されず綴りが相手に残らないので、書き込みもスレッドも要らない経路を先に試し、D と R を作る経路とスレッドを立てる経路をその後にする ([SPEC の「connect の順」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#connect-の順)):

- **proc-direct**: ソケットのディレクトリを開き、`/proc/self/fd/<fd>/<ファイル名>` で渡す
- **relative**: cwd からの相対パスが渡せる長さなら、それで渡す。文字列を書き直すだけで、cwd は変えない
- **at-direct**: `connectat(2)` にソケットのディレクトリの fd とファイル名を渡す
- **thread-cwd / proc / at**: Listen と同じく D と R を作って渡す
- **proc-held**: D の中にソケットのファイルを指す symlink `s` を置き、`/proc/self/fd/<D の fd>/s` で渡す (R は作らない)。綴りにファイル名が出ないので、ファイル名の長さによらず届く
- **symlink-file**: Listen と同じ置き場の symlink で渡す (connect は symlink を辿るので、どの OS でも使える)

D は 1 回の呼び出しで 1 度だけ作り、D を使う経路で共有する。どこにも作れなければ、D を使う経路はどれも飛ばす (作り直さない)。

どの経路もプロセスの cwd も呼び出し元のスレッドの cwd も変えないので、どの goroutine から呼んでもよい。

### SPEC から外れる経路

| 経路 | Go 版 | 理由 |
|---|---|---|
| fork-cwd・fork-cwd-sockdir (bind)、fork-cwd-direct (最終手段を使う connect) | なし (`Skipped` に `no fork route in sockat-go` で入り、次へ進む) | Go の runtime では生の fork を使えない |
| widened (macOS の Dial の 2 番目) | なし (飛ばして thread-cwd へ進む) | 自前のソケットを標準の型に渡すと、相手が広げた形で bind している時に `RemoteAddr()` が nil になる (`syscall` が `sockaddr_un` より長いアドレスを断る) |

**fork が無いので、次の環境では長いパスの Listen が `ENAMETOOLONG` になる:**

- **NetBSD / OpenBSD / illumos** で bind が symlink を辿らない時: D を通す経路の手段 (thread-cwd、proc、at) が無い
- Linux で seccomp の filter が掛かり `/proc` も無い環境
- macOS で `__pthread_fchdir` が使えず、symlink-file の置き場 (`TMPDIR`・`/tmp`) も無い環境

それでも届かせたい時は `ListenEx(path, sockat.UseLastResort)` を使う (上の「最終手段を使う」)。

### どこから届かなくなるか

上限を超えるパスが `ENAMETOOLONG` になる時 (ディレクトリがあり、D を作れる前提。数字はバイト)。

| OS | Listen | Dial |
|---|---|---|
| Linux (`/proc` あり) | ファイル名 > 96。seccomp の filter が掛かった環境ではおよそ 79 (proc) | 届く (proc-held) |
| Linux (`/proc` 無し) | ファイル名 > 96。seccomp の filter が掛かった環境では全部 | thread-cwd が使えず (ファイル名 > 96 かスレッド cwd が使えない)、相対パス > 107 で、symlink-file も使えない時 |
| macOS | 届く (symlink-file)。symlink-file の置き場が無い時はファイル名 > 92 | thread-cwd が使えず (ファイル名 > 92 かスレッド cwd が使えない)、相対パス > 103 で、symlink-file も使えない時 |
| FreeBSD | ファイル名 > 92 | ファイル名 > 103 |
| NetBSD / OpenBSD | 全部 | 相対パス > 103 で symlink-file も使えない時 |
| illumos | 全部 | 相対パス > 107 で symlink-file も使えない時 |

- Listen の欄で symlink-file を数えているのは macOS だけ。FreeBSD・NetBSD・OpenBSD・illumos は bind が symlink を辿るか未確認で、欄は数えていない (辿れば、symlink-file の置き場がある時はファイル名によらず届く)
- `/proc` のある Linux で D を作れる時と、FreeBSD 以外で `/tmp` に symlink を作れる普通の環境では、Dial はディレクトリの深さにもファイル名の長さにもよらず届く。D をどこにも作れない時、Dial は D を使わない経路 (proc-direct、relative、at-direct、symlink-file) の分だけ届く
- パス全体が `PATH_MAX` (macOS 1024、Linux 4096 バイト) を超えるパスは、他のシステムコールでも開けないので sockat の責務外で、`ENAMETOOLONG` になる

## 標準と違う点

- **NUL を含むパスは長さによらず `EINVAL`**。標準は NUL で黙って切り詰め、別のパスに bind する。先頭が NUL か `@` の Linux の abstract socket の名前は、標準にそのまま渡す

長いパスでだけ違う点:

- **listener の Close でソケットファイルを消さない**。標準 (と短いパス) は消す。長いパスの listener が覚えているアドレスはカーネルに渡した綴りなので、`SetUnlinkOnClose(true)` を呼ばない。消すなら `ListenEx` の `discard` を使う
- **長いパスの listener の `Addr()` (と accept したソケットの `LocalAddr()`) は渡したパスを返さない**。bind でカーネルに渡した綴り `R/<ファイル名>` (proc は `/proc/self/fd/<fd>/R/<ファイル名>`、symlink-file は `<置き場>/sockat.XXXXXXXXXX`) を返す。そういう listener に connect した側の `RemoteAddr()` も、Dial がどの経路を通ったかによらず同じ綴りを返す (カーネルは相手が bind した綴りを返す)。R も symlink ももう無いので、その綴りを消したり stat したりしても何にも当たらないが、他のプロセスに渡して繋がせる用途には使えない。元のパスは自分で持っておく
- **名前で終わらないパス** (`dir/`、`dir/.`、`dir/..`) は何も作らずに `EINVAL`
- **ディレクトリを先に引く**: D を通す経路は R を作る時にソケットのディレクトリを realpath で引く。proc-direct・relative・at-direct はディレクトリを引いてからファイル名で渡す。その間の rename や symlink の張り替えは反映されない。symlink-file は標準と同じ
- **他の goroutine の `os.Chdir`**: relative (Dial) の途中で cwd が変わったら、結果を捨ててエラーを返す
- **D 無しの形の差し替え**: sticky でなく他人も書けるソケットのディレクトリでは、D 無しの形 (Listen) の R を作ってから bind するまでに R を差し替えられ、別のディレクトリに bind しうる (同じ名前が既にあれば `EADDRINUSE` で、上書きはしない)
- **残骸**: 消す前にプロセスが死ぬと、`sockat.XXXXXXXXXX` (D、symlink) が置き場に、`.sockat.XXXXXXXXXX` (D) がソケットのディレクトリに残る

## ドキュメント

- [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) — 言語によらない振る舞い (経路、安全条件、エラー)
- [docs/decisions/](./docs/decisions/INDEX.md) — Go 版の設計の判断 (DR)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## ライセンス

MIT License, Yoshiaki Kawazu (@kawaz)
