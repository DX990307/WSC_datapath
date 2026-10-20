# P13：ISCA'27 observation / evaluation 的数据收集（第一批：只加统计的运行 + 需要确认的准备工作）

---
继续 CuPath 的 ISCA 2027 工作。这次的依据是 `isca27/observations/README.md`（新 O1–O7、图表清单 F1–F7 / E1–E8 / 表 A–D、运行清单 R1–R8），先通读它。

**0. 先同步**（SYNC.md 第 2 节）
- 读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目，用 `git diff` 读 Cowork 新增和改过的文件：
  - 新文件：`observations/README.md`、`next_paper_scheduling.md`、本文件；
  - 改过的：`tasks/T18_new_m2_two_sided_merge.md`（重写）、`tasks/T19_filter_guided_wg_stealing.md`、`ideas_from_T14.md`、`README.md`、`figs/`、`site/`。
- 注意 `prompts/P12_measurements_for_T18_T19.md` 已恢复成你执行过的版本。
- 以 `cowork: ...` 提交，推送到 `isca27/docs`。
- 用户 10-20 的决定（细节在 SYNC）：
  - ISCA'27 只做通信；T18 保留；**T19 放弃**，不要启动 T19 的评估，STATUS 里 T19 的待决事项改成"放弃"。
  - m3（Remote Reuse）保留，但不作为重点。
  - 论文和文档里 RDMA 改叫"GPM 网络接口"，`-rdma-max-outstanding` 叫"远程请求跟踪表（RRT）项数"。代码和 flag 名不改。
- 顺带回答一个问题：T18 冒烟里"W ≥ 1024 时每 GPM 只有 40–75 个在途"是时间平均还是峰值、是否只算网络在途？PagedAttention 3.34× 时在途反而比 64 少，口径要说明。

**A. 新统计**（代码分支 `isca27/obs-stats`，从 `isca27/t18-merge` 当前头开出；评估用的二进制是 `ca8b831`，如果头不是它，说明差异）

所有新统计默认关闭，只挂被动 hook，在线聚合，不要逐请求写 trace（T17 一次运行有上千万个远程读）。

1. **E1 远程延迟分段**（`observations/README.md` 新 O2）
   - 时间点 t0–t6：L1V 交给本地网络接口 → 拿到 RRT 表项、注入 mesh → 到达 owner 网络接口 → 转给 L2 → L2 / HBM 响应回到 owner 网络接口 → 回到请求方网络接口 → L1V 收到。
   - 按跳数分桶，每段记 count、sum 和对数直方图，用来出 mean / p50 / p95；owner 服务分 L2 命中和缺失。
   - T18 打开时：被合并的请求另记"在合并里等待"的时间；多播响应按每个目的 GPM 记回程。
   - 核对 T17 `l2_summary` 远程延迟的起止点，和 E1 对上。
2. **RRT / owner 端占用**（新 O3）
   - 每个 GPM 每 1 µs 采样：已用表项、在等表项的请求数；owner 端网络接口收到的在途数也同样采样。
   - 按（请求方, 目的 GPM）只存聚合值：平均、最大、≥ 32 的时间比例。
   - 输出：占满（= 上限）的时间比例、平均等待数、每 GPM 实际远程带宽。
3. **链路利用率**：每条有向链路的总字节数，加每 1 µs 窗口的利用率；输出所有链路的平均、p99、最大，以及时间窗口上的最大值和 mesh 总字节数（E5 用）。
4. **远程复用和 L2 空余**（新 O7，m3 用）
   - 先找 HPCA 版的统计代码（WSC_datapath 的 `mechanism_1` 分支，HPCA 稿图 6、图 7），能复用就复用，口径和 HPCA 稿一致。
   - 要输出：每个请求方 GPM 读过的不同远程 line 里被读不止一次的比例、第二次及以后的读占全部远程读的比例、复用间隔分布、L2 空余比例（无效或直到被替换都没再被读的 way）。
   - T18 打开时另记：多播送达后又被同一 GPM 再读的比例。
5. 共享统计直接用 P12 的 `-report-m1-sharing`。

**回归**：
- 新统计全关时，fir、MM（`-max-wg 4096`）和 T18 冒烟里的 FW 配置与 `ca8b831` 逐位一致。
- 新统计全开时，total_time 和统计 md5（排除新文件）与全关时相同。
- 报告统计开销：墙钟、峰值内存。

**B. 运行 R1–R3**（只加统计，不需要配置确认；按 CONFIG.md 第 3 节自己确认后跑）
- workload 和规模和 T18 正式评估完全相同：
  - FW、I2C、PR、MT、MM：`-max-wg 32768`；SPMV：1536；KM：8192；
  - PagedAttention batch 66、MoE skew 1.75、R-GAT 320 种子。
- 配置：
  - **R1** mi200 baseline；
  - **R2** T18 评估里"只放大跟踪表 W=4096、不合并"那个配置；
  - **R3** T18 评估里"全开 W=1024"那个配置。
- 每个都加 A 的全部统计、`-report-m1-sharing`、`-report-l2-source -l2-source-tile-width=7`。统计全开如果太慢或内存太大，拆成两次运行（E1 一次，其余一次），说明怎么拆的。
- 共 30 个。并行数按用户 10-20 定的内存规则，和正在跑的 T18 评估共用机器。
- R1 的 total_time 必须和 T18 评估里同一 workload 的 baseline 一致，R2、R3 同理，不一致就停下来查。
- **输出**：
  - 位置：`results_index/obs/`，包括 `README.md`、`runs.csv`；
  - CSV：`obs2_latency_segments.csv`、`obs3_occupancy.csv`、`obs3_links.csv`、`obs5_sharing.csv`、`obs5_fanin_hist.csv`、`obs6_owner_shift.csv`、`obs7_remote_reuse.csv`，列按 `observations/README.md`；
  - 每个 CSV 配一张草图 PNG（`results_index/obs/draft/`），最终的论文图由 Cowork 画。

**C. 可以并行做的准备**（不需要确认的直接做，需要确认的只准备、不运行）
1. **m3 修复**（用户 10-20：m3 保留）
   - 修复内容：按 HPCA 稿 IV-C 的描述补 kernel 边界失效。在 L1 失效的同一时刻，失效请求方 L2 里保留的远程 line；未确认的写要等 owner 确认。
   - 检查 T18 和 m3 能不能同时打开。
   - 如果需要改 T18 或 m3 的设计（不只是接线），先写方案，⏸ 停下来等我。
   - 回归：baseline 和 T18 逐位一致；m3 修复前后在 fir、MM 上的差异写进 STATUS。
   - 修好后跑 **R8**：m3 单独、T18 全开 W=1024 + m3（Complete），10 个 workload，规模同上。
2. **mesh 尺寸的 flag**（规模实验 R5、R6 用）
   - 现在 7×7 好像是写死的，先确认。
   - 加 flag，默认 7×7、CPU 在中心，回归逐位一致。
   - 准备 3×3 / 5×5 的配置确认：弱缩放，每个 GPM 的工作量和数据量不变，workload 规模和 `-max-wg` 按 GPM 数等比例缩放，页放置规则不变。
   - 写出 FW、PR、MT、R-GAT、PagedAttention 在三种尺寸下的完整命令，⏸ **不要运行**。
3. **CACTI**（表 C）
   - 用和 HPCA 稿相同的方法（CACTI 7，32 nm）算 RRT：W = 256 / 1024 / 4096，cuckoo 索引的指纹阵列 + 旁路完整地址 + 等待者指针。
   - 同时算**同容量的全相联 CAM**做对比，再算 OMT（指纹 + 48 位位图 + 状态）。
   - 报告面积、访问时间、功耗。
   - CACTI 不在机器上就写清楚需要什么。
4. **配置确认的准备**（⏸ 只写、不跑）
   - **R4**：补 oracle。KM 8192 的 B / O1 / O2b / O3，以及其他和 T15 规模不一致的格子；参数和 T15 相同。
   - **R6**：微基准。3×3 / 5×5 上的跳数延迟和带宽；7×7 上远程带宽随 `-rdma-max-outstanding` 扫 64 / 256 / 1024 / 4096，1 跳和 6 跳，T14 诊断的方法。
   - **R7**：敏感性。`-bandwidth=24`（384 GB/s）、`-switch-latency=20`、L2 命中延迟加长（使 L2 命中约 150 ns，参数你提）× {baseline, T18 全开 W=1024} × FW、PR、MT、R-GAT、PagedAttention。
   - 每组列出完整参数、和 `mi200.json` 的差异、运行数、预计时间和内存。

**完成后**：
- 更新 STATUS：任务总览加 P13；"需要用户决定"写 C1 的方案（如果有）、C2 和 C4 的配置确认；结果摘要写 R1–R3、R8 的主要数字和 E1 的发现；日志。
- 追加 SYNC 日志，推送代码分支和 `isca27/docs`。
- ⏸ 停下来等我。

规则不变：
- 代码只由你修改。
- 不改 mi200 或 hpca 的任何参数；oracle、mesh 尺寸、敏感性都是有意的配置偏离，必须先给我确认。
- 所有运行保留 `-magic-memory-copy`，不跑 verify。
- 只推 `isca27/*` 分支（main 只在我要求时更新），不要 force push。
- 作废或被取代的结果目录直接删除（D22）。
- T19 不再推进。
---
