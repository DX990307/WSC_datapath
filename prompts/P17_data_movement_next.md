# P17：接手 10-21 的进展——收尾在跑的实验，做 data movement 的下一步分析

> 用户 10-21："都写到 readme 里，然后给我一个 prompt"。背景全部在 `../README.md` 的"当前进展与 idea 汇总（10-21）"一节和 `../STATUS.md`。
> 本文件给新开的 Claude Code 会话用。用户只考虑 **data movement** 方向（不做调度 / 负载均衡 / 地址翻译机制）。

---
继续 CuPath 的 ISCA 2027 工作。全程用中文汇报；任何实验结果一出来就告诉用户，不要攒着。

**0. 先同步**（SYNC.md 第 2 节）
- 读 `README.md` 的"当前进展与 idea 汇总（10-21）"、`STATUS.md` 里 10-21 的各节（v3 @ 384 GB/s、M2 / 分配方式的敏感性、data movement 离线分析）、SYNC 日志 10-21 的条目。
- `git status` / `git diff` 读未提交的改动（`results_index/pm/` 下可能有 page-migration 那边未提交的文件），按 SYNC 规则提交、推送 `isca27/docs`。
- 不要引用 W = 1024 的旧结果；修复前（`4235991` 之前）的结果不可信。

**1. 收尾两组在跑的实验**（服务器 yss2 / yss3，只读查看，不要动别人的调度器和进程）
- (a) 网络紧张场景补 19 个 workload：`~/akita-exp-test/v3m2w/`，tag `w256b48m`（48 GB/s、W = 256、baseline / Complete，完整统计）。跑完后把 25 个 workload 的 Complete 加速比、GMEAN（含 / 不含 PR）整理进 `results_index/m2w/`（用 `m2w/scripts/collect.py`，把 `w256b48m`、`w2048b48` 加进 COND），并对照主配置 1.73×。
- (b) page migration：`~/akita-exp-test/pm/`，分支 `isca27/page-migration`（二进制 `c74ef96`），25 个 workload × 9 个配置。结果写在 `results_index/pm/{pm.csv, analysis.md}`。如果原来负责的会话已经不在，就自己收尾：汇总、确认 observe 与 baseline 逐位一致、写结论（迁移 vs baseline vs Complete、来回迁移次数、热点 owner 份额是否下降、能否和 Complete 叠加），测试通过后推送该分支（不要 force）。
- yss2 上的 PID 2415098（v3c384 的 MG M1+M2，挂住）**不要动**，等用户决定。

**2. data movement 的两项分析**（只读，不改机制代码；需要新跑的只跑 baseline 加统计/trace 开关）
- (A2) **从邻居取的上限**：对每次远程读，估计 1 / 2 跳内是否有邻居 GPM 的 L2（M3）在这之前已持有该 line，按"从邻居取"重算跳数和往返时间；给出可省的远程读、字节×跳数、对 owner 热点链路负载的影响。数据先用 `results_index/dm/` 已有的统计（`sharing_windows.csv` 里的最近共享者距离等）；不够就说明需要什么新统计。
- (A3) **子页放置的访问模式**：先确认 `-page-sharing-trace`（`akkalat/baseline/runner/page_sharing_trace.go`）是否记录页内偏移和 GPM；如果记录了，用采样 trace 跑 baseline：PR、npbcg、MM、graphtc、atax（热点）、MG、CFD、MoE、shocsort（区域性）、sssp（随机对照），主配置 384 GB/s、W = 64。统计：每页按 512 B / 1 KB / 2 KB 切分后，各部分被哪些 GPM 访问；按子页最优位置（使用者加权跳数最小）放置后的字节×跳数、热点 owner 负载；对比"整页最优放置"和 Complete。没记录偏移的话，写出需要加的只读统计（文件、位置、开销），⏸ 等用户同意再改代码。
- 结果写进 `results_index/dm/`（新文件，不覆盖已有的），更新 `dm/analysis.md` 的排序一节。

**3. ⏸ 停下来，等用户决定**（把下面每项的选项、代价、你的建议写进 STATUS，然后停）
1. 是否实现 A1（M3 跨 kernel 保留只读远程 line）：给出正确性方案（owner 记录写过的页 / line 并在 kernel 边界只失效这些，或写纪元）、要改的文件、单元测试和回归计划。
2. 是否实现 A2 / A3（看第 2 步的上限）。
3. 是否做 wafer 规模扫描（5×5 / 7×7 / 9×9）：先查 `isca27/mesh-size` 分支的改动有没有合进 `isca27/cupath-v3`。
4. 什么时候修 L2TLB→IOMMU 丢唤醒 bug（`l2tlb.go` 的 sendDownstream / sendToIOMMU 用 IOMMUCache 的端口发送，连接腾出空位时通知不到 L2TLB）；修了会改时序。
5. yss2 上 PID 2415098 要不要杀。

**规则**
- 代码只在本机改，服务器只同步、编译、运行；每批实验用同一个 commit；只看 exit 0 的 `summary.csv`。
- 服务器内存：调度器按 MemAvailable 留 30 GB 余量；不要碰 `~/exp`。
- 每完成一步：更新 STATUS、在 SYNC 追加 `[claude-code]` 条目、推送 `isca27/docs`（只推 `isca27/...` 分支，不 force）。
---
