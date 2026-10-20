# CuPath → ISCA 2027 工作目录

> 维护：dx（决策在 Cowork 中讨论，由 Cowork 侧的 Claude 同步到这里）。最后更新：2026-10-12。
> 这里**一个任务对应一个文档**。为了让每个文档单独打开也能看懂，必要的背景会在各文档里重复。

## 项目一句话

论文 CuPath（HPCA 2027 #1329）第一轮被拒，评分 1 WA + 5 WR。现在改投 **ISCA 2027**。截稿时间按 ISCA 2026 推算：摘要约 **11/9**，全文约 **11/16**，以 CFP 发布为准。主要改动有三块：补齐强 baseline、换用现代 workload、改进机制以提升新颖性。

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
