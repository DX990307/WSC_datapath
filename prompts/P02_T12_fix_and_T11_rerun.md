# P02：T12 修 driver + 用论文配置重跑 T11 的 P3/P4（复制分隔线之间的内容给本地 Claude Code）

---
继续 CuPath 的 ISCA 2027 准备工作。先读这几个文件：`~/Desktop/CuPath/isca27/README.md`、**`CONFIG.md`（论文配置，不可修改）**、`tasks/T12_driver_flush_fix.md`、`tasks/T11_probe_placement_comm.md` 末尾的“10-13 补充”，以及 `STATUS.md`。

用户 10-13 的决定：
1. **flush 竞争要直接修代码**（不再只靠“每个 GPM 一个 Context”来绕开），要求修完之后能跑起来。
2. 之前怀疑“网络带宽和论文不一致”，是因为 T11 用了二进制的默认参数，不是论文配置。**以后所有实验都必须用 `CONFIG.md` 里的论文配置**，参数从 `runall2_constants.py` 导入，不要手写。T12 修的是 driver 的 bug，**不是配置**；`CONFIG.md` 第 1 节列出的文件一律不改。

**用户 10-13 的规定：之前设置的配置就是论文始终使用的配置，不要改。每一个 task 执行之前，都要按 `CONFIG.md` 第 3 节做配置确认：检查配置文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → 停下来等我确认，之后才能运行。** T12 和 T11 的重跑各自都要做一次。

这次要做的事，按顺序：

**1. T12：修复 `mgpusim/driver/memorycopy.go` 的命令完成判断。**
按 T12 文档的第 1 步改：`processFlushReturn` 也要在 `Reqs` 为空时让命令出队，H2D 和 D2H 共用一个收尾函数。第 2 步（共享的 `cyclesLeft`）是可选的，先评估一下，把结论写进 STATUS，**不要直接改**。
验收：单元测试覆盖两种回复顺序；用论文配置加较小的 `-max-wg` 跑 fir 和 matrixmultiplication，**周期数和修复前完全一致**；探针加一个 `-probe-shared-ctx` 开关，在共用 Context、不加 `-magic-memory-copy` 的情况下跑 P4，48 个 GPM 全部完成。

**2. T11 重跑：用论文配置重跑 P3a、P3b、P3c、P4。**
先把 `t11_run_probe.sh` 改成从 `runall2_constants.py` 取参数。
STATUS 里原来的 P3/P4 数字标为“默认参数，作废”，并加一张新表：论文配置下的单 GPM 远程读带宽和本地带宽之比，1、3、6、12 跳的延迟，远端 L2 命中和未命中的时间差，以及 48 个 GPM 并发时和单个 GPM 的时间比。

**规则不变**：代码只在本机改；工作区不干净时不跑实验；只改 `STATUS.md`，不改 isca27 下的其他文档；不碰 `[提案]` 的任务（T7–T10）。
**立刻停下来问我的情况**：修复之后已有 benchmark 的周期数发生变化；论文配置下 P3a 的远程读不经过远端 L2；需要改 flush 的触发条件。

完成标准：STATUS 里 T12 标为“完成”，T11 的重跑表填好，写明修改的文件和行号、两个 commit 的 hash。
---
