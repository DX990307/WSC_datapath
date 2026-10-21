# P13 C2 / C3 / C4 准备（2026-10-20，Claude Code）

**本文件只是准备和配置确认，R4 / R5 / R6 / R7 都没有运行（⏸ 等用户确认）。** 服务器上只跑了 mesh flag 的回归、几个冒烟检查和 L2 延迟参数的校准（`~/akita-exp-test/meshprep/`，yss2）。

代码：worktree `wt-mesh-size`，分支 `isca27/mesh-size`（从 `d09b18b` 开出），未推送。

| commit | 内容 |
|---|---|
| `4ad26b2` | `-mesh-size`（N×N，默认 7，CPU/IOMMU 在中心 tile） |
| `766d8e8` | `isca27_config.py`：`-mesh-size` 记为配置偏离（不再算 benchmark 参数） |
| `228ff31` | CACTI：`akkalat/cost/run_isca27_rrt_cacti.py` + 原始输出 `akkalat/cost/cacti_isca27/` |
| `11e804b` | `-fw-nodes`、`-mt-width`、`-pr-nodes`、`-pr-connections`（默认值 = 原来写死的大小） |
| `3211be7` | `-l2-bank-latency`（默认 10 = 原来每个 L2 的值），R7 用；记为配置偏离 |

**回归（yss2，`~/akita-exp-test/meshprep/`，mi200，`-max-wg 4096`，全部退出码 0）**：md5 = `stats/metrics.csv` 去掉第 1 列（行号）后排序的 md5，和 STATUS 里的口径一样。

| 运行 | total_time | 行数 | md5 | 墙钟 / 峰值内存 |
|---|---|---|---|---|
| fir，`d09b18b`（参照，w2c 的回归） | 26.568 µs | 164,567 | `d56a681494ae` | — |
| fir，`4ad26b2` | 26.568 µs | 164,567 | `d56a681494ae` | 11.9 min / 6.1 GB |
| fir，`3211be7`（分支头的 Go 代码） | 26.568 µs | 164,567 | `d56a681494ae` | 13.8 min / 6.1 GB |
| MM，`d09b18b`（参照） | 345.433 µs | 169,867 | `22a58b272d89` | — |
| MM，`4ad26b2` | 345.433 µs | 169,867 | `22a58b272d89` | 44.2 min / 7.1 GB |
| MM，`3211be7` | 345.433 µs | 169,867 | `22a58b272d89` | 45.3 min / 7.1 GB |

`766d8e8`、`228ff31` 只改 Python 和 CACTI 文件，`11e804b` 的 Go 改动包含在 `3211be7` 里，所以 `3211be7` 的回归覆盖了全部 Go 改动。另外起过一组 `11e804b` 的 fir / MM，为了保持每台最多 8 个并行，按记录的 PID 停掉了（被 `3211be7` 那组取代，退出码 143，不算结果）。
按 CONFIG.md 第 1 节，`runner.go`、`timingplatform.go`、`r9nanobuilder.go`、`flag.go` 的 blob 变了（只加了 flag 和接线，mi200 / hpca 的参数和默认值都没有改），回归如上逐位一致。

---

## C2：mesh 尺寸

### 7×7 是不是写死的：是（部分写死）

- `timingplatform.go` 的 builder 本来就有 `tileWidth / tileHeight`（默认 7），建 GPU 时按行跳过中心 tile，CPU（driver）和 IOMMU 挂在 `(W/2, H/2)`。但是没有任何 flag 能改它。
- 下面这些地方另外写死了 48 或 7，只改 builder 不够：
  - `runner.go`：`createUnifiedGPUs` 固定把 GPU 1..48 合成一个 unified GPU；`buildEmuPlatform` 固定建 48 个。
  - `gpmtracer.go`（T16）、`p12tracer.go`（P12 共享统计）：mesh 宽 7、中心 (3,3) 是常量。
  - `-l2-source-tile-width`（默认 7）：L2 来源统计、memory-path、observation 统计用它算跳数。
  - platbench（T14 微基准）：`TileOf`、`Hops`、`gpuAt`、snake 顺序都写死 7×7、中心 tile 24。
  - benchmark 包装：T17 PagedAttention / MoE 的 `NumGPMs = 48`（只用于打印预期远程比例）、allreduce `Ranks: 48`、T16 small 的 `t16NumGPM = 48`。
- 和 GPM 个数无关、不用改的：driver 的页分配（按 unified GPU 里的 GPU 个数等分）、WG 分配、全局内存（`(1+numGPU) × 8 GB`）、T18 的拓扑（从 builder 的 tile 循环建）、T18 的 `t18MaxHops = 13` 和 owner 扇出直方图 `[49]`（小 mesh 用不满，不越界）、`page_sharing_trace`（已用 builder 的宽高）。

### 改了什么（`4ad26b2`）

- 新 flag `-mesh-size N`：N 必须是奇数且 ≥ 3，GPM 数 = N² − 1，CPU/IOMMU 在中心 tile，GPM 仍按行创建、跳过中心。可以写在命令行或 `-config` 文件里。
- 上面列出的写死处全部改为跟随 `MeshSize()` / `NumGPMs()`。
- `-l2-source-tile-width`：7×7 时照旧用 flag 的值；其他尺寸时，flag 是默认值 7 就自动跟随 mesh，显式给了别的值且和 mesh 不一致就报错。**3×3 / 5×5 的命令不要写 `-l2-source-tile-width=7`**（会报错），写 `=3` / `=5` 或者不写。
- platbench 加了单元测试：3×3 / 5×5 / 7×7 上 GPU ID ↔ tile ↔ 坐标是一一对应的，中心 tile 没有 GPU，7×7 的映射和原来一样（`TileOf(24)=23`、`Hops(1,48)=12`）。
- `isca27_config.py`（`766d8e8`）：`-mesh-size` 和 `-l2-bank-latency` 在 meta.json 的 `config_deviation` 里记为“有意偏离，需要用户确认”。
- 2×2（4 个 GPM 的 MCM，CPU 不在 mesh 上）没有做：现在的布局要求 CPU 占一个 tile，2×2 的话 CPU 要挂在 mesh 外面，改动比较大。需要的话再说。

### 冒烟检查（只验证 flag 能用，不是 R5）

| 运行 | 结果 |
|---|---|
| fir，`-mesh-size=3 -max-wg 683` | 退出码 0；`Active GPUs 8`；统计里只有 GPU[0..3, 5..8]（中心 tile 4 没有 GPU）；25.94 µs；墙钟 1 分钟，2.9 GB |
| fir，`-mesh-size=5 -max-wg 2048` | 退出码 0；墙钟 4.8 分钟，4.3 GB |
| floydwarshall，`-mesh-size=3 -fw-nodes 4736 -max-wg 256` | 退出码 0；总 WG 350,464 = (4736/8)² |
| PagedAttention，`-mesh-size=3 -pa-batch=11 -max-wg 300` | 退出码 0；`Active GPUs 8`；pa_partial 1,664 个 WG（每 CU 7.4 个）；7.3 分钟，1.0 GB |
| R-GAT，`-mesh-size=3 -rgat-seeds=53 -max-wg 300` | 退出码 0；`Active GPUs 8`；墙钟 3.7 分钟，1.0 GB |
| pagerank，`-mesh-size=5 -pr-nodes 524288 -pr-connections 66322432 -max-wg 256` | 退出码 0；309.1 µs；墙钟 14.2 分钟，7.4 GB |

### R5 规模实验的配置确认（⏸ 只写、不跑）

**弱缩放规则**：每个 GPM 的工作量和数据量不变。
- 数据量和 GPM 数 G 成正比：方阵类（FW 的邻接矩阵、MT 的矩阵）边长 ∝ √G；PR 的节点数、边数 ∝ G（平均度 126.5 不变）；R-GAT 种子数 ∝ G；PagedAttention batch ∝ G。
- `-max-wg` ∝ G（32768 × G / 48）。
- 页放置不变：driver 仍按 GPM 数等分每个 buffer。
- 其他参数全部是 `mi200.json`，只多一个 `-mesh-size`。

| workload | 7×7（G=48，= T18 评估 / R1） | 5×5（G=24） | 3×3（G=8） | 说明 |
|---|---|---|---|---|
| floydwarshall | `-fw-nodes 11584 -max-wg 32768` | `-fw-nodes 8192 -max-wg 16384` | `-fw-nodes 4736 -max-wg 5461` | 数据比 0.500 / 0.167；截断运行，截断点都是第 1 个 pass 的 1.6% |
| pagerank | `-pr-nodes 1048576 -pr-connections 132644864 -max-wg 32768` | `-pr-nodes 524288 -pr-connections 66322432 -max-wg 16384` | `-pr-nodes 174784 -pr-connections 22110176 -max-wg 5461` | 节点数取 64 的倍数 |
| matrixtranspose | `-mt-width 11520 -max-wg 32768` | `-mt-width 8128 -max-wg 16384` | `-mt-width 4672 -max-wg 5461` | 宽度取 64 的倍数；总 WG 32,400 / 16,129 / 5,329，都小于 `-max-wg`，三个尺寸都完整跑完 |
| R-GAT | `-rgat-seeds=320` | `-rgat-seeds=160` | `-rgat-seeds=53` | 数据集（IGBH tiny）是固定的，分配到的总量不变，所以每个 GPM 上分到的数据集反而变多；**实际读到的数据**和种子数成正比。完整运行 |
| PagedAttention | `-pa-batch=66` | `-pa-batch=33` | `-pa-batch=11` | KV 池随 batch 缩放；序列长度按同一个 seed 从 Azure trace 取，batch 变小时长度分布会有变化。完整运行 |

R-GAT 的完整参数：`-benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=S -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1`（数据集只在 yss2 上）。

**注意：上面的 7×7 用的是 T18 评估的规模。** 用户 10-20 要求每个实验 6–8 小时、footprint 尽量大。如果 R1 换了新规模，R5 的 7×7 用新规模，5×5 / 3×3 按同样的规则重新算：边长 × √(G/48)，节点数、种子数、batch、`-max-wg` × G/48。

**配置**（每个尺寸 × 每个 workload）：
- `mi200`（baseline）
- `t18_full,t18_w1024`（T18 全开 W=1024，即 R3 的配置；W ≥ 1024 时加不加 `t18_dist` 结果逐位相同）
- 可选：`t18_w4096`（只放大跟踪表，R2 的配置）

**统计开关**：`-report-l2-source -l2-source-tile-width=N`，再加 P13 A 的统计（E1 分段、占用、链路；在 `isca27/obs-stats` 合进来之后定名，下面记作 `$OBS`）。R5 的二进制应当是 `isca27/obs-stats` 和 `isca27/mesh-size` 合并后的那个 commit（下面记作 `<C>`；现在 mesh-size 的头是 `3211be7`）。

**完整命令**（在服务器的源码目录下，`ISCA27_BINDIR=<bin 目录> akkalat/isca27_run.sh <exp_id> <C> P13 <configs> <参数>`；每一行前面都省略了 `akkalat/isca27_run.sh`）：

```
# ---- 3x3 (G=8) ----
1021_abl_floydwarshall-m3_mi200+mesh3_<C>              <C> P13 mi200                -benchmark floydwarshall -fw-nodes 4736 -max-wg 5461 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_floydwarshall-m3_mi200+mesh3+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark floydwarshall -fw-nodes 4736 -max-wg 5461 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_pagerank-m3_mi200+mesh3_<C>                    <C> P13 mi200                -benchmark pagerank -pr-nodes 174784 -pr-connections 22110176 -max-wg 5461 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_pagerank-m3_mi200+mesh3+t18full+w1024_<C>      <C> P13 t18_full,t18_w1024 -benchmark pagerank -pr-nodes 174784 -pr-connections 22110176 -max-wg 5461 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_matrixtranspose-m3_mi200+mesh3_<C>             <C> P13 mi200                -benchmark matrixtranspose -mt-width 4672 -max-wg 5461 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_matrixtranspose-m3_mi200+mesh3+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark matrixtranspose -mt-width 4672 -max-wg 5461 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_t17-rgat-s53-m3_mi200+mesh3_<C>                <C> P13 mi200                -benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=53 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_t17-rgat-s53-m3_mi200+mesh3+t18full+w1024_<C>  <C> P13 t18_full,t18_w1024 -benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=53 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_t17-pagedattn-b11-m3_mi200+mesh3_<C>           <C> P13 mi200                -benchmark pagedattn -pa-batch=11 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
1021_abl_t17-pagedattn-b11-m3_mi200+mesh3+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark pagedattn -pa-batch=11 -mesh-size=3 -report-l2-source -l2-source-tile-width=3 $OBS
# ---- 5x5 (G=24) ----
1021_abl_floydwarshall-m5_mi200+mesh5_<C>               <C> P13 mi200                -benchmark floydwarshall -fw-nodes 8192 -max-wg 16384 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_floydwarshall-m5_mi200+mesh5+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark floydwarshall -fw-nodes 8192 -max-wg 16384 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_pagerank-m5_mi200+mesh5_<C>                    <C> P13 mi200                -benchmark pagerank -pr-nodes 524288 -pr-connections 66322432 -max-wg 16384 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_pagerank-m5_mi200+mesh5+t18full+w1024_<C>      <C> P13 t18_full,t18_w1024 -benchmark pagerank -pr-nodes 524288 -pr-connections 66322432 -max-wg 16384 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_matrixtranspose-m5_mi200+mesh5_<C>             <C> P13 mi200                -benchmark matrixtranspose -mt-width 8128 -max-wg 16384 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_matrixtranspose-m5_mi200+mesh5+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark matrixtranspose -mt-width 8128 -max-wg 16384 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_t17-rgat-s160-m5_mi200+mesh5_<C>               <C> P13 mi200                -benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=160 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_t17-rgat-s160-m5_mi200+mesh5+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=160 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_t17-pagedattn-b33-m5_mi200+mesh5_<C>           <C> P13 mi200                -benchmark pagedattn -pa-batch=33 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
1021_abl_t17-pagedattn-b33-m5_mi200+mesh5+t18full+w1024_<C> <C> P13 t18_full,t18_w1024 -benchmark pagedattn -pa-batch=33 -mesh-size=5 -report-l2-source -l2-source-tile-width=5 $OBS
# ---- 7x7 (G=48): 复用 R1（mi200）和 R3（t18_full,t18_w1024），参数见上表 7×7 一列 ----
# 可选“只放大跟踪表”：把上面每个 baseline 行的 configs 换成 t18_w4096、exp_id 的配置段换成 mi200+mesh{3,5}+w4096
```

**和 `mi200.json` 的差异**（`isca27_config.py --deviation` 的输出口径）：
- 配置偏离（要确认）：`-mesh-size 3` 或 `-mesh-size 5`。
- 机制配置文件：`t18_full`（`-t18-req-merge -t18-owner-merge -t18-owner-multicast`）+ `t18_w1024`（`-t18-window=1024`）；T18 已确认过的配置。
- 统计开关：`-report-l2-source -l2-source-tile-width=N`、`$OBS`。
- benchmark 参数：`-benchmark`、`-fw-nodes`、`-pr-nodes`、`-pr-connections`、`-mt-width`、`-max-wg`、`-rgat-*`、`-pa-batch`。
- 不变：`-magic-memory-copy`（来自 `hpca.json`）；没有 `-verify`。

**运行数、时间、内存**：
- 20 个（3×3、5×5 各 5 workload × 2 配置）；可选“只放大跟踪表”再加 10 个；7×7 复用 R1、R3（如果 R1 用的规模或二进制不同，就再加 10 个）。
- 墙钟按 GPM 数线性估计（每个 GPM 工作量不变，模拟器的事件数大约和 GPM 数成正比），7×7 baseline 的参考值：FW 0.12 h（T15，截断）、PR 1.5 h（T15）、MT 4.2 h（T18 评估，带 L2 来源统计）、R-GAT 3.5 h、PagedAttention 4.3 h（P12 第 5 步，带 L2 来源统计）。T18 全开的墙钟是 baseline 的 0.5–1.0 倍。
  - 5×5（× 1/2）：FW 4 分钟、PR 0.75 h、MT 2.1 h、R-GAT 1.8 h、PA 2.1 h。
  - 3×3（× 1/6）：FW 1 分钟、PR 0.25 h、MT 0.7 h、R-GAT 0.6 h、PA 0.7 h。
  - 合计约 15 CPU 小时；8 个并行时墙钟约 2–3 小时（受 MT、PA 的 5×5 限制）。$OBS 的开销还没测，会更长。
- 峰值内存：7×7 的参考值 MT 28 GB、PA 25 GB（都带 L2 来源统计）、FW 10.7 GB、R-GAT 4.1 GB；5×5 估计约一半，3×3 约 1/6 加上固定部分（R-GAT 数据集约 2 GB）。都远低于每台 30 GB 的余量要求。
- **注意**：FW 和 PR 在 T18 评估的规模下很短（7×7 只要 0.12 h / 1.5 h），不满足用户 10-20 “每个实验 6–8 小时”的要求。如果要放大，按上面的公式重算。

**预期能回答什么**（F4、E6）：平均跳数 3×3 2.14、5×5 3.41、7×7 4.72（所有 GPM 对的平均曼哈顿距离），最大跳数 4 / 8 / 12。按 T18 文档的往返时间（84 + 71.4 ns × 跳数）、100 B/ns 远程带宽，Little 定律需要的在途数：平均距离 370 / 511 / 657，最远 578 / 1,024 / 1,470，1 跳都是 243。三个尺寸都远大于 64，所以 3×3 上 baseline 也会被跟踪表限制，差别在程度。

---

## C3：CACTI（表 C）

### HPCA 稿的方法

- 不在 `mechanism_1` 分支上（这个分支里只有 `akita/tracing` 有一个文件提到 cacti）。HPCA 稿的面积来自 `akkalat/cost/`，提交 `95b4f46`（“Update CuPath sensitivity runs, CACTI cost analysis, ...”，CONFIG.md 第 1 节的基准 commit），本分支也有。
- 方法（`akkalat/cost/CUPATH_CACTI_AREA.md`、`run_full_cacti.py`）：CACTI 7.0，32 nm，ITRS-HP 的单元和外围，UCA，360 K，模板 `cacti32/cupath_filter.cfg`。cuckoo filter 用 CACTI 的 4 路 cache 模型代替（tag = 指纹 + 类型，1 字节数据放状态；2 个读口对应两个候选 bucket，1 个读写口做更新），其余表都按 scratch RAM 算。用的 CACTI 是 `topanitanw/Cacti-7.0` @ `9144cf2`。

### 这次用的 CACTI

- Mac 和两台服务器上都没有装 CACTI。按要求只从官方仓库下载：yss2 上 `git clone https://github.com/HewlettPackard/cacti.git`（`master` @ `1ffd8dfb10303d306ecd8d215320aea07651e878`，“updated README to include references”），放在 `~/akita-exp-test/cacti/src`，`make` 编译通过（g++ 11.4）。没有下载别的东西。
- **和 HPCA 稿用的 fork 对比**：拿 HPCA 的 14 个 `.cfg` 直接重跑，10 个的访问时间和面积**完全相同**（L2 分区 13.6276 mm² / 6.32735 ns、typed filter 0.625934 mm² / 0.954857 ns、RDMA line table、waiter table 等）；另外 4 个很小的表（1–2 KB，64 B 或 32 B 行，比如 `m1_response_data`、`requester_batch_table`）官方版报 “no valid data array organizations found”，fork 应该修过这个。这次要算的表都能在官方版上算出来，所以方法一致。

### 表结构（每个 GPM；按 T18 的默认值：表 S = 2W 个槽、4 路、12 位指纹、等待者池 4S、owner 窗口 = W）

| 结构 | 怎么建模 | 位宽 |
|---|---|---|
| RRT 指纹阵列 | 4 路 cache 代替，S 个槽，tag 12 位，1 B 状态；2 读口 + 1 读写口；每 2K 个槽一个 bank（同 HPCA filter） | 指纹 12 + 有效 / 打开 / 已发 |
| RRT 旁路完整地址 | RAM，S × 8 B | 42 位 line 地址 + 6 位目的 GPM + 等待者链表头 log2(4S) 位，补齐到 64 位 |
| RRT 等待者指针 | RAM，4S × 4 B | CU 号 5 位 + L1V MSHR 号 4 位 + next 指针 ≤ 15 位，补齐到 32 位 |
| 对照：全相联 CAM（W 项） | CACTI cache，associativity 0，1 个 search 口；tag 42 位（line 地址），数据 8 B | 和 RRT 同样的跟踪能力（W 个在途） |
| 对照：全相联 CAM（2W 项） | 同上，项数 = cuckoo 表的槽数 | |
| OMT 指纹阵列 | 同 RRT 指纹阵列（S = 2W） | |
| OMT 旁路 | RAM，S × 16 B | 42 位地址 + 48 位请求者位图 + 4 位状态（有效 / 打开 / 多播 / 占窗口），补齐到 128 位 |

多播响应只带地址、由请求方 RRT 认领（T18 的设计），所以 OMT 不用存每个请求者的事务号。没算：cuckoo 的哈希和踢出逻辑、OMT 等 L2 数据时的数据缓冲、switch 里的复制逻辑（CACTI 只算规则阵列，和 HPCA 稿同样的限制）。

### 结果（32 nm，CACTI 7）

单个阵列（`results_index/obs/cacti/rrt_cacti_results.csv`；功耗 = 每次访问的动态能量 + 漏电；漏电 = 每 bank 待机漏电 × bank 数）：

| 阵列 | W | 面积 mm² | 访问时间 ns | 周期 ns | 读能量 pJ | 搜索能量 pJ | 漏电 mW |
|---|---|---|---|---|---|---|---|
| RRT 指纹 | 256 | 0.0124 | 0.194 | 0.126 | 1.37 | — | 2.1 |
| RRT 地址 | 256 | 0.0069 | 0.197 | 0.154 | 1.59 | — | 3.1 |
| RRT 等待者 | 256 | 0.0130 | 0.211 | 0.185 | 2.57 | — | 5.7 |
| CAM（W 项） | 256 | 0.0149 | 0.484 | 0.579 | 1.66 | 12.4 | 5.4 |
| CAM（2W 项） | 256 | 0.0268 | 0.767 | 0.890 | 1.91 | 22.6 | 10.2 |
| OMT 指纹 | 256 | 0.0124 | 0.194 | 0.126 | 1.37 | — | 2.1 |
| OMT 旁路 | 256 | 0.0140 | 0.211 | 0.185 | 3.59 | — | 5.7 |
| RRT 指纹 | 1024 | 0.0438 | 0.251 | 0.190 | 3.07 | — | 5.9 |
| RRT 地址 | 1024 | 0.0257 | 0.241 | 0.270 | 5.53 | — | 10.5 |
| RRT 等待者 | 1024 | 0.0537 | 0.367 | 0.270 | 5.60 | — | 21.2 |
| CAM（W 项） | 1024 | 0.0828 | 0.756 | 0.579 | 6.96 | 57.1 | 23.5 |
| CAM（2W 项） | 1024 | 0.1407 | 1.090 | 0.890 | 8.67 | 101.3 | 42.9 |
| OMT 指纹 | 1024 | 0.0438 | 0.251 | 0.190 | 3.07 | — | 5.9 |
| OMT 旁路 | 1024 | 0.0494 | 0.313 | 0.328 | 9.75 | — | 20.9 |
| RRT 指纹 | 4096 | 0.1849 | 0.439 | 0.190 | 6.39 | — | 24.4（4 bank） |
| RRT 地址 | 4096 | 0.1058 | 0.439 | 0.555 | 12.03 | — | 38.0 |
| RRT 等待者 | 4096 | 0.2108 | 0.594 | 0.555 | 11.82 | — | 76.4 |
| CAM（W 项） | 4096 | 0.4028 | 1.035 | 0.438 | 19.35 | 226.5 | 96.8 |
| CAM（2W 项） | 4096 | 0.6239 | 1.249 | 0.579 | 22.88 | 391.5 | 170.8 |
| OMT 指纹 | 4096 | 0.1849 | 0.439 | 0.190 | 6.39 | — | 24.4（4 bank） |
| OMT 旁路 | 4096 | 0.2092 | 0.542 | 0.613 | 23.05 | — | 75.4 |
| 参照：mi200 L2 slice（256 KB，16 路） | — | 3.631（× 8 = 29.05 / GPM） | 3.385 | 5.102 | 550 | — | 247 |

汇总（每个 GPM）：

| W | RRT 面积（指纹 + 地址 + 等待者） | OMT 面积 | RRT + OMT 占 mi200 L2 面积 | 查找延迟：cuckoo（指纹再读地址，串行） vs CAM（W 项） | 每次查找能量：cuckoo vs CAM 搜索 | 索引部分漏电：cuckoo vs CAM（W 项） |
|---|---|---|---|---|---|---|
| 256 | 0.032 mm² | 0.026 mm² | 0.20% | 0.39 vs 0.48 ns | 3.0 vs 12.4 pJ（4.2×） | 5.2 vs 5.4 mW |
| 1024 | 0.123 mm² | 0.093 mm² | 0.74% | 0.49 vs 0.76 ns | 8.6 vs 57.1 pJ（6.6×） | 16.5 vs 23.5 mW |
| 4096 | 0.502 mm² | 0.394 mm² | 3.1% | 0.88 vs 1.03 ns | 18.4 vs 226.5 pJ（12.3×） | 62.4 vs 96.8 mW |

- **结论**：索引部分（指纹 + 地址）和同样跟踪能力的 CAM 比，面积只小 16–28%（W=1024：0.070 vs 0.083 mm²），**主要差别在能量和延迟**：每次查找的能量低 4–12 倍，而且差距随 W 变大；W=4096 时 CAM 的访问时间超过 1 ns（1 GHz 下一个周期放不下），cuckoo 两个阵列各 0.44 ns。按每周期一次查找（1 GHz），W=4096 的动态功耗是 18 mW 对 227 mW。
- 等待者池（4S 项）是 RRT 里最大的一块，CAM 方案也需要它，比较时两边都要加上（W=1024：cuckoo 0.123 mm² vs CAM 0.137 mm²）。
- W=1024 时 RRT + OMT 共 0.22 mm²，是 mi200 每 GPM L2（29.05 mm²）的 0.74%。HPCA 稿 Complete 的 CACTI 阵列开销是 4 MB L2 的 6.86%，作为对照。
- 和 HPCA 稿一样，这是“CACTI 阵列面积”，不是综合后的总面积。

---

## C4：R4 / R6 / R7 的配置确认（⏸ 只写、不跑）

所有运行都用 `akkalat/isca27_run.sh`（`-config` 配置文件 + 命令行参数），都带 `-magic-memory-copy`（来自 `hpca.json`），不带 `-verify`。

### R4：补 oracle

参数和 T15 相同（yss2 `~/akita-exp-test/t18eval/genq.py` 里 P12 第 5 步的写法；T15 = runall2 baseline + 末尾追加）：
- O1：`-force-local-data-access`（L1V 缺失一律走本地 L2/DRAM）
- O2b：`-rdma-max-outstanding=4096 -rdma-pipeline-width=64`（跟踪表不受限）
- O3：`-switch-latency=1`（距离几乎免费）

**现在的规模下缺的格子**：

| workload | B | O1 | O2b | O3 | 二进制 |
|---|---|---|---|---|---|
| kmeans `-max-wg 8192` | 有：`1020_abl_kmeans-wg8k_mi200_ca8b831`（3.163 ms，5.2 h，10 GB，带 L2 来源统计） | 缺 | 缺 | 缺 | `ca8b831`（和 B 相同） |
| MoE skew 1.75 | 有（`ed1aca0`） | 缺（T17 暂停时撤出） | 有 | 缺（同左） | `ed1aca0` |
| PagedAttention batch 66 | 有 | 缺（同上） | 有 | 缺（同上） | `ed1aca0` |
| R-GAT 320 种子 | 有 | 有 | **在跑**（yss2，`p12/`，已跑约 2 h） | **在跑**（同左） | `ed1aca0` |
| FW、I2C、PR、MT、MM（32768）、SPMV（1536） | T15 已有，规模和 T18 评估相同 | 有 | 有 | 有 | T15 是 `61b6ae8` |

命令（每行前面省略 `akkalat/isca27_run.sh`）：
```
1021_diag_kmeans-wg8k_mi200+o1_ca8b831   ca8b831 R4 mi200 -benchmark kmeans -max-wg 8192 -report-l2-source -l2-source-tile-width=7 -force-local-data-access
1021_diag_kmeans-wg8k_mi200+o2b_ca8b831  ca8b831 R4 mi200 -benchmark kmeans -max-wg 8192 -report-l2-source -l2-source-tile-width=7 -rdma-max-outstanding=4096 -rdma-pipeline-width=64
1021_diag_kmeans-wg8k_mi200+o3_ca8b831   ca8b831 R4 mi200 -benchmark kmeans -max-wg 8192 -report-l2-source -l2-source-tile-width=7 -switch-latency=1
1021_diag_t17-moe-skew1.75_mi200+o1_ed1aca0 ed1aca0 R4 mi200 -benchmark moe -moe-tokens=256 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75 -report-gpm-trace -force-local-data-access
1021_diag_t17-moe-skew1.75_mi200+o3_ed1aca0 ed1aca0 R4 mi200 -benchmark moe -moe-tokens=256 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75 -report-gpm-trace -switch-latency=1
1021_diag_t17-pagedattn_mi200+o1_ed1aca0    ed1aca0 R4 mi200 -benchmark pagedattn -pa-batch=66 -report-gpm-trace -force-local-data-access
1021_diag_t17-pagedattn_mi200+o3_ed1aca0    ed1aca0 R4 mi200 -benchmark pagedattn -pa-batch=66 -report-gpm-trace -switch-latency=1
```
（T17 四行就是 10-20 已确认、T17 暂停时撤出的那 4 个运行，命令原样。R-GAT 的两个如果退出码不是 0，再按原命令补。）

- 和 `mi200.json` 的差异：上面列的 oracle 参数（有意偏离，分析实验）；统计开关 `-report-l2-source -l2-source-tile-width=7`、`-report-gpm-trace`；benchmark 参数。
- 运行数：7 个（R-GAT 两个在跑，不算）。
- 时间：kmeans 按 T15 的比例（O1 / O2b / O3 ≈ B 的 0.34 / ≈ 只放大跟踪表的运行 / 0.75）估计 1.8 h / 2–5 h / 3.9 h；MoE、PA 的 O1 / O3 估计 3–4.5 h（B 3.6 h / 4.3 h）。7 个并行约 5 小时。
- 内存：kmeans 约 10 GB，MoE 约 6 GB，PA 约 25 GB（带统计时实测 23.8–25.7 GB）。

**如果 R1 换成用户 10-20 要求的新规模（6–8 h、大 footprint）**，oracle 也要在新规模上重做才能和 F1 对上：10 个旧 workload × 3 个 oracle = 30 个，16 个新 benchmark 也没有 oracle，再加 48 个；B 可以直接用 R1 的 baseline（新统计只挂被动 hook，逐位一致），前提是同一个二进制。每个 6–8 h，按每台 8 个并行，78 个约 2.5–3 天。请用户决定：只补现在规模下缺的 7 个，还是等新规模定了一起做。

### R6：微基准（T14 的方法，platbench）

二进制：`isca27/mesh-size` 的头（`3211be7`），配置 `mi200`。源 GPM 都是 driver GPU 1（tile 0，角上），和 T14 相同。

**(a) 3×3 / 5×5 的跳数延迟和带宽**

| 尺寸 | 测试 | 参数 |
|---|---|---|
| 3×3 | 1 → 全部的延迟 | `-benchmark platbench -plat-test hopmatrix -plat-sources 1 -plat-dsts 0 -plat-footprints 1M -plat-steps 10000 -mesh-size=3` |
| 3×3 | 全部 → 1 的延迟 | `-benchmark platbench -plat-test hopmatrix -plat-sources 0 -plat-dsts 1 -plat-footprints 1M -plat-steps 10000 -mesh-size=3` |
| 3×3 | 远程带宽，1 跳 | `-benchmark platbench -plat-test remotebw -plat-dsts 2 -plat-footprints 16M -plat-wgs 1,2,4,8,16,32,64,128,256 -mesh-size=3` |
| 3×3 | 远程带宽，4 跳（最远） | 同上，`-plat-dsts 8` |
| 5×5 | 1 → 全部、全部 → 1 | 同 3×3，`-mesh-size=5` |
| 5×5 | 远程带宽，1 跳 / 6 跳 / 8 跳 | `-plat-dsts 2` / `14` / `24`，其余同上 |

7×7 复用 T14 的 `1015_plat_hop-1toall`、`hop-allto1`、`rbw-d2-16M`、`rbw-d7-16M`（`67f1c03`，mi200；平台代码之后没有改过，默认参数下应当逐位一致；如果要严格同一个二进制，再加 4 个）。

运行数：3×3 4 个、5×5 5 个，共 9 个。

**(b) 7×7 远程带宽 vs 跟踪表项数（T14 诊断的方法：只改 `-rdma-max-outstanding`）**

```
-benchmark platbench -plat-test remotebw -plat-dsts D -plat-footprints 16M -plat-wgs 1,2,4,8,16,32,64,128,256,512,1024 -rdma-max-outstanding=R
D = 2（1 跳）、7（6 跳）；R = 64、256、1024、4096
```
- 8 个运行（R=64 可以复用 `rbw-d{2,7}-16M`，但那两个的 `-plat-wgs` 只到 256，建议一起重跑）。
- **要先说明的限制**：一个 GPM 发出的远程读同时最多 28 个 CU × 16 个 L1V MSHR = 448 个（T18 评估里在途峰值正好都是 448）。所以表项数 1024 / 4096 时，带宽会停在 448 个在途对应的值，测不到 1024 / 4096 本身。要看到表项数的效果，可选再加 4 个：R = 1024 / 4096 × D = 2 / 7，加 `-l1v-mshr-entries=64`（T14 诊断 A 用过同样的改法，又一个配置偏离）。
- 和 `mi200.json` 的差异：`-rdma-max-outstanding`（有意偏离，诊断用）；可选的 `-l1v-mshr-entries=64`；(a) 的 `-mesh-size`。

**时间和内存**：T14 的 platbench 运行都在 1 小时以内、4 GB 以下（没有记墙钟，按 10-15 当天的批次估计）；小 mesh 更快。共 17 个（+4 可选），8 个并行约 2 小时。

### R7：敏感性

**L2 命中延迟用的参数**：模拟器原来没有能改 L2 延迟的 flag。L2 每个 slice 的流水是 directory 10 周期 + data bank 10 周期（写死，后者是 writeback cache 的默认值）。mi200 上 L2 命中的端到端延迟是 129.0 ns（T14 延迟阶梯，L1V 命中 115.0 ns）。新加 `-l2-bank-latency`（默认 10，逐位不变），加长 data bank 的流水。

校准（yss2，platbench 延迟阶梯，`-plat-test latency -plat-dsts 1 -plat-groups A`，二进制 `3211be7`，各约 2 分钟、1.7 GB）：

| `-l2-bank-latency` | L1V 命中（4–16 KB） | L2 命中（32 KB–1 MB） | 本地 HBM（4–8 MB） |
|---|---|---|---|
| 10（默认） | 115.0 ns | 129.0 ns | 198.5–199.5 ns（和 T14 的 mi200 数一致） |
| **31（建议）** | 115.1 ns | **150.0 ns** | 221.1–221.5 ns |
| 41 | 115.1 ns | 160.0 ns | 230.4–230.5 ns |

每加 1 个周期，L2 命中正好多 1 ns。**副作用**：L2 缺失也经过 data bank 这一级，所以本地 HBM 延迟同样多 21 ns（199 → 221 ns）；远程读在 owner 端的 L2 服务时间也多 21 ns。如果只想加长命中、不动缺失，需要在 writeback cache 里单独给命中路径加延迟，是更大的改动，这次没做。


| 组 | 改的参数 | 和 `mi200.json` 的差异 |
|---|---|---|
| 链路 384 GB/s | `-bandwidth=24` | `-bandwidth` 48 → 24（mi200 每条链路 48 × 16 GB/s = 768 GB/s） |
| 每跳 20 周期 | `-switch-latency=20` | `-switch-latency` 32 → 20 |
| L2 命中延迟加长 | `-l2-bank-latency=31` | 新参数，10 → 31（L2 命中约 150 ns） |

× 配置 {`mi200`（baseline）, `t18_full,t18_w1024`（T18 全开 W=1024）} × workload {FW、PR、MT、R-GAT、PagedAttention}，规模和 R1 相同（现在是 T18 评估的规模：FW / PR / MT `-max-wg 32768`、R-GAT 320 种子、PA batch 66）。不加 L2 来源统计和 P13 的新统计（只看加速比，内存小）；T18 自己的计数器照常输出。

命令（`<C>` = 用户确认时的二进制；每行前面省略 `akkalat/isca27_run.sh`，`<X>` ∈ {bw24, sw20, l2lat31}，`<P>` 是对应的参数）：
```
1021_sens_floydwarshall_mi200+<X>_<C>                 <C> R7 mi200                -benchmark floydwarshall -max-wg 32768 <P>
1021_sens_floydwarshall_mi200+t18full+w1024+<X>_<C>   <C> R7 t18_full,t18_w1024  -benchmark floydwarshall -max-wg 32768 <P>
1021_sens_pagerank_mi200+<X>_<C>                      <C> R7 mi200                -benchmark pagerank -max-wg 32768 <P>
1021_sens_pagerank_mi200+t18full+w1024+<X>_<C>        <C> R7 t18_full,t18_w1024  -benchmark pagerank -max-wg 32768 <P>
1021_sens_matrixtranspose_mi200+<X>_<C>               <C> R7 mi200                -benchmark matrixtranspose -max-wg 32768 <P>
1021_sens_matrixtranspose_mi200+t18full+w1024+<X>_<C> <C> R7 t18_full,t18_w1024  -benchmark matrixtranspose -max-wg 32768 <P>
1021_sens_t17-rgat-s320_mi200+<X>_<C>                 <C> R7 mi200                -benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=320 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1 <P>
1021_sens_t17-rgat-s320_mi200+t18full+w1024+<X>_<C>   <C> R7 t18_full,t18_w1024  -benchmark rgat -rgat-data=/home/daoxuanxu/akita-exp-test/datasets/igbh/tiny -rgat-seeds=320 -rgat-fanouts=10,5 -rgat-hidden=128 -rgat-heads=1 <P>
1021_sens_t17-pagedattn_mi200+<X>_<C>                 <C> R7 mi200                -benchmark pagedattn -pa-batch=66 <P>
1021_sens_t17-pagedattn_mi200+t18full+w1024+<X>_<C>   <C> R7 t18_full,t18_w1024  -benchmark pagedattn -pa-batch=66 <P>
<X>=bw24: <P>=-bandwidth=24 ； <X>=sw20: <P>=-switch-latency=20 ； <X>=l2lat31: <P>=-l2-bank-latency=31
```

- 运行数：3 组 × 2 配置 × 5 workload = **30 个**。默认参数下的对照用 R1（baseline）和 R3（T18 W=1024），不重跑（前提是同一个二进制；否则加 10 个）。
- **可选**：L2 延迟这一组主要是给 M3 用的（README：我们的 L2 命中延迟偏小，M3 的收益可能被高估），只测 baseline 和 M1 看不到 M3。建议 L2 延迟这一组再加 `complete_new`（Complete）和 M1 + M2（`m1m2_dedupM1`），+10 个。
- 时间（7×7，按 baseline 墙钟；T18 是 0.5–1.0 倍）：FW 0.12 h、PR 1.5 h、MT 约 4 h、R-GAT 3.5 h、PA 约 4 h。链路减半时 baseline 的模拟时间会变长，墙钟可能多 10–30%。每组 10 个运行约 25 CPU 小时，3 组约 75 CPU 小时；8 个并行时约 10–12 小时（受 MT、R-GAT、PA 限制）。
- 内存（不带 L2 来源统计）：PA 约 6.5 GB、FW 约 11 GB、R-GAT 约 4 GB、MT、PR 估计 5–10 GB；30 个分两台，每台 8 个并行时 < 80 GB。
- 同样的提醒：如果 R1 换成新规模（6–8 h），R7 也用新规模，每个运行 6–8 h，30 个约 2 天。

---

## ⏸ 需要用户确认的

1. **R5**（mesh 3×3 / 5×5，弱缩放规则、5 个 workload 的大小、20 个运行，可选 +10）；要不要加 2×2（要改 CPU 的挂法）。
2. **R4**：只补现在规模下缺的 7 个（kmeans 8192 的 O1/O2b/O3、MoE 和 PA 的 O1/O3），还是等 R1 的新规模定了以后在新规模上整体重做（约 78 个）。
3. **R6**：17 个 platbench 运行；表项数扫描受 L1V MSHR 的 448 上限，要不要加 `-l1v-mshr-entries=64` 的 4 个。
4. **R7**：L2 命中延迟用 `-l2-bank-latency=31`（约 150 ns）；30 个运行，L2 延迟组要不要加 Complete 和 M1 + M2。
5. 所有这些要用的二进制：`isca27/mesh-size` 和 `isca27/obs-stats` 合并后的 commit（R5 要 E1 统计）；R4 用和 B 相同的旧二进制（`ca8b831`、`ed1aca0`）。
