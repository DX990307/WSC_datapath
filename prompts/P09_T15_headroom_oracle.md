# P09：T15 oracle 实验（量出 wafer 层的优化空间）

---
继续 CuPath 的 ISCA 2027 工作。

**先同步**（SYNC.md 第 2 节）：读 SYNC 日志里你上一条之后的 `[cowork]` 条目，用 `git diff` 读 Cowork 改过的文件，包括 `00_background.md`（D19–D22、D19 的理由）、`CONFIG.md`、`ideas_from_T14.md`（10-16 的两节补充）、**新文件 `tasks/T15_headroom_oracle.md`**、`site/`、`prompts/P08`、`prompts/P09`（本文件）。以 `cowork: ...` 提交并推送到 `isca27/docs`。如果 P08 还没做完，先做完 P08（这一轮也要用到它整理的诊断 A 和 mi200 ablation 结果）。

**然后做 T15**（详见 `tasks/T15_headroom_oracle.md`）：
1. **确认用哪个 mi200 版本**：如果已经冻结（`isca27-mi200-v1`），就用冻结版；如果还没冻结，停下来问我（每个 L2 slice 的 MSHR 是否改为 128 还没定，改了的话 T15 要重跑）。
2. ⏸ **配置确认**：6 组（B、O1、O2a、O2b、O2、O3）的完整参数、和 `mi200.json` 的差异、每组的运行个数和估算时间。如果诊断 A 已经给出了 L1V、RDMA 的饱和点，O2a/O2b 的取值按它调整并说明。等我确认。
3. **运行**：14 个 workload × 6 组，短片段（`-max-wg 32768`，spmv 1536），全部加 `-report-l2-source`。长实验放 yss3；yss2 内存条有硬件故障，只放短的。
4. **有效性检查**：每格和 B 比较 kernel 数、WG 数、总指令数，差异超过 1% 的标为“oracle 无效”。
5. **写结果**：按 T15 的格式写进 STATUS（每个 workload 一行，加几何平均），CSV 放进 `results_index/oracle/`。另外用几句话回答 T15 开头的 4 个问题。
6. 完成后：更新 STATUS → 追加 SYNC 日志 → 推送两个仓库 → ⏸ 停下来等我。

规则不变：代码只由你修改，这一轮不需要改模拟器；不碰 `[提案]` 的任务；需要改动 mi200 或 hpca 配置的任何数值时，立刻停下来问我。
---
