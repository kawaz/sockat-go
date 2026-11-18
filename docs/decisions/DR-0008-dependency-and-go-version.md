# DR-0008: 依存は golang.org/x/sys だけ、Go は支援中の 2 版

- Status: Accepted
- Date: 2026-11-18

## Decision

- 依存は `golang.org/x/sys` だけ (`O_PATH`、`O_SEARCH`、`unshare`、syscall 番号、sysctl、`Stat_t`)
- `go.mod` の go は x/sys が求める版 (1.26.0)
- CI は Go の支援中の 2 版 (oldstable / stable) で回す

## 理由

- `syscall` は凍結されていて、`O_PATH` 等の定数や syscall 番号が OS ごとに欠ける。x/sys は Go チームが保守し、利用も広い
- Go の支援期間は直近の 2 版

## 判断の出所

- 実装側の判断 2026-11-18
