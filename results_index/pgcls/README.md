# pgcls：按页给远程访问分类（数据、代码、运行）

Claude Code，2026-10-22。结论和表格的解读在 `analysis.md`。

## 1. 这批运行同时是新基线的正式结果

### 表 1：新基线（`c08698b` / `149dff0`）384 GB/s、W = 256 下的正式结果

| workload | baseline (µs) | v4_complete (µs) | v4 / baseline 加速 | 全部本地 / baseline | 全部本地 / v4 |
|---|---|---|---|---|---|
| pr-g24 | 866.7 | 122.5 | 7.076 | 39.417 | 5.571 |
| npbcg-W | 2745.6 | 952.1 | 2.884 | 4.413 | 1.530 |
| tc-s12e16 | 197.8 | 94.3 | 2.098 | 2.521 | 1.202 |
| mm-g12 | 251.8 | 157.0 | 1.603 | 1.731 | 1.080 |
| atax-2688x8192-r64 | 449.5 | 297.7 | 1.510 | 1.860 | 1.253 |
| fw-n2048-p4 | 221.5 | 150.8 | 1.469 | 4.347 | 2.958 |
| lud-n1024 | 2884.8 | 2834.1 | 1.018 | 1.344 | 1.320 |
| npbis-n1m-i1 | 392.8 | 344.1 | 1.142 | — | — |
| cfd-c172k-b128 | 151.2 | 115.7 | 1.306 | 2.502 | 1.930 |
| mg-W | 815.2 | 662.8 | 1.230 | 2.227 | 1.810 |
| moe-t64 | 1434.4 | 1287.2 | 1.114 | 1.700 | 1.525 |
| pa-b14s7p32 | 280.9 | 256.3 | 1.096 | 1.329 | 1.209 |
| shocsort-n688k | 414.4 | 381.9 | 1.085 | 1.826 | 1.682 |
| sssp-s18e4-r1 | 416.0 | 287.9 | 1.445 | 2.361 | 1.634 |
| bfs-s18e2 | 710.8 | 724.0 | 0.982 | 6.791* | 6.917* |
| ivf-n8k-q328 | 230.8 | 191.9 | 1.203 | 1.846* | 1.527* |
| ar-pc1 | 173.0 | 165.8 | 1.043 | 1.965 | 1.884 |
| bh-n86k | 429.4 | 374.6 | 1.146 | 1.706 | 1.488 |
| embbag-b512 | 488.1 | 489.7 | 0.997 | 1.001 | 1.004 |
| gups-L18m8 | 197.1 | 192.4 | 1.024 | 1.743 | 1.702 |
| fft2d-n1024 | 483.6 | 441.1 | 1.097 | 1.429 | 1.303 |
| i2c-g64 | 54.6 | 49.6 | 1.100 | 1.212 | 1.102 |
| km-g1 | 400.5 | 401.8 | 0.997 | 2.465 | 2.509 |
| mt-g4 | 1824.7 | 1826.2 | 0.999 | 1.002 | 1.002 |
| spmv-g32 | 1169.5 | 1170.0 | 1.000 | 0.997 | 0.997 |
| **GMEAN** | | | **1.315**（25） | 2.091（22 个有效） | 1.555（22 个有效） |

- 全部本地 = 同配置加 `-force-local-data-access`（T15 的 O1）。`*` = 指令数和不加时差 > 1%（bfs −8.3%、ivf +1.6%），不进 GMEAN；npbis 的全部本地运行崩溃（被写数据读到旧值），没有结果。
- bfs 自身有运行间不确定性（pm/ 测到 ±5–10%），bfs 的 0.982 按噪声读。

- 二进制 `149dff0`（分支 `isca27/page-class`，= 新基线 `c08698b` 合并进来（`isca27/fix-l2tlb-wakeup`：L2TLB 丢唤醒、IOMMU 拓扑、DrainCommandQueue、bfs 先上传再 enqueue）+ 只读统计 `-report-pgcls` + 按页 oracle 开关）。
- 主配置：384 GB/s、W = 256：`-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256`；v4_complete 另加 `-t18-window=256 -t18-compose-table-entries=512`。不跑 48 GB/s。
- 25 个 workload × {baseline（`configs/mi200.json`）, v4_complete（`configs/v4_complete.json`）} = 50 个运行，参数照 `~/akita-exp-test/v3c384/queue.tsv` 的 mi200 行，统计开关只有 `-report-pgcls -pgcls-sample 16`。
- **逐位一致校验（yss2 `~/akita-exp-test/pgcls/verify2/`，npbcg class S）**：`metrics.csv` 按行排序后的 md5（原始文件只有输出顺序不同，是已有的 map 遍历顺序）：

  | 配置 | `c08698b` 原版 | `149dff0` 不开 | `149dff0` 开 `-report-pgcls` | total_time |
  |---|---|---|---|---|
  | baseline | 8bffefbb720c | 8bffefbb720c | 8bffefbb720c | 669.364 µs |
  | v4_complete | 87dd0b3a1552 | 87dd0b3a1552 | 87dd0b3a1552 | 517.305 µs |
  | baseline + 全部页 oracle / `-force-local-data-access` | — | 803f576c05c7 / 803f576c05c7 | — | 466.614 µs |

  更早在 `7e499e5` 上：npbcg S、kmeans（`-t16-wg-per-cu 1`）开 / 关也逐位一致（`verify/`）。所以这 50 个运行可以直接当新基线的 baseline / v4_complete 结果用。
- 另：pgcls 记到的远程读 / 写数和 RDMA 的 `rdma_observed_remote_reads/writes` 完全相等；baseline 下每页的网络 line 数等于远程读数。
- 服务器：yss2 / yss3 的 `~/akita-exp-test/pgcls/`（`sched.py` 复制自 `v3m2w`，30 GB 余量；`queue.tsv`、`commands.tsv`、`done.tsv`）；结果在 `src/results/1022_diag_pgcls-w256b384-<item>[-or-<类>]_<config>_149dff0/`。yss3 编译用 go 1.22.2（`meta.json` 里的 go_version 是 PATH 里的 1.18，不是实际编译器），yss2 用 go 1.27.3。
- `isca27_config.py` 还不认识 `-report-pgcls` / `-pgcls-oracle-pages`，`meta.json` 的 config_deviation 把它们标成"benchmark 参数"；它们分别是统计开关和 oracle。

## 2. 代码（`isca27/page-class`，默认全关）

| 提交 | 内容 |
|---|---|
| `9edf1b6` 及之前 | `-report-pgcls`：远程访问在请求方 RDMA 第一次看到 L1 请求时记（和 obs 的 `ObsAdmit` 同一点，owner 精确）；本地访问在 L1V 把 miss / 写发给本地 L2 时记。按 4 KB 物理页精确聚合（不采样）：每个读者的读数（按 512 B 子块分 8 份）、读过的 line 位图、同 GPM 在本 kernel 内 / 跨 kernel 重读同一 line（被任何 GPM 写过就清掉）、写者、"读到别的 GPM 写的 line"（同一 kernel / 更早 kernel 分开记）、每个全局 kernel 和每个 10 µs 窗口的读者集合（平均读者数、相邻集合的 Jaccard）。kernel 号来自 CP 的 KernelLaunchHooks（只在打开时安装）；全局 kernel：一个 GPM 在当前全局 kernel 里第二次启动 kernel 时加 1。 |
| `149dff0` | 每页的网络读 line 数（含预取）和 line×跳、远程读延迟总量（RDMA 收到 L1 请求 → 回复 L1）；`-pgcls-oracle-pages <文件>`：列出的页的 L1V 访问改走请求方自己的 L2 / DRAM（按页的 `-force-local-data-access`，T15 的 O1 用的就是这个路由）。 |

- 输出：`<metric-file-name>_pgcls_{pages.csv.gz, readers.csv.gz, kernels.csv, totals.csv}`；`-pgcls-sample N` 另外输出按页哈希采样（1/N 的页、这些页的全部访问）的原始 trace `_trace.csv.gz`。
- **采样只影响原始 trace，不影响分类**：分类和上限全部用精确聚合。`sampling_error.csv` 给出"只用 1/16 采样页"估计各类占比的误差，供以后用原始 trace 做时间相关分析时参考。
- oracle 的局限：数据靠全局存储保持正确，但被多个 GPM 写的页在 oracle 下可能在别的 GPM 的 L2 里留旧副本（这是 oracle，不是协议）。每个 oracle 运行都和不加 oracle 的运行比较指令数和 WG 数（`class_traffic.csv` 的 `oracle_valid`）。
- 单元测试 `akita/mem/trace/pgcls_test.go`。

## 3. 分析脚本（`scripts/`）

| 文件 | 作用 |
|---|---|
| `pgcls_page.py` | 一个 baseline 运行：每页特征、分类、各做法之后剩下的远程读 / 读×跳 → `<item>.json`、`<item>_pages.csv.gz` |
| `pgcls_v4.py` | 一个 v4_complete 运行，按 baseline 的页类别汇总：逻辑远程读、网络 line、line×跳、延迟 → `<item>_v4.json` |
| `pgcls_oracle_lists.py` | 每个 workload 占远程读 ≥ 5% 的类 → oracle 页表 |
| `pgcls_aggregate.py` | 汇总成本目录的 csv |
| `server/` | `sched.py`、`mkqueue.py`（主运行）、`mkoracle.py`（oracle 运行）、`analyze_srv.sh`（在服务器上跑上面三个脚本） |

## 4. 输出文件

| 文件 | 内容 |
|---|---|
| `class_mix.csv` | 每个 workload 各类占远程读 / 读×跳的比例，是否混合 |
| `class_features.csv` | 每个 workload × 类：读者数、主读者占比、读者两两跳数、10 µs 内并发读者数、窗口间 Jaccard（固定伙伴）、被写比例、生产-消费比例、同 GPM 重读比例、子块读者不同的比例 |
| `class_traffic.csv` | 每个 workload × 类：远程读 / 读×跳 / 延迟占比，v4 之后剩下的比例，最佳做法之后剩下的比例，oracle 加速 |
| `bounds.csv` | 每个 workload：各做法（保留、放置、复制、子页放置；按类选、每页 oracle、每个 workload 只用一种）之后剩下的远程读 / 读×跳 |
| `a3_subpage.csv` | 子页放置（A3）的潜力 |
| `oracle.csv` | 每个 workload：baseline / v4 时间，全部本地的上限，每类 oracle 的加速 |
| `class_summary.csv` | 总表：类 × {流量占比, v4 已吃掉, 最佳做法上限, oracle 加速} |
| `sampling_error.csv` | 1/16 页采样的误差 |
| `runs.tsv` | 两台服务器全部运行：exp、exit、total_time、指令数、WG 数（含 oracle 和作废的 `-or-all-or-all`） |
