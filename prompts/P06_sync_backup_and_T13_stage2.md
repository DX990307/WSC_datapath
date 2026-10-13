# P06：建立同步与备份 + T13 阶段 1 的答复 + 继续阶段 2（取代 P05）

---
继续 CuPath 的 ISCA 2027 工作。这条消息包含三部分，按顺序做。

**分工（用户 10-13 明确）**：代码（模拟器、benchmark、运行脚本、分析脚本）只由你修改；Cowork 只读代码，只改 `~/Desktop/CuPath/isca27/` 下的文档。所有 git 提交和推送都由你负责。

## 第一部分：先同步，再建立 GitHub 备份（不要打断正在跑的实验）

1. **同步**：读 `isca27/SYNC.md` 全文（新文件：同步与备份规则 + 变更日志），以及 README 新增的“同步与备份”一节。然后读 Cowork 在 10-13 改过的文件：`README.md`、`SYNC.md`、`tasks/T12_driver_flush_fix.md`（新增的加注）、`site/`（记录网页的源文件备份）、`prompts/P05`、`prompts/P06`（本文件）。
2. **建立文档仓库**：在 `~/Desktop/CuPath/isca27/` 里 `git init`，加 `.gitignore`（`.DS_Store` 等）。在 GitHub 新建**私有**仓库 `DX990307/cupath-isca27`（**建之前先告诉我**），设为 remote，第一次提交 `cowork: initial import of isca27 docs` 并推送。
3. **补备份已完成的任务**：
   - 代码仓库：把 `isca27/t11-probe-placement-comm`、`isca27/t12-driver-flush-fix` 推送到 origin；`isca27/t13-mi300x` 只推送**已经提交**的部分。只推这几个任务分支，不碰已有分支，不 force push。
   - 把 T11、T12 每个实验的 `meta.json` 和 `summary.csv` 复制到 `isca27/results_index/<exp_id>/`，提交并推送。
4. 在 SYNC.md 日志里追加一条 `[claude-code]`，写明做了什么，以及两个仓库推送后的 commit hash。
5. **以后严格按 SYNC.md 第 2 节执行**：每次开工先同步（读 SYNC 日志 + `git diff` 读 Cowork 的改动，以 `cowork: ...` 提交并推送）；每个任务或⏸停点完成后，更新 STATUS → 追加 SYNC 日志 → 推送两个仓库。

## 第二部分：T13 阶段 1 的答复（STATUS 里 3 条待决事项）

参数定位表确认。3 条待决事项的答复：
1. **旧配置的 HBM 带宽**：同意。阶段 3 校准时，旧配置也用同一个微基准实测，以实测值为准。
2. **新 HBM channel**：选**方案 A**：64-bit 总线、BL8、每次访问 64B、DRAM 时钟 1.23 GHz、时序按纳秒不变换算。**延迟不做每周期等效缩放**，把这一点写进 STATUS 和 meta。最终以阶段 3 的实测为准。
3. **修改配置文件**：同意方案 A。预设的取值集中写在 `akkalat/baseline/runner/gpm_preset.go`，原文件只做“常量 → 预设字段”的替换，**旧配置的数值一个都不改**。

把这三条在 STATUS 里标为已决定。

**另外核实两处论文和代码的不一致，结果写进 STATUS**：
- (a) HPCA 投稿版实验的 HBM 带宽实际是多少（论文 Table II 写的是每 GPM 0.375 TB/s，代码算出来是 128 B/周期）；
- (b) HPCA 投稿版实验的 typed filter 实际用了多少个 bucket 和槽（论文写的是每分区 4K bucket × 4 槽 = 16K 槽、每 GPM 168 KiB；代码默认是 2 × 块数 = 每分区 32K 槽）。翻一下当时的运行参数和 meta 确认；再查一下 CACTI 面积是按哪个容量算的。

## 第三部分：继续 T13 阶段 2

- 我看到工作区里已经有还没提交的 `gpm_preset.go` 和 `shaderarray.go` 改动。先对照第二部分的答复检查一遍，不一致的地方改过来。
- ⏸ 开始运行之前，先按 `CONFIG.md` 第 3 节做配置确认，等我确认。
- 阶段 2 的验收标准：
  - `-gpm-preset` 默认值等于旧配置，用旧配置做回归，**结果完全一致**（统计排序后逐项比较，`total_time` 单独比较）；
  - 检查分析脚本里有没有假设 L2 只有 4 个分区的地方；
  - 所有运行都保留 `-magic-memory-copy`。
- 阶段 2 完成后：更新 STATUS → 追加 SYNC 日志 → 推送两个仓库 → ⏸ 停下来等我确认，然后才进入阶段 3（校准）。

规则不变：不碰 `[提案]` 的任务（T4 的 cu8、T7–T10）；以下情况立刻停下来问我：旧配置回归结果不一致；需要改动旧配置的任何数值；论文和代码还有别的不一致。
---
