---
title: ListenEx で bind は通ったのにファイルを覚えられない分岐が未試験
status: open
category: task
created: 2026-11-19
---

# ListenEx で bind は通ったのにファイルを覚えられない分岐が未試験

`ListenEx` は bind が通った直後にソケットのファイルの realpath と `(st_dev, st_ino)` を覚え、覚えられなければ listener を閉じ、ファイルは消さずにエラー (`errNotRecorded`) を返す (DR-0001)。このうち「bind は通ったのに、その直後の lstat が失敗する」分岐を試験で作れていない (bind の直後にファイルを消すか権限を変える割り込みが要る)。相対パスで cwd が変わった時の同じ扱いは `TestListenExWhoseCwdChangesBeforeTheBindRecordsNothing` が確かめている。

直すなら、`bindEx` (bind を呼ぶ変数の継ぎ目) で bind の後にファイルを消す差し替えを入れれば作れる見込み。
