# T12 修复 driver 的 flush 竞争（多个 queue 共用一个 Context 时死锁）

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]`（用户 10-13：“改代码，要能跑起来”）· **周次**：W1 · **依赖**：无 · **被依赖**：T10 · **工作量估计**：半天

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。T10 要写自己的通信型 benchmark：数据按 GPM 分片，每个 GPM 一个 command queue。T11 的 P4 实验中，多个 queue 共用一个 Context 时模拟器死锁（48 个 GPM 只有 17 个启动）。当时的临时办法是每个 GPM 一个 Context；用户决定**直接修代码**。
> 代码仓库：`~/Desktop/CuPath/<Screen Recording ... .mov>/`（软链接 `~/Desktop/CuPath/cupath`），fork 的 mgpusim 是 v3。
> 索引：`../README.md` · 进度写入：`../STATUS.md`

## 问题定位（Cowork 侧读代码得出的结论，10-13）

文件：`mgpusim/driver/memorycopy.go`（`defaultMemoryCopyMiddleware`）

1. `processMemCopyH2DCommand`（约第 38 行）：如果目标 buffer 的 `l2Dirty` 为真，先 `sendFlushRequest`，给**全部 48 个 GPU** 各发一个 `FlushReq`，并加进 `cmd.Reqs`；然后把 H2D 请求也加进 `cmd.Reqs`（经过 `cyclesPerH2D` 延迟后才发出）。D2H（约第 93 行）也一样。
2. 只要任意一个 kernel 被 launch，就会调用 `queue.Context.markAllBuffersDirty()`，把整个 Context 的所有 buffer 都标脏（`driver.go`）。所以只要多个 queue 共用一个 Context，任何一个 queue 的 kernarg H2D 都会触发对全部 GPU 的 flush。
3. **死锁的原因**：命令完成的判断只写在 `processMemCopyH2DReturn` / `processMemCopyD2HReturn` 里（`len(Reqs)==0` 时 `Dequeue`）。而 **`processFlushReturn`（约第 293 行）只调用 `RemoveReq`，从不检查命令是否已经完成**。如果最后一个返回的是 flush 回复（比 H2D 回复晚到），这个命令就永远不会出队，它所在的 queue 就卡死了。
4. 另外：`defaultMemoryCopyMiddleware` 的 `cyclesLeft` 和 `awaitingReqs` 是所有 queue 共享的状态，多个 queue 同时发起拷贝时，`cyclesLeft` 会被后来的命令重置。这不会导致死锁，但会让 H2D 的发出时间不准。

**注意**：论文实验用的 `BASE_COMMON_FLAGS` 里有 `-magic-memory-copy`，这时用的是 `globalStorageMemoryCopyMiddleware`（`memorycopyglobalstorage.go`），**完全不会发 flush**，所以论文实验不受这个 bug 影响。T11 的探针没有加 `-magic-memory-copy`，才触发了这个 bug。

## 要做什么

1. **修复完成判断**（最小改动）：
   - 抽出一个辅助函数，比如 `completeIfDone(now, cmd, queue)`：当 `len(cmd.GetReqs())==0` 时，按命令类型收尾。H2D：`queue.IsRunning=false`、`Dequeue`、`logCmdComplete`。D2H：先把 `RawData` 解码到 `Dst`，再做同样的收尾。
   - 在 `processMemCopyH2DReturn`、`processMemCopyD2HReturn`、**`processFlushReturn`** 三个地方都调用这个函数。`processFlushReturn` 里要改成用 `cmd, queue := findCommandByReq(req)` 拿到 queue。
2. **（可选，先评估再决定）** 把 `cyclesLeft` 和 `awaitingReqs` 改成按命令或按 queue 记录，避免多个 queue 之间互相干扰。**这会改变多 queue 场景下的时序；单 queue 场景（所有现有 benchmark）必须完全不受影响。**
3. **不要改**：flush 的触发条件、flush 发给全部 GPU 的行为、`-magic-memory-copy` 的路径。这些都是现有行为，改了会影响论文已有的数据。

## 验收标准

- [x] 单元测试：构造“flush 回复晚于 H2D 回复”和“flush 回复早于 H2D 回复”两种顺序，命令都能出队；D2H 同样测两种顺序。
- [x] **回归测试**：用论文的配置（`BASE_COMMON_FLAGS`）跑 2–3 个现有 benchmark（比如 fir、matrixmultiplication），设定一个较小的 `-max-wg`，**周期数和修复前完全一致**。
- [x] **共用 Context 的 P4**：骨架改回“所有 rank 共用一个 Context”的模式（加一个参数切换，比如 `-probe-shared-ctx`），**不加** `-magic-memory-copy`，48 个 GPM 全部完成。
- [x] 在 `STATUS.md` 里记录修改的文件和行号、测试结果，以及可选的第 2 步做没做、为什么。


## 10-13 用户决定：所有运行都保留 `-magic-memory-copy`

> **Cowork 侧加注（10-13）**：下面这一节写于 R2/R3 运行之后。R2 和 R3（去掉 magic copy）是在这个决定之前、按用户当时的确认跑完的，只用于验证 T12，已经写进 `config_deviation`，不进论文。从此以后的所有运行都要保留 `-magic-memory-copy`。

用户要求**保留 `-magic-memory-copy`**，包括 T12 的测试在内，所有运行都不能去掉它。由此带来的调整：

- 带 magic copy 时，driver 用的是 `globalStorageMemoryCopyMiddleware`，**根本不会发 flush**，所以这个死锁在任何实际运行里都不会出现，T10 也可以让所有 GPM 共用一个 Context。
- **T12 的代码修复仍然做**，作为防御性修复：改动很小，而且只影响不带 magic copy 的那条路径，**只用单元测试验证**（覆盖两种回复顺序）。
- **取消 R2**（去掉 magic copy 的回归）。
- **R1 不变**：论文配置下 fir 和 matrixmultiplication 修复前后逐行一致（墙钟时间等每次运行都会变的字段除外）。
- **R3 改为带 magic copy 运行**：`-probe-shared-ctx` 模式（48 个 GPM 共用一个 Context），用论文配置，修复后跑一次。验收标准：48 个 GPM 都有指令、两遍都完成、心跳没有报卡死。这一项证明的是 T10 在论文配置下可以用共用 Context 的写法。
- 不再要求“修复前复现死锁”。

## 完成情况（10-13，Claude Code 补记，经用户同意修改本文档）

修复在分支 `isca27/t12-driver-flush-fix`，commit `9c30a89`（修复前的基准是 `7d89a72`）。详细记录见 `../STATUS.md` 的 T12 日志。

- **修改**：`mgpusim/driver/memorycopy.go` 新增 `completeIfDone`（第 299–323 行），H2D、D2H、flush 三种回复都调用它（第 254、272、294 行）。flush 的触发条件、flush 发给全部 GPU、`-magic-memory-copy` 的路径、`cyclesLeft` 都没有改。
- **单元测试**：`mgpusim/driver/memorycopy_test.go` 新增 4 个，覆盖 H2D、D2H 各两种回复顺序；对修复前的代码运行时，正好两个“flush 最后返回”的测试失败。
- **回归**：fir、matrixmultiplication，`-max-wg=4096`；论文配置（R1）和去掉 `-magic-memory-copy`（R2，让拷贝走被修改的代码）两组，修复前后 total_time 和全部统计**排序后**完全一致（统计输出的行顺序本来就每次不同，原因是 `report.go:302` 遍历 Go map）。
- **共用 Context 的 P4**（R3，`-probe-shared-ctx`，不加 `-magic-memory-copy`）：修复前死锁在模拟时间 58.7 µs（640 个 WG）；修复后 48 个 GPM 两遍都完成。
- **可选的第 2 步（`cyclesLeft`）没有做**：akkalat 平台没有设置拷贝延迟（`cyclesPerH2D = cyclesPerD2H = 0`），共享的 `cyclesLeft` 不会让多个 queue 互相推迟。
- 使用说明见代码仓库的 `akkalat/docs/ISCA27_TOOLS.md`。
