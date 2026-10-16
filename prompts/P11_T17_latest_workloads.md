# P11：T17 新 workload（PagedAttention、MoE、R-GAT；DLRM 备选）

---
继续 CuPath 的 ISCA 2027 工作。

**先同步**（SYNC.md 第 2 节）：读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目，用 `git diff` 读 Cowork 改过的文件：
- `ideas_from_T14.md`：10-16 新增的几节：placement 以外的办法、W1 的已有证据、新 workload、模拟器约束、模拟速度预算
- **新文件** `tasks/T17_latest_workloads.md`
- `tasks/T16_position_imbalance.md`
- `tasks/T5a/T5b/T5c/T5d/T9`（开头加了“被取代”的说明）
- `README.md`、`figs/`、`site/`、`prompts/P10`、`prompts/P11`（本文件）

以 `cowork: ...` 提交，推送到 `isca27/docs`。

**顺序**：如果 P10（T16 阶段 1 + T15）还没做到它的停止点，先做完 P10 的前两步（核实 + tracer 回归），在 P10 的配置确认处停下。然后在等我确认、或者服务器在跑实验的时候，开始做 T17 的开发。T17 的开发不占服务器的正式实验位置。

**T17**（详见 `tasks/T17_latest_workloads.md`）：
1. **第 0 步：指令检查（原 T1）**。
   - 核实 T17 里写的模拟器约束：global atomic、scratch、DS 指令、编译链。
   - 给 W-A、W-B、W-C 各写一个最小的 kernel，用 emu 和 timing 各跑一次小输入，把 panic 的 opcode 列成表。
   - 在编译脚本里加一步检查：用 `llvm-objdump` 扫描 `buffer_`/`scratch_`、`flat_atomic` 和没实现的 `ds_` 指令，发现就报错。
   - 缺 ALU 指令时 ⏸ 先问我，不要直接改模拟器。
2. **实现三个 workload**：按 T17 的“写法原则”（unified、不用 atomic、pull、索引在 host 端算、真实的分布）。
   - 先做 W-A（PagedAttention，算子大多现成），再做 W-B、W-C。
   - 需要下载数据集（长度分布、IGB）时，⏸ 先报告来源、大小和许可。
3. **合理性检查**：kernel 数、WG 数、footprint > 96 MB、下标不越界；用 emu 跑一个极小输入，和 host 端的参考实现比对一次。
4. **定规模**：
   - 按 1/8 规模试跑 mi200 baseline，用心跳测速度和流量，外推到 baseline 约 3 小时。
   - 不用 `-max-wg`，超过 5 小时就缩小输入。
   - ⏸ **配置确认 + 规模确认**：把每个 workload 的完整参数（应该和 `mi200.json` 没有差异）、规模、预计耗时、峰值内存给我看，等我确认。
5. **第一轮正式运行**：每个 workload 跑 baseline，加 `-report-l2-source`，有 T16 的 tracer 就一起打开。报告：远程比例、远程延迟占比、owner 集中度、每个 GPM 的结束时间热力图、wall 时间、峰值内存。结果写进 STATUS，CSV 放进 `results_index/t17/`。
6. 完成后：更新 STATUS → 追加 SYNC 日志 → 推送两个仓库（代码放在新分支 `isca27/t17-workloads`）→ ⏸ 停下来等我。

**MoE 的两件事等我决定**：expert 负载不均的程度（T17 建议最大/平均约 1.5–2，另跑一个均匀版作对照）；以及远程比例太低时要不要改成 prefill。在这之前用建议值做开发。

规则不变：
- 代码只由你修改。
- 不改 mi200 或 hpca 的任何参数。
- 所有运行保留 `-magic-memory-copy`，不跑 verify。
- 只推 `isca27/*` 分支，不要 force push。
- `[提案]` 的部分不要开始。
---
