# P08：补同步 + 把 10-14 到 10-16 的新结果整理出来 + Cowork 的几条复核意见

---
继续 CuPath 的 ISCA 2027 工作。先补上同步，再整理结果；**这一轮不改模拟器、不改任何配置数值、不启动新的长实验**。

## 1. 补同步（SYNC.md 第 2 节）
- 读 SYNC.md 日志里你上一条之后的 `[cowork]` 条目（10-13 两条、10-16 一条），用 `git diff` 读 Cowork 改过但还没提交的文件：`ideas_from_T14.md`（新文件）、`CONFIG.md`、`00_background.md`（D19–D22）、`site/` 三页、`prompts/P08`（本文件）。以 `cowork: ...` 提交并推送到 `isca27/docs`。
- 10-14 之后你的十几次提交都没有写进 SYNC 日志，补一条汇总，列出代码 commit 和标签。
- **以后每个停点都按 SYNC 第 2 节执行**（开工先读 Cowork 的改动并提交；结束时更新 STATUS → 追加 SYNC → 推送 → 把 meta.json/summary.csv 复制到 `results_index/`）。

## 2. 把 10-14 到 10-16 的新结果整理进 `results_index/` 和 STATUS
把下面这些运行的 `meta.json`、`summary.csv`（以及 platbench 的 `platform.csv`）复制到 `results_index/`，并在 STATUS 的“结果摘要”里**各写一张表**（已经删除或作废的不用）：
1. **诊断 A**：L2 带宽 × L1 每 CU 在途数 16/32/64；单对远程带宽 × RDMA 在途上限 64/128/256（1 跳、6 跳）。重点回答：远程带宽的瓶颈到底在 L1V 的 16 条在途、RDMA 的 64 个上限，还是别处？放大之后“端点受限”的结论还剩多少？
2. **诊断 B**：远程 TLB 缺失和到中心距离的关系。
3. **诊断 C**：数据放置量化（14 个 workload，hpca）：每个 workload 的远程读比例、owner 集中度（前 3 个 owner 占多少远程流量）、有多少 buffer 小于 192 KB 被整个放在 GPM 1。
4. **诊断 D 和 10-15 的 mi200 / hpca 短片段 ablation**：每个 workload × baseline/m1/m2/m3/Complete 的加速比表，两套配置并排，加 GMEAN；单独标出没跑完的格子。
5. **mi200 的 T14 平台测量**：和 hpca、mi300x 三套并排的摘要表（同 T14 第一档的格式）。
6. **可复现性检查**（fir、pagerank 的 baseline 和 Complete 各跑两次）：是否完全一致。
7. kmeans 诊断（hpca baseline 完整跑完，9,561,046 ns，和 7 月一致）。

## 3. Cowork 的复核意见（先回答，不要改代码）
请逐条读代码或已有结果回答，写进 STATUS 的“需要用户决定”或“规格与代码的差异”：
- **(a) m3（Remote Reuse）保留在请求方 L2 里的远程 line，在 kernel 边界有没有被失效？** 论文 IV-C 写的是“retained lines are invalidated at the same kernel boundaries and synchronization points”，而瓶颈审查第 8 条说模拟器 kernel 之间不做任何 cache 失效。如果没有失效，论文的描述和实现不一致，m3 的收益可能偏乐观；同时 T8（写签名选择性失效）在模拟器里也就没有可以优化的对象。给出代码位置。
- **(b) B1 baseline（远程数据总进请求方 L2）**：真实 MI200 默认就会缓存对端显存（MTYPE_NC），审稿人 A 也点名问过“为什么 baseline 不缓存远程响应”。用户决定 baseline 默认保持现状（D20）；请评估把 T3 的 B1 作为一个**对照 baseline**（不改默认）加进主结果需要多少工作量和模拟时间。
- **(c) 数据放置**：如果诊断 C 显示小 buffer 集中在 GPM 1、远程比例随 CU 数变化，给出 2–3 个候选的放置修正（例如小 buffer 按页交错、WG 段和页段按同一规则切分），以及各自对 hpca 旧结果可复现性的影响。只给方案，不实现。
- **(d) mi200 的 HBM 读只到峰值 31%**：列出“每 slice MSHR 32 → 128”这一项单独改动的影响范围（只影响 mi200；是否需要重跑已完成的 mi200 结果），供用户在冻结前决定。

完成后：更新 STATUS → 追加 SYNC 日志 → 推送两个仓库 → ⏸ 停下来等我。
---
