# T15 用 oracle 实验量出 wafer 层的优化空间

**状态**：`[已定]`（用户 10-16：“可以先用实验测量一下”）· **周次**：mi200 冻结之后的第一个实验，排在全量重跑之前 · **依赖**：mi200 预设（最好是冻结版 `isca27-mi200-v1`）· **工作量估计**：只改参数，短片段，几个小时

> 🔒 **执行前必做**：按 `../CONFIG.md` 做配置确认。oracle 都是**有意偏离论文配置**的分析实验，每一组都要把参数和差异给用户看，等用户确认。所有运行保留 `-magic-memory-copy`。
>
> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027，baseline 改为 mi200（D19）。用户担心：现在的 page placement 让大部分访存发生在本地，wafer 层（远程路径）的优化空间可能不够；如果先把本地做好再做 wafer 层，空间可能更小。Cowork 的看法是要看**远程占用的时间**而不是远程访问的个数，并且用 oracle 实验直接量出每个 workload 的上限，再决定论文方向（见 `../ideas_from_T14.md` 的“10-16 讨论”）。
> 索引：`../README.md` · 进度写入：`../STATUS.md` · 同步：`../SYNC.md`

## 要回答的问题

1. 每个 workload 在 wafer 层**最多还能快多少**？
2. 这个上限里，有多少来自**端点窗口**（在途请求上限），有多少来自**距离**？
3. 现有 CuPath 的 m2、Complete 拿到了上限的百分之多少？
4. 远程访问占访问个数的多少、占访存时间的多少？

## 实验矩阵（mi200，短片段）

workload：现有 14 个（`-max-wg 32768`，spmv 1536；kmeans 如果短片段也太慢，先报告再决定）。每个 workload 跑下面 6 组，全部加 `-report-l2-source`：

| 编号 | 名称 | 改动（相对 `mi200.json`） | 含义 |
|---|---|---|---|
| B | baseline | 无 | 基准 |
| O1 | 全部本地 | `-force-local-data-access`（L1V miss 一律走本地 L2/DRAM；地址转换不变） | wafer 层优化的上限（不含远程地址转换） |
| O2a | L1 窗口放大 | `-l1v-mshr-entries` 和 `-l1v-max-concurrent-trans` 改成 256 | 发起方 L1V 在途上限的影响 |
| O2b | RDMA 窗口放大 | `-rdma-max-outstanding` 改成 4096，`-rdma-pipeline-width` 改成 64 | RDMA 在途上限和流水线宽度的影响 |
| O2 | 端点不受限 | O2a + O2b | 端点窗口的总影响 |
| O3 | 距离几乎免费 | `-switch-latency=1` | 每跳延迟的影响（端点的固定开销仍在） |

如果诊断 A 已经得出 L1V 或 RDMA 的饱和点，O2a、O2b 的数值可以改成“明显超过饱和点”的值，在配置确认时说明。

## 必须做的有效性检查（不跑 verify）

- **每组都要和 B 比较完成的 kernel 数、WG 数、总指令数。** O1 改变了数据的来源路径；如果数据依赖型 workload（例如 spmv、pagerank、kmeans、floydwarshall）因此读到不同的值、走了不同的控制流，总指令数会变。**指令数相差超过 1% 的格子标为“oracle 无效”**，不要用它的加速比。
- O1 里远程地址转换没有去掉，在结果表里注明。

## 输出（写进 STATUS 的“结果摘要”，CSV 放进 `results_index/oracle/`）

一张表，每个 workload 一行：
`workload, B 周期数, 远程访问占比, 远程延迟占比, O1 加速比, O2a, O2b, O2, O3, 有效性（指令数差异）, m2 加速比, Complete 加速比, m2 占 O1 上限的比例, Complete 占 O1 上限的比例`
- **远程访问占比** = 远程的 L1V miss 数 / 全部 L1V miss 数（来自 `-report-l2-source`）。
- **远程延迟占比** = 远程访问的总延迟 / 全部 L1V miss 的总延迟（同一份统计）。
- **“占上限的比例”** = (加速比 − 1) / (O1 加速比 − 1)。m2、Complete 的加速比用 10-15 的 mi200 短片段 ablation（`-max-wg` 相同才能比；不同就补跑）。
- 最后一行是几何平均（只算有效的格子）。

## 判断标准（建议，最后由用户决定）

- 如果现有 workload 的 O1 几何平均上限就明显超过 1.5×：wafer 层的空间足够。
- 如果只有 1.1–1.2×：再看 T10 的现代负载；现代负载也只有这个水平，就要认真考虑本地方向或者换题。

## 之后（`[提案]`，不要开始）

T10 的现代负载做出来以后，用同样的 6 组再跑一次。


## 10-16 补充：用户贴出的 mi200 短片段 ablation（`1015_abl_wafer48_mi200_67f1c03`，yss3，13 个 workload）

GMEAN：Remote Aggregation 1.24、Remote Reuse 1.10、Local Pairing 1.03、Complete 1.30。收益几乎全部来自 FWS（RA 约 2.5、LP 约 1.6、Complete 约 2.55）、KM（RA 约 2.8、Complete 约 2.85）、MM（RA 约 2.05、RR 约 3.25、Complete 约 3.85）和 I2C（约 1.1–1.2）；AES、BT、FFT、FIR、RELU、SC、PR、MT 都在 1.0 左右；**FWT 的 LP 和 Complete 约 0.88，是退化**。

因此 T15 额外要做三件事：
1. **按 O1 上限给 workload 分类**：“wafer 敏感”（O1 上限 > 1.05）和“本地型”（≤ 1.05）。分类标准在看结果之前定好，避免被说挑 workload。论文里两组的 GMEAN 都要报。
2. **检查三个大收益 workload 有没有 baseline 的人为因素**（审稿人 A 专门提过“要排除 baseline artifact”）：
   - KM：瓶颈审查发现小 buffer 全放在 GPM 1，约 97% 是远程读、挤在 2–3 个 owner 上。RA 的 2.8× 有多少是在缓解这个放置造成的热点？从 `-report-l2-source` 看 owner 集中度。
   - MM：RR 3.25×。真实 MI200 默认把对端显存缓存在请求方 L2（NC），我们的 baseline 不缓存。B1（远程数据总进 L2）能拿到其中多少？
   - FWS：RA 2.5×、LP 1.6×，看远程请求里重复 line 的比例和 owner 集中度。
3. **FWT 的退化**：LP 和 Complete 都约 0.88，查原因（额外的配对读占了带宽或 MSHR？）。如果 LP 被删掉，Complete 在 FWT 上应该回到 1.0，顺带确认。
