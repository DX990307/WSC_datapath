# 启动 T11 的 prompt（复制下面的分隔线之间的全部内容，粘贴给本地 Claude Code）

---
我们在为论文 CuPath 改投 ISCA 2027 做准备。所有背景、约定和任务都在 `~/Desktop/CuPath/isca27/` 里。

**开始前先读这 3 个文件：**
1. `~/Desktop/CuPath/isca27/README.md`：索引、状态标记、通用实验约定
2. `~/Desktop/CuPath/isca27/tasks/T11_probe_placement_comm.md`：这次的任务
3. `~/Desktop/CuPath/isca27/STATUS.md`：进度汇报的格式

**这次只做 T11，按下面的顺序：**

**阶段 A：只看代码，不运行任何东西。**
回答 T11 第 0 步的 5 个问题：fork 的版本；`SelectGPU`、`CreateCommandQueue`、`EnqueueLaunchKernel`、`DrainCommandQueue`、`AllocateMemory`、`Remap`、`Distribute` 是否存在、签名是什么；有没有 `benchmarks/mccl`；wafer 平台上远程请求走的是哪个组件，哪个统计能区分远端 L2 命中和远端 HBM；现有 benchmark 用的是 unified GPU 还是每个 GPM 一个队列。
每个回答都要附上文件路径和行号。写进 `STATUS.md`，然后**停下来，把阶段 A 的结论总结给我，等我确认之后再进入阶段 B**。

**阶段 B：探针实验（等我确认之后才开始）。**
按 T11 写 `probe` benchmark，在本机编译通过之后，按 README 的工作流同步到服务器，用 timing 模式跑 P1–P4。**不做 verify。**
每个实验在 `STATUS.md` 里给出“通过 / 不通过”和关键的统计数字，原始结果放在 `results/<exp_id>/`。

**规则：**
- 代码只在本机修改；服务器上只同步、编译、运行。
- 工作区不干净时，不要提交实验。
- 只改 `STATUS.md`，不要改 `isca27/` 下的其他文档。文档和代码对不上时，以代码为准，把差异记进 `STATUS.md` 的“规格与代码的差异”。
- 不要动状态为 `[提案]` 或 `[待验证]` 的任务（T7、T8、T9、T10），也不要修改模拟器原有的行为。T11 只新增一个探针 benchmark。
- **立刻停下来问我的情况**：P3a 发现远程读不经过远端 L2；需要改模拟器才能实现数据放置或 kernel 放置；遇到不支持的指令。

**完成的标准：**
`STATUS.md` 里 T11 的状态变成“完成”，P1–P4 都有结论；探针代码整理成可复用的 SPMD 骨架（每个 GPM 分配分片、建立指针表、每个 GPM 一个队列、按阶段 launch 和 drain），说明写在 `STATUS.md` 的日志里。
---
