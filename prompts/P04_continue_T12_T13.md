# P04：继续实验 —— T12 修 driver → T13 换成 1/8 MI300X 配置并全量重跑（合并了 P02 和 P03）

---
继续 CuPath 的 ISCA 2027 准备工作。开始之前先读这几个文件：
- `~/Desktop/CuPath/isca27/README.md`
- `~/Desktop/CuPath/isca27/CONFIG.md`（论文配置，**不可修改**；每个 task 执行前都要做配置确认）
- `~/Desktop/CuPath/isca27/tasks/T12_driver_flush_fix.md`
- `~/Desktop/CuPath/isca27/tasks/T13_mi300x_config.md`
- `~/Desktop/CuPath/isca27/tasks/T11_probe_placement_comm.md` 末尾的“10-13 补充”
- `~/Desktop/CuPath/isca27/STATUS.md`

## 先更新 STATUS 里两条待决事项（用户 10-13 的答复）
1. **flush 竞争**：用户选择**修 driver**（方案 B），要求修完能跑起来，见 T12。把这条标成已决定。
2. **远程带宽只有本地的 1/18**：原因是 T11 用了 baseline 二进制的**默认参数**（`-bandwidth=1`、`-switch-latency=20`、没有 `-magic-memory-copy` 等），不是论文配置。论文配置是 `-bandwidth=48`（768 GB/s）、`-switch-latency=32`，见 CONFIG.md。把这条标成已决定，并把 T11 结果摘要里 P3 和 P4 的数字标为“默认参数，作废”。
   - 以后所有实验的参数都**从 `runall2_constants.py` 导入**，不要手写，也不要用二进制默认值。先把 `akkalat/t11_run_probe.sh` 改成这样。

## 然后按顺序做下面 3 段，每个“⏸”都要停下来等我确认

**第 1 段：T12 修复 driver（新分支，例如 `isca27/t12-driver-flush-fix`）**
- ⏸ 配置确认：按 CONFIG.md 第 3 节，检查 7 个配置文件和 95b4f46 是否一致，打印回归测试要用的完整参数，列出差异。
- 修改 `mgpusim/driver/memorycopy.go`：H2D、D2H、flush 三种回复都调用同一个收尾函数，请求全部返回时命令出队（详见 T12）。共享的 `cyclesLeft` 只评估、不改。**不要改** flush 的触发条件，也不要改 `-magic-memory-copy` 的路径。
- 验收：
  - 单元测试覆盖两种回复顺序
  - 用论文配置加较小的 `-max-wg` 跑 fir 和 matrixmultiplication，**周期数和修复前完全一致**
  - 探针加 `-probe-shared-ctx` 开关，在共用 Context、不加 `-magic-memory-copy` 的条件下跑 P4，48 个 GPM 全部完成
- 写进 STATUS（修改的文件和行号、commit hash），⏸ 停下来给我看结果。

**第 2 段：T13 阶段 1–3（新分支，例如 `isca27/t13-mi300x`，基于第 1 段的结果）**
- **阶段 1**：定位 T13 目标表里每个参数在哪里设置，判断要不要改代码，有没有和 2 的幂相关的冲突。**不改代码**。⏸
- **阶段 2**：实现预设 `-gpm-preset=mi300x-eighth`，默认值保持旧配置；在 `runall2_constants.py` 里**新增** `MI300X_EIGHTH_FLAGS`，**不改** `BASE_COMMON_FLAGS`。用旧配置做回归，周期数必须完全一致。⏸
- **阶段 3**：校准。用微基准实测本地 HBM 带宽（目标约 315 B/周期/GPM）、L2 带宽（目标约 2 KB/周期/GPM）、L1/L2/HBM 的空载延迟，以及远程读带宽和 1/3/6/12 跳的延迟（也就是新配置下的 T11 P3）。偏差超过 10% 就报告，**不要调参数凑数**。⏸
- 每个阶段运行之前，都要做 CONFIG.md 第 3 节的配置确认。

**第 3 段：T13 阶段 4 —— 全量重跑（等我确认新配置之后才能开始）**
- 顺序：T11 探针 P1–P4 → 论文主结果（14 个 workload × baseline、Remote Aggregation、Remote Reuse、Local Pairing、Complete；Fig 11–13）→ 敏感性实验（Fig 14–16）→ LLM 算子实验（Fig 17）。
- 全部用论文原有的运行脚本和机制组合，只额外加上新预设；`-max-wg` 等设置沿用原脚本的值。
- ⏸ **开跑之前**，先估算总的模拟时间和两台服务器的分配方式，连同完整参数一起给我确认。
- 跑的过程中定期更新 STATUS；用心跳检测卡死的运行。完成后给出新旧配置的对照表：每个 workload 的 baseline 周期数、CuPath 加速比，以及 GMEAN。

## 规则（不变）
- 代码只在本机修改；服务器上只做同步、编译、运行。工作区不干净时不跑实验。
- 只改 `STATUS.md`，不改 `isca27/` 下的其他文档；文档和代码对不上时，以代码为准，把差异记进 STATUS。
- 不碰 `[提案]` 的任务：T4 的 cu8、T7、T8、T9、T10。
- 旧结果不删。新结果的 `exp_id` 带上预设名，`meta.json` 写 `config_deviation`。
- **立刻停下来问我的情况**：修复之后旧配置的周期数变了；新配置下远程读不经过远端 L2；需要修改 `CONFIG.md` 第 1 节列出的文件里和旧配置相关的内容；校准偏差超过 10%。
---
