# m1w：和 wafer 有关的 M1（RDMA 端）新机制

Claude Code，2026-10-22。分支 `isca27/m1-wafer`（worktree `~/Desktop/CuPath/wt-m1w`，基于新基线 `c08698b`）。设计见 `DESIGN.md`，分析和结论见 `analysis.md`。

## 一句话

在新基线、384 GB/s、W = 256、8 个 workload 上：
- 最好的组合是"路由拐点合并 hub（自适应）+ 窗口半满以上才 hold 100 ns"，对 v4_complete 的 GMEAN 是 **1.023**（ar 1.07、tc 1.05、bfs 1.04；最差 sssp 0.996）。
- 单独的拐点 hub 是 1.008（PR 1.06、bfs 1.045、tc 1.03；sssp 0.95）。
- 区域 hub（HMG 式）是 0.846。

W = 256 下窗口不是瓶颈，所以"省窗口位"的机制收益小；合并的空间只在角落热点的 PR、graphtc、MM 上。

## 文件

| 文件 | 内容 |
|---|---|
| `DESIGN.md` | 上限统计的定义、两个原型（合并 hub、按距离 hold）、一致性、硬件代价、和 HMG / Proxies / HDPAT 等相关工作的区别 |
| `analysis.md` | 上限统计和原型结果、原因分析、开发中修掉的 bug、局限 |
| `speedup.csv` | 每个 workload：baseline、v4complete 的时间，各变体对 v4complete 的加速比，重复运行之比 |
| `runs.csv` | 每个运行一行（exp_id、主机、二进制、配置、exit、total_time） |
| `bound_summary.csv` | 上限统计的关键列（baseline 和 v4complete，w256b384，`c26d97e`） |
| `bound_c26d97e.csv` | 上限统计的全部指标（`metrics_m1w_bound.csv` 的全部行） |
| `mech_stats.csv` | 原型的机制统计：hub 合并率、转发往返、自适应直连数、hold 的批数、请求事务数、窗口满的时间比例 |
| `verify.csv` | 回归、-verify、诊断运行（yss2 `~/akita-exp-test/m1w/verify/runs/`） |
| `raw/` | 拉回的 summary.csv、meta.json、metrics_m1w_bound.csv（metrics.csv 没有放这里，在服务器上） |
| `scripts/` | `pull.sh`（拉结果）、`mkqueue.py`（生成队列）、`speedup.py`、`bound_table.py` |

## 运行

- 服务器目录：`~/akita-exp-test/m1w/`（yss2、yss3）。
  - `m1w/`：旧二进制 `e714d47` 的上限统计，被 PAUSE 后停掉，只留了 PR、tc 的参考结果，**不引用**。
  - `m1w/v2/`：新基线的全部运行（sched.py 拷自 v3m2w；`src`、`bin` 是软链接；48 GB/s 的行按用户要求删除了，记录在 `DROPPED_48GBps.txt`）。
- 二进制：`c26d97e`（上限统计、v4complete、baseline、hold100、holdrtt25）、`97fdca5`（hub 修正后的 hubturn / hubq2 / hubturn + holdrtt25，外加两个对照）、`04115a1`（hubturna、hold100o、hubturna + hold100o，外加一个对照）。三个二进制下 v4complete 的时间完全一致（tc、PR）。
- 只用 exit 0 的 summary.csv。没有挂住的运行。

## 回归和正确性

- **开关关闭**：
  - fir、MM（mi200，默认网络，`-max-wg 4096`），`c26d97e` 和新基线 `c08698b` 逐位一致：fir 26.314 µs / md5 `571d7e7f620b`，MM 346.375 µs / md5 `81c2f67645b6`（metrics 去掉序号列后排序再算 md5）。
  - v4_complete 的 fir：26.191 µs / `3b87f6fe0bb1`，开不开 `-report-m1w-bound` 都一样。
  - rebase 前，`e714d47` 和 `4235991` 一致：26.568 µs / `d56a681494ae`，345.433 µs / `22a58b272d89`。
- **-verify**：bfs、sssp、npbcg、MM、graphtc 在所有原型配置下都通过（`verify.csv`）。
- **单元测试**：`mgpusim/timing/rdma/m1w_bound_test.go`（分区、区域合并、kernel 边界和写作废、拐点 / 网内合并、hold、hub 路由），`CGO_ENABLED=0 go test ./timing/rdma`。
