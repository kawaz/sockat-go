# DR-0003: 長いパスの経路と試す順番

- Status: Accepted
- Date: 2026-11-18

## Context

[ハブの SPEC](https://github.com/kawaz/sockat/blob/main/docs/SPEC-ja.md) は OS ごとの経路と試す順番を決め、その言語で作れない経路は飛ばして README に明示することを求める。Go では fork が使えず ([DR-0004](DR-0004-thread-cwd-route.md))、macOS の広げた sockaddr は connect にしか使えない ([DR-0005](DR-0005-widened-sockaddr-only-for-dial.md))。

## Decision

| OS | Listen | Dial |
|---|---|---|
| Linux / Android (`/proc` を使える) | /proc | /proc |
| Linux / Android (`/proc` を使えない) | 相対パス → symlink → スレッド cwd | 同左 |
| FreeBSD | bindat | connectat |
| macOS | 相対パス → symlink → スレッド cwd | 相対パス → 広げた sockaddr → symlink → スレッド cwd |
| NetBSD / OpenBSD / illumos / その他 | 相対パス → symlink | 相対パス → symlink |

- 各経路のやり方・使える条件・相対パスの作り方・symlink の置き場と名前と安全条件と片付けは SPEC のまま
- 経路が実際に bind / connect して失敗したら、そのエラーを返し、次の経路を試さない。次へ進むのは経路自体が使えない時だけ (SPEC)
- symlink の名前の乱数は `crypto/rand` (OS の安全な乱数)
- 最後の経路まで使えなければ、ディレクトリを開いてから `ENAMETOOLONG` (ディレクトリが無ければ `ENOENT`)

## 理由

- SPEC の順番に従い、作れない経路だけを飛ばす
- `crypto/rand` は Linux で `getrandom(2)`、macOS / BSD で `arc4random` 系を使う。SPEC の「OS の安全な乱数」をそのまま満たす

## 判断の出所

- 順番: SPEC (kawaz 裁定)。Go で作れない経路の扱い: 実装側の判断 2026-11-18
