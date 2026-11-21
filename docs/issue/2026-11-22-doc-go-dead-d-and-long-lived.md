---
title: doc.go に「死んだ D を自動で消さない」と「長命のプロセスは ListenEx で片付ける」を足す (次の版)
status: open
category: task
created: 2026-11-22
---

# doc.go に「死んだ D を自動で消さない」と「長命のプロセスは ListenEx で片付ける」を足す (次の版)

ハブの決定 (AS-Q1 = a、ハブ 125c4627) を README (日英) には v0.4.0 で入れた。package doc (doc.go) は、試験以外の .go を変えると just push の版の検査で 0.4.1 が要るので、次の版で入れる。

足す文 (README の「置き場に残る物と Sweep」と同じ趣旨):

- sockat は死んだ D を自分では消さない (resolve の時も、プロセスの初めと終わりにも消さない)
- 長いパスに何度も bind する長命のプロセス (セッションごとにソケットを作る daemon 等) は、既定の Listen の E がプロセスの終わりまで溜まる。ListenEx を使い、要らなくなったソケットごとに Remove (ファイルも消す) か Cleanup (E だけ) で片付ける

置き場所は doc.go の「B and D are not removed by sockat: ...」の段落の終わり。
