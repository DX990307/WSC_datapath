# P05：建立同步与 GitHub 备份（在下一个⏸停点做，做完再继续当前任务）

---
用户 10-13 新增了两条要求：**每完成一个任务就备份到 GitHub；每次和 Cowork 或你聊完，另一方都要先读改动再继续。** 规则写在 `~/Desktop/CuPath/isca27/SYNC.md`，README 也加了一节“同步与备份”。

请在当前任务的下一个⏸停点（不要打断正在跑的实验）做下面这些事：

1. **先同步**：读 `isca27/SYNC.md`（全文），以及 README 新增的“同步与备份”一节。然后读 Cowork 在 10-13 改过的文件：`README.md`、`SYNC.md`、`tasks/T12_driver_flush_fix.md`（新增的加注）、`site/`（记录网页的源文件备份）、`prompts/P05`（本文件）。
2. **建立文档仓库**：在 `~/Desktop/CuPath/isca27/` 里 `git init`，加 `.gitignore`（`.DS_Store` 等）。在 GitHub 上新建**私有**仓库 `DX990307/cupath-isca27`（用 gh CLI 或者让我手动建；建之前先告诉我），设为 remote，第一次提交时把当前所有文件作为 `cowork: initial import of isca27 docs` 推送上去。
3. **补备份已完成的任务**：
   - 代码仓库：把 `isca27/t11-probe-placement-comm`、`isca27/t12-driver-flush-fix`，以及 `isca27/t13-mi300x` 上**已经提交**的内容推送到 origin（只推这几个任务分支，不碰已有分支，不 force push）。T13 阶段 2 没提交的改动先不要推。
   - 结果索引：把 T11、T12 每个实验的 `meta.json` 和 `summary.csv` 复制到 `isca27/results_index/<exp_id>/`，提交并推送。
4. **在 SYNC.md 的日志里追加一条 `[claude-code]`**：写明做了什么，以及两个仓库推送后的 commit hash。
5. 以后严格按 SYNC.md 第 2 节执行：**每次开工先同步；每个任务或⏸停点完成后，更新 STATUS → 追加 SYNC 日志 → 推送两个仓库**。

做完之后停下来告诉我，然后再继续 T13（T13 阶段 1 待决事项的答复，我会另外发给你）。
---
