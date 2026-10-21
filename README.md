# CuPath → ISCA 2027 工作目录

> 维护：dx（决策在 Cowork 中讨论，由 Cowork 侧的 Claude 同步到这里）。最后更新：2026-10-21（"当前进展与 idea 汇总"一节）。
> 这里**一个任务对应一个文档**。为了让每个文档单独打开也能看懂，必要的背景会在各文档里重复。

## 项目一句话

论文 CuPath（HPCA 2027 #1329）第一轮被拒，评分 1 WA + 5 WR。现在改投 **ISCA 2027**。截稿时间按 ISCA 2026 推算：摘要约 **11/9**，全文约 **11/16**，以 CFP 发布为准。主要改动有三块：补齐强 baseline、换用现代 workload、改进机制以提升新颖性。

## 当前进展与 idea 汇总（10-21，Claude Code 按用户要求写入）

> 数据出处：`results_index/abl3c384/`（384 GB/s 主配置消融）、`results_index/m2w/`（W 和带宽的敏感性）、`results_index/dm/`（data movement 离线分析）、`results_index/pm/`（page migration，在跑）。摘要都在 `STATUS.md`。以下"新 idea"全部是 `[提案]`，没有用户确认前不写代码。

### 1. 主要结论

- **模拟器**：修了两个 timing bug（barrier 缓冲区满跳指令、kernel 启动不失效 L1），`isca27/cupath-v3` @ `4235991`；修复前的结果不可信。还有一个原有 bug 没修：L2TLB 直接用 IOMMUCache 的端口发送，丢唤醒，会挂死（MG 的 M1+M2）或造成翻译延迟尖峰。
- **主配置**（384 GB/s、W = 64，25 个 workload）：Complete 对 baseline GMEAN **1.73×**。瓶颈是请求方等那 64 个窗口位（链路平均只用 1–5%）。
- **W 的敏感性**：W 放大到 256，baseline 自己快约 3 倍，Complete 的收益大幅缩小；每 GPM 的在途读还会被 L1V 封顶在 28 × 16 = 448（`-l1v-mshr-entries` 和 `-l1v-max-concurrent-trans` 两个都是 16）。
- **网络紧张、表和带宽匹配**（48 GB/s ≈ 220 CU 配 384 GB/s 的每 CU 比例，W = 256）：6 个 workload 的 Complete GMEAN 2.71×（不含 PR 1.86×），放大窗口不再削弱收益；剩下 19 个 workload 在跑。
- **用户的判断（10-21）**：带宽大概率不是瓶颈，wafer 相关的是 **hop**（每跳 32 cycle，对角线单程约 400 ns）。数据支持的说法：hop 通过"往返时间 × 有限的在途资源"限制吞吐（吞吐 = W × 64 B ÷ RTT），wafer 直径越大，需要的表越大，所以**减少往返次数比减少字节更重要**。
- **热点**：7 个 workload（MM、PR 100%，npbcg 92%，graphtc 79%，atax 67%，shocsort 63%，FW 54%）的远程读集中在一个 owner，**都在角落**（tile 0 / 48：小于 192 KB 的 buffer 整个放在 GPM 0、余数页放在最后一个 GPM）；角落只有 2 条出链路，先 Y 后 X 路由让 PR 87% 的出流量走一条链路。其余 18 个基本均匀。
- **区域性共享**：同时读同一条 line 的 GPM 多数是邻居（MG、shocsort、CFD、MoE、PA 的共享者平均相距 1.9–2.6 跳，随机是 4.7 跳；npbcg 88%、MG 79%、PR 69% 的最近共享者在 1 跳内）。图和检索类（sssp、bfs、IVF）是随机的。
- **page migration**（对比 baseline，新写的迁移管理器，分支 `isca27/page-migration`，设计见 `results_index/pm/DESIGN.md`）：目前只在 PR 上有收益（fault-4K 2.9×，Complete 13.3×）；bfs、sssp、allreduce 上变慢，2 MB 最差（0.29–0.44×）；零软件开销的理想版本几乎不变 → 问题在于页被很多 GPM 共享，不在软件开销。

### 2. 现有机制的去留

| 机制 | 结论 | 依据 |
|---|---|---|
| M1 打包 | **保留**，最重要 | 去掉慢 22%；作用是让一个窗口位带 1.8–2.1 条 line（按字节只省 3–15%） |
| M1 请求方合并 | **保留** | 去掉慢 11%；MM、atax、FW 上最大 |
| M3 请求方 L2 保留 | **保留**，省数据搬运的主力 | 去掉慢 21%；省下的字节×跳数主要来自它 |
| M1 owner 合并 | 待定，倾向删 | 合并率 0–18%，没单独测过 |
| M2 网内多播 | **删或重做** | 所有条件下 ±3%；共享在时间上相隔几百 ns 到几 µs，owner 合并窗口只有 10–20 ns |
| M1 按距离分配（rtt / near） | **删**，改共享池 | shared 与 rtt ±1%；near 更差；唯一例外 allreduce @ W=256、48 GB/s rtt 快 18%（疑似限流作用，未确认） |

### 3. 新 idea（全部 `[提案]`）

**A. 有数据支持、上限较高**

| # | idea | 上限 / 依据 | 状态 |
|---|---|---|---|
| A1 | **M3 跨 kernel 保留只读远程 line**：kernel 边界只作废被写过的 line（owner 记录写过的页 / line，或写纪元）；重用距离长时放宽"两次触碰才保留" | M3 之后剩余网络 line 平均省 32%（npbcg 84%、MG 64%、CFD 62%）；npbcg 141 万次填入 135 万在边界被作废；L2 70–95% 空闲 | 离线分析完成，**最推荐** |
| A2 | **从邻居取**：1–2 跳内的邻居 L2 已有副本就从邻居拿，不去 5–6 跳外的 owner | 共享者多数是邻居（见上）；省跳数、缩短 RTT、分散热点 | 上限未算 |
| A3 | **子页放置**（用户 10-21 提出）：一页的不同部分放到各自使用者的区域，例如 GPM 1–10 用前 2 KB、11–20 用后 2 KB，就把两半分别放到 GPM 4 和 GPM 14。可以在 RDMA 加一张"子页重定向表"实现，不改页大小和 TLB | 可能拆散角落的小 buffer 热点，也解决页迁移的"页级伪共享"来回搬；对广播数据（PR rank 向量）和随机访问无效 | 需要先用页共享 trace（`-page-sharing-trace`，要确认记录了页内偏移）确认模式 |
| A4 | **热点小数据放中间或复制** | 放中间：PR 最忙链路负载 ×0.63；4 份副本：×0.15，字节×跳数 ×0.27；M3 已替 PR 做了复制，主要在 48 GB/s 下有用 | 离线分析完成；论文里作为放置问题单独交代 |

**B. 有潜力但风险大**

| # | idea | 上限 | 风险 |
|---|---|---|---|
| B1 | 按共享者主动推送（替代 M2）：owner 记住最近读者，读时多播并存入其 L2 | oracle K=4：sssp 31%、IVF 36%、bfs 21%、ar 17% 的远程读 | 无用推送 13–99%，需要预测器；和 A1 重叠 |
| B2 | 分配方式改成拥塞控制（按实测 RTT 限制发往拥塞目的地的在途数） | 只有 allreduce 一个线索（+18%） | 先跑 rtt 对 shared 带完整统计的对照 |
| B3 | 按准确率开关预取 | 无用预取让 ar、mt、mg 多出 45–130 MB·跳 | 收益小 |

**C. 基本放弃**：owner 等待后再多播（字节省 1–5%，W=64 下净负）；压缩（384 GB/s 下串行化每跳只有约 0.25 ns，没有性能空间；需要的只读统计提案在 `results_index/dm/analysis.md`）；单纯加大 MSHR。

### 4. 论文定位（讨论中）

- 核心论点：wafer 上远程访问的代价由 hop 决定，GPM 的在途资源跟不上 wafer 的直径 → 减少往返次数、减少每次往返占用的资源。
- 实验：W 作为变量扫描（64 / 128 / 256）；wafer 规模扫描（5×5 / 7×7 / 9×9，有 `isca27/mesh-size` 分支）；48 GB/s 网络紧张场景作补充；page migration 作为"为什么不直接迁移页"的对照。

### 5. 待用户决定

1. 是否实现 A1（M3 跨 kernel 保留只读数据）。
2. 是否先做 A2、A3 的数据分析（从邻居取的上限、子页访问模式）。
3. 是否安排 wafer 规模扫描。
4. 什么时候修 L2TLB→IOMMU 丢唤醒 bug（修了会改时序）。
5. yss2 上挂住的 MG 进程（PID 2415098，v3c384）要不要杀。

## 状态标记

| 标记 | 含义 | Claude Code 怎么处理 |
|---|---|---|
| `[已定]` | 用户已拍板 | 可以直接做 |
| `[提案]` | 讨论中提出，用户还没确认 | **不要写代码**，只能读代码、做调研 |
| `[待验证]` | 要看实验数据 | 等对应数据出来、用户决定之后再做 |

## 文件索引

| 文件 | 内容 | 状态 | 周次 |
|---|---|---|---|
| [`SYNC.md`](SYNC.md) | **同步与备份规则 + 三方的变更日志** | 必读 | — |
| [`CONFIG.md`](CONFIG.md) | **论文配置（不可修改）和每个 task 执行前的确认步骤** | 必读 | — |
| [`00_background.md`](00_background.md) | 论文现状、审稿意见、决策和理由、相关工作 | 参考资料 | — |
| [`STATUS.md`](STATUS.md) | **Claude Code 写的进度汇报**（Cowork 侧会读） | 持续更新 | — |
| [`tasks/T0_infra.md`](tasks/T0_infra.md) | 实验脚本：run / sync / pull / meta | `[已定]` | W1 |
| [`tasks/T1_isa_check.md`](tasks/T1_isa_check.md) | 检查 MGPUSim 是否支持 atomic 等指令 | `[已定]` | W1 第 1 天 |
| [`tasks/T2_stats.md`](tasks/T2_stats.md) | 新增统计：M1 共享度、M2 跨 kernel 复用、M3 跳数等 | `[已定]` | W1 |
| [`tasks/T3_baselines.md`](tasks/T3_baselines.md) | 强 baseline B1–B4 | `[已定]` | W1 |
| [`tasks/T4_configs.md`](tasks/T4_configs.md) | `cu8` / `mi100match` 配置（**偏离论文配置，等用户确认**） | `[提案]` | — |
| [`tasks/T5a_llm_decode_prefix.md`](tasks/T5a_llm_decode_prefix.md) | 新 workload：共享前缀的 LLM decode | ~~`[已定]`~~ 被 T17 的 PagedAttention 取代 | W1–W2 |
| [`tasks/T5b_gcn_arxiv.md`](tasks/T5b_gcn_arxiv.md) | 新 workload：GCN（ogbn-arxiv） | ~~`[已定]`~~ 被 T17 的 R-GAT 取代 | W1–W2 |
| [`tasks/T5c_dlrm_emb.md`](tasks/T5c_dlrm_emb.md) | 新 workload：DLRM embedding bag | ~~`[已定]`~~ 降为 T17 的备选 W-D | W1–W2 |
| [`tasks/T5d_ssb_join.md`](tasks/T5d_ssb_join.md) | 新 workload：SSB hash join | ~~`[已定]`~~ 暂停（10-16） | W1–W2 |
| [`tasks/T6_truncation_check.md`](tasks/T6_truncation_check.md) | 验证截断运行的结果是否具有代表性 | `[已定]` | W1 |
| [`tasks/T7_mech1_region_replication.md`](tasks/T7_mech1_region_replication.md) | 机制 1：按距离分区域的远程复制 | `[提案]` | W2–W3 |
| [`tasks/T8_mech2_write_signature.md`](tasks/T8_mech2_write_signature.md) | 机制 2：基于写签名的选择性失效 | `[提案]` | W2–W3 |
| [`tasks/T9_workloads_batch2.md`](tasks/T9_workloads_batch2.md) | 第二批 workload：MoE、GAP 图计算、stencil | ~~`[提案]`~~ MoE 移到 T17；其余暂停 | W2+ |
| [`tasks/T10_spmd_comm_suite.md`](tasks/T10_spmd_comm_suite.md) | 自写的通信型 benchmark 套件（SPMD 分片 + 显式通信） | `[提案]` | W1–W3 |
| [`tasks/T11_probe_placement_comm.md`](tasks/T11_probe_placement_comm.md) | 探针实验：数据放置、kernel 放置、远程访问路径（T10 的前置任务） | `[已定]` | W1 |
| [`tasks/T12_driver_flush_fix.md`](tasks/T12_driver_flush_fix.md) | 修复 driver 的 flush 竞争（多个 queue 共用一个 Context 时死锁） | `[已定]` · ✅ 完成（10-13，commit `9c30a89`，见 STATUS） | W1 |
| [`tasks/T13_mi300x_config.md`](tasks/T13_mi300x_config.md) | **GPM 改成 1/8 MI300X**（按比例缩放）+ 全量重跑 | `[已定]` | W1–W2 |
| [`tasks/T14_wafer_platform_data.md`](tasks/T14_wafer_platform_data.md) | wafer 平台特征数据（延迟阶梯、带宽、跳数矩阵、incast、通信模式、kernel 开销），和 T13 阶段 3 一起跑 | 第一档 `[已定]`；第二、三档 `[提案]` | W1–W2 |
| [`tasks/T15_headroom_oracle.md`](tasks/T15_headroom_oracle.md) | oracle 实验：量出 wafer 层的优化空间（全部本地 / 端点不受限 / 距离几乎免费） | `[已定]` | mi200 冻结后 |
| [`tasks/T16_position_imbalance.md`](tasks/T16_position_imbalance.md) | W1：GPM 在 mesh 上的位置是否造成负载不均（阶段 1 只测量） | 阶段 1 `[已定]`，阶段 2 `[提案]` | 和 T15 一起 |
| [`tasks/T17_latest_workloads.md`](tasks/T17_latest_workloads.md) | 新 workload：PagedAttention、MoE、R-GAT（DLRM 备选），不用 atomic、统一 pull，每个实验 3–5 小时 | `[已定]` | 10-27 前跑出 baseline |
| [`tasks/T18_new_m2_two_sided_merge.md`](tasks/T18_new_m2_two_sided_merge.md) | 新 m2：请求跟踪表规模化 + owner 端跨 GPM 合并多播（相对 HPCA 版 m2 的新增部分；前提：地址转换由 HDPAT 解决） | 实现完成（`isca27/t18-merge` @ `ca8b831`），正式评估运行中 | 10-20 |
| [`tasks/T19_filter_guided_wg_stealing.md`](tasks/T19_filter_guided_wg_stealing.md) | cuckoo filter 指导的就近 WG 窃取 | `[下一篇论文]`（10-20：ISCA'27 只做通信，见 `next_paper_scheduling.md`）；代码已实现（`isca27/t19-steal` @ `eaa9c73`），冒烟结果在 STATUS | — |
| [`observations/README.md`](observations/README.md) | ISCA'27 observation 一节要收集的数据：删 O6（Local Pairing）、换 O2/O3，新 O1–O6 各自的数据、统计定义、运行清单 | `[提案]`，等用户确认后写提示词 | 10-20 |
| [`prompts/P13_observation_data.md`](prompts/P13_observation_data.md) | 给 Claude Code：observation / evaluation 第一批数据（新统计 + R1–R3、m3 修复 + R8、mesh 尺寸 flag、CACTI、R4 / R6 / R7 的配置确认准备） | 待执行 | 10-20 |
| [`prompts/P14_config_14cu.md`](prompts/P14_config_14cu.md) | 给 Claude Code：论文配置改为每 GPM 14 个 CU（D23）——新预设、校准、28 对 14 CU 验证、workload 重新定规模 | 待执行 | 10-20 |
| [`prompts/P15_complete_w64.md`](prompts/P15_complete_w64.md) | 给 Claude Code：Complete 不用 1024 窗口（`t18-window` 64，距离 credit 开，表默认 128 槽），15 个 W2 benchmark × baseline / M1+M2+M3 = 30 个运行，二进制 `d09b18b` | 待执行 | 10-20 |
| [`prompts/P16_cupath_v3_ablation.md`](prompts/P16_cupath_v3_ablation.md) | 给 Claude Code：按新结构实现（M1 RDMA 端 = 合并 / 打包 / 分配，新增"近处多留"份额；M2 网络 = 多播；M3 保留），再跑小 footprint 消融：26 个 workload × 9 个配置，baseline ≤ 1 小时、每个 benchmark 用到全部 48 个 GPM | 待执行 | 10-20 |
| [`prompts/P17_data_movement_next.md`](prompts/P17_data_movement_next.md) | 给 Claude Code：接手 10-21 的进展（收尾在跑的实验 → A2/A3 数据分析 → ⏸ 等用户选 A1 实现、wafer 规模扫描、IOMMU bug） | 待执行 | 10-21 |
| [`prompts/P18_astrasim_crosscheck.md`](prompts/P18_astrasim_crosscheck.md) | 给 Claude Code：用 ASTRA-sim（或 BookSim2 / Garnet）独立交叉验证网络结论（384 GB/s 下网络不是瓶颈、W×RTT、48 GB/s 下 µs 级排队、角落热点链路）；先调研 ⏸ 再搭建 | 待执行 | 10-21 |
| [`guides/G1_port_workload_to_mgpusim.md`](guides/G1_port_workload_to_mgpusim.md) | **操作指南**：怎么把新 workload 移植进 MGPUSim（以 GCN 为例） | 参考资料 | — |

## 时间线

| 周次 | 日期 | 内容 |
|---|---|---|
| W1 | 10/12–10/18 | T0–T6：测量、强 baseline、新配置、开始移植 workload → **go/no-go 关口** |
| W2–W3 | 10/19–11/1 | T7、T8（前提是 W1 通过关口并且用户确认）；第一批 workload 完成 |
| W4 | 11/2–11/8 | 全量实验 + 敏感性分析；约 11/9 注册摘要 |
| W5 | 11/9–11/16 | 写作 |

**go/no-go 关口**：W1 结束时，用户根据 M1（跨 GPM 共享度）和 M2（跨 kernel 复用）决定是否走新主线（T7、T8）。如果不走，就退回“保留原机制、补齐评估”，并考虑改投 MICRO 2027。

## 通用实验约定 `[已定]`（每个任务文档里也会重复）

1. **代码只在本机修改。** 服务器上只做同步、编译、运行，不允许改代码。
2. **工作区不干净时禁止提交实验。** `git status --porcelain` 输出非空时，`run_exp.sh` 直接报错退出。
3. **两台服务器使用同一个 commit。** 每批实验开始前先同步代码，再在服务器上编译。Go 版本固定，并写进 meta。
4. **每次运行都产出 `results/<exp_id>/`**，包含 `meta.json`、`summary.csv`、`stats/`、`stdout.log`、`stderr.log`。
5. **`exp_id` 命名**：`<MMDD>_<workload>_<config>_<mechanism>_<短hash>`。
6. **用 GNU parallel 加 `--sshloginfile` 分发到两台服务器**，打开 `--joblog` 和 `--resume-failed`。
7. **结果用 rsync 拉回本机的 `results/`**，分析只用本机这一份。
8. **page placement 只用两种**：`local-aware`（现有）和 `page-interleave`。不要为了制造远程流量去挑差的 placement。
9. **新 kernel 一律用 fp32**，不用矩阵核心或 MFMA。
10. **`summary.csv` 加字段只在末尾追加**，见 `tasks/T0_infra.md`。

## 实验配置：论文配置不可修改 `[已定]`（用户 10-13）

**之前设置的配置就是论文始终使用的配置，不要改。** 完整内容和检查方法见 [`CONFIG.md`](CONFIG.md)。
**每一个 task 执行之前，都要按 CONFIG.md 第 3 节做配置确认，并等用户确认之后才能运行。**

## 同步与备份 `[已定]`（用户 10-13）

**每完成一个任务就备份到 GitHub；每次和 Cowork 或 Claude Code 聊完，另一方都要先读改动再继续。**
**分工：代码（模拟器、benchmark、运行脚本、分析脚本）只由 Claude Code 修改；Cowork 只读代码，只改 `isca27/` 下的文档。** 详细流程见 [`SYNC.md`](SYNC.md)。要点：
- Claude Code 开工前：读 SYNC.md 日志 → `git diff` 读 Cowork 未提交的改动 → 以 `cowork: ...` 提交并推送。
- Claude Code 每个任务或⏸停点完成后：更新 STATUS → 在 SYNC.md 追加日志 → 推送代码的任务分支和文档仓库 → 把 meta.json 和 summary.csv 复制到 `results_index/`。
- 只推送 `isca27/...` 任务分支；不推送已有分支，不 force push。

## Claude Code 的工作方式

1. 每次开始工作前，**先按 `SYNC.md` 第 2 节同步**，再读本文件和 `CONFIG.md`，再读要做的那个任务文档。需要背景时读 `00_background.md`。
2. **每个 task 执行之前，先按 `CONFIG.md` 第 3 节做配置确认**：检查配置文件有没有变，打印完整参数，列出和论文配置的差异，**等用户确认之后再运行**。
3. 只做 `[已定]` 的任务。遇到 `[提案]` 或 `[待验证]` 的内容，先停下来问用户。
4. 每完成一个阶段、每遇到阻塞、每有需要用户决定的事情，都更新 `STATUS.md`，格式见该文件。
5. 任务文档里的规格和代码现实不符时（比如文件位置、接口不一样），以代码为准，并在 `STATUS.md` 里记录差异。
6. **不要修改本目录下的任务文档**（STATUS.md 除外）。需要改的话写进 STATUS.md，由用户在 Cowork 侧更新。
