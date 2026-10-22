# geo：地理广播响应（geo-push）和按亲和性推送（affinity-push）

Claude Code，2026-10-21 / 22。设计见 `DESIGN.md`，分析和结论见 `analysis.md`。

## 代码

- worktree `~/Desktop/CuPath/wt-geo`，分支 `isca27/geo-push`（从 `isca27/cupath-v3` @ `4235991` 开出，已推送，没有用 force）。

| commit | 内容 |
|---|---|
| `8a30511` | 上限统计 `-report-geo-push-bound`（只读） |
| `6b44a5a` | geo-push 原型：`-geo-push-radius 0/1/2`、`-geo-push-filter none/shared`；配置 `v3_complete_geo1/geo1s/geo2` |
| `438698f` | cherry-pick `ad0dfe3`（`v4_complete.json`） |
| `a41340d` | `v4_complete_geo1/geo1s/geo2`（extends `v4_complete.json`） |
| `3b85824` | 共读矩阵 `-report-coread-matrix`（只读） |
| `106dc0d` | affinity-push：`-geo-push-mode neighbor/affinity`、`-affinity-k`、`-affinity-decay-hints`、`-affinity-min-count`；配置 `v4_complete_aff1/aff2/aff2s` |

- 所有新开关默认关。
- `6b44a5a`、`a41340d`、`106dc0d` 的 linux 二进制在新开关关闭时与 `4235991` 一致：
  - fir（mi200，768 GB/s 默认网络，`-max-wg 4096`）：26.568 µs，md5 `d56a6814`；
  - MM：345.433 µs，md5 `22a58b27`。
  - md5 是对 `cut -d, -f2- metrics.csv | sort` 算的：metrics.csv 的行顺序本来就不固定，旧的 `d56a6814` 也是这样算的。
  - 本地 bfs s12、sssp s12 r4 的 `v3_complete` metrics 也逐位一致。`106dc0d` 跑 `v3_complete_geo1` 时，和 `6b44a5a` 逐位一致。
- `a41340d` 和 `6b44a5a` 编出的二进制 md5 相同（`5286f73a`），代码没变，只多了配置。
- 单元测试：
  - `mgpusim/timing/rdma/geo_bound_test.go`：拓扑、多播树、上限记账、多播拆分、共享表、hint、亲和学习和衰减、共读；
  - `akita/mem/cache/writeback/geo_push_fill_test.go`：推送回填、过期、不挤掉有效 line、计数。
  - rdma、runner、writeback、noc 的测试全部通过。`akita/mem/vm/tlb` 有一个用例在 `4235991` 上本来就失败；`akkalat/test_isca27_configs.py` 的 `test_complete_is_complete_new_at_w64` 也本来就失败。
- `-verify`（小规模，384 GB/s）全部通过：

| 规模 | 配置 |
|---|---|
| bfs s12 | v3 geo1、geo1s、geo2；v4 geo1、geo1s、geo2；aff1、aff2、aff2s |
| sssp s12 r4 | v3 geo1、geo2；v4 geo1、geo2；aff1、aff2s |
| npbcg class S | v3 geo1、geo2；aff1、aff2s |
| graphtc（默认） | v3 geo1、geo2；aff1、aff2s |
| MM `matrixmultiplication-pipeline-smoke` | v3 geo1、geo2；v4 geo1、geo2；aff1、aff2s |

  MM 要加 `-magic-copy-coherent`；不加的话连 mi200 baseline 都不过，是原有问题。

## 服务器

- 目录：yss2 / yss3 的 `~/akita-exp-test/geo/`。
  - `src/` 是 wt-geo 的副本（不含 .git）；`bin/baseline_<commit>`；
  - `sched.py` 复制自 v3m2w，带看门狗和 30 GB 内存余量，commit 取 exp_id 的最后一段；
  - `queue.tsv`、`launched.tsv`、`done.tsv`、`sched.log`；`verify/` 是回归运行。
- 命名：
  - 上限统计：`1021_diag_geob-w64b384-<wl>_{mi200,mi200+v3complete}_8a30511`；
  - geo-push：`1021_abl_geo-<场景>-<wl>_mi200+{v4complete,v4geo1,v4geo1s,v4geo2,v4complete-rep2}_a41340d`；
  - 共读矩阵：`1022_diag_coread-w64b384-<wl>_mi200+v4complete_3b85824`；
  - affinity：`1022_abl_geo-{w256b384,w256b48m}-<wl>_mi200+{v4aff1,v4aff2,v4aff2s,v4geo1s}_106dc0d`。
- 场景：
  - **w64b384**：`-bandwidth=24 -switch-buffer-flits=16`，W = 64，表 128；
  - **w256b48m**（网络紧张）：`-bandwidth=3 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512`；
  - **w256b384**（用户 10-22 定的新主配置）：`-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512`。
- 规模：照 v3c384 的 mi200 行（`scripts/wl.py`）。
- 统计开关：`-report-cupath-v3-stats -report-geo-push-bound -report-obs-links`（共读运行用 `-report-coread-matrix`）。
- 复用的结果：
  - w64b384 的 baseline：v3c384 的 `1021_abl_v3c-<wl>_mi200_4235991`；
  - w256b48m 的 baseline：v3m2w 的 `1021_sens_m2w-w256b48m-<wl>_mi200_4235991`；
  - v4complete（geo 关）没有复用，是用 `a41340d` 新跑的。
  - 复用的前提是二进制在新开关关闭时与 `4235991` 一致（见上）。
- 只用 exit 0 的运行。
  - 上限统计 12 个、geo-push 74 个、共读 8 个，全部 exit 0，没有挂住（`hangs.tsv` 为空）。
  - affinity：用户 10-22 在 geo 目录放了 PAUSE（新基线要修 L2TLB/IOMMU 等问题），PAUSE 之前启动的 25 个跑完后只作参考，其余 31 个没有启动。

## 文件

| 文件 | 内容 |
|---|---|
| `DESIGN.md` | 机制设计：触发、多播树、邻居回填、和在途请求的交互、一致性、亲和学习 |
| `analysis.md` | 上限统计、实验结果、结论 |
| `bound.csv` | 上限统计，每个 workload × {baseline, Complete} 一行（`geo_bound_*` 的全部字段） |
| `geo.csv` | 实验结果，每个运行一行：用时、相对 baseline 和对照的加速比、推送统计、网络 line、mesh 字节、热点链路利用率 |
| `coread/<wl>.csv` | 48×48 共读矩阵（窗口 1 µs / 10 µs；`gpm_j = -1` 的行是该 GPM 的网络读数） |
| `coread/coread_summary.csv` | 共读汇总：top-1 / top-2 份额、伙伴类型、跳数 |
| `scripts/` | `sched.py`、`wl.py`（规模和场景）、`ext_geo.py`（服务器上抽取）、`collect.py`、`analyze.py`、`bound_table.py`、`coread.py`、`lrun.sh`（本地运行） |

没有引用 W = 1024 的旧结果。没有改 STATUS / SYNC。
