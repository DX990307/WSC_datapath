# 进度汇报（由 Claude Code 维护）

> **写的人**：本地 Claude Code。**读的人**：dx，以及 Cowork 侧的 Claude。
> **规则**：只在“日志”里追加，不删除旧条目；“任务总览”和“需要用户决定”保持为最新状态。
> 项目背景见 `README.md` 和 `00_background.md`；每个任务的规格见 `tasks/`。

## 任务总览

状态取值：`未开始` / `进行中` / `完成` / `阻塞` / `跳过`

| 任务 | 状态 | 最后更新 | 一句话说明 |
|---|---|---|---|
| T0 实验脚本 | 未开始 | | |
| T1 指令支持检查 | 未开始 | | |
| T2 新增统计 | 未开始 | | |
| T3 强 baseline | 未开始 | | |
| T4 新配置 | 未开始 | | |
| T5a LLM decode | 未开始 | | |
| T5b GCN arxiv | 未开始 | | |
| T5c DLRM embedding | 未开始 | | |
| T5d SSB join | 未开始 | | |
| T6 截断验证 | 未开始 | | |
| T7 机制 1 | `[提案]`，不要开始 | | |
| T8 机制 2 | `[提案]`，不要开始 | | |
| T9 第二批 workload | `[提案]`，不要开始 | | |
| T10 SPMD 通信套件 | `[提案]`，不要开始 | | |
| T12 driver flush 修复 | 完成（用户已确认；10-13 15:17 补跑带 magic copy 的 R3，通过） | 10-13 15:17 | 修复 `9c30a89`；单元测试、R1、R2、R3 全部通过 |
| T13 MI300X 配置 | 进行中 | 10-13 | 阶段 3 校准偏差（HBM 读 −23%、L2 −69%）暂按实测值接受，没有调参数；**阶段 4 全量重跑已启动**（`260ce73`，10-13 19:36 UTC 重启；用户 10-13 确认），预计约 2.5 天 |
| T14 平台数据（第一档） | 完成（⏸ 等用户确认） | 10-13 | 7 项 × 2 套配置全部跑完；摘要表见“结果摘要”，CSV 在 `results_index/platform/` |
| T11 探针实验 | 完成 | 10-13 04:55 | P1–P4 全部有结论（P1/P2/P4 通过，P3 路径通过、细节见结果摘要）；SPMD 骨架在 `mgpusim/benchmarks/microbench/probe/spmd.go` |

## 需要用户决定

<!-- 每条格式：- [ ] (T#) 问题 —— 可选方案 A / B，推荐 X，理由 -->
- [x] (T11) 阶段 B 的运行工作流 —— 已按推荐 B 执行（用户 10-13 说“先实现吧”）：用 `exp` 工具，经由符号链接 `~/Desktop/CuPath/cupath` 运行（真实目录名带空格，`exp` 处理不了）；结果按约定放在 `results/<exp_id>/`。
- [x] (T11) 工作区不干净 —— 已按推荐 B 执行，但没有删除：两个文件都移到了仓库外的 `~/Desktop/CuPath/_moved_out_of_repo/`。另外把 `/remote/`、`/results/` 加进了 `.gitignore`（否则每次 `exp pull` 之后工作区都不干净）。
- [x] (T11) P2 的统计 —— 已按推荐 A 执行（`-report-inst-count`）。
- [x] (T10/T11) **已决定（用户 10-13）：修 driver（方案 B），要求修完能跑起来，见 `tasks/T12_driver_flush_fix.md`。** 原问题：**driver 的 flush 竞争 bug 要不要修**：同一个 Context 里多个 queue 并发时，任一 kernel launch 会把整个 Context 的 buffer 标脏，其他 queue 的 kernarg H2D 于是触发**对全部 48 个 GPM 的 L2 flush**（flush 会清空 L2 目录，`akita/mem/cache/writeback/flusher.go:176-181`）；而 `processFlushReturn`（`mgpusim/driver/memorycopy.go:293`）只删除自己、不检查命令是否已完成，flush 回复晚于 H2D 回复时命令永远不出队 → 死锁（第一次 P4 就是这样卡住的，只有 17/48 个 GPM 启动）。—— A：不改模拟器，T10 一律每个 GPM 一个 Context（探针骨架已经这样做，和 mccl 的 `CommInitAllMultipleContexts` 一致）/ B：修 driver（改原有行为，需要你批准）。推荐 A。对已有数据的影响应该很小：现有 benchmark 在 runner 下只拿到一个 unified GPU，所以只有一个 queue（例如 fir 的 `exec`，`mgpusim/benchmarks/heteromark/fir/fir.go:146`，`b.gpus` 只有 1 个元素），不会出现多个 queue 互相触发 flush；只有自己按 GPM 建多个 queue、又共用一个 Context 的代码才会中招。
- [x] (T10) **已决定（用户 10-13）：这是参数用错了，不是模型问题。** T11 第一轮用的是 baseline 二进制的默认参数（`-bandwidth=1` 即 16 GB/s、`-switch-latency=20`、没有 `-magic-memory-copy`、`-num-memory-banks=4`、`-mmutlb-lookup-latency=80`），不是论文配置（`-bandwidth=48` 即 768 GB/s、`-switch-latency=32` 等，见 `CONFIG.md`）。P3/P4 的性能数字作废，要用论文配置重跑；以后所有实验的参数都从 `runall2_constants.py` 导入（`t11_run_probe.sh` 已改，commit `d6b765c`）。原问题：**单个 GPM 的远程读带宽只有本地的约 1/18**：P3a 里一个 GPM 远程读 2 MB 用 200.9 µs（≈10.4 GB/s，平均远程延迟 3.05 µs），本地读同样 2 MB 用 11.4 µs（≈184 GB/s）；而且远端 L2 命中和远端 DRAM 的时间几乎一样（199.5 vs 200.9 µs），说明瓶颈在 RDMA/mesh 这一段而不在数据来源。T10 的通信 kernel 如果是带宽受限的，CuPath 在“远端 L2 命中 vs 未命中”上的收益会被掩盖。—— 是否先确认 RDMA/mesh 参数（`-rdma-max-outstanding 64`、`-rdma-pipeline-width 8`、`-bandwidth 1`、`-network-flit-size 16`）就是论文要用的配置？

- [x] (T12) ~~R2 回归太慢，要不要换成小输入的 benchmark~~ —— **不需要决定了**：4 个 R2 在 07:17–07:56 UTC 全部正常跑完（exit 0）。我“要十几个小时”的估算是错的（拷贝结束后模拟速度明显变快）。结果见日志。

- [x] (T12/T13) **用户 10-13 决定：保留 `-magic-memory-copy`。** 论文配置（`BASE_COMMON_FLAGS`）里本来就有，没有改过；之后的 T11 重跑、T13 新预设和全量重跑都保留它。只有 T12 的 R2、R3 两组验证临时去掉了它（在 `config_deviation` 里注明，不进论文）。

- [x] (T13) **已决定（用户 10-13）：同意按代码的分析；阶段 3 校准时旧配置也用同一个微基准实测，以实测值为准。** 原问题：(T13) **旧论文配置的 HBM 带宽，按代码算是 128 B/周期/GPM，不是 T13 表里的 375**：DRAM 控制器是 500 MHz、256-bit、BL4，`burstCycle = BL/2 = 2` 个 DRAM 周期（`akita/mem/dram/builder.go:781-793`），每次访问 128 B，所以每个控制器的理论峰值是 128 B / 4 ns = 32 B/ns；论文配置 `-num-memory-banks=4`，每 GPM 4 个控制器 = **128 B/周期**。（T11 P1 实测 184 B/ns 并不矛盾：那次用的是二进制默认的 `-num-memory-banks=16`，理论峰值 512 B/ns。）这意味着 T13 里“新配置 HBM 带宽比旧配置低约 16%”的说法按代码不成立：新目标 315 B/周期约是旧配置的 **2.5 倍**。—— 建议：阶段 3 的校准也在**旧配置**下跑一遍同样的微基准，用实测值确认旧配置到底是多少；论文 Table II 里旧配置的 HBM 带宽数字需要你核对。
- [x] (T13) **已决定（用户 10-13）：方案 A（64-bit、BL8、每次访问 64 B、DRAM 时钟 1.23 GHz，时序按纳秒不变换算）。延迟不做每周期等效缩放（带宽按 2.1 GHz 每周期等效，延迟保持纳秒值），写进 STATUS 和 meta。最终以阶段 3 实测为准。** 原问题：(T13) **新配置的 HBM channel 怎么设**：每 channel 的目标是 19.7 B/周期（315.5 / 16）。现在的 DRAM 时序全部写死（`r9nanobuilder.go:945-1000`），如果只把控制器数从 4 改成 16、时序不变，每 GPM 理论峰值是 512 B/周期，比目标高 62%。—— A：按 HBM3 的结构建模：每 channel 64-bit 总线、BL8（每次访问 64 B，正好一条 cache line），DRAM 时钟取 1.23 GHz，使每 channel = 64 B / (4 / 1.23 GHz) ≈ 19.7 B/ns；tCL、tRCD 等时序参数按纳秒不变的原则从 500 MHz 换算到 1.23 GHz（乘以 2.46 后取整）。/ B：保持 256-bit、BL4 不变，只把 DRAM 时钟降到约 308 MHz，使每 channel = 128 B / (2 / 0.308 GHz) ≈ 19.7 B/ns（改动最小，但每次访问 128 B，比 64 B 的 line 大）。推荐 A（更接近 HBM3，访问粒度和 cache line 一致），最终都以阶段 3 实测为准。
- [x] (T13) **已决定（用户 10-13）：同意方案 A。预设取值集中在 `akkalat/baseline/runner/gpm_preset.go`，原文件只做“常量 → 预设字段”的替换，旧配置的数值不改；阶段 2 用旧配置回归（排序后逐项比较，total_time 单独比较），并检查分析脚本里有没有假设 L2 只有 4 个分区。** 原问题：(T13) **阶段 2 需要修改 CONFIG.md 第 1 节列出的文件**：新预设要改的值大多写死在 `shaderarray.go`、`r9nanobuilder.go`、`timingplatform.go` 里，需要在 `flag.go` 加 `-gpm-preset`，并把这些写死的值改成由预设决定的参数。**旧配置的数值一个都不改**（默认预设 = 现在写死的值），并用回归证明周期数完全一致。按你的规则“需要修改 CONFIG.md 第 1 节列出的文件”要先问你。—— A：照上面做，预设的取值集中写在一个新文件 `akkalat/baseline/runner/gpm_preset.go` 里，上面几个文件只做“把常量换成预设字段”的替换 / B：你有别的偏好。推荐 A。

- [x] (T13 阶段 2) **用户 10-13 确认：旧配置回归“跑出来再说”；之后又确认了改用配置文件后的两组运行（fir 配置文件验证、T12 R3），全部确认。** 原问题：**配置确认（CONFIG.md 第 3 节）：请确认下面两组运行的参数和差异。** 注意：旧配置回归**已经在 P06 到达之前启动**（我当时按 T12 R1 已确认的参数直接跑了，没有为阶段 2 单独等确认，这一点不符合 P06 的要求）；它还在跑，按 SYNC 规则没有打断。如果你不认可，我把结果作废、等确认后重跑。
  - **旧配置回归（运行中，yss2，commit `f444076`，默认预设 hpca）**：`-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -disable-servers -mmutlb-lookup-latency=80 -max-wg=4096 -benchmark fir|matrixmultiplication`。差异：只有 `-max-wg=4096`（截断参数）和 `-benchmark`。和修改前的基准（T12 R1，`9c30a89`）参数完全相同。
  - **T12 R3（带 magic copy，T12 文档新要求，待确认后运行）**：`-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -disable-servers -report-l2-source -l2-source-tile-width=7 -mmutlb-lookup-latency=80 -benchmark probe -probe-all-gpms -probe-shared-ctx -probe-bytes 2097152 -probe-passes 2`。差异：统计开关 `-report-l2-source`/`-l2-source-tile-width=7`，探针参数。用修复后的二进制（`9c30a89`，模拟器代码和 T12 分支头 `36bde23` 相同），yss3。验收：48 个 GPM 都有指令、两遍都完成、心跳不报卡死。
- [ ] (同步) **用户 10-13 决定：不新建 GitHub 仓库，文档仓库作为代码仓库 `DX990307/WSC_datapath` 的新分支 `isca27/docs` 备份。** **代码仓库的三个任务分支已推送**（`isca27/t11-probe-placement-comm` `d6b765c`、`isca27/t12-driver-flush-fix` `36bde23`、`isca27/t13-mi300x` `f444076`）。**文档分支 `isca27/docs` 还没推**：在 `isca27/` 里加 remote 的操作被自动模式拦下，需要用户在终端运行一次：`cd ~/Desktop/CuPath/isca27 && git remote add origin git@github.com:DX990307/WSC_datapath.git && git push -u origin main:isca27/docs`。原问题：**GitHub 推送被本机的权限规则拦下，需要你处理**：① 这台机器没有安装 `gh`，没法用命令行在 GitHub 上建 `DX990307/cupath-isca27`；② `git push origin isca27/...` 被 Claude Code 的自动模式拦截（“Out-of-Place Publication”）。—— A：你在 GitHub 网页上新建空的私有仓库 `DX990307/cupath-isca27`（不要勾选 README），并允许我推送（例如在 `/permissions` 里允许 `Bash(git push origin isca27/*)` 和 `Bash(git push -u origin main)`，只在 isca27 目录），我来加 remote 并推送 / B：你自己在终端运行推送命令（见日志里的命令）。

- [x] (T13) **用户 10-13 决定：配置放在专门的配置文件里，不再在命令行写一长串；配置文件可以保存多份、运行时读取不同的文件，方便做敏感性实验。** 另外**不再使用 `-disable-servers`**（用户担心它会带来 bug）。实现见日志。

- [x] (T13 阶段 3 + T14 第一档) **用户 10-13 确认。** 配置确认（CONFIG.md 第 3 节），详见日志 10-13“T13 阶段 3 + T14：配置确认”。两套配置 `hpca`、`mi300x_eighth`，代码 `0e38983`；运行计划 29 个进程/配置，预计约 1 小时（hopmatrix 最慢）。

- [x] (T13 阶段 3) **用户 10-13：先按 mi300x 配置把论文实验跑一遍（相当于暂时接受实测值，不调参数）；诊断以后再说。** 原问题：**校准偏差超过 10%，按规则停下来报告（没有调任何参数）。** 详见“结果摘要 → T13 阶段 3 校准”。
  - **mi300x HBM 读**：峰值 243 B/周期（8 个 CU），满 38 个 CU 时 224；目标 315 → **−23%**（满 CU −29%）。写 318（+1%）、复制 295（−6%）达标。读比写低，说明不是 DRAM 的总线带宽不够，而是读的路径（可能是 DRAM 的行冲突/调度，或 L2 MSHR 每 channel 16 个）限制了。
  - **L2 带宽（两套配置都一样）**：读峰值约 630 B/周期（16 个 CU），写约 640（8 个 CU），复制约 850；满 CU 时反而降到 420–700。目标 2 KB/周期 → **−69%**（读、写），复制 −58%。hpca 的 L2 理论吞吐（4 KB/周期）是 mi300x 的 2 倍，但两套曲线几乎重合，所以**瓶颈在 L2 之前**（候选：每个 L1V 16 个 MSHR/在途请求、地址转换/TLB、L1→L2 的连接），不是 L2 本身。
  - **旧配置（hpca）HBM**：实测 59 B/周期（读）、66（写）、61（复制），1–2 个 CU 就饱和；代码算的理论值 128，按 64 B 请求的有效值约 64，实测和后者一致。投稿版 Table II 写的 0.375 TB/s/GPM 是实测的 6.4 倍。
  - 可选方案 —— **A**：接受实测值，把它们作为配置的实际特性写进论文和 CONFIG（不改参数）/ **B**：先做诊断：只加统计（L1V MSHR 满的周期、L2 每 channel 的请求率、DRAM 行命中率等），不改任何配置数值，找出 L2 和 HBM 读的瓶颈，再请你决定要不要调 / **C**：直接调参数凑目标（要改 mi300x 预设的数值，必须你批准）。**推荐 B**：L2 −69% 两套配置都有，很可能也影响旧配置下论文结果的解读，先弄清楚瓶颈再决定。

## 规格与代码的差异

<!-- 任务文档写的和代码实际不一致的地方。格式：- (T#) 文档写的：… / 实际情况：… / 我的处理：… -->
下面的路径都相对于仓库根目录 `~/Desktop/CuPath/Screen Recording 2024-08-29 at 12.01.50 PM.mov/`（git 仓库 `DX990307/WSC_datapath`，T11 分支 `isca27/t11-probe-placement-comm`）。

- (T11) 文档写的：上游是 mgpusim **v5** / 实际情况：fork 是 **mgpusim v3**（`mgpusim/go.mod:1` 的 `module github.com/sarchlab/mgpusim/v3`），akita 也是 v3（`akkalat/go.mod:29-31` 用 `replace` 指向本地 `../mgpusim`、`../akita`），Go 1.22.0（`mgpusim/go.mod:48`）/ 我的处理：以 v3 代码为准；用到的 driver API 在 v3 里都有，签名和上游一致（见日志）。
- (T11) 文档写的：探针放在 `amd/benchmarks/microbench/probe/` / 实际情况：没有 `amd/` 这一层，benchmark 在 `mgpusim/benchmarks/` 下；实验入口是 `akkalat/baseline/main.go`（`-benchmark <名字>`），名字在 `akkalat/benchmarkselection/benchmark.go:81` 的 `SelectBenchmark` 里注册 / 我的处理：探针放在 `mgpusim/benchmarks/microbench/probe/`，在 `SelectBenchmark` 里新增一个 `case "probe"`。
- (T11) 文档写的：“GPM 5”、“48 个 GPM” / 实际情况：有**两套编号**。driver 的 GPU ID 是 1–48（0 号是 CPU）；组件名是 `GPU[t]`，t = x + 7y 是 7×7 mesh 上的格子编号，中心格子 t=24 留给 driver/IOMMU，所以 ID ≤ 24 时 t = ID−1，ID ≥ 25 时 t = ID（`akkalat/baseline/runner/timingplatform.go:570-581`、`:790-792`）。L2 source 统计里的 GPM 编号用的是 t（`akita/mem/trace/l2source_util.go:14` 从名字里解析），跳数是 7×7 上的曼哈顿距离（`l2source_util.go:27`）/ 我的处理：探针参数用 driver ID；STATUS 里同时写出 driver ID 和 `GPU[t]`。P3a 的 owner=5、runner=1 对应 `GPU[4]`(4,0) 和 `GPU[0]`(0,0)，相距 4 跳；P3b 用 driver ID 2/4/7/48（`GPU[1]`/`GPU[3]`/`GPU[6]`/`GPU[48]`），离 runner 1 分别是 1/3/6/12 跳。
- (T11) 文档写的：远程请求走“RDMA 还是 CuPath 新加的 NI” / 实际情况：没有单独的 NI 组件，远程访问只走 RDMA 引擎。CuPath 的远程数据通路（batching/去重/filter/prefetch）是加在 RDMA 引擎里面的（`mgpusim/timing/rdma/remote_requester.go:357`），默认关闭，用 `-remote-data-path-enable` 打开（`akkalat/baseline/runner/flag.go:165`）/ 我的处理：T11 先用默认关闭的配置跑；如果要对比打开的情况，等用户决定。
- (T11) 文档未提：unified GPU 的**内存分配策略被 fork 改过**。上游按页 round-robin，fork 改成连续分块（每个 GPU 分到连续的 numPages/numGPUs 页）（`mgpusim/driver/internal/memoryallocator.go:159-166`、`:213-234`）。对普通 GPU（非 unified）没有影响，`AllocateMemory` 仍然分配在当前选中的 GPU 上 / 我的处理：只记录，不改。
- (T11) 文档未提：`EnqueueLaunchKernel` 会把 code object、kernarg 和 AQL packet 分配在 `ctx.currentGPUID` 上（`mgpusim/driver/kernel.go:40-51`），而不是 queue 绑定的 GPU 上 / 我的处理：SPMD 骨架里每次 enqueue 前都先 `SelectGPU(ctx, g)`，否则取指和读 kernarg 会变成远程访问，污染 P1/P4 的远程事务数。

- (T11) 文档写的：P2 看“每个 GPM 的 WG 完成记录” / 实际情况：没有现成的 WG 完成统计 / 我的处理：用 `-report-inst-count` 的每 CU 指令数（为 0 即没执行过 WG）。
- (T11) 文档写的：P3a 预期“第 2 遍明显比第 1 遍快” / 实际情况：默认配置（32768 个线程）下带宽受限，两遍几乎一样（200.9 vs 199.5 µs），尽管第 2 遍确实全部命中远端 L2 / 我的处理：加了一个延迟受限的变体（只有 1 个 WG，256 线程），第 2 遍快 31%（1458.6 → 1002.0 µs）。P3b 也同样补了 1 个 WG 的变体。
- (T11) 文档写的：P3c 记录“写之后 GPM 5 的 L2 是否变成 dirty” / 实际情况：没有直接导出 L2 dirty 位的统计 / 我的处理：从统计推断：写全部是 `remote_write_allocate`（在远端 L2 分配，不读 DRAM），整个运行中 GPM 5 的 DRAM 写事务为 0，所以写入的数据停留在远端 L2 里，是 dirty 的。
- (T11) 文档写的：`run_exp.sh` + GNU parallel 分发 / 实际情况：T0 没做，两台服务器也都没装 GNU parallel / 我的处理：用 `exp` 工具 + `akkalat/t11_run_probe.sh`（写 meta.json、summary.csv、stats/、stdout.log、stderr.log）。服务器上同步的代码没有 `.git`，commit 由本机检查工作区干净后传进去。
- (T11) 文档写的：探针放在 owner 上直接 `AllocateMemory` / 实际情况：探针在本机编不出 hsaco（macOS 的 clang 没有 amdgcn 目标），`kernels.hsaco` 是在 yss2 上用 `build_hsaco.sh` 编译后拉回本机提交的（clang 14，`-mcpu=fiji`，code object v2，和 LLM 算子的编译选项一致）。

- (T12) 文档写的：回归“周期数和修复前完全一致”，我在确认时写的是“metrics.csv 逐行完全一致” / 实际情况：`akkalat/baseline/runner/report.go:302` 用 `for name, value := range values`（Go map）输出 filter_prefetch 系列统计，**同一个二进制每次运行的行顺序都不一样**（这段代码从 `8648917` 起没有改过） / 我的处理：比较“去掉行号、排序之后的内容”是否完全一致；`total_time` 单独比较。
- (T12) 文档写的：用论文配置做回归 / 实际情况：论文配置里的 `-magic-memory-copy` 会让 driver 根本不加载 `defaultMemoryCopyMiddleware`（`mgpusim/driver/builder.go:102-114`），所以 R1 只能证明论文配置不受影响 / 我的处理：按用户同意加了 R2（去掉 `-magic-memory-copy`），见“需要用户决定”。

- (T13) 文档写的：CuPath typed filter “每个分区 16K 槽（= 1 MB / 64B）”，新配置“每个 channel 4K 槽” / 实际情况：容量是**每个 L2 分区 2 × 块数**，再向上取整成 2 的幂个 bucket（`akita/mem/cache/writeback/builder.go:356-358`，`typed_filter.go:310-320`）：旧配置每个 1 MB 分区 32K 槽（每 GPM 128K），新配置每个 256 KB channel 8K 槽（每 GPM 仍是 128K） / 我的处理：以代码为准；新配置不用改代码，每 GPM 的总槽数自动保持不变。
- (T13) 文档写的：旧配置“每 GPM 375 B/周期”HBM 带宽 / 实际情况：按代码是 128 B/周期（见“需要用户决定”）。
- (T14) 文档写的：第 4 项要给出“RDMA 表满的周期占比、链路利用率”作为瓶颈证据 / 实际情况：模拟器没有这两种统计 / 我的处理：链路利用率按 YX 路由和每对流量推算（`pattern_links.csv`，标注为推算值）；RDMA 表满的占比没有给出，只用 Little 定律推算在途请求数。要拿到直接证据需要加统计（改代码），属于第二档的延迟分解，没做。
- (T14) 文档写的：第 3 项 48×48 全部 GPM 对 / 实际情况：按文档的备选方案测了 329 对（GPM 1 → 全部、全部 → GPM 1、另外 5 个起点 → 全部）。
- (T14) 文档写的：第 1 项“只用 1 个线程” / 实际情况：1 个线程时 wave 内地址一致，编译器会把指针追逐编成标量加载 `s_load_dword`，走 16 KB 的 L1 标量缓存而不是 L1V / 我的处理：起点加上 `get_local_id(0)`（恒为 0），迫使编译器用 `flat_load_dword`（`ca7ea0b`，反汇编已确认）。`0e38983` 的延迟和跳数结果作废，已用 `ca7ea0b` 重跑。

- (T13) **用户要求核实 (a)：HPCA 投稿版实验的 HBM 带宽实际是多少**（用户说论文 Table II 写的是 0.375 TB/s/GPM）。核实结果：
  - **代码**：从第一个 commit（`6f6985d`）到 `95b4f46`，每个 DRAM 控制器的参数都没有变过：500 MHz、HBM 协议、256-bit 总线、BL4（`r9nanobuilder.go` 的 `createDramControllerBuilder`）。`burstCycle = BL/2 = 2` 个 DRAM 周期 = 4 ns，每次访问 128 B，所以**每个控制器的理论峰值是 32 GB/s**。
  - **控制器数**：`a185a7e`（07-11）之前 runall2 用 `-num-memory-banks=16`，之后改成 4。`akkalat/results/` 里 07-21 以后的所有实验（sampled-validation、baseline-complete-observation、m1-m2-m3-rerun、cupath-llm、两次 cupath-sensitivity、filter-authoritative-validation、baseline-library）记录的命令里**全部是 `-num-memory-banks=4`**。
  - 所以 HPCA 投稿版实验的 HBM **理论峰值是每 GPM 4 × 32 = 128 GB/s = 0.128 TB/s**。另外，访问单位是 128 B，而 L2 缺失请求是 64 B 一条 line，一次 64 B 请求也要占用一整个 128 B 的 burst，所以**按 64 B 请求算的有效峰值只有约 64 GB/s/GPM**（除非相邻两条 line 合并成一次访问）。以上都是理论推算，阶段 3 用微基准实测为准（用户已同意）。
  - **论文文字**：仓库里能找到的版本（`4a04003` 和 `95b4f46` 的 tex，`main-draft.pdf`、`95b4f46` 的 `main.pdf`）写的都是“HBM 8 GB/GPM, four controllers, **1.23 TB/s**, BL4”，没有出现 0.375；0.375 TB/s/GPM 只出现在 `isca27/00_background.md` 里（和审稿人 C 说的“4 个 GPM 1.5 TB/s”一致）。**投稿的 PDF 不在仓库里，我没法核对 Table II 的原文**。不管是 0.375 还是 1.23，都和代码的 0.128 不一致。
- (T13) **用户要求核实 (b)：HPCA 投稿版实验的 typed filter 用了多少 bucket/槽，CACTI 按哪个容量算**。核实结果：
  - **代码默认**：`config.Capacity = numBlocks * 2`（`akita/mem/cache/writeback/builder.go:357`；从 `4a04003` 起就是这样，更早的 commit 没有 typed filter 的容量逻辑），1 MB 分区有 16384 块 → 容量 32768 槽 → 8192 个 bucket × 4 槽。
  - **实际运行**：主实验没有设 `-typed-filter-capacity`。运行统计里记录的 `typed_filter_buckets` 在 `2026-07-22-m1-m2-m3-rerun`（8064 行）、`2026-07-24-05-33-37-cupath-llm`、`2026-07-26-filter-authoritative-validation` 里**全部是 8192**，`typed_filter_slots_per_bucket` 全部是 4。也就是**每分区 8K bucket × 4 槽 = 32K 槽，每 GPM 128K 槽**，是论文写的 4K bucket 的 2 倍。
  - **敏感性实验不一样**：`run_cupath_sensitivity.py:49` 的参考点是 `REFERENCE_FILTER_BUCKETS = 16 * 1024`（每 GPM 16K bucket = **每分区 4K bucket**），用 `-typed-filter-capacity=16384` 显式设置。`2026-07-25-19-41-19-cupath-sensitivity` 里扫 filter 大小的点是每分区 1K/2K/4K/8K bucket，**其他敏感性参数都是在每分区 4K bucket 下跑的**，和主实验的 8K 不是同一个点。
  - **论文文字**：`4a04003`（07-23）的 tex 写的是“One shared array/L2 partition, **4K buckets**, four slots/bucket”（和用户说的一致）；`8648917` 改成“32K buckets/GPM”；`95b4f46` 写的是“Four physical arrays/GPM, **8K buckets/array**”，和实际运行一致。
  - **CACTI**：`akkalat/cost/CUPATH_CACTI_AREA.md`（`95b4f46` 版）按**每阵列 8K bucket**算：4 个 filter 共 336 KiB/GPM、2.50 mm²，CuPath 全部规则阵列 3.74 mm²，占 4 MB L2（54.51 mm²）的 6.86%，和实际运行的容量一致。`CUPATH_4K_CACTI_AREA.md` 是更早的 **4K bucket** 版本，文件开头写明是“earlier 4K-bucket what-if point”。投稿时（论文写 4K）引用的面积如果来自这个 4K 版本，那么**面积是按实际运行容量的一半算的**。投稿版 PDF 里引用的是哪个面积数字，需要用户对照投稿稿确认。用户 10-13 说论文写的是“每 GPM 168 KiB”：这正好是 4 个分区 × 4K bucket × 4 槽 × 21 bit = 64K 槽 × 21 bit = 168 KiB，对应 4K bucket 的配置；**实际运行的容量对应 336 KiB/GPM**（8K bucket × 4 槽 × 4 个分区 = 128K 槽），是论文写的 2 倍。

## 结果摘要

<!-- 已经跑完的实验，指向 results/ 下的目录，并给出关键数字 -->

### T11 探针（commit `f7b9eef`，timing 模式，不做 verify，10-13）

> ⚠️ **10-13 更正：这一轮用的是 baseline 二进制的默认参数（`-bandwidth=1`、`-switch-latency=20`、没有 `-magic-memory-copy` 等），不是论文配置。** P1、P2 是功能性结论，仍然有效；**P3a、P3b、P3c、P4 的性能数字（时间、延迟、带宽）是“默认参数，作废”**，路径类结论（远程读写经过远端 L2）需要在论文配置下重新确认。重跑见 T13 阶段 3 / 阶段 4。

结果目录：仓库根目录 `results/1013_probe-<实验>_wafer48_baseline_f7b9eef/`（16 个，全部 exit 0；`results/` 已加入 `.gitignore`，只在本机）。汇总脚本：`python3 akkalat/t11_analyze_probe.py results/<exp_id>`。GPM 编号：driver ID g ↔ 组件名 `GPU[t]`，g ≤ 24 时 t = g−1。buffer 2 MB，默认 32768 个线程（每 GPM 32 CU × 4 WG × 256）。

| 实验 | 结论 | 关键数字 |
|---|---|---|
| **P1 数据放置**（owner = runner = 5） | ✅ 通过 | RDMA 进出事务 = 0 / 0；只有 `GPU[4]` 的 DRAM 有读：32782 次（2 MB / 64 B = 32768，多出的 14 次是 code object/kernarg）；第 1 遍 32768 次 `local_dram`，第 2 遍 32768 次 `local_l2_cache` 命中；每遍 11.4 / 7.8 µs |
| **P2 kernel 放置**（同 P1） | ✅ 通过 | 只有 `GPU[4]` 的 32 个 CU 有指令（共 268288 条），其余 47 个 GPM 全为 0 |
| **P3a 远程读路径**（owner 5、runner 1，相距 4 跳） （**默认参数，作废**）| ✅ 路径通过；⚠️“第 2 遍明显更快”只在延迟受限时成立 | 路径：L1V → 本地 RDMA → mesh → 远端 RDMA → **远端 L2** → 远端 DRAM。第 1 遍 32768 次 `remote_dram`（远端 L2 miss），第 2 遍 32768 次 `remote_l2_cache`（远端 L2 hit）；RDMA 事务 32768/遍 = bytes/64。带宽受限配置：200.9 → 199.5 µs，平均远程延迟 3.05 µs；1 个 WG：1458.6 → 1002.0 µs（快 31%），平均远程延迟 337 ns |
| **P3b 距离**（runner 1，owner 2/4/7/48） （**默认参数，作废**）| ✅ 通过 | 1 个 WG（延迟受限）：1/3/6/12 跳的平均远程延迟 = **212 / 316 / 472 / 784 ns**，单调增加，约 52 ns/跳。带宽受限配置：3053 / 3054 / 3454 / 5917 ns（近处被排队掩盖，远处才拉开） |
| **P3c 远程写**（同 P3a，mode = write） （**默认参数，作废**）| ✅ 记录完成 | 写走和读相同的路径（RDMA → mesh → 远端 L2）。第 1 遍 32768 次 `remote_write_allocate`（在远端 L2 分配，不读 DRAM），第 2 遍 32768 次 `remote_l2_cache` 写命中；GPM 5 的 DRAM 读写都是 0 → 数据 dirty 地留在远端 L2。200.3 / 199.3 µs |
| **P4 并发**（48 个 GPM 各一个 queue，各读自己的 2 MB） （**默认参数，作废**）| ✅ 通过 | 48 个 GPM 都有指令；RDMA 事务 = 0；DRAM 读 1573536 = 48 × 32782；第 2 遍 48 × 32768 次本地 L2 命中。每遍 **13.9 / 10.3 µs**，单个 GPM（P1）是 11.4 / 7.8 µs → 约 1.2×，不是 48× |

### T13 阶段 3 校准（`platbench`，10-13，⏸ 偏差超过 10%，等用户决定）

代码：带宽、远程带宽、incast、通信模式、kernel 用 `0e38983`；延迟和跳数用修正后的 `ca7ea0b`（见日志）。两套配置的 `config_deviation` 只有 `-gpm-preset`、`-num-memory-banks`（mi300x）和 benchmark 参数。B/周期按 1 GHz 计（1 B/周期 = 1 GB/s）。

| 项 | 目标（mi300x） | hpca 实测 | mi300x 实测 | mi300x 偏差 |
|---|---|---|---|---|
| HBM 读（16 MB/GPM，读一遍） | 315 B/周期 | 59（2 CU 饱和；满 32 CU 57） | **243**（8 CU 饱和；满 38 CU 224） | **−23%** ⚠️ |
| HBM 写 | 315 | 66（1 CU） | 318（4 CU） | +1% ✅ |
| HBM 复制（读+写） | 315 | 61（1 CU） | 295（4 CU） | −6% ✅ |
| L2 读（1 MB，热身后 4 遍） | 2048 B/周期 | 622（16 CU；满 CU 419） | **631**（16 CU；满 CU 465） | **−69%** ⚠️ |
| L2 写 | 2048 | 638（8 CU） | **640**（8 CU） | **−69%** ⚠️ |
| L2 复制 | 2048 | 846（16 CU） | **856**（16 CU） | **−58%** ⚠️ |
| L1V 命中延迟（单线程，A 组） | — | 115.0 ns（≤ 16 KB） | 115.0–115.4 ns（≤ 32 KB） | 拐点和 L1V 容量一致 ✅ |
| L2 命中延迟 | — | 129.0 ns（24 KB–1 MB），130.7（2–4 MB） | 129.0 ns（48 KB–1 MB），130.7（2–4 MB） | |
| HBM 延迟（≥ 6 MB，A 组） | — | 169.7 ns | 164.7 ns | |
| TLB miss 代价（B − A，≥ 6 MB） | — | +104 ns | +104 ns | |
| 远程读延迟，远端 L2 命中（1 MB）1/3/6/12 跳 | — | 261 / 405 / 621 / 1053 ns | 261 / 405 / 621 / 1053 ns | |
| 远程读延迟，远端 HBM（64 MB，A 组）1/3/6/12 跳 | — | 312 / 456 / 672 / 1104 ns | 307 / 451 / 667 / 1099 ns | |
| 跳数延迟拟合（329 对，远端 L2 命中） | — | 198.1 ns + 71.8 ns/跳，最大残差 5.3 ns | 同左 | |
| 单对远程读带宽峰值（1 跳 / 6 跳，远端 L2） | — | 26.3 / 7.9 B/周期 | 26.4 / 8.0 B/周期 | |
| 单对远程读带宽峰值（1 跳 / 6 跳，远端 HBM 16 MB） | — | 21.7 / 7.5 B/周期 | 22.4 / 7.5 B/周期 | |

- **旧配置 HBM**：代码算的理论值 128 B/周期（4 个控制器 × 32 B/ns），实测 59–66 B/周期，和“64 B 请求只用到 128 B burst 的一半”的推算（约 64）一致。投稿版 Table II 写的 0.375 TB/s/GPM 是实测值的 6.4 倍。
- 延迟是**向量访存路径**的数字：单线程的 L1V 命中就要 115 ns（标量缓存命中只要 38 ns，即向量路径的流水线、地址转换和合并占了约 77 ns），各级之间只差 14 ns（L1→L2）和 35–40 ns（L2→HBM）。
- 延迟“不做每周期等效缩放”（用户 10-13 决定），所以 mi300x 的延迟和 hpca 基本相同；HBM 延迟差 5 ns 来自 DRAM 时序按纳秒换算后的取整。
- L2 拐点两套配置都在 4–6 MB 之间（每 GPM 的 L2 都是 4 MB：hpca 4 × 1 MB，mi300x 16 × 256 KB）。

### T14 第一档平台数据摘要（两套配置并排；CSV 在 `results_index/platform/`）

| 项 | 关键数字 | hpca | mi300x-eighth |
|---|---|---|---|
| 1 延迟阶梯 | L1V / L2 / HBM（ns，A 组） | 115.0 / 129.0 / 169.7 | 115.0 / 129.0 / 164.7 |
| | L1 拐点、L2 拐点 | 16→24 KB；4→6 MB | 32→48 KB；4→6 MB |
| | B − A（TLB）：≤128 KB / 256 KB–1 MB / 2–4 MB / ≥6 MB | 0 / +3 / +104 / +104 ns | 同左 |
| | 远程 B − A（64 MB，1–12 跳） | +732 ns（与跳数无关） | +732 ns |
| 2 带宽—CU 数 | HBM 读 / 写 / 复制峰值，B/周期（饱和 CU 数） | 59 (2) / 66 (1) / 61 (1) | 243 (8) / 318 (4) / 295 (4) |
| | L2 读 / 写 / 复制峰值（饱和 CU 数）；满 CU 时 | 622 (16) / 638 (8) / 846 (16)；419 / 423 / 698 | 631 (16) / 640 (8) / 856 (16)；465 / 460 / 702 |
| 3 跳数矩阵 | 固定开销 + 每跳 | 198.1 ns + 71.8 ns/跳 | 198.1 ns + 71.8 ns/跳 |
| | 残差（329 对） | 最大 5.3 ns，RMS 2.3 ns | 同左 |
| 4 单对远程带宽 | 1 跳：远端 L2 / 远端 HBM，B/周期（饱和 WG 数） | 26.3 (8) / 21.7 (16) | 26.4 (8) / 22.4 (16) |
| | 6 跳：远端 L2 / 远端 HBM | 7.9 (4) / 7.5 (8) | 8.0 (4) / 7.5 (8) |
| | 推算在途 line 数（带宽 × 空载延迟 / 64 B） | 1 跳约 109，6 跳约 78 | 同左 |
| 5 incast（owner = driver 25，1 MB） | 共享读 k = 1/2/4/8/16/47（总 B/周期） | 25.6 / 51.2 / 70.5 / 108.1 / 111.8 / 113.5 | 25.6 / 51.2 / 70.5 / 108.3 / 111.8 / 113.5 |
| | 不相交读 k = 1/2/4/8/16/47 | 26.4 / 50.5 / 92.5 / 115.3 / 113.0 / 90.8 | 26.4 / 50.3 / 64.9 / 92.2 / 92.9 / 93.5 |
| 6 通信模式 | all-to-all（每对 16 KB）：聚合 B/周期（推算的最大链路利用率） | 339（1.6%） | 340（1.6%） |
| | 邻居交换（256 KB） | 899（0.7%） | 902（0.7%） |
| | 转置 | 160（3.0%） | 157（2.9%） |
| | 环（按 driver ID）/ 蛇形环 | 182 / 182（0.5%） | 178 / 178（0.5%） |
| 7 kernel 边界开销 | 单 GPM / 48 GPM 各一个 queue / 统一 GPU 1 GPM / 统一 GPU 48 GPM（连续 10 个的平均间隔） | 896 / 945 / 928 / 1114 ns | 886 / 936 / 927 / 1114 ns |

**和预期不一样的现象**

1. **L2 带宽在 16 个 CU 处见顶，之后下降 25–33%**，而且两套配置几乎一样（两者的 L2 理论吞吐差 2 倍）→ 瓶颈在 L2 之前，CU 越多越差，像是某处的争用或重试。
2. **mi300x 的 HBM 读（243）比写（318）低 24%**；hpca 1 个 CU 就能把 HBM 打满。
3. **远程 TLB miss 很贵**：本地 B − A 是 104 ns，远程是 732 ns，而且和数据离多远无关（发起方固定是 GPM 1）。推测远程页的地址转换要去中心的 IOMMU。这对应第二档第 12 项（`[提案]`，没做）。
4. **跳数延迟只取决于跳数**（残差 ≤ 5.3 ns）：边角和中心没有差别，中心 tile 没有让路由绕路。唯一的系统性差别是方向：从 `GPU[0]` 到 `GPU[48]` 比反方向慢约 10 ns（1064.7 vs 1054.7 ns）。
5. **单对远程带宽只有链路带宽的 3%（26 / 768），而且大致和延迟成反比**（6 跳只有 7.9）。推算的在途请求数在 1 跳和 6 跳时不同（109 vs 78），都超过 RDMA 的 64，所以不是单纯被“64 个 outstanding”卡住；没有 RDMA 表的统计，没法确认瓶颈。
6. **incast 的共享读没有任何好处**：k ≥ 8 时，共享读和不相交读都卡在约 113 B/周期（owner 端），共享读的总时间随 k 线性增长 → owner 端没有合并相同 line 的请求。对 T7（区域复制）来说：热点 owner 最多只能服务约 4 个全速读者。hpca 的不相交读在 k = 47 时从 115 降到 91；mi300x 在 k = 4 时就比 hpca 低（65 vs 92）。
7. **48 个 GPM 同时通信时，每个 GPM 的带宽远低于单对**（环：每 GPM 3.8 B/周期，单对 1 跳是 26），而推算的链路利用率不到 3% → 瓶颈不在 mesh 链路（至少按 YX 路由推算如此），在端点（RDMA 或 owner 端）。T10 的代价模型不能只按链路带宽算。
8. 第 1 项最初的实现测到的是**标量缓存**（见日志），已修正重跑。

## 日志

<!-- 追加格式：
### YYYY-MM-DD HH:MM (T#)
- 做了什么
- 结果 / 遇到的问题
- 下一步
-->

### 2026-10-12 23:39 (T11) 阶段 A：第 0 步代码核对（只读代码，没有运行任何东西）

**Q1 fork 的版本**
- `mgpusim/go.mod:1`：`module github.com/sarchlab/mgpusim/v3`，依赖 `github.com/sarchlab/akita/v3 v3.0.0`（`mgpusim/go.mod:10`）。
- `akkalat/go.mod:29-31`：`replace` 到本地的 `../mgpusim` 和 `../akita`，所以实际用的是仓库里改过的源码。Go 1.22.0（`mgpusim/go.mod:48`、`akkalat/go.mod:33`）。
- 结论：fork 是 **v3**，不是上游的 v5。

**Q2 driver API（全部存在，签名和 T11 表格一致）**
| 函数 | 位置 | 签名 / 行为 |
|---|---|---|
| `SelectGPU` | `mgpusim/driver/api.go:57` | `(c *Context, gpuID int)`，只设置 `c.currentGPUID` |
| `CreateUnifiedGPU` | `api.go:66` | `(c *Context, gpuIDs []int) int` |
| `CreateCommandQueue` | `api.go:103` | `(c *Context) *CommandQueue`，`q.GPUID = c.currentGPUID`，所以 queue 绑定到创建时选中的 GPU |
| `DrainCommandQueue` | `api.go:116` | `(q *CommandQueue)`，阻塞到这个 queue 为空 |
| `AllocateMemory` | `api.go:133` | `(ctx *Context, byteSize uint64) Ptr`，内部调用 `memAllocator.Allocate(pid, size, ctx.currentGPUID)`（`api.go:150`）；普通 GPU 用 `device.allocatePage()`，页面在这个 GPU 上（`internal/memoryallocator.go:98`、`:199-206`） |
| `Remap` | `api.go:175` | `(ctx *Context, addr, size uint64, deviceID int)` |
| `Distribute` | `api.go:182` | `(ctx *Context, addr Ptr, byteSize uint64, gpuIDs []int) []uint64` |
| `EnqueueLaunchKernel` | `mgpusim/driver/kernel.go:14` | `(queue *CommandQueue, co *insts.HsaCo, gridSize [3]uint32, wgSize [3]uint16, kernelArgs interface{})`；queue 绑定普通 GPU 时，整个 kernel 只发给这个 GPU；绑定 unified GPU 时走 `enqueueLaunchUnifiedKernel`（`kernel.go:145`） |
| `LaunchKernel` | `kernel.go:86` | 便捷版：新建 queue、enqueue、drain |

- GPU 编号从 1 开始：`d.GPUs[queue.GPUID-1]`（`mgpusim/driver/driver.go:358`、`:391`）。
- 注意：`EnqueueLaunchKernel` 用 `ctx.currentGPUID` 分配 code object/kernarg/packet（`kernel.go:40-51`），见“规格与代码的差异”。

**Q3 有没有 `benchmarks/mccl`**
- 有：`mgpusim/benchmarks/mccl/`（`allreduce.go/.cl`、`broadcast.go/.cl`、`communicator.go` 等）。`broadcast.go:20` 给每个 GPU 建一个 queue，`:49` `SelectGPU`，`:56` `EnqueueLaunchKernel`，`:66` `DrainCommandQueue`；`broadcast.cl:1` 是 `pushData`（远程写），`allreduce.cl:1` 是 `reduceData`。
- mccl 没有在 `akkalat/benchmarkselection/benchmark.go` 里注册，现有实验流程跑不到它。

**Q4 wafer 平台上远程请求走哪个组件；哪个统计能区分远端 L2 命中和远端 HBM**
- 路径：**L1V → 本 GPM 的 RDMA 引擎 → mesh → 远端 GPM 的 RDMA 引擎 → 远端 L2 → （miss 时）远端 DRAM**。
  - 请求方：`connectL1ToL2` 里，地址不在本 GPM 8 GB 范围内时由 `ModuleForOtherAddresses = rdmaEngine.ToL1` 接走（`akkalat/baseline/runner/r9nanobuilder.go:541-544`、`:578`）。RDMA 用 `RemoteRDMAAddressTable` 找到 owner 的 RDMA（`mgpusim/timing/rdma/comp.go:487`；表按 8 GB 一个 bank 建，`timingplatform.go:591-596`、`:820-829`）。
  - Owner 方：`processReqFromOutside` 用 `localModules.Find(addr)` 选本地 L2，然后从 `ToL2` 发出（`comp.go:600-616`；`localModules` 就是本地 L2 的 Top 口，`r9nanobuilder.go:554-561`）。L2 下面接的是本地 DRAM（`r9nanobuilder.go:596-606`）。
  - 所以**按代码，远程读会经过远端 L2**；P3a 用实验确认。
  - 网络是 akita 的 mesh（`timingplatform.go:19`、`:602-616`），7×7，没有单独的 NI 组件。CuPath 的远程数据通路在 RDMA 内部（`rdma/remote_requester.go:357`），默认关闭。
- 统计：用 **`-report-l2-source`**（`akkalat/baseline/runner/flag.go:207`；启用在 `runner.go:186-197`），输出 `<metric-file-name>_l2_source_*.csv`（`akita/mem/trace/l2source_dump.go`）：
  - `_data_source.csv` 的 `source` 列：L2 命中记成 `l2_cache` / `l2_mshr`（`akita/mem/cache/writeback/directorystage.go:378`、`:410`、`:543`、`:561`），L2 miss 后从 DRAM 填充记成 `dram`（`writebufferstage.go:832-842`）。经过 RDMA 的请求加前缀 `remote_`，否则加 `local_`（`akita/mem/trace/l2source_record.go:254-259`）。所以 **`remote_l2_cache` / `remote_l2_mshr` 对 `remote_dram`** 就能区分远端 L2 命中和远端 HBM，并且每行都带 requester/provider GPM 和跳数。
  - `_summary.csv` 的 `remote_gpm` 行（表头见 `l2source.go:21`）和 `_remote_matrix.csv`：每对 GPM 的远程访问数、字节数、平均延迟和跳数，可以直接用于 P3b。
  - 辅助统计：`-report-rdma-transaction-count`（`flag.go:77`）、`-report-dram-transaction-count`（`flag.go:79`）。

**Q5 现有 benchmark 用 unified GPU 还是每个 GPM 一个队列**
- **unified GPU**。runner 在 `Init` 里无条件调用 `createUnifiedGPUs()`（`akkalat/baseline/runner/runner.go:160`），把 GPU 1–48 绑成一个 unified 设备，`r.GPUIDs = []int{unifiedID}`（`runner.go:515-529`）；`AddBenchmark` 把它传给 benchmark（`runner.go:547-553`，入口 `akkalat/baseline/main.go:28-34`）。
- 以 fir 为例：`SelectGPU(ctx, gpus[0])` 选中 unified 设备（`mgpusim/benchmarks/heteromark/fir/fir.go:78`），只建一个 queue（`fir.go:59`）。
- unified launch 时 WG 的切分方式（`mgpusim/driver/driver.go:433-466`）：`wgPerCU = ceil(总WG/总CU)`，每个 GPU 分到 `cuCount × wgPerCU` 个连续的 WG。**和上游一样，WG 少的时候前面的 GPU 分到全部 WG，后面的一直空闲**（`driver.go:386-389` 跳过分到 0 个 WG 的 GPU）。
- 另外有 `AddBenchmarkWithoutSettingGPUsToUse`（`runner.go:557`），探针可以用它绕过 unified 设备，自己按 GPM 建 queue。

**阶段 B 的计划（等用户确认）**
- 新增 `mgpusim/benchmarks/microbench/probe/`（`touch` kernel 用 fp32，hsaco 在本机编译），并在 `SelectBenchmark` 里加 `case "probe"`。不改任何已有组件的行为。
- P2 用 `-report-inst-count`（`flag.go:69`；每个 CU 输出 `cu_inst_count`，`report.go:846-868`）加上每个 GPM 的 CP `kernel_time`（`report.go:951-957`）。
- 下一步：等用户确认阶段 A，并决定上面“需要用户决定”的 3 条。

### 2026-10-13 04:55 (T11) 阶段 B：探针实验完成

- 用户 10-13 确认阶段 A（“先实现吧”），三个待定事项按推荐方案执行（见“需要用户决定”）。分支 `isca27/t11-probe-placement-comm`，提交：`b465d45` 探针 + 骨架，`0ca173f` 运行脚本，`a097f31` `.gitignore`，`d1f6d8c` 汇总脚本，`f7b9eef` 每个 rank 一个 Context（修死锁），`a874e5f` 心跳。
- **SPMD 骨架**（给 T10 复用）：`mgpusim/benchmarks/microbench/probe/spmd.go`
  - `NewSPMD(driver, baseCtx, gpus)`：每个 GPM 一个 rank，每个 rank 一个 **Context（`InitWithExistingPID`，共享 PID，所以虚拟地址空间相同）** 和一个 command queue。
  - `AllocShards(bytes)` / `AllocFor(rank, gpu, bytes)`：分片 i 的物理页在 `GPUs[i]` 上，并由 rank i 的 Context 跟踪脏状态。
  - `UploadPtrTable(shards)`：把所有分片地址写成 uint64 数组，复制到每个 GPM，kernel 里用 `table[j]` 访问 rank j 的分片（push / pull 都可以）。
  - `RunPhase(co, []Launch)`：先给每个 rank enqueue，再逐个 drain，一个阶段结束才进入下一个阶段；`Now()` 读模拟时间，用来算每阶段耗时。
  - 注意：每个 rank 的 Context 一直选在自己的 GPM 上，因为 driver 把 code object / kernarg / packet 分配在当前选中的 GPU 上（`kernel.go:40-51`）；必须单 goroutine 驱动。
  - 探针本身：`probe.go`（`touch` kernel，fp32），在 `akkalat/benchmarkselection/probe.go` 注册为 `-benchmark probe`，参数 `-probe-owner/-probe-runner/-probe-bytes/-probe-mode/-probe-passes/-probe-threads/-probe-all-gpms`。
- **遇到的问题**：
  1. 第一版骨架所有 rank 共用一个 Context，P4 死锁（只有 17/48 个 GPM 启动）。原因见“需要用户决定”里的 flush 竞争；改成每个 rank 一个 Context 后 48 个 GPM 全部完成，P1–P3 的数字和改之前完全一致。修复前的结果（commit `0ca173f` / `a097f31` / `d1f6d8c`）作废，以 `f7b9eef` 为准。
  2. 第一次 P4 跑了 40 分钟没人发现卡住。为此给 runner 加了**可选的心跳**（`akkalat/baseline/runner/heartbeat.go`，`-heartbeat-file` 或环境变量 `EXP_HEARTBEAT`）：每 10 秒写模拟时间和已完成 WG 数，不改模拟器行为。`exp web` 网页据此判定卡住：模拟时间 3 分钟不推进，或完成的 WG 数 15 分钟不增加。用旧的死锁版本验证过：模拟时间停在 24533 ns、WG 停在 136，1 分钟阈值下 61 秒被标出。
- **对 T10 的结论**：数据放置、kernel 放置、远程 load/store 都可以在不改模拟器的前提下实现；远程读写都经过远端 L2。需要注意：(1) 一定要每个 GPM 一个 Context；(2) 远程带宽约为本地的 1/18，带宽受限时远端 L2 命中带来的好处被 RDMA/mesh 排队掩盖；(3) kernel 之间 L2 内容会保留（P1、P3a 第 2 遍都命中），但前提是没有触发 flush。
- 下一步：等用户看“需要用户决定”里的两条新问题；T10 仍是 `[提案]`，没有开始。

### 2026-10-13 (T11/T12) 更新待决事项；T12 配置确认（⏸ 等用户确认）

- 按用户 10-13 的答复，把“需要用户决定”里的 flush 竞争（→ T12 修 driver）和“远程带宽 1/18”（→ 参数用错）两条标为已决定；T11 结果摘要里 P3a/P3b/P3c/P4 的性能数字标为“默认参数，作废”。
- `akkalat/t11_run_probe.sh` 改为从 runall2 取参数：新增 `akkalat/t11_probe_flags.py`，调用 `runall2_config.build_common_flags(runall2_args.parse_args(["--disable-servers", "--max-wg", "0", "--report-l2-source"]))`，没有手写任何配置参数；meta.json 新增 `config` 和 `config_deviation` 字段（commit `d6b765c`，分支 `isca27/t11-probe-placement-comm`）。
- 新建分支 `isca27/t12-driver-flush-fix`（基于 `d6b765c`）。

**CONFIG.md 第 3 节 · 第 1 步：7 个文件和 95b4f46 对比**

| 文件 | HEAD | 95b4f46 | 结果 |
|---|---|---|---|
| `akkalat/runall2_constants.py` | c46181e7ddbc | c46181e7ddbc | 一致 |
| `akkalat/runall2_config.py` | 073257215378 | 073257215378 | 一致 |
| `akkalat/baseline/runner/flag.go` | 142c66f8fec7 | 142c66f8fec7 | 一致 |
| `akkalat/baseline/runner/shaderarray.go` | 0cad15f3f860 | 0cad15f3f860 | 一致 |
| `akkalat/baseline/runner/r9nanobuilder.go` | be11d0d9cd45 | be11d0d9cd45 | 一致 |
| `akkalat/baseline/runner/timingplatform.go` | 7f4c214b78e6 | 7f4c214b78e6 | 一致 |
| `akkalat/baseline/runner/runner.go` | c84865b80dcc | c84865b80dcc | 一致 |

95b4f46 之后 `akkalat/baseline`、`runall2_*`、`mgpusim/driver`、`akita` 下只多了 `heartbeat.go`（新文件）和 `tracers.go` 的 2 行（启动心跳），另外新增了探针 benchmark（`mgpusim/benchmarks/microbench/probe/`、`akkalat/benchmarkselection/probe.go` 和一个 `case "probe"`）。

**第 2、3 步：T12 要跑的实验、完整参数、和论文配置的差异**

论文配置的公共参数（由 `build_common_flags(--disable-servers --max-wg 4096)` 生成）：
```
-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16 -bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10 -rdma-max-outstanding=64 -magic-memory-copy -report-all -disable-servers -mmutlb-lookup-latency=80 -max-wg=4096
```

⚠️ **重要发现**：论文配置里的 `-magic-memory-copy` 会让 driver 用 `globalStorageMemoryCopyMiddleware`，**根本不加载**要修改的 `defaultMemoryCopyMiddleware`（`mgpusim/driver/builder.go:102-114`）。所以 R1 只能证明“论文配置不受影响”，测不到修改本身是否改变了单 queue 的时序。为此建议加 R2。

| 编号 | 内容 | 参数 | 和论文配置的差异 |
|---|---|---|---|
| R1 回归（论文配置） | fir、matrixmultiplication，修复前（`d6b765c`）和修复后各跑一次，同一台服务器 | 上面的公共参数 + `-benchmark fir` / `-benchmark matrixmultiplication` | `-max-wg=4096`（benchmark 截断参数，T12 要求“较小的 -max-wg”；原脚本默认 78600）。无配置修改 |
| R2 回归（**建议追加**，走被修改的代码） | 同 R1，但去掉 `-magic-memory-copy`，让单 queue 的 H2D/D2H 走 `defaultMemoryCopyMiddleware` | 公共参数去掉 `-magic-memory-copy` | **少了 `-magic-memory-copy`**（为了覆盖被修改的代码路径；只用于 T12 回归，不进论文） |
| R3 共用 Context 的 P4 | 探针 `-probe-all-gpms -probe-shared-ctx`（新开关，所有 rank 共用一个 Context），2 MB/GPM，2 遍；修复后跑一次。**建议**修复前也跑一次（带心跳），确认在论文网络参数下也能复现死锁 | 公共参数去掉 `-magic-memory-copy` 和 `-max-wg`，加 `-report-l2-source -l2-source-tile-width=7` + `-benchmark probe -probe-all-gpms -probe-shared-ctx -probe-bytes 2097152 -probe-passes 2` | **少了 `-magic-memory-copy`**（T12 明确要求：不加它才会走 flush 路径）；统计开关 `-report-l2-source`；benchmark 参数 `-probe-*` |

判定标准：R1、R2 比较修复前后的 `metrics.csv`，要求**逐行完全一致**（包括 `kernel_time`、`total_time` 和所有 cache/DRAM/RDMA 计数）；R3 要求 48 个 GPM 都有指令、两遍都完成、心跳不报卡死。单元测试（`mgpusim/driver` 的 Go 测试）不涉及配置。

- 下一步：⏸ 等用户确认上面的参数和差异（尤其是 R2、R3 修复前那次要不要跑），然后再改 `memorycopy.go`。

### 2026-10-13 06:40 (T12) 修复 driver flush 竞争：实现、单元测试、回归（⏸ 等用户看结果）

- 用户确认了配置确认里的参数和差异，并同意追加 R2、R3 修复前也跑一次。
- **提交**（分支 `isca27/t12-driver-flush-fix`）：
  - `7d89a72`（修复前的基准）：探针新增 `-probe-shared-ctx`（`mgpusim/benchmarks/microbench/probe/spmd.go` 的 `NewSharedCtxSPMD`，`akkalat/benchmarkselection/probe.go`）；通用运行脚本 `akkalat/isca27_run.sh` + 参数工具 `akkalat/isca27_flags.py`（从 `build_common_flags` 生成参数，去掉的参数连同原因写进 `config_deviation`）。
  - `9c30a89`（修复）：只改了 `mgpusim/driver/memorycopy.go` 和它的测试。
- **修改内容**（`mgpusim/driver/memorycopy.go`）：
  - 新增 `completeIfDone`（第 299–323 行）：命令的请求全部返回时收尾：`IsRunning=false`；D2H 先把 `RawData` 解码到 `Dst`；`Dequeue`；`logCmdComplete`。收尾顺序和原来两处代码一致。
  - `processMemCopyH2DReturn`（第 254 行）、`processMemCopyD2HReturn`（第 272 行）改为调用它（原来各自写了一份收尾代码）。
  - `processFlushReturn`（第 277–297 行）改用 `cmd, cmdQueue := findCommandByReq(req)`，删除请求后也调用 `completeIfDone`（第 294 行）。**这就是修复本身**：原来 flush 回复最后到达时没人检查命令是否完成。
  - **没有改**：flush 的触发条件（`needFlushing`）、flush 发给全部 GPU（`sendFlushRequest`）、`-magic-memory-copy` 的路径（`memorycopyglobalstorage.go`）、`cyclesLeft`/`awaitingReqs`。
- **可选的第 2 步（`cyclesLeft` 按命令记录）：评估后不做。** akkalat 平台创建 driver 时没有设置拷贝延迟（`akkalat/baseline/runner/timingplatform.go:455` 只调用了 `driver.MakeBuilder()`，没有 `WithH2DCycles`/`WithD2HCycles`），所以 `cyclesPerH2D = cyclesPerD2H = 0`：每次拷贝都把共享的 `cyclesLeft` 设成 0，下一个 tick 就把所有积压的请求一起发出，多个 queue 之间**不会互相推迟**。只有 mgpusim 自带的 sample runner 设了 8500/14500 周期，我们不用它。
- **单元测试**（`mgpusim/driver/memorycopy_test.go`，新增 4 个）：H2D 和 D2H 各测“flush 回复晚于拷贝回复”和“flush 回复早于拷贝回复”两种顺序，命令都能出队，D2H 的数据正确解码。`go test ./driver/` 全部通过（29 个）。**用同样的测试跑修复前的 `memorycopy.go`，正好是两个“flush 最后返回”的测试失败**，说明测试确实测到了这个 bug。
- **回归和 P4 结果**（结果目录在本机 `remote/<服务器>/results/`，同时在服务器的 `~/exp/cupath/results/`）：

| 编号 | 实验 | 修复前（`7d89a72`） | 修复后（`9c30a89`） | 结论 |
|---|---|---|---|---|
| R1 | fir，论文配置，`-max-wg=4096` | total_time 26.662 µs | total_time 26.662 µs | ✅ 100477 个统计排序后**完全一致** |
| R1 | matrixmultiplication，论文配置，`-max-wg=4096` | total_time 378.553 µs | total_time 378.553 µs | ✅ 105952 个统计排序后**完全一致** |
| R2 | fir，去掉 `-magic-memory-copy`，`-max-wg=4096` | total_time 1499.347 µs | total_time 1499.347 µs | ✅ 100476 个统计排序后**完全一致** |
| R2 | matrixmultiplication，同上 | total_time 1869.706 µs | total_time 1869.706 µs | ✅ 105952 个统计排序后**完全一致** |
| R3 | P4，48 个 GPM 共用一个 Context，去掉 `-magic-memory-copy`，2 MB/GPM，2 遍 | **死锁**：运行 4.5 分钟后模拟时间停在 58669 ns、完成 640 个 WG，进程还活着但不再推进（CPU 占用降到很低）；心跳 3 分钟不推进被检测到，手动结束（exit 143），卡住时的心跳存在 `heartbeat_at_hang.json` | **完成**：48 个 GPM 都有指令，两遍都完成（114.5 / 114.6 µs），RDMA 事务 0，exit 0 | ✅ 论文网络参数下也能复现死锁，修复后消失 |

  exp_id：`1013_t12-R1-<benchmark>-maxwg4096-<pre|post>-fix_wafer48_paper_<commit>`、`1013_t12-R3-P4-sharedctx-<pre|post>-fix_wafer48_paper-nomagic_<commit>`；每个 meta.json 都有 `config_deviation`。
- 13:55 更新：R2 已全部跑完并通过（见上表），**T12 完成**。T12 的 10 个结果目录复制到了本机仓库根目录的 `results/1013_t12-*`。
- 处理网页上的“失败 / 异常中断”（用户要求）：yss3 上 3 条都已查明原因、无需重跑，把日志从 `~/exp/cupath/logs/` **移到** `logs/archived/`（没有删，附 `README.txt` 说明）：`p3b`（我启动命令里的循环变量 bug，已重跑）、`p4`（T11 第一次 P4，撞上 T12 修的死锁，`exp stop` 结束，已重跑）、`t12-r3-pre`（预期内：修复前复现死锁后手动结束，证据在结果目录）。结果目录都没有动。
- 下一步：⏸ 等用户看 T12 结果，确认后进入第 2 段（T13 阶段 1，基于 `isca27/t12-driver-flush-fix` 新建分支）。

### 2026-10-13 (T13) 阶段 1：参数定位（只读代码，没有运行、没有改代码；⏸ 等用户确认）

- 新建分支 `isca27/t13-mi300x`（基于 T12 的 `9c30a89`）。配置确认第 1 步：7 个文件和 95b4f46 的 blob 全部一致（同 T12 那次）。
- 符号：🟢 已有命令行参数，不用改代码；🟡 写死在代码里，需要改成预设参数；⚪ 不变。路径相对仓库根目录。

| 参数 | 旧配置（代码里的实际值） | 新配置目标 | 在哪里设置 | 要不要改代码 |
|---|---|---|---|---|
| 每 GPM 的 CU 数 | 8 个 SA × 4 CU = 32 | 19 × 2 = 38 | SA 数和每 SA 的 CU 数是平台默认值 `numSAPerGPU: 8`、`numCUPerSA: 4`（`akkalat/baseline/runner/timingplatform.go:100-101`），**没有命令行参数**；传给 GPU builder 在 `timingplatform.go:684-685` | 🟡 |
| driver 登记的 CU 数 | `CUCount: 32`（`timingplatform.go:801`） | 38 | 写死。它决定 unified GPU 切分 WG 的方式（`mgpusim/driver/driver.go:441-466`），必须和真实 CU 数一致 | 🟡（改成 SA 数 × 每 SA 的 CU 数） |
| L1 vector | 每 CU 16 KB，4 路，64B line，1 个 bank，延迟 10 | 32 KB，4 路 | `akkalat/baseline/runner/shaderarray.go:381-392`（`WithTotalByteSize(16 * mem.KB)` 写死） | 🟡 |
| L1 instruction | 每 SA 32 KB，4 路，每周期 4 个请求 | 每 2 个 CU 共享 64 KB，8 路 | `shaderarray.go:537-549`，写死；共享范围 = 一个 SA | 🟡（SA 改成 2 个 CU 后，共享范围自然变成 2 个 CU） |
| L1 scalar | 每 SA 16 KB，4 路 | 每 2 个 CU 共享 16 KB | `shaderarray.go:459-468`，写死 | ⚪（大小不变；共享范围随 SA 自动变成 2 个 CU） |
| LDS | 64 KB | 64 KB | `mgpusim/timing/cu/computeunit.go:142-144`，写死 | ⚪ |
| L2 容量 | 4 MB | 4 MB | `-l2-cache-size-mb`（默认 4，`flag.go:112`） | 🟢 |
| L2 分区数 | 4 个 × 1 MB（`-num-memory-banks=4`） | 16 个 × 256 KB | `-num-memory-banks`（`flag.go` 默认 16，`BASE_COMMON_FLAGS` 设成 4）；每个分区大小 = 容量 / 分区数（`r9nanobuilder.go:779`）。**这个参数同时决定 L2 分区数和 DRAM 控制器数，两者 1:1 相连**（`r9nanobuilder.go:593-606`） | 🟢（预设里设成 16） |
| L2 相联度 | 16 路 | 16 路 | `r9nanobuilder.go:784`，写死 | ⚪ |
| L2 吞吐 | 每分区每周期 16 个请求（4 × 16 × 64B ≈ 4 KB/周期） | 每 channel 2 个（16 × 2 × 64B = 2 KB/周期） | `WithNumReqPerCycle(16)`，`r9nanobuilder.go:787`，写死 | 🟡 |
| L2 MSHR | 每分区 64（每 GPM 256） | 每 channel 16（每 GPM 256） | `WithNumMSHREntry(64)`，`r9nanobuilder.go:786`，写死 | 🟡 |
| L2 目录延迟 | 10 | 不在目标表里 | `r9nanobuilder.go:788` | ⚪ |
| L2 交错粒度 | 128 B（`adaptivePairInterleavingLog2(2)` = 2^7，`timingplatform.go:360-368`、`:688-689`） | 不变 | 由 `-l2-adaptive-pair-region-lines`（默认 2）决定 | ⚪ |
| 内存 channel 数 | 4 | 16 | 同上：`-num-memory-banks` | 🟢 |
| HBM 带宽 | 理论 128 B/周期/GPM（见“需要用户决定”） | ≈ 315 B/周期/GPM，每 channel 19.7 | DRAM 时序全部写死：500 MHz、HBM 协议、256-bit、BL4、tCL 7、tRCD 7、tRP 7、tRAS 17 等（`r9nanobuilder.go:945-1000`）；带宽由时序推出（`akita/mem/dram/builder.go:435-436`、`:529-551`、`:781-793`） | 🟡（取法见“需要用户决定”） |
| HBM 容量 | 8 GB/GPM | 8 GB | 每控制器 = 8 GB / 控制器数（`r9nanobuilder.go:945-949`），16 个控制器时整除 | ⚪ |
| typed filter | 每 1 MB 分区 32K 槽 | 每 256 KB channel 8K 槽，每 GPM 总量不变 | 自动按分区大小计算（见“规格与代码的差异”）；格式、端口数、延迟来自 `DEFAULT_BENCHMARK_FLAGS` | ⚪（不用改） |
| `-prefetch-predictor-entries` | 256 | 256 | `DEFAULT_BENCHMARK_FLAGS` | ⚪ |
| 时钟 | 1 GHz | 1 GHz | `r9nanobuilder.go:122` | ⚪ |
| wafer 网络、RDMA、MMU、L1 MSHR | `BASE_COMMON_FLAGS` | 不变 | — | ⚪ |

**和 2 的幂相关的检查**（38 个 CU、19 个 SA、16 个 channel）：
- WG 分派：CP 的 partition 和 round-robin 两种算法都只用 `NumCU()` 做除法和取模（`mgpusim/timing/cp/internal/dispatching/partition.go:41-58`），没有 2 的幂假设。unified GPU 切分 WG 用 driver 登记的 `CUCount`（`driver.go:441-466`），只要把它改成 38 就没问题。
- SA 数 19：`buildSAs` 只是循环（`r9nanobuilder.go:772-775`），没有发现依赖 SA 数是 2 的幂的地方。
- L2 / DRAM 交错：`InterleavedLowModuleFinder` 按“地址 / 交错单位 mod 分区数”选分区，16 是 2 的幂，旧配置的 4 也是。
- typed filter：bucket 数总是向上取整到 2 的幂，对任何容量都成立。
- DRAM 内部地址映射：选方案 A 时，每控制器 512 MB、64-bit 设备宽度，rank 数 = 16（2 的幂）；选方案 B 时 rank 数 = 4。两种都是整数，`8 GB / 16` 也能整除（`r9nanobuilder.go:945-949` 的检查能通过）。
- `-max-wg`：按完成的 WG 总数截断（`akkalat/baseline/runner/wgtracer.go`），和 CU 数无关。但 CU 多了 19%，同样的 `-max-wg` 在新配置下对应的模拟时间会变短，阶段 4 解读截断结果时要注意。
- **风险**：akkalat 的部分分析脚本可能假设 L2 只有 4 个分区（例如按 `L2[0..3]` 汇总），阶段 2 实现时一起检查。

- 下一步：⏸ 等用户确认本表，并决定“需要用户决定”里的 3 条（旧 HBM 带宽的数字、新 HBM channel 的取法、阶段 2 能否修改那几个配置文件）。

### 2026-10-13 (T12) 补写文档（用户同意破例修改 isca27/ 下的文档）

- 用户要求把 T12 的完成情况写回相应文档、并补上说明，同意这次修改 STATUS.md 以外的文档。
- `isca27/README.md`：索引里 T12 一行的状态改为“`[已定]` · ✅ 完成（10-13，commit `9c30a89`，见 STATUS）”。其他行没有动。
- `isca27/tasks/T12_driver_flush_fix.md`：勾上 4 条验收标准，末尾新增“完成情况”一节（修改、测试、回归、R3、`cyclesLeft` 不做的原因）。
- 代码仓库新增 `akkalat/docs/ISCA27_TOOLS.md`：`isca27_run.sh` / `isca27_flags.py` 的用法、模拟器心跳、探针参数和 GPM 编号、SPMD 骨架、T12 修复说明、监控网页。commit `36bde23`（在 `isca27/t12-driver-flush-fix` 上，`isca27/t13-mi300x` 已快进到同一个 commit）。

### 2026-10-13 (T13) 阶段 2：实现预设 `-gpm-preset`（用户已确认阶段 1 和 3 条决定）

- 用户 10-13 确认：T12 结果；阶段 1 参数定位表；旧配置 HBM 带宽以阶段 3 实测为准；新 HBM channel 用方案 A（延迟不做每周期等效缩放）；同意按方案 A 修改配置文件。另要求核实 (a)(b) 两处论文与代码的不一致，结果已写进“规格与代码的差异”。
- **提交** `f444076`（分支 `isca27/t13-mi300x`，基于 `36bde23`）。
- **Go 侧**：
  - 新文件 `akkalat/baseline/runner/gpm_preset.go`：`gpmPreset` 结构和两个预设。`hpca`（默认）= 投稿版原来写死的值；`mi300x-eighth` = 19 × 2 个 CU、L1V 32 KB 4 路、L1I 64 KB 8 路（每 2 个 CU 共享）、L1S 16 KB、每 L2 channel 16 个 MSHR 和每周期 2 个请求、要求 `-num-memory-banks=16`；HBM channel 64-bit、BL8、1.23 GHz，时序按纳秒不变换算（四舍五入，最大误差 0.4 ns：tCL/tRCD/tRP 17、tRAS 42、tREFI 4797、tRRDS 5、tRRDL 7、tWTRS 7、tWTRL 10、tWR 20、tCCDS/L 2、tRTP 7、tPPD 5、tCWL 5）。理论峰值 19.68 B/ns/channel × 16 = **314.9 B/周期/GPM**（目标 315.5）。`lookupGPMPreset` 在预设和 `-num-memory-banks` 不一致时直接报错。
  - `flag.go:186`：新增 `-gpm-preset`（默认 `hpca`）。
  - `runner.go:388-394`：按 `-gpm-preset` 查预设、打印 `[Config] GPM preset=...`，传给平台。
  - `timingplatform.go`：`numSAPerGPU`/`numCUPerSA` 默认值改成取自 `hpcaGPMPreset`（`:101-103`）；新增 `WithGPMPreset`；传给 GPU builder（`:697`）；driver 登记的 `CUCount: 32` 改成 `b.numSAPerGPU * b.numCUPerSA`（`:812`）。
  - `r9nanobuilder.go`：新增 `gpmPreset` 字段和 `WithGPMPreset`，默认 `hpcaGPMPreset`；把预设传给 shader array（`:765`）；L2 的 `WithNumMSHREntry(64)`/`WithNumReqPerCycle(16)` 换成预设字段（`:796-797`）；DRAM 的结构和全部时序换成 `b.gpmPreset.DRAM` 的字段（`createDramControllerBuilder`，`:955` 起）。
  - `shaderarray.go`：新增 `l1Sizes` 字段和 `withGPMPreset`；L1V（`:396-400`）、L1S（`:476`）、L1I（`:548-550`）的大小和相联度换成预设字段。
  - 单元测试 `gpm_preset_test.go`：① hpca 预设和投稿版的常量逐项相等（32 个 CU、L1 大小、L2 64/16、DRAM 全部 17 个时序）；② mi300x 的 CU 数、MSHR 总数 256、L2 2 KB/周期、DRAM 访问单位 64 B、理论峰值在 310–320 B/ns；③ 预设和 `-num-memory-banks` 不一致时报错；④ **实际构建整个 48-GPM 平台**：hpca 建出 32 个 CU + 4 个 L2/4 个内存控制器，mi300x 建出 38 个 CU + 16 个 L2/16 个内存控制器。全部通过。
- **Python 侧**（让论文原有的脚本可以选预设）：
  - `runall2_constants.py`：新增 `MI300X_EIGHTH_FLAGS = ["-gpm-preset=mi300x-eighth", "-num-memory-banks=16"]`、`GPM_PRESET_FLAGS`、`DEFAULT_GPM_PRESET = "hpca"`；`BASE_COMMON_FLAGS` 没有动。
  - `runall2_config.py`：新增 `apply_gpm_preset`（同名参数替换，其余追加），`build_common_flags` 先套用预设。
  - `runall2_args.py`、`run_cupath_llm.py`、`run_cupath_sensitivity.py`：新增 `--gpm-preset`（默认 `hpca`），各自的 `common_flags` 套用预设。
  - **默认参数完全不变**：改动前后，runall2（3 种参数组合）、run_cupath_llm、run_cupath_sensitivity 生成的参数列表逐字相同。
  - `isca27_flags.py`：预设带来的差异（`-gpm-preset=mi300x-eighth`、`-num-memory-banks=4 → 16`）在 `config_deviation` 里写明原因；`isca27_run.sh`：meta.json 新增 `gpm_preset` 和 `notes`（mi300x 时写明“带宽按 2.1 GHz 每周期等效、延迟保持纳秒值不缩放、HBM channel 参数”）。
  - 已有测试：`test_runall2_config`、`test_run_cupath_llm`、`test_runall2_process`、`test_run_cupath_sensitivity` 全部通过；`test_cupath_source_invariants` 里两个检查“源码里写着 64/16/256/BL4”的测试改成检查 hpca 预设里的同样数值，并检查 builder 用的是预设字段。剩下一个失败（`test_paper_uses_current_cupath_story_and_unambiguous_l2_name`，论文文字相关）**在改动前的 HEAD 上就失败**，和本次无关。
- **分析脚本里“L2 只有 4 个分区”的假设**（用户要求检查）：
  - ⚠️ `run_cupath_sensitivity.py:51` 写死 `FILTERS_PER_GPM = 4`，用来把“每 GPM 的 bucket 数”换算成每个分区的 `-typed-filter-capacity`。新配置下会把 filter 总容量放大 4 倍。**已修**：改成从预设生成的 `-num-memory-banks` 读出（hpca 仍是 4，manifest 不变，旧 campaign 可以继续 resume；mi300x 是 16）。参考点 16K bucket/GPM 在 mi300x 下 = 每 channel 1K bucket。
  - ⚠️ `cost/run_full_cacti.py:42-65` 写死“4 个 1 MiB L2 分区、4 个 filter、每分区 8K bucket”。只影响面积估算，新配置的面积要另算（阶段 4 之后再做，需要时我再提出）。
  - 其余脚本：`analyze_hidden_latency.py`、`analyze_pipeline_costs.py`、`plot_llm_like_bottleneck_snapshot.py`、`t11_analyze_probe.py` 用 `".L2[" in where` 通用匹配，不依赖分区数；`plot_cupath_work_reduction.py` 的 `range(4)` 是 4 个统计指标；`analyze_observations.py` 里的 `L2[0]` 是测试用的样例数据。
  - 遗留的不一致（不是本次引入）：主实验 filter 用代码默认的“2 × 块数”（hpca 每 GPM 32K bucket），敏感性实验的参考点是 16K bucket/GPM。阶段 4 重跑时这个差异仍然存在，需要时在“需要用户决定”里再提。
- **CONFIG.md 第 1 节的 blob 都变了**（用户同意的修改）：`runall2_constants.py` c46181e7ddbc → 0a6b0c4f2f5c；`runall2_config.py` 073257215378 → ea22add79a66；`flag.go` 142c66f8fec7 → 9ca510de581f；`shaderarray.go` 0cad15f3f860 → 743e0ae29af1；`r9nanobuilder.go` be11d0d9cd45 → 6c7c78ea4916；`timingplatform.go` 7f4c214b78e6 → c8636d99cc06；`runner.go` c84865b80dcc → 378f7313f8d4。CONFIG.md 我没有改，需要 Cowork 侧更新快照。
- **旧配置回归**（运行中）：参数和 T12 R1 完全相同（论文配置 + `-max-wg=4096`，用户在 T12 已确认，没有新的差异），fir 和 matrixmultiplication 用 `f444076` 在 yss2 上跑。修改前的基准复用 T12 R1 修复后的结果（`9c30a89`，同一台 yss2；`9c30a89` 到 `36bde23` 之间只加了一份文档，模拟器代码相同）。比较方法：`total_time` 单独比较，全部统计去掉行号排序后逐项比较。

### 2026-10-13 (同步/T13) 按 P06：同步、建立文档仓库、结果索引；推送待用户放行

- **同步**：读了 `SYNC.md` 全文、README 的“同步与备份”、`tasks/T12_driver_flush_fix.md` 新增的“10-13 用户决定：所有运行都保留 `-magic-memory-copy`”及 Cowork 加注、`site/`（8 个页面）、`prompts/P05`、`P06`。理解到的规格变化：① **以后所有运行都保留 `-magic-memory-copy`**；② T12 取消 R2（已经跑完的 R2/R3 只用于 T12 验证，不进论文）；③ **T12 的 R3 改为带 magic copy、用论文配置、修复后跑一次**（新要求，待配置确认后运行，见“需要用户决定”）；④ 每个停点要更新 STATUS → 追加 SYNC 日志 → 推送两个仓库。
- **P06 第二部分**（3 条答复、核实 (a)(b)）在 P06 到达之前已经按用户在对话里的相同答复完成，见“需要用户决定”里的已决定条目和“规格与代码的差异”里的 (a)(b)；补了一句 168 KiB 的来源。
- **P06 第三部分**：工作区里的 `gpm_preset.go`、`shaderarray.go` 等改动已经提交为 `f444076`，对照答复检查过：HBM 方案 A、延迟不缩放（写进了 STATUS 和 meta 的 `notes`）、预设集中在 `gpm_preset.go`、原文件只做常量替换（另加了 `-gpm-preset` 开关和 runner 的接线），没有需要改的地方。
- **文档仓库**：`~/Desktop/CuPath/isca27/` 已 `git init`（分支 `main`），`.gitignore` 忽略 `.DS_Store` 等；首次提交 `fb0eacd`（`cowork: initial import of isca27 docs`）。**还没有 remote**（见“需要用户决定”）。
- **结果索引**：`results_index/` 收录 T11 最终 16 个（`*_f7b9eef`）和 T12 的 10 个实验的 `meta.json`、`summary.csv`（T12 R3 修复前那个还附了 `heartbeat_at_hang.json`），加了一个 `README.md` 说明哪些数字作废；提交 `3332d37`。T11 修复死锁前的几轮已作废，没有收录。
- **代码仓库推送**：`isca27/t11-probe-placement-comm`（`d6b765c`）、`isca27/t12-driver-flush-fix`（`36bde23`）、`isca27/t13-mi300x`（`f444076`）都已提交、工作区干净，但 `git push` 被权限规则拦下，**还没有推送**。

### 2026-10-13 (T13) 配置文件 `-config`；不再用 `-disable-servers`

- **用户决定**：配置写进专门的配置文件，命令行不再写一长串；可以保存不同的配置、读取不同的文件做敏感性实验；不用 `-disable-servers`。
- **核实 `-disable-servers`**：它只关掉两个后台网页服务（Go pprof、akita 监控页面），都监听随机端口（`:0`，`runner.go:98-110`、`akita/monitoring/monitor.go` 的 `StartServer`），多个实验同时跑也不冲突。历史上的论文实验两种都有：带它的有 baseline-complete-observation、m1-m2-m3-rerun、两次 cupath-sensitivity、baseline-library；不带的有 sampled-validation、cupath-llm、filter-authoritative-validation。
- **实现**（commit `1b1e829`，已推送 `isca27/t13-mi300x`）：
  - `akkalat/baseline/runner/config_file.go`：`-config a.json,b.json`（键 = 参数名；后面的文件覆盖前面的；`"extends"` 继承另一个文件；`_` 开头的键是注释；参数名写错直接报错；命令行显式写的参数覆盖所有文件，并打印出来）；`-config-out` 写出每个参数的实际值和来源（默认值 / 哪个文件 / 命令行），其中的 `flags` 可以直接当配置文件用。`flag.go` 的 `ParseFlag` 第一行调用它。**二进制的默认值没有改。**
  - `akkalat/configs/hpca.json`（HPCA 投稿版 = `BASE_COMMON_FLAGS` + `-mmutlb-lookup-latency=80`）、`akkalat/configs/mi300x_eighth.json`（`extends` hpca，只改 `gpm-preset` 和 `num-memory-banks=16`）。两个都保留 `-magic-memory-copy`，都不含 `-disable-servers`。
  - `akkalat/isca27_config.py`：读取/合并配置文件，生成 `config_deviation`（和 `configs/hpca.json` 比）；`isca27_run.sh` 改成 `isca27_run.sh <exp_id> <commit> <task> <配置名> [benchmark 参数]`，配置名如 `hpca`、`mi300x_eighth,l2_8mb`；结果目录多一个 `config_used.json`。
  - 测试：`test_isca27_configs.py`（`hpca.json` ≡ `BASE_COMMON_FLAGS`；`mi300x_eighth.json` ≡ 预设参数；所有配置保留 magic copy、不含 disable-servers）；`runner/config_file_test.go`（继承、循环检测、仓库里的配置只用已有参数）。本机实测了单文件、继承、三层叠加、命令行覆盖、拼写错误。已有测试全部通过。
  - 论文原来的 runall2 / run_cupath_llm / run_cupath_sensitivity 不变，照旧显式传参。
- **用户确认后启动的两组**（yss3，`1b1e829`）：
  - 配置文件验证：`isca27_run.sh 1013_t13-cfgcheck-fir-maxwg4096-servers-on_wafer48_hpca_1b1e829 1b1e829 T13 hpca -benchmark fir -max-wg 4096`。和之前“写全参数 + `-disable-servers`”的 fir（T12 R1 / T13 回归）比较，统计必须完全一致，同时证明“配置文件 = 写全参数”和“开着服务不影响结果”。
  - T12 R3（带 magic copy）：`isca27_run.sh 1013_t12-R3-P4-sharedctx-magic_wafer48_hpca_1b1e829 1b1e829 T12 hpca -report-l2-source -benchmark probe -probe-all-gpms -probe-shared-ctx -probe-bytes 2097152 -probe-passes 2`。
  - 两个都已确认读到了 `hpca.json`，`disable-servers=false`、`magic-memory-copy=true`、`bandwidth=48`。
- 旧配置回归：fir 已通过（见阶段 2 日志）；matrixmultiplication 还在跑。

### 2026-10-13 15:25 (T13) 阶段 2 完成（⏸ 等用户确认）

| 检查 | 修改前（基准） | 修改后 | 结论 |
|---|---|---|---|
| 旧配置回归 fir（`-max-wg=4096`） | T12 R1 `9c30a89`（yss2）：total_time 26,662 ns | `f444076`（yss2，默认预设 hpca）：26,662 ns | ✅ 100,477 项统计排序后完全一致 |
| 旧配置回归 matrixmultiplication（`-max-wg=4096`） | T12 R1 `9c30a89`：378,553 ns | `f444076`：378,553 ns | ✅ 105,952 项统计排序后完全一致 |
| 配置文件 + 开着服务 = 写全参数 + `-disable-servers` | T13 回归 fir（写全参数，`-disable-servers`，yss2） | `1b1e829`，`-config hpca.json`，不带 `-disable-servers`（yss3） | ✅ 100,477 项完全一致；同时说明两台服务器的结果可以完全复现 |
| T12 R3（带 magic copy，48 个 GPM 共用一个 Context，按 T12 文档的新要求） | — | `1b1e829`，`-config hpca.json`（yss3） | ✅ 48 个 GPM 都有指令，两遍都完成（96 个 kernel），第 1 遍 37,602 ns、第 2 遍 6,803 ns，exit 0，没有卡死 |

- 结果目录：本机 `results/1013_t13-*`、`results/1013_t12-R3-P4-sharedctx-magic_*`；`results_index/` 已收录 meta.json、summary.csv、config_used.json。
- 阶段 2 的验收：`-gpm-preset` 默认值等于旧配置、旧配置回归完全一致 ✅；分析脚本里“L2 只有 4 个分区”的假设已检查（修了 `run_cupath_sensitivity.py`，`cost/run_full_cacti.py` 待以后另算面积）✅；所有运行都保留 `-magic-memory-copy` ✅。
- 另外（用户要求）：监控网页 `exp web` 新增单个结果的配置和统计汇总、2–4 个结果的对比（关键结果、配置差异、统计是否完全一致及差异列表）。这是本机工具（`~/.local/share/exp/`），不在代码仓库里。
- **下一步：⏸ 等用户确认阶段 2，然后进入阶段 3（校准）。** 阶段 3 运行前会按 CONFIG.md 第 3 节给出完整参数（现在是配置文件名 + benchmark 参数）和差异。

### 2026-10-13 (T13 阶段 3 + T14 第一档) 同步、微基准实现、配置确认（⏸ 等用户确认）

- **同步**：读了 SYNC 日志里 Cowork 的 2 条；`git diff` 读了 SYNC.md（第 1、3 节改为推送 `isca27/docs`）、CONFIG.md（10-13 更新：配置文件、基准 `1b1e829` 和 blob 表、两条检查方法）、00_background.md（投稿版 Table II：HBM 0.375 TB/s、4K bucket、168 KiB、4.70%）、README.md、T13 末尾补充、新文件 T14、site/、P07。以 `cowork: ...` 提交为 `eecffb4`。文档分支 `isca27/docs` 用户已推过一次（`c44d08f`），现已推到 `eecffb4`。
- **理解到的规格变化**：配置确认改为“配置文件名 + `-config-out` + 和 hpca.json 的差异”；T14 第一档 7 项和 T13 阶段 3 共用运行，结论分开写；测量方法按 T14 的规则。
- **微基准 `platbench`**（commit `0e38983`，已推送 `isca27/t13-mi300x`）：`mgpusim/benchmarks/microbench/platbench/`，`-benchmark platbench -plat-test <项>`；7 项的方法和参数写在 `akkalat/docs/ISCA27_TOOLS.md` 第 2.6 节。要点：
  - 指针追逐：1 个线程、随机指针环（每个 cache line 一个节点），A 组页内集中、B 组每步跨页；≤ 8 MB 先完整走一遍热身；然后各发一次 0 步和 N 步的 kernel，延迟 = (t_N − t_0)/N（抵消 kernel 启动开销）。已确认 kernel 之间 L1 不会被清空、单个 WG 每次都落在同一个 CU（partition 分派器），所以 L1 热身有效（冒烟：16 KB ≈ 38–40 ns，1 MB ≈ 54 ns）。
  - 带宽：每 CU 8 个 WG；启动“CU 数 × 8”个 WG，编号超出前 k 个 CU 的直接退出，从而只让 k 个 CU 干活。≥ 16 MB 视为 HBM（只读一遍），< 16 MB 视为 L2（热身后重复 4 遍）。
  - **遇到模拟器不支持的指令**：`s_cmp_le_u32`（SOPC opcode 9，`mgpusim/emu/alusopc.go:32`）。没有改模拟器，而是把我自己 kernel 里的计数和比较改成有符号 int，重新编译后 hsaco 里已经没有这条指令。
  - 每个点输出一行 `PLATCSV`，`isca27_run.sh` 抽成 `platform.csv`（统一列名，GPM 同时给 driver ID 和 `GPU[t]`）。
  - mesh 路由是先 Y 后 X（`akita/noc/networking/mesh/mesh_routing_table.go:36-43`），中心 tile 上是一个交换机（driver/IOMMU 挂在上面），路由可以穿过它，不会绕路。**模拟器没有链路利用率的统计**，第 6 项的每条链路利用率只能按 YX 路由和每对的流量推算（标注为推算值）。
  - `gpm_preset.go` 新增了 `CUsPerGPM()`（不改任何配置数值），所以它的 blob 变成 `4a149c51b45a`，和 CONFIG.md 表里的 `487bbd5e578d` 不同，需要 Cowork 更新表格。
- **冒烟测试**（本机，hpca 为主，只用于检查能跑和估算时间，**不作为结果**）：7 项都能跑。看到的苗头：hpca HBM 读满 CU 约 57 B/周期（代码推算 64 B 请求下的有效峰值约 64）；mi300x HBM 约 236 B/周期（目标 315，−25%）；L2 读 hpca 414、mi300x 480 B/周期（目标 2 KB/周期，−77%，可能受每个 L1 只有 16 个在途请求限制）；单对远程读 64 个 WG 只有约 26 B/周期，正好是 RDMA 64 个 outstanding × 64 B ÷ 约 200 ns。正式结果出来后按规则报告。

**配置确认**

| 配置 | `-config-out` 的 flags（13 个） | 和 `hpca.json` 的差异 |
|---|---|---|
| `hpca` | bandwidth=48 gpm-preset=hpca l1v-max-concurrent-trans=16 l1v-mshr-entries=16 magic-memory-copy=true mmutlb-lookup-latency=80 num-memory-banks=4 rdma-max-outstanding=64 rdma-pipeline-latency=10 rdma-pipeline-width=8 report-all=true switch-latency=32 timing=true | 无 |
| `mi300x_eighth` | 同上，但 gpm-preset=mi300x-eighth、num-memory-banks=16 | `-gpm-preset` hpca → mi300x-eighth；`-num-memory-banks` 4 → 16 |

两套都保留 `-magic-memory-copy`，都不带 `-disable-servers`。其余命令行参数都是 benchmark 参数（`-benchmark platbench -plat-*`）。

**运行计划**（每套配置 29 个进程；hpca 放 yss2、mi300x 放 yss3，各自并行；每个进程的模拟时间按冒烟测试的速度估算，mi300x 约慢 1.3–1.8 倍）

| 项 | 进程（每套配置） | 规模 | 估算（hpca / mi300x） |
|---|---|---|---|
| 1 延迟（本地） | 2：≤ 8 MB、≥ 16 MB | 4K,8K,16K,24K,32K,48K,64K,128K,256K,512K,1M,2M,3M,4M,6M,8M（热身）；16M,32M,64M,128M,256M,512M,1G（不热身）；A、B 两组；20000 步 | 5 / 8 分钟；10 / 15 分钟（1 GB 那一点的主机内存约 1.2 GB） |
| 1 延迟（远程） | 1 | 发起方 GPM 1，数据在 driver 2/4/7/48（1/3/6/12 跳）；1 MB（热身）、64 MB；A、B；20000 步 | 15 / 25 分钟 |
| 2 带宽—CU 数 | 3：read、write、copy | 1 MB（L2，热身后 4 遍）、16 MB（HBM，一遍）；CU 1,2,4,8,16,24,32,(38) | 每个 8 / 12 分钟 |
| 3 跳数矩阵 | 7 | 1 MB、10000 步；GPM 1 → 全部 47；全部 47 → GPM 1；另 5 个起点（角 7/43/48、中心左邻 24、上边中点 4）各到全部 47 个。共 329 对 | 每个约 40 / 60 分钟 ⚠️（最慢的项，mi300x 可能接近 1 小时） |
| 4 单对远程带宽 | 4 | 1 跳（1→2）、6 跳（1→7）× 1 MB（远端 L2）、16 MB（远端 HBM）；WG 1,2,4,…,256 | 每个 15 / 20 分钟 |
| 5 incast | 6（每个 k 一个） | owner = driver 25（中心右邻）；k = 1,2,4,8,16,47；shared、disjoint；1 MB | k=47 约 20 / 30 分钟，其余更短 |
| 6 通信模式 | 5 | all-to-all 每个来源 16 KB（256 KB 时总流量 577 MB，要 3 小时，所以缩小）；neighbor、transpose、ring、snake 每个来源 256 KB | 每个 10 / 15 分钟 |
| 7 kernel 开销 | 1 | 空 kernel：单 GPM、48 个 GPM 各一个 queue、统一 GPU（1 个 GPM / 全部 GPM），连续 10 个 | 1 / 1 分钟 |

- 没有单个测量点超过 1 小时；单个进程最长的是 mi300x 的跳数矩阵（约 1 小时）。总共约 1 小时墙钟时间（两台服务器并行）。
- exp_id：`1013_t14-<项>-<部分>_wafer48_<配置>_0e38983`。每个进程一个 tmux 会话（可以在监控网页上逐个看到并用心跳检测卡死）；为此会给 `exp` 加一个“只同步一次、启动多个任务”的用法。
- 完成后：T13 阶段 3 的校准结论写进 T13 部分；T14 的平台数据摘要表（两套配置并排）写进 STATUS；CSV 放进 `results_index/platform/`。偏差超过 10% 按规则停下来报告。

### 2026-10-13 17:00 (T13 阶段 3 + T14 第一档) 运行完成、校准结论（⏸ 偏差超过 10%，等用户决定）

- **运行**：用户确认后在 `0e38983` 启动，每套配置 29 个进程（hpca 在 yss2，mi300x 在 yss3），全部 exit 0。
- **发现并修正微基准 bug**：mi300x 的延迟阶梯在 16 KB 之后就从 38 ns 跳到 54 ns，可它的 L1V 是 32 KB。先用一个临时测试（反射读出建好的平台）确认预设生效（hpca L1V 16 KB、mi300x 32 KB，阶段 2 没有问题）；再反汇编发现 `chase` 内核的 `p = next[p]` 被编成了 `s_load_dword`（单线程时地址在 wave 内一致），测的是两套配置都一样的 16 KB **L1 标量缓存**。修正：起点加上 `get_local_id(0)`，反汇编确认只剩 `flat_load_dword`（`ca7ea0b`）。依赖 `chase` 的 10 项（lat-small/large/remote、7 个 hop）× 2 套配置停掉后用 `ca7ea0b` 重跑（exp_id 以 `_ca7ea0b` 结尾）。旧的 `0e38983` 延迟/跳数结果**作废**：已跑完的 lat-small、lat-large 保留在 `results/`，没有放进 `results_index/`；被停掉的 16 个只有部分输出，也保留。其他 19 项不用 `chase`（用 `flat_load_dwordx4`），结果有效。修正后 hpca 拐点在 16 KB、mi300x 在 32 KB，和容量一致。
- **结果**：见“结果摘要”的 T13 阶段 3 校准表和 T14 平台数据摘要表。`results_index/` 新增 58 个 `1013_t14-*` 目录（meta.json、summary.csv、config_used.json、platform.csv）；`results_index/platform/` 是汇总：`latency.csv`、`bandwidth.csv`、`hopmatrix.csv`、`remotebw.csv`、`incast.csv`、`patterns.csv`、`kernel.csv`、`pattern_links.csv`（每条链路的推算负载）、`summary.json`。汇总脚本 `akkalat/t14_analyze.py`（`a4738c8`）。
- **校准偏差超过 10%**：mi300x HBM 读 −23%，L2 读/写 −69%（两套配置都一样）。**按规则停下来，没有调任何参数**，见“需要用户决定”。**T13 阶段 4（全量重跑）没有开始。**
- 给 Cowork：这次没有改动模拟器和配置；`gpm_preset.go` 的 blob 仍是 `4a149c51b45a`（上一条已说明，CONFIG.md 的表需要更新）。

### 2026-10-13 19:15 (T13 阶段 4) 配置确认、启动全量重跑

- **用户 10-13 决定**：先用 mi300x 配置把论文实验跑一遍；四部分都跑，按 ① T11 探针 → ② 主结果 + filter 统计 → ④ LLM → ③ 敏感性 的顺序；**hpca 用当前代码重跑 ①②** 作为新旧对照；两台服务器各最多 16 个同时运行，留 30 GB 内存。用户确认了下面的配置和计划（“OK，跑实验吧”）。
- **用的脚本**（都是论文原来的，只加 `--gpm-preset`）：② `runall2.py --remote-ablation --configs=all`（70 个）和 `--configs=complete --extra-benchmark-flags=-typed-filter-authoritative-audit=true`（14 个，Fig 13 用）；④ `CuPathLLM.py --configs=baseline,complete --cu-per-gpu=38`（44 个）；③ `run_cupath_sensitivity.py --include-reference`（252 个，拆成 filter 和 l2、row 两半）。① 用 `isca27_run.sh` 加配置文件，参数和 10-13 的 16 个探针相同，另加 `-l2-source-tile-width=7`。
- **配置确认**：三个原脚本的 `--dry-run` 命令逐个核对，和 `hpca.json` 相比只差 `-gpm-preset`（mi300x-eighth）和 `-num-memory-banks`（16）；hpca 重跑没有差异。五种机制组合和论文的 Complete / baseline / Local Pairing / Remote Aggregation / Remote Reuse 一致；都保留 `-magic-memory-copy`，都不带 `-disable-servers`；截断 `-max-wg=78600`；敏感性和 LLM 按论文原来的做法用抽样模拟；敏感性在 mi300x 下的 filter 点自动换算成每 GPM 4K/8K/16K/32K bucket。
- **代码**：`71b08b0` 敏感性脚本去掉写死的 `-disable-servers`（单元测试通过）；`71b08b0`/`fd42936`/`9220740` 新增 `akkalat/isca27_stage4.sh`（每台服务器一份计划，依次调用原脚本；原脚本不改）。
- **计划**：yss2：hpca ① → hpca ②（主结果 12 个位置 + filter 统计 4 个位置并行）→ ④ LLM → ③ filter 部分（112 个）；yss3：mi300x ① → mi300x ② → ③ L2 和 row 部分（140 个）。各部分内部按历史运行时间“长的先跑”（kmeans 单个就要 18–25 小时，放在最后会拖一整天）。结果在 `results/1013_t13s4-<部分>_wafer48_<配置>_9220740/`。估计每台约 2.5 天。
- **启动**：10-13 19:14 UTC，`exp` 会话 `t13s4-yss2`、`t13s4-yss3`（监控网页上的组 `t13-stage4`）。同一会话里同时有多个模拟，所以关掉了共用的心跳文件，改由本机的监视脚本看阶段日志、报错和内存。
- **注意**：mi300x 的结果要带“L2 带宽只有目标约 1/3，原因未查清”的说明；敏感性实验存档的结果和论文正文的数字对不上（见子代理核对），这次重跑没有可直接比对的旧数据。

### 2026-10-13 19:40 (T13 阶段 4) 改成两台服务器各 16 个位置一直占满；修复启动失败后重启

- **用户指出**：每台服务器可以一直同时跑 16 个。原来的脚本是“一个部分全部结束才开始下一个”，T11 探针只剩 P4 时每台只有 1 个模拟在跑，②④ 收尾时也会空出位置。
- **改法**（`8b4040e`）：新增 `akkalat/slot_pool.py`，每台服务器 16 个共享位置（文件锁，进程退出自动释放）。`runall2.run_all_experiments`（三个原脚本共用的调度函数）在设置了 `EXP_SLOT_DIR` 时，每启动一个模拟先拿一个位置；空出的位置给还有任务没启动、优先级最高的部分（① 探针 1、② 主结果和 filter 统计 2、④ LLM 4、③ 敏感性 5）。不设环境变量时行为完全不变。内存检查（可用内存高于 30 GB 才启动）照旧。单元测试 5 个（位置上限、优先级、过期登记、调度器峰值并发、不设变量时不变）。`isca27_stage4.sh` 改成所有部分同时启动、按优先级排队。
- **启动失败和修复**（`260ce73`）：三个原脚本写 `EXPERIMENT_METADATA.json` 时要执行 `git rev-parse`，而服务器上同步的代码没有 `.git`，所以所有 campaign 在启动第一个模拟之前就退出了（T11 探针不受影响）。修复：git 失败且设置了 `EXP_GIT_REVISION` 时，记录调用方给的 commit（本机检查过工作区干净），并在 metadata 里标注 `git_source` 为调用方；新增单元测试。在 yss3 上用极小规模（`-max-wg=64`）冒烟测试了三个脚本，都能写完 metadata 并开始模拟。
- 停掉的会话：`t13s4-*`（`9220740`，只跑了 T11 探针）、`t13s4b-*`（`8b4040e`，campaign 启动失败）。它们的结果目录保留，不收录。
- **重启**：10-13 19:36 UTC，会话 `t13s4c-yss2`、`t13s4c-yss3`，结果目录 `results/1013_t13s4-<部分>_wafer48_<配置>_260ce73/`。两台服务器冻结的二进制都确认支持 `-gpm-preset`。

### 2026-10-13 21:35 (T13 阶段 4) 内存是瓶颈：两台服务器都加了内存守护进程（暂停而不是杀掉）

- **现象**：模拟启动后内存一直涨，每个大约每 10 分钟多 1–2 GB（Complete 配置和 filter 统计涨得最快）；一小时后单个就有 15–20 GB。16 个同时跑超过了服务器内存（yss3 235 GB；yss2 251 GB，其中约 23 GB 是其他用户的进程）。原调度器只在启动新模拟前检查内存，管不了已经在跑的。
- **处理**：
  1. yss3 可用内存降到 26 GB、每分钟少约 1 GB 时，停掉了两个刚启动约 15 分钟的模拟：mi300x 主结果 Complete 配置的 `fft` 和 `floydwarshall`（在 `runall2` 里记为失败，主结果跑完后用 `--rerun-missing` 补跑）。
  2. 两台服务器各起一个内存守护进程（`~/isca27_memgov.py`，`exp` 会话 `memgov4-*`）：可用内存低于 20 GB 且每分钟下降超过 0.5 GB（或低于 10 GB）时，暂停（SIGSTOP）涨得最快的模拟；高于 35 GB 时按先后恢复（SIGCONT）。有模拟暂停时，它会占住空出来的共享位置，让腾出的内存先给暂停的模拟用，而不是启动新的。**暂停不改变模拟结果**（模拟时间和墙钟时间无关），也不丢已经算好的部分；原脚本没有超时。
  3. yss3 上临时占住 2 个位置的进程已经停掉（改由守护进程管理）。
- **当前**（21:35）：yss2 共 14 个模拟，其中 3 个暂停；yss3 共 13 个，其中 3 个暂停；已经有模拟跑完并开始恢复暂停的。
- **影响**：按两台服务器的内存，这批模拟实际能同时跑约 9–12 个，**阶段 4 会比原估计的 2.5 天更久**；等有更多模拟跑完再按实际速度重新估算。
