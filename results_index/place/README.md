# place：按读者分布的放置 + M3+ 保留（P20，已按用户 10-23 决定结束）

Claude Code，2026-10-22 / 23。用户 10-23："P20 就不要了吧，直接跑 P21"——服务器上已放 PAUSE，正在跑的跑完后用已有结果收尾，没有再排运行。结论见 `analysis.md`；离线上限见 `bound.md`。

## 1. 场景和代码

- 主配置：384 GB/s、W = 128、组合表 128：`-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=128`，T18 配置（M3+、v4m3 及叠在上面的）另加 `-t18-window=128 -t18-compose-table-entries=128`；baseline 原版 RDMA；所有运行保留 `-magic-memory-copy`。新基线 `c08698b`。
- 分支 `isca27/placement`（worktree `~/Desktop/CuPath/wt-place`）：
  - `20f323e`：`isca27/a1-retain` @ `1335def` + `isca27/page-class` @ `149dff0` + 两遍法放置 oracle（`-place-table`、`-place-random-seed`、pass 1 统计 `-pgcls-place-ext`），配置 `v4m3.json`、`m3p.json`（M3+ = v4m3 关掉打包、预取、合并）。
  - `810531a`：+ `isca27/page-migration` @ `15beab2` + P19 Q6 `-pm-page-list` + `-pm-target argmax|median|class` 及阈值开关、`-pm-count-local`、`-pm-count-network`；配置 `c4k`、`c4k_med`、`c4k_cls`、`c4k_clsstay`、`f4k`、`c2m` 及 `m3p_` 前缀版本。
  - 之后只加了配置 `v4m3_c4k_cls.json`（`53955c8`，没有跑）。
- 回归（全部逐位一致，`runs.csv` 之外的 `1022_diag_placereg-*`）：开关全关时 fir、MM（`-max-wg=4096`）× {mi200, v4m3}：`810531a` = `20f323e` = `1335def`；npbcg S v4m3：`20f323e` = `9e87ac5`（P19）；`-report-pgcls` / `-pgcls-place-ext` / 空放置表开关不改变结果；npbcg S × {c4k, f4k, v4_complete + c4k}：`810531a` 和 pm 的 `15beab2` 时间、指令、WG 完全相同，metrics 只多一行 `pm_class_filtered_accesses = 0`（cherry-pick 的 Q6 计数）。
- 正确性：两遍法 oracle 的随机放置（每个 1 KB 块哈希到一个 GPM）× {mi200, M3+, v4m3}：a1check、npbcg S、FW n256（加 `-magic-copy-coherent`）全部 `-verify` 通过；FW n256 不带放置表、不加 `-magic-copy-coherent` 也失败（读回 0），是 `fix_l2tlb/README.md` 记录的 magic 拷贝老问题。`-pm-target`：atax、bfs、npbcg、fft2d、lud、MG、sssp、tc 小规模 × {median, class, class + M3+, class 压力（阈值 16、冷却 0、不限次数）} 32 个 `-verify -pm-check` 全部通过。fir / MM 全规模随机放置自测 6 个（rc = -15）是我主动杀掉的（跑 3.4 小时只到 20–40%），不是失败。

## 2. 跑了什么、没跑什么

| 批 | 内容 | 状态 |
|---|---|---|
| 两遍法 oracle（`20f323e`） | 14 个 workload × {baseline + pass 1 统计, M3+, 全部本地, P-整页, P-子块, P-按类（v2）, P-按类 + M3+, P-按类 + v4m3}；P-按类-中心 × PR、tc、MM；P-med、P-med + M3+ × CFD、BH、embbag、sssp、IVF、FW、tc | **全部完成**（129 个，exit 0）；v4m3 参照复用 P19 的 `qt-w128c128b384` |
| `-pm-target` 主对比（`810531a`） | 16 个 workload（+ MG、atax）× {c4k, c4k-median, c4k-class, c4k-class-stay, f4k, c2m} × {baseline, M3+} + Q6 × 2 + 1 KB 交错 × 2 | **部分完成**：209 / 352 个（全部 exit 0）。**没跑（用户决定停止）**：MG 全部；IVF、BH 的全部迁移运行；MM 的 class / class-stay / f4k / c2m / Q6 / 交错；PR、atax、MT 个别格 |
| 阈值敏感性（M3+ 上：X = 0.25、读者上限 7 / 47、不减回副本命中的计数） | 16 × 5 | 只完成 KM、FW、PA（部分）；其余**没跑** |
| 最好一组 + v4m3 | 16 × `v4m3_c4k_cls` | **没跑** |

## 3. 文件

| 文件 | 内容 |
|---|---|
| `bound.md`、`bound.csv`、`bound.json`、`bound_med.csv` | 第 1 步离线上限（1 KB 子块、热点拆分、稳定性、搬运量、页大小、med 对照、热点集中规则） |
| `runs.csv`、`speedup.csv`、`tables.md` | 两遍法 oracle 的运行和加速比（`scripts/analyze.py`） |
| `runs_pm.csv`、`speedup_pm.csv`、`tables_pm.md` | `-pm-target` 和其他迁移机制（`scripts/analyze_pm.py`），含迁移次数、来回、pin、目标决策计数 |
| `scripts/` | 离线：`place_bound.py`、`place_stability.py`、`place_bound_agg.py`、`place_targets.py`（读者分组 + 108 组阈值扫描）；服务器：`server/{mktable.py, gen_tables.py, collect.py}`；汇总 `analyze.py`、`analyze_pm.py` |

- 服务器：yss2 / yss3 `~/akita-exp-test/place/`（`sched.py`、`queue.tsv`、`done.tsv`、`lists/` 放置表和 Q6 页表、`PAUSE`）。结果 `src/results/1022_{abl,diag}_place*`。
- 有效性：同配置比 WG 数和指令数（差 ≤ 1%），只有 IVF 全部本地无效（`*`，不进 GMEAN）。`-place-table` 运行的 `misrouted` 全部为 0。
