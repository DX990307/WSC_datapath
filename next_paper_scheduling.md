# 下一篇论文的素材：wafer 上的 thread / data scheduling（用户 10-20 决定：ISCA'27 这篇只做通信，这些留给下一篇）

> 这里收集的是 10-19 / 10-20 讨论里和调度有关的 idea 和数据，**不在 ISCA'27 的范围内**。ISCA'27 以 CuPath（HPCA'27 投稿版，1 WA + 5 WR）为底，只做远程数据通路（通信）。

## 数据里支持调度方向的四个 wafer 特有事实

| 事实 | 数据（T16 / T17，mi200） |
|---|---|
| 一个 kernel 填不满 wafer | R-GAT 时间加权平均 9.3 个 GPM 有工作、MoE 27 个；R-GAT 85% 的时间 ≤ 13 个 GPM |
| 不均来自数据，不是位置 | PagedAttention 主 kernel 最慢 ÷ 平均 1.72（ρ +0.29）、MoE 1.7–1.8（\|ρ\| ≤ 0.2）；完美均衡上限 PA 1.74×、MoE 1.61×（铺满 48 个 GPM 时 2.94×）、R-GAT 1.16×（6.18×） |
| WG 和页都是静态连续切分 | `distributeWGToGPUs`（每 GPM 28 × ⌈N/1344⌉）、`selectActualGPUForDistributedPage`（48 等分、余数给最后一个、< 48 页放第一个）；错开时 kmeans 0% → 42.9% 远程 |
| 有中心和角落 | 平均跳数 3.7（中心）vs 6.1（角落）；负载下每跳 180–360 ns |

## idea 列表

1. **WG 级 compute-to-data dispatch**：把 WG 派到它主要读的数据所在的 GPM（不动页）。目标函数是端点负载而不是跳数。PagedAttention 把 `-pa-part-tokens` 改成 16 就能一个 WG 对一个 KV block；MoE 的 expert tile 派到权重所在 GPM；R-GAT 目标节点行本地、源节点 gather 仍远程。T15 的 O1 是上限（FW 11×）。前人：MCM-GPU 的 locality-aware CTA 调度（ISCA'17）。**注意**：ISCA'27 的审稿人可能会问"为什么不把 WG 派到 owner"，通信论文里要在讨论或 baseline 里回应。
2. **多 kernel 空间并发**：无依赖的 kernel 同时跑在 wafer 不同区域、放在离数据近的地方；用读写足迹签名（cuckoo filter）做保守的依赖判断。R-GAT 上限 6.2×。前人：GPU spatial multitasking（HPCA'12）、Bulk 签名消歧（ISCA'06）。
3. **cuckoo filter / MinHash 指导的就近 WG 窃取**（原 T19，设计见 `tasks/T19_filter_guided_wg_stealing.md`，图 `figs/fig_t19_neighbor_stealing.*`）：先跑完的 GPM 从 1–2 跳内的邻居偷未开始的 WG，优先偷"数据已在手边"的组。需要 WG 足迹 tracer、亲和性和历史稳定性的测量（原 P12 第 1–3 项）。
4. **按中心度放共享数据**：分配时把多读者的 buffer 放到中心 tile。收益上限不高，适合一小节。
5. **错开同一时刻各 GPM 访问的 owner**（incast 的时间去同步）：FW 的 48 个 GPM 同时读第 k 行，调度器让各 GPM 从不同的行开始。
6. 修 baseline 的放置问题（P1 页段按 CU 数占比切、P2 运行时小分配、P3 小 buffer 交错）：通信论文里作为 baseline 修正，调度论文里作为动机。

## 需要的测量（下一篇再做）

WG 足迹 tracer（每个 WG 读过的页）→ 亲和性、历史稳定性；compute-to-data 后剩余远程比例；R-GAT 的 kernel 依赖图和理想并发时间。
