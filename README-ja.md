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
- 長いパスの bind は、カーネルが覚える綴りを後でも辿れるように、アプリの置き場の外 (`/tmp/sockat-<uid>` 等) に D と E を残す。OS の一時ディレクトリの掃除か `sockat.Sweep()` で消える。生きているプロセスの D を手で消しても、綴りが辿れなくなるだけで、元のパスのソケットは働く。試験を何度も回す環境では `sockat.Sweep()` を 1 回呼べば死んだ D が溜まらない ([置き場に残る物と `Sweep`](#置き場に残る物と-sweep))

### 関数

| 関数 | 返す物 | SPEC の関数 |
|---|---|---|
| `Listen(path)` | `*net.UnixListener` | `bind` |
| `Dial(path)` | `*net.UnixConn` | `connect`<br>`connect_nonblocking` |
| `ListenEx(path, Options)` | `*net.UnixListener`<br>`*Bound` | `bind_ex` |
| `DialEx(path, Options)` | `*net.UnixConn`<br>`Report` | `connect_ex`<br>`connect_nonblocking_ex` |
| `ListenUnixgram(path)` | `*net.UnixConn` (DGRAM) | `datagram::bind` |
| `DialUnixgram(path)` | `*net.UnixConn` (DGRAM、bind していない) | `datagram::unbound` と `datagram::connect` |
| `ListenUnixgramEx(path, Options)` | `*net.UnixConn`<br>`*Bound` | `datagram::bind_ex` |
| `DialUnixgramEx(path, Options)` | `*net.UnixConn`<br>`Report` | `datagram::connect_ex` |
| `ResolveForBind(path, Options)` | `*ForBind` | `resolve_for_bind` |
| `ResolveForConnect(path, Options)` | `*ForConnect` | `resolve_for_connect` |
| `Sweep()` | | `sweep` |
| `SweepBase(base)` | | `sweep_base` |

- `Dial` は 1 つ。Go の標準の connect は元から待たない (backlog が満杯なら Linux は即 `EAGAIN`、macOS は `ECONNREFUSED`) ので、`_nonblocking` を分けない
- `ListenEx` 等の `Ex` は、標準の型と一緒に後始末と診断を返す。失敗は `*sockat.RouteError`
- DGRAM の `send_to` と SEQPACKET は SPEC で未確定なので無い

### ソケットのファイルを消す (`ListenEx` と `SocketFile`)

```go
l, b, err := sockat.ListenEx(path, sockat.Options{}) // l は *net.UnixListener
if err != nil {
	return err
}
defer l.Close()
defer b.File.Remove() // ソケットのファイルを消す (l は閉じない)。l.Close より先に走る
```

- `b.File` (`*sockat.SocketFile`) は bind した直後にソケットのファイルの realpath と `(st_dev, st_ino)` を覚え、`Remove()` はそのパスが今も同じファイルの時だけ消す。覚えられなければ `ListenEx` は listener を閉じ、綴りの E を片付け、ファイルは消さずにエラーを返す
- 元のパスの最後の要素が既にある symlink の時 (macOS・NetBSD では標準の bind がそれを辿り、その先にソケットを作る)、覚えるのは辿った先のソケットのファイルで、`Remove()` はそれを消す (利用者の symlink は消さない)。辿った先がソケットでなければ覚えられない時と同じに扱う
- **`Remove()` は listener を閉じる前に呼ぶ** (上の `defer` の順で `Remove` が先に走る)。置き換えを見分けられるのは listener が開いている間だけ: 開いている間はカーネルがソケットのファイルを掴んでいるので、他のプロセスが消して作り直した物は別の `(st_dev, st_ino)` になり、`Remove` は消さない。閉じた後に消して作り直された物は、番号をすぐ使い回すファイルシステム (Linux の ext4、BSD の UFS / FFS) では同じ番号になり、`Remove` が消しうる
- `Remove()` はファイルを消した後に `Cleanup()` もする。`Cleanup()` は綴りが通る E (symlink) だけを片付け、ファイルは消さない。どちらも成功した後の呼び出しは何もしない。失敗した後の呼び出しはやり直す (`Remove` はファイルを消せた後に `Cleanup` だけ失敗したなら、次はファイルに触らず `Cleanup` だけをやり直す。ファイルをやり直す時も同じファイルかを確かめ直す)。並行して呼ばれても消す操作は同時に 1 つ。自動では呼ばれない
- `Remove()` がエラーを返すのは、ファイルを確かめられない時 (権限が無い等) と消すのに失敗した時。既に無い・置き換わっていた時は nil。エラーでも `Cleanup` までは行う
- 確かめてから消すまでの一瞬に置き換えられると見分けられない (POSIX に inode を指定して消す手段が無い)
- `ListenEx` の listener は Close でファイルを消さない (短いパスも)。消すのは `Remove` だけ

### 綴りの性質 (`Spelling`)

長いパスでは、カーネルが覚えるアドレス (listener の `Addr()`、accept した接続の `LocalAddr()`、相手の `RemoteAddr()`) は渡したパスでなく、bind の時にカーネルに渡した綴り。`Bound.Spelling` (と `ForBind.Spelling`) は、その綴りを後で使えるかを返す ([SPEC の「綴りの性質」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#綴りの性質-spelling))。分岐はこれで書く。

| `Kind` | 綴り | 経路 |
|---|---|---|
| `Original` | 元の絶対パス (abstract の名前も) | standard |
| `CwdRelative` | 元の相対パス。cwd を変えると別の所を指す (標準が元から持つ危険) | standard |
| `Linked` | 置き場の B・D・E を通り、元のパスと同じ所を指す | link-dir、link-file |
| `Dangling` | 片付け済みの E を含み、どこにも届かない | thread-cwd、at |

`Linked` の時だけ意味を持つ項目 (他の `Kind` では false かゼロ値):

| 項目 | 意味 |
|---|---|
| `Removable` | 綴りで unlink すると実体が消えるか。link-dir は true (途中の E を辿る)、link-file は false (最後の symlink は辿られず E が消える) |
| `Until` | 綴りがいつまで辿れるか。`UntilProcess` (`/proc/<pid>/fd/<B の fd>` を通る綴り: プロセスが終われば辿れない)、`UntilCleanup` (B の実パスを通る綴り: `Cleanup` まで) |
| `UsableCrossUID` | 別の uid の相手が辿れるか。`/proc` を通る綴りは false。実パスの綴りは B までの途中と B 自身が全部 other に x を持てば true (`/tmp` の B) |
| `UsableWithPrivateTmp` | 自分か相手が `/tmp` を隔離していても (systemd の `PrivateTmp` 等) 相手が辿れるか。`/proc` を通る綴りは true。実パスの綴りは、B の置き場か綴りが `/tmp`・`/var/tmp` を通れば false |

```go
switch sp := b.Spelling; sp.Kind {
case sockat.Original, sockat.CwdRelative:
	// 元のパスそのもの
case sockat.Linked:
	// sp.Until の間は l.Addr() で元のパスに届く
case sockat.Dangling:
	// l.Addr() はどこにも届かない。元のパスを持っておく
}
```

`Spelling` は `==` で比べられ、`Kind` のゼロ値は使わない (失敗した時のゼロ値を `Original` と読ませない)。`Kind.String()` と `Until.String()` は SPEC の名前 (`"Linked"`、`"Process"` 等) を返す。

### 診断 (`Route`・`Base`・`Skipped`) と `RouteError`

```go
c, rep, err := sockat.DialEx(path, sockat.Options{}) // c は *net.UnixConn
if err != nil {
	var re *sockat.RouteError
	if errors.As(err, &re) {
		log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
	}
	return err
}
log.Printf("dial %s: route %s after %v", path, rep.Route, rep.Skipped)
```

- `Route` は通った経路の名前、`Skipped` はそれより前に試して使えなかった経路 (試した順) と理由。長いパスでは `standard` も入る。`Skipped` は nil にならない
- `Bound.Base` は綴りが通った B (`proc`・`tmp`・`runtime`・`tmpdir`・`sockdir`・`given`)。綴りが B を通らない時 (standard・thread-cwd・at) は `""`
- どれも診断用 (ログ等) で、中身は約束しない。分岐は `Spelling` で書く
- 失敗した時のエラーは `*sockat.RouteError` で、`Route` (実際に bind / connect して失敗した経路。全部使えなかった `ENAMETOOLONG` と、ディレクトリが無い等の経路を試す前の失敗では空) と `Skipped` を持つ。`Unwrap` で `Listen` / `Dial` と同じ `*net.OpError` に届くので、`errors.Is(err, syscall.ENOENT)` 等が効く

### `Options`

| 項目 | 意味 |
|---|---|
| `UsableCrossUID bool` | 別の uid の相手も辿れる綴りを求める。B の置き場のうち途中と B 自身が全部 other に x を持つ所 (`/tmp` 等) だけを使い、`/proc` を通る綴りを使わない。満たせなければ満たさない綴りに落とさず thread-cwd / at (`Dangling`) へ進む。満たせたかは `Spelling.UsableCrossUID` で分かる。bind 系 (`ListenEx`・`ListenUnixgramEx`・`ResolveForBind`) だけが見る |
| `Base string` | B そのもののパス (アプリが管理する場所。例 `/tmp/app-1000/sockat`)。`""` は指定しない。そこに B を作り、確かめに外れたらエラーで、自動の置き場に落とさない。connect 系でも B の置き場として使う。B は sockat 専用で、中に他の物を置かない |

`Base` のエラー:

- 相対パス、最後が名前でない (`dir/`・`..`・`/`): パスの長さによらず `EINVAL`
- 以下は長いパスの時だけ確かめる (短いパスは標準に渡すので見ない)
  - 途中のディレクトリが無い: `ENOENT`
  - B の場所がディレクトリでも symlink でもない: `ENOTDIR`
  - B が symlink、他人が書ける、持ち主が違う、途中が他人に付け替えられる: `EPERM`
  - `UsableCrossUID` と一緒の bind 系で、途中か B 自身が other に x を持たない: `EPERM`

ゼロ値の `Options{}` が既定で、`ListenEx` / `DialEx` は `Listen` / `Dial` と同じ経路を同じ順に試す。

### DGRAM

```go
srv, err := sockat.ListenUnixgram(path) // *net.UnixConn
c, err := sockat.DialUnixgram(path)     // *net.UnixConn (bind していない)
```

- `ListenUnixgram` は `Listen` と同じ経路で bind する。カーネルが覚える綴り (`LocalAddr()`、相手から見た送り元) は `Listen` と同じで、綴りが `Linked` の間は返信が届く。Close でファイルを消さないのは標準と同じ
- `DialUnixgram` は bind していないソケットを作って connect する (標準の `net.DialUnix("unixgram", nil, ...)` の形)。`Dial` と同じ経路を試す
- bind したソケットから長いパスへ送る・connect する時は、`ResolveForConnect` で綴りを作って標準に渡す:

```go
to, err := sockat.ResolveForConnect(serverPath, sockat.Options{})
if err != nil {
	return err
}
defer to.Cleanup()
_, err = cl.WriteToUnix(msg, &net.UnixAddr{Name: to.Path, Net: "unixgram"})
```

### resolve (`ResolveForBind` / `ResolveForConnect`)

綴りを自分で標準に渡したい時 (他のライブラリにパスを渡す、DGRAM の送り先にする等) は、長いパスを短い綴りに言い換えて返す resolve を使う。綴りはただのパスなので、ソケットの型と操作を問わず標準に渡せる。

```go
f, err := sockat.ResolveForBind(path, sockat.Options{})
if err != nil {
	return err // ENAMETOOLONG: Listen の thread-cwd / at でしか届かない長さ
}
l, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.Path, Net: "unix"})
if err != nil {
	f.Cleanup()
	return err
}
l.SetUnlinkOnClose(false) // 消すのは SocketFile の Remove
defer l.Close()
sf, err := f.SocketFile() // bind の後・l を閉じる前に呼ぶ
if err != nil {
	return err
}
defer sf.Remove()
```

```go
f, err := sockat.ResolveForConnect(path, sockat.Options{})
if err != nil {
	return err
}
defer f.Cleanup()
c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.Path, Net: "unix"}) // 何度でも
```

| | `ResolveForBind` | `ResolveForConnect` |
|---|---|---|
| 綴り | link-dir の `B/D/E/<ファイル名>` を先に、bind が symlink を辿ると実測した OS (macOS・NetBSD) だけ link-file の `B/D/E` をその後に | link-file の `B/D/E`。ファイル名の長さによらない |
| 返す物 | `*ForBind` (`Path`、`Spelling`、`Cleanup()`、`SocketFile()`、診断) | `*ForConnect` (`Path`、`Cleanup()`、診断) |
| 使えない時 | `ENAMETOOLONG` (thread-cwd・at でしか届かない長さ) | `ENAMETOOLONG` (B を作れない等) |

- 標準に渡す長さ以下のパスはそのまま返す (`Spelling` は `Original` / `CwdRelative`)
- `ForBind.SocketFile()` は呼んだ時点で綴りを辿り、ソケットのファイルを覚えた `*SocketFile` を返す (元のパスの最後の要素が symlink なら、`ListenEx` と同じく辿った先を覚える)。bind の前 (`ENOENT`)、辿った先がソケットでない (`ENOTSOCK`)、`CwdRelative` の綴りで resolve の時と cwd が違う時はエラー。返す `SocketFile` の `Cleanup` は `ForBind` の `Cleanup` と同じ E を片付ける (先に成功した方が消し、以後はどちらも何もしない)
- `ForConnect` は `SocketFile` を返さない (消し間違いを型で防ぐ)。`UsableCrossUID` は見ない (connect の綴りは相手に残らない)
- エラーは `*RouteError` で、中は `*os.PathError` (`Op` は `resolve`、`Path` は渡したパス)
- 片付けた後の綴りは使わない

### 置き場に残る物と `Sweep`

長いパスの綴りは、利用者ごとの置き場 B の中の、プロセスごとのディレクトリ D と symlink E を通る。B・D・E はアプリの置き場の外 (`/tmp` 等) に残る ([SPEC の「置き場に残る物」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#置き場に残る物))。

| | B | D | E |
|---|---|---|---|
| 場所と名前 | `/tmp/sockat-<uid>` → `$XDG_RUNTIME_DIR/sockat` → `$TMPDIR/sockat-<uid>` → ソケットのディレクトリの `.sockat.<乱数>` の順で最初に作れた所。`Options.Base` を渡せばそこ | B の中の乱数 10 文字 | resolve の E と thread-cwd・at の E は D の中。既定の connect の E は B の直下 |
| 作る単位 | 利用者ごと。プロセスをまたいで使い回す | (プロセス, B) ごとに 1 つ。bind 系と `ResolveForConnect` だけが作る | resolve ごと。thread-cwd・at と既定の connect は呼び出しごと |
| 生きている間 | 消さない | プロセスが共有ロック (flock) を掛けた fd を持ち続ける | resolve の E は `Cleanup` まで残る。呼び出しごとの E は呼び出しの中で消す |
| 片付け | sockat はしない (`Options.Base` の B はアプリが管理) | sockat はしない。死んだプロセスの D は OS の一時ディレクトリの掃除で消える。`Sweep` を呼んだ時だけ、ロックの外れた D を消す | `Cleanup` (`Remove`) |

- 既定の `Listen` は `SocketFile` を返さないので、その E はプロセスが終わるまで残り、D と一緒に OS の掃除で消える。片付けを気にするなら `ListenEx` か resolve を使う
- 既定の `Dial` はプロセスの中に D も E も残さない (B の直下の E を connect の後すぐ消す)
- B は (`Options.Base` で指定した物も) sockat 専用で、利用者は中に他の物を置かない
- `Sweep()` は `/tmp/sockat-<uid>`・`$XDG_RUNTIME_DIR/sockat`・`$TMPDIR/sockat-<uid>` のうち、あって確かめに通る物の中の死んだ D を消す。B は作らない。`SweepBase(base)` は `Options.Base` に渡した B の中の死んだ D を消す (その B を管理するアプリが呼ぶ)。消すのは名前が D の形 (英数字 10 文字) のディレクトリだけで、生きている他のプロセスの D、symlink でない物がある D、B の直下の E には触らない。自動では呼ばれない
- 呼び出しの中で E を片付けるのに失敗しても、その呼び出しの成否も返すエラーも変わらない
- **ソケットのディレクトリの B (`.sockat.<乱数>`) と、確かめに外れて落とした B (`sockat-<uid>.<乱数>`) は、`Sweep` も OS の掃除も消さずに残る**。乱数の名前なので次のプロセスも使い回さず、プロセスごとに増えうる。ソケットのディレクトリの B は利用者のディレクトリに残る

OS の一時ディレクトリの掃除 ([ハブの調査](https://github.com/kawaz/sockat/blob/main/docs/research/2026-11-20-tmp-cleaners-and-persistent-base.md)):

| 置き場 | 死んだ D | 生きている D |
|---|---|---|
| Linux の `/tmp` | systemd-tmpfiles が 10 日で消す | ロックの掛かった D は消されない (systemd v242 以上)。**v242 より古い環境 (RHEL 8 等) では 10 日で消されうる** |
| `$XDG_RUNTIME_DIR` | ログアウトと再起動まで残る | 同左 |
| macOS の `/tmp` | 再起動まで残る | 消えない |
| macOS の `$TMPDIR` | dirhelper が 3 日で消しうる | **同左 (消されうる)** |
| FreeBSD・NetBSD・OpenBSD・illumos | 既定は消えないか、再起動で消える | 同左 |

生きている D が消された時は、綴りが辿れなくなるだけで、無関係なファイルには当たらない。

## 使ってはいけない書き方

無関係なファイルを消したり、ファイルを消し損ねたりする書き方 ([SPEC の「使ってはいけない書き方」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#使ってはいけない書き方))。避け方のコードは試験 ([example_long_test.go](./example_long_test.go) の Example 関数) として動かしている。

| 書き方 | なぜいけないか | 代わりに |
|---|---|---|
| `Spelling` を見ずに `os.Remove(l.Addr().String())` / `os.Stat(l.Addr().String())` | 長いパスでは渡したパスでない。link-file の綴りでは E が消えるだけでファイルは残り、`Dangling` の綴りはどこにも届かない | 元のパスを持っておいて使う。`b.File.Remove()` (`ExampleListenEx`) |
| `Cleanup` の後やプロセスが終わった後に、古い綴り (`ForBind.Path`、`ForConnect.Path`、`l.Addr()`) を使う | 片付けた後の `/tmp` の B は、他人が同じ名前で作り直しうる。`UntilProcess` の綴りはプロセスが終われば辿れない | 綴りは `Spelling.Until` の間だけ使う |
| 相対パスで Listen し、`os.Chdir` の後にその相対パスで `os.Remove` する | 別の場所のファイルに当たる (`CwdRelative`) | Listen の前に `filepath.Abs` で絶対パスにする (`ExampleListenEx_absolutePath`)。`b.File.Remove()` |
| `l.Close()` だけでソケットのファイルが消えると当てにする | 長いパスでは綴りしだいで消えない (「[標準と違う点](#標準と違う点)」)。`ListenEx` の listener は閉じても消さない | `b.File.Remove()` か、元のパスで `os.Remove(path)` |
| `b.File.Remove()` で listener も閉じると当てにする | `Remove` はファイルを消すだけ | `defer l.Close()` を自分で書く |
| `l.Close()` の後に `b.File.Remove()` を呼ぶ | 閉じた後に作り直された別のファイルを見分けられないことがある | listener を閉じる前に呼ぶ (`defer` は `l.Close` を先に書く) |
| `/proc` を通る綴りや `Spelling.UsableCrossUID` が false の綴りを、別の uid の相手に辿らせる | 辿れない | `Options{UsableCrossUID: true}` を渡し、結果の `Spelling` を確かめる (`ExampleSpelling`) |
| `Route`・`Base`・`Skipped` の `Name` / `Reason` の文字列で分岐する | 診断用で、中身を約束しない (経路は増えたり変わったりする) | `Spelling` で分岐する |

## なぜ要るか

標準の `net` は `sun_path` に収まらないパスを `EINVAL` で断る。ファイルシステムには上限が無いのに、深い一時ディレクトリ (macOS の `$TMPDIR` はそれだけで 50 バイト前後、`t.TempDir()` がさらに掘る。[golang/go#62614](https://github.com/golang/go/issues/62614)) にソケットを置くとすぐに当たる。上限が効くのは `bind(2)` / `connect(2)` に渡す引数の長さなので、sockat は同じ場所を指す短い綴りでカーネルに渡す。

## 長いパスへの届き方

標準にそのまま渡す長さ:

| OS | 長さ |
|---|---|
| Linux / Android / illumos | 107 バイトまで |
| Apple の OS / FreeBSD / NetBSD / OpenBSD | 103 バイトまで |

それを超えるパスは、まずソケットのディレクトリを引き (無ければ `ENOENT`、ディレクトリでなければ `ENOTDIR`)、短い綴りを作る経路 ([SPEC の「経路の名前」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md#経路の名前)) を左から順に試し、最初に使えた経路で bind / connect する。経路が実際に bind / connect して失敗したら、そのエラーを返して次の経路でやり直さない。

bind (`Listen`・`ListenEx`・`ListenUnixgram`・`ListenUnixgramEx`):

| OS | 経路 |
|---|---|
| Linux / Android | link-dir (`/proc` を通す綴り → B の実パス) → thread-cwd |
| Apple の OS | link-dir → link-file → thread-cwd |
| FreeBSD | link-dir → at |
| NetBSD | link-dir → link-file |
| OpenBSD / illumos | link-dir |
| その他の Unix | link-dir → link-file (bind が symlink を辿るかを実行時に見分ける) |

connect (`Dial`・`DialEx`・`DialUnixgram`・`DialUnixgramEx`):

| OS | 経路 |
|---|---|
| Linux / Android (`/proc` を使える) | proc-direct → thread-cwd-direct → link-file |
| Linux / Android (`/proc` を使えない) | relative → thread-cwd-direct → link-file |
| Apple の OS | relative → widened (作らない) → thread-cwd-direct → link-file |
| FreeBSD | at-direct → link-file |
| NetBSD / OpenBSD / illumos / その他の Unix | relative → link-file |
| Windows | 対象外 |

resolve: `ResolveForBind` は bind の列の link-dir と (macOS・NetBSD だけ) link-file、`ResolveForConnect` は link-file だけ (Linux は `/proc` を通す綴り → B の実パス)。

bind の経路 (綴りを残すので、生きている間は `l.Addr()` の綴りで消す上のライブラリと DGRAM の返信が元のパスと同じに働く):

- **link-dir**: D の中にソケットのディレクトリ (realpath) を指す E を作り、`B/D/E/<ファイル名>` で bind する。B は `/proc/<pid>/fd/<B の fd>` (Linux で `/proc` を使えて `UsableCrossUID` でない時) か B の実パスで綴る。E は `Cleanup` まで残す。`Spelling` は `Linked` (`Removable`)
- **link-file**: D の中にソケットのファイルを指す E を作り、`B/D/E` で bind する。bind が最後の要素の symlink を辿る OS だけで使える (辿ると実測したのは macOS と NetBSD。辿らないと実測した Linux / Android・FreeBSD・OpenBSD・illumos では試さない。他の OS では bind が失敗して元のパスに物が無ければ次へ進む)。綴りにファイル名が出ないので、ファイル名の長さによらず届く。`Spelling` は `Linked` (`Removable` でない)
- **thread-cwd**: OS スレッドに固定した使い捨ての goroutine で、そのスレッドだけ cwd を D に移し、すぐ消す E を通す `E/<ファイル名>` で bind する。スレッドは使い終わったら捨てる (Unlock せずに goroutine を終える)。Linux は `unshare(CLONE_FS)` → `fchdir` (seccomp の filter が掛かった環境では使わない。docker の既定等)、Apple の OS は `__pthread_fchdir`。`Spelling` は `Dangling`
- **at**: thread-cwd と同じ綴りを、`bindat(2)` に D の fd で渡す (FreeBSD)。`Spelling` は `Dangling`

connect の経路 (connect した側のソケットは bind されず綴りが相手に残らないので、書き込みもスレッドも要らない経路を先に試す):

- **proc-direct**: ソケットのディレクトリを開き、`/proc/self/fd/<fd>/<ファイル名>` で渡す
- **relative**: cwd からの相対パスが渡せる長さなら、それで渡す。文字列を書き直すだけで、cwd は変えない
- **at-direct**: `connectat(2)` にソケットのディレクトリの fd とファイル名を渡す
- **thread-cwd-direct**: 使い捨てのスレッドの cwd をソケットのディレクトリに移し、ファイル名で渡す。D も E も作らない
- **link-file**: B の直下にソケットのファイルを指す乱数の名前の E を作り、B の実パスの `B/E` で渡して、すぐ E を消す。D は作らない。ファイル名の長さによらず届く

B の作り方 ([SPEC-routes の「B の作り方」](https://github.com/kawaz/sockat/blob/main/docs/SPEC-routes-ja.md#b-の作り方)):

- 置き場を realpath で実パスにし、ルートから置き場までの途中を確かめる (どれも持ち主が root か自分で、他人が書けないか sticky)。外れた置き場は使わない
- 置き場を `O_NOFOLLOW` で開いて fd で掴み、`mkdirat(0700)` → 0711 にそろえる → 開いて実ディレクトリ・持ち主が自分かを確かめる。既にあれば同じく確かめ (他人が書けないことも)、外れたら同じ置き場の `sockat-<uid>.<乱数>` に落とす
- `$XDG_RUNTIME_DIR`・`$TMPDIR` が空か相対パスの時は、その置き場を使わない
- プロセスは最初に作れた B を以後も使う (パスの長さでは変わらない)。綴りが収まらないと先に分かる時は B を作らず、その経路を使えないとして飛ばす
- ソケットのディレクトリの B は `/proc` を通す綴りでだけ使う (実パスの綴りは元のパスより長い)。`/proc` の無い OS では thread-cwd / at の D の置き場にだけなる
- 持っている B・D が消されていたら、次の呼び出しで作り直す

どの経路もプロセスの cwd も呼び出し元のスレッドの cwd も変えないので、どの goroutine から呼んでもよい。

### SPEC から外れる経路

| 経路 | Go 版 | 理由 |
|---|---|---|
| widened (Apple の OS の connect の relative の次) | なし (`Skipped` に `no widened route in sockat-go` で入り、次へ進む) | 自前のソケットを標準の型に渡すと、相手が広げた形で bind している時に `RemoteAddr()` が nil になり `String()` で落ちる (`syscall` が `sockaddr_un` より長いアドレスを断る) |

widened が無いので、Apple の OS で relative に収まらず、thread-cwd-direct も (`__pthread_fchdir` が無いかファイル名が 103 バイトを超える) link-file も (B を作れない) 使えない時、元のパスか相対パスが 253 バイト以内の connect は `ENAMETOOLONG` になる。fork は SPEC どおりどの経路でもしない。

### どこから届かなくなるか

ハブの [届く範囲の表](https://github.com/kawaz/sockat/blob/main/pages/reach/README-ja.md) の見積もりで、普段の環境 (スレッドの cwd と `/proc` を使え、置き場は OS の既定のまま書け、`Options` を指定しない) で届くファイル名の長さ (バイト。ディレクトリは標準に渡す長さを超えるほど深い前提):

| OS | `Listen` / `ListenEx` | `ResolveForBind` | `Dial` / `ResolveForConnect` |
|---|---|---|---|
| Linux / Android | 96 まで (thread-cwd)。seccomp の filter が掛かった環境では link-dir の分 (およそ 76) | およそ 76 | 255 まで |
| macOS | 255 まで (link-file) | 255 まで | 255 まで |
| FreeBSD | 92 まで (at) | およそ 72 | 255 まで (link-file は FreeBSD で未確認。at-direct は 103 まで) |
| NetBSD | 255 まで (link-file) | 255 まで | 255 まで |
| OpenBSD | およそ 72 | およそ 72 | 255 まで |
| illumos | およそ 76 | およそ 76 | 255 まで |

- link-dir の届くファイル名は、sockaddr に入る長さ (Linux / illumos 107、他 103) − 15 − B の実パスの長さ (`/tmp/sockat-1000` は 16、macOS の `/private/tmp/sockat-501` は 23)。`/proc` を通す綴りはおよそ 73 (pid の桁で変わる)
- B を作れない環境 (置き場が全部書けない等) では、B を通る経路 (link-dir・link-file) が使えない。connect は B を使わない経路 (proc-direct、relative、at-direct、thread-cwd-direct) の分だけ届く
- パス全体が `PATH_MAX` (macOS 1024、Linux 4096 バイト) を超えるパスは、他のシステムコールでも開けないので sockat の責務外で、`ENAMETOOLONG` になる

## 標準と違う点

- **NUL を含むパスは長さによらず `EINVAL`**。標準は NUL で黙って切り詰め、別のパスに bind する。Linux / Android では、先頭が NUL か `@` の abstract socket の名前を標準にそのまま渡す (`Spelling` は `Original`)。abstract socket の無い OS (illumos を含む) では先頭の NUL も `EINVAL`
- **illumos では `@` で始まるパスを cwd の `@...` のファイルとして bind / connect する**。標準は先頭の `@` を abstract の名前に書き換えるが、illumos のカーネルには abstract socket が無い。sockat は `./` を付けて標準に渡すので、listener の `Addr()` は `./@...` を返し、`Spelling` は `CwdRelative`。listener の Close はファイルを消さない (他の OS の標準が `@` で始まるパスを消さないのとそろえる。消すなら `ListenEx` の `Remove`)。`./` を付けて上限を超える 106・107 バイトのパスは長いパスとして経路に回る

長いパスでだけ違う点:

- **listener の `Addr()` (と accept した接続の `LocalAddr()`、相手の `RemoteAddr()`) は渡したパスを返さず、bind の綴りを返す**。後で使えるかは `Spelling` で分かる。`ss -x`・`lsof`・`/proc/net/unix` 等に出るアドレスも綴りになる (link-file の綴りにはファイル名が出ない)
- **既定の `Listen` の listener を閉じた時に消える物が綴りで違う**。Go の `UnixListener.Close` は覚えている綴りを unlink する。link-dir の綴りは途中の E を辿ってソケットのファイルを消す (標準と同じ)。**link-file の綴りは E 自体が消え、ソケットのファイルは残る**。`Dangling` の綴りでは何も消さない (`SetUnlinkOnClose(false)` にしてある)。`ListenEx` の listener はどの綴りでも消さない
- **名前で終わらないパス** (`dir/`、`dir/.`、`dir/..`) は何も作らずに `EINVAL`
- **ディレクトリを先に引く**: link-dir と thread-cwd・at の E はソケットのディレクトリの realpath を指し、proc-direct・at-direct・thread-cwd-direct はディレクトリを引いてからファイル名で渡す。その間の rename や symlink の張り替えは反映されない
- **他の goroutine の `os.Chdir`**: relative (connect) の途中で cwd が変わったら、結果を捨ててエラーを返す。相対パスの `ListenEx` (短いパスも) は、realpath を引いてから bind するまでに cwd が変わったら、listener を閉じ、ファイルは消さずにエラーを返す
- **状態がアプリの置き場の外に増える**: B・D・E が `/tmp`・`$XDG_RUNTIME_DIR`・`$TMPDIR` (とソケットのディレクトリ) に残る (「[置き場に残る物と `Sweep`](#置き場に残る物と-sweep)」)
- **生きている D が消されうる環境**: systemd v242 より古い Linux と、B が macOS の `$TMPDIR` にある時。綴りが辿れなくなるだけ
- **`PrivateTmp` の相手**: 相手の `/tmp` が隔離されていると、`/tmp` を通る綴りを相手は辿れない。Linux では `/proc` を通す綴りを先に使うので、まず起きない。辿れるかは `Spelling.UsableWithPrivateTmp` で分かる

## ドキュメント

- [sockat の仕様](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) — 言語によらない振る舞い (公開する操作、resolve、綴りの性質、片付け、経路、エラー)
- [docs/decisions/](./docs/decisions/INDEX.md) — Go 版の設計の判断 (DR)
- [pkg.go.dev](https://pkg.go.dev/github.com/kawaz/sockat-go) — API

## ライセンス

MIT License, Yoshiaki Kawazu (@kawaz)
