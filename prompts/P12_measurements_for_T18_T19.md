# P12：为新机制（T18 新 m2、T19 WG 窃取）做的测量

---
继续 CuPath 的 ISCA 2027 工作。

**先同步**（SYNC.md 第 2 节）：读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目，用 `git diff` 读 Cowork 改过的文件：
- **新文件** `tasks/T18_new_m2_two_sided_merge.md`、`tasks/T19_filter_guided_wg_stealing.md`、`prompts/P12_measurements_for_T18_T19.md`（本文件）
- `ideas_from_T14.md`（10-19 新增的几节：闭环排队模型、仿真精度、地址转换核查、下一步的前提和机制方向）
- `README.md`、`site/`

以 `cowork: ...` 提交，推送到 `isca27/docs`。

**背景（用户 10-19 决定）**：论文前提是地址转换已经由 HDPAT 解决，CuPath 解决数据访问。机制方向：T18（m2 升级为两端合并：请求方用改造的 cuckoo 表管理在途窗口、按距离分配窗口、owner 端合并 + 多播）和 T19（cuckoo filter 指导的就近 WG 窃取）。两者都是 `[提案]`，**这次只做测量，不实现机制**。

**要做的测量**（先做只加统计的 1–4；5 是 oracle 运行，要配置确认）：

1. **每个 WG 的访问足迹 tracer**（只加统计，开关默认关）：记录每个 WG 读过的页集合（4 KB 页号；如果太大，可以记录前 N 个或采样，说明做法）。回归：开 / 关 tracer，mi200 的 fir、MM（`-max-wg 4096`）total_time 和排序后的统计必须完全一致，有差异先停下来报告。
2. **亲和性**（T19 的问题 1）：用 1 的数据算编号相邻的 WG 之间、同一组 WG 之间的页重叠（Jaccard，或“B 读的页里有多少 A 也读过”）。分别给：MoE（同一个 expert 的 WG）、R-GAT（相邻目标节点）、PagedAttention（对照）、pagerank、kmeans。
3. **历史稳定性**（T19 的问题 2）：同一个 kernel 多次启动时，同一组 WG 的足迹是否基本不变。用 pagerank、kmeans 的多轮迭代；PagedAttention 加一个 benchmark 参数 `-pa-steps`（连续 decode 多步，默认 1，默认行为必须不变），跑 2–3 步的小规模。报告相邻两次启动之间的足迹重叠。
4. **M1 共享统计**（T18 的问题）：在时间窗口（例如 1 µs 和 10 µs）内，同一条远程 line 被几个 GPM 读、这些 GPM 之间相距几跳；以及在途重复请求数（同一 GPM 内、跨 GPM 到同一 owner）。用 T17 三个 workload 和 floydwarshall（完整小输入或现有 T16 小输入）。只加统计。
5. **T17 的 O1 / O2b / O3**（T15 同样的三组 oracle，参数相同）：PagedAttention、MoE skew 1.75、R-GAT。⏸ **配置确认**：oracle 是有意偏离 `mi200.json` 的分析实验，把每组完整参数和差异给我看，等我确认后再跑。R-GAT 是否先改成铺满 48 个 GPM（T17 的方案 B）等我决定；没决定前用现有版本。

1–4 尽量用短运行（小输入或 1/8 规模，每个 ≤ 1 小时），不占长实验位置。结果写进 STATUS，CSV 放进 `results_index/p12/`。完成后：更新 STATUS → 追加 SYNC 日志 → 推送两个仓库（代码放在新分支 `isca27/p12-measure`）→ ⏸ 停下来等我。

规则不变：
- 代码只由你修改。
- 不改 mi200 或 hpca 的任何参数；新统计、新 benchmark 参数默认关闭 / 默认不变。
- 所有运行保留 `-magic-memory-copy`，不跑 verify。
- 只推 `isca27/*` 分支，不要 force push。
- `[提案]` 的部分（T18、T19 的实现）不要开始。
---
