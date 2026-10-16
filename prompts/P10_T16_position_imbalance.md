# P10：T16 阶段 1（位置导致的负载不均，只测量）+ 和 T15 合并运行

---
继续 CuPath 的 ISCA 2027 工作。

**先同步**（SYNC.md 第 2 节）：读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目，用 `git diff` 读 Cowork 改过的文件：`ideas_from_T14.md`（10-16 新增 placement 的补充，以及“placement 以外的办法”W1–W6）、`tasks/T15_headroom_oracle.md`（10-16 补充）、**新文件 `tasks/T16_position_imbalance.md`**、`README.md`、`site/`、`prompts/P10`（本文件）。以 `cowork: ...` 提交并推送到 `isca27/docs`。

**然后做 T16 阶段 1**（详见 `tasks/T16_position_imbalance.md`）：
1. **核实 Cowork 读代码时发现的三件事**：WG 静态连续切分（`distributeWGToGPUs`）、页按 48 等分（`selectActualGPUForDistributedPage`）、`-max-wg` 是全局计数，到了就直接退出。另外确认 `UnifiedGPUIDs` 的下标 → driver ID → mesh 坐标的对应关系。如果和 Cowork 说的不一样，写进 STATUS。
2. **1a：实现只做统计的 per-GPM tracer**（字段见 T16），用参数开关，默认关闭。回归：mi200 下 fir 和 MM 短片段，开和关的总周期数必须完全一致。不一致就停下来报告。
3. ⏸ **配置确认**：用冻结版 `isca27-mi200-v1`。列出 T16 阶段 1 所有运行的参数（不应该和 `mi200.json` 有任何差异），以及 1c、1d 的输入规模、每组的运行个数和估算时间。
   - 如果 T15 还没开始跑，建议在 T15 的 6 组里都打开这个 tracer，这样 1b 不用单独再跑。T15 的配置确认和这一步一起给我看。
   - 等我确认。
4. **运行**：
   - 1b：T15 的运行（或者单独的 B 组）都打开 tracer。
   - 1c：在 mi200 上补跑 T14 的 all-to-all；在 platbench 里加远程比例 f 的参数（f = 0、0.1、0.25、0.5、1），规模要能跑完整个 kernel。Cowork 已经从 T14 的 `metrics.csv` 读出 hpca 的结果（最慢/平均 1.23），见 T16 的“已有的证据”。
   - 1d：给 PR、I2C、MT、SPMV、KM、MM、AES 选能跑完一个 kernel 的小输入，选不出来就报告。
   - 长实验放 yss3，yss2 只放短的。
5. **写结果**：按 T16 的“输出”格式写进 STATUS，并给出 7×7 热力图（1c 和 1d），CSV 和图放进 `results_index/t16/`。用几句话回答：
   - (i) 位置差是否存在、有多大？
   - (ii) 不均里有多少和位置无关（后面的 GPM 没活干、GPM 1 热点）？
   - (iii) 按判断标准，W1 是否成立？
6. 完成后：更新 STATUS → 追加 SYNC 日志 → 推送两个仓库（tracer 和微基准提交到新的 `isca27/t16-position` 分支）→ ⏸ 停下来等我。

规则不变：
- 代码只由你修改。
- 阶段 2（M-a、M-b、M-c）是 `[提案]`，不要开始。
- 任何需要改动 mi200 或 hpca 配置数值的地方，立刻停下来问我。
- 只推 `isca27/*` 分支，不要 force push。
---
