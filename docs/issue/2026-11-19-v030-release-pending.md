---
title: v0.3.0 (中心の経路、ListenEx / DialEx、Listen の symlink-file、適合試験の CLI) の release 待ちと残り
status: open
category: task
created: 2026-11-19
---

# v0.3.0 (中心の経路、ListenEx / DialEx、Listen の symlink-file、適合試験の CLI) の release 待ちと残り

main@origin は v0.2.0 の後の README の 1 commit まで。それより後の commit (DR-0011〜0015 の実装、ListenEx / DialEx、適合試験の CLI) はローカルの main にあり、push していない。VERSION は 0.2.0 のまま。

## 待っているもの (ハブの統括 → kawaz)

- release の OK。出す時は `just bump-version minor` (0.3.0) → `just push`

## 残り

- ListenEx で bind は通ったのにファイルを覚えられない分岐は試験で作れず未試験 (DR-0001)
- 相対パスの ListenEx で、realpath を引いてから bind するまでに他の goroutine が `os.Chdir` すると、覚えるファイル (元の cwd の下) と bind した所 (新しい cwd の下) が食い違う。元の cwd の下に同じ名前の別のファイルがあれば、その (st_dev, st_ino) を覚え、`discard()` がそれを消す (_ex の変更のレビューから、コードを読んで確定、実行では未再現)。他の言語にも同じ形がありうるので、直し方 (bind の前後で cwd の (st_dev, st_ino) を比べ、変わっていたら覚えられなかったとして扱う等) をハブの統括に問い合わせ中 (2026-11-19)
