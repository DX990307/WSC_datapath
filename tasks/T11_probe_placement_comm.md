# T11 探针实验：确认数据放置、kernel 放置和远程访问路径

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[已定]`（10-12）· **周次**：W1，**T10 开工之前必须完成** · **依赖**：T0 · **工作量估计**：1 天
**被依赖**：T10（自写的通信型 benchmark 套件）

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。用户想自己写新的 benchmark，而且要有 GPM 之间的通信（T10）。做法是：数据按 GPM 分片，每个 GPM 计算自己的分片，通信阶段用 kernel 直接 load/store 对端 GPM 的缓冲区。这样数据才会走 L1 → NI → mesh → 远端 L2 这条路径，CuPath 的机制才能起作用。
> 开工前要先确认 3 件事；用户不确定，所以用本任务逐一验证。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 已经从上游代码中查到的信息（Cowork 侧，10-12）

查的是上游 `sarchlab/mgpusim` main 分支（commit a3bba08，2026-10-10，**v5 版本**）。**CuPath 的 fork 可能更老，函数签名和行为都要在自己的代码里再核对一遍**，第 0 步就是做这件事。

| 问题 | 上游的做法 | 结论 |
|---|---|---|
| ① 能否把缓冲区放到指定的 GPM 上 | `driver.SelectGPU(ctx, g)` 之后调用 `AllocateMemory(ctx, bytes)`，内存就分配在当前选中的 GPU 上（`memAllocator.Allocate(pid, size, ctx.currentGPUID)`）。另外还有 `Remap(ctx, addr, size, deviceID)` 可以把已有的一段内存迁到别的 GPU，`Distribute(ctx, addr, bytes, gpuIDs)` 可以把一段内存分散到多个 GPU | 很可能可以 |
| ② 能否让 kernel 只在指定的 GPM 上运行 | 先 `SelectGPU(ctx, g)`，再 `q := CreateCommandQueue(ctx)`，这个队列就绑定到 GPU g。之后用 `EnqueueLaunchKernel(q, co, grid, wg, args)` 提交，用 `DrainCommandQueue(q)` 等待。**每个 GPM 建一个队列，就能让各 GPM 并发执行各自的 kernel** | 很可能可以 |
| ③ kernel 能否直接读写其他 GPM 的缓冲区 | 统一虚拟地址空间（同一个 pid）。上游自带的 **`benchmarks/mccl`**（集合通信库）就是这么做的：`pushData` kernel 在 GPU i 上运行，直接写 GPU i+1 上的缓冲区；ring all-reduce 由 push 和 reduce 两种 kernel 交替完成。远程访问经过 RDMA 引擎，runner 有 `-report-rdma-transaction-count` 选项可以统计 | 很可能可以；**是否经过远端 L2 还需要用 P3 验证** |

**其他注意事项**：
- **GPU 编号从 1 开始**：`d.GPUs[gpuID-1]`，0 号设备是 CPU。
- **unified GPU（`CreateUnifiedGPU`）切分 WG 的方式**：按 CU 数切成连续的块，每个 GPU 分到 `cuCount × ceil(总WG数/总CU数)` 个 WG。**WG 总数少的时候，前面的 GPU 会分到全部 WG，后面的 GPU 一直空闲。** 现有的 benchmark 如果走 unified GPU 这条路，也要检查这个问题。
- **mccl 的 push 是远程写**。CuPath 的机制主要优化远程读，所以 T10 里的通信 kernel 最好**同时实现 push（写）和 pull（读）两个版本**，并且分别报告。

## 第 0 步：在 CuPath 的代码里核对（不运行，只看代码）

在 `STATUS.md` 里回答下面 5 个问题：
1. fork 的 MGPUSim 是哪个版本？看 `go.mod` 里 module 的版本号。
2. 有没有 `SelectGPU`、`CreateCommandQueue`、`EnqueueLaunchKernel`、`DrainCommandQueue`、`AllocateMemory`、`Remap`、`Distribute` 这些函数？签名和上表一致吗？
3. 有没有 `benchmarks/mccl`？
4. wafer 平台上，L1 发出的远程请求走的是哪个组件（RDMA 还是 CuPath 新加的 NI）？哪个统计能区分“远端 L2 命中”和“远端 HBM”？
5. 现有的 benchmark 用的是 unified GPU，还是每个 GPM 一个队列？

## 第 1 步：写一个探针 benchmark `probe`

代码放在 `amd/benchmarks/microbench/probe/`（目录按 fork 的结构调整），只用一个 kernel：

```c
// mode 0：读（累加到寄存器，最后写一个值，防止被编译器优化掉）；mode 1：写
__kernel void touch(__global float* buf, __global float* sink, const int n, const int mode) {
  int tid = get_global_id(0), nt = get_global_size(0);
  float acc = 0.0f;
  for (int i = tid; i < n; i += nt) {
    if (mode == 0) acc += buf[i]; else buf[i] = (float)i;
  }
  if (mode == 0 && acc == 12345.0f) sink[0] = acc;
}
```

Go 侧的参数：`-owner g_o`（缓冲区放在哪个 GPM）、`-runner g_r`（kernel 在哪个 GPM 上跑）、`-bytes`（默认 2 MB，小于单个 GPM 的 L2）、`-mode read|write`、`-passes`（连续 launch 几次，默认 2）。

## 第 2 步：探针实验（全部用 timing 模式，不做 verify）

| 编号 | 配置 | 要验证什么 | 预期结果（确认的依据） |
|---|---|---|---|
| **P1 数据放置** | owner = runner = 5 | 缓冲区确实在 GPM 5 上 | RDMA / NI 的远程事务数 ≈ 0；GPM 5 的 HBM 读字节数 ≈ bytes |
| **P2 kernel 放置** | owner = 5，runner = 5，同时打开每个 GPM 的 CU 统计 | kernel 只在 GPM 5 上运行 | 只有 GPM 5 的 CU 有 WG 完成记录，其他 47 个 GPM 都是 0 |
| **P3a 远程读的路径** | owner = 5，runner = 1，mode = read，passes = 2 | 远程读走 NI，并经过远端 L2 | 远程事务数 ≈ bytes/64；**第 1 遍时 GPM 5 的 L2 miss，第 2 遍时 GPM 5 的 L2 hit**（2 MB 能放进 4 MB 的 L2）；第 2 遍明显比第 1 遍快 |
| **P3b 距离** | owner 分别取离 runner 1、3、6、12 跳的 GPM | 远程延迟随距离增加 | 平均远程延迟随跳数单调增加（和论文 Fig 4 的趋势一致） |
| **P3c 远程写** | 同 P3a，但 mode = write | 远程写的路径 | 记录远程写经过哪些组件，以及写之后 GPM 5 的 L2 是否变成 dirty |
| **P4 并发** | 48 个 GPM 各建一个队列，各自读自己的分片（owner = runner = g），一起 launch 后再一起 drain | 多个 GPM 能并发执行 | 总时间 ≈ 单个 GPM 的时间（而不是 48 倍）；远程事务数 ≈ 0 |

## 验收标准

- [ ] 第 0 步的 5 个问题都在 `STATUS.md` 里回答了
- [ ] P1–P4 每一项都给出“通过 / 不通过”，并贴出关键统计数字
- [ ] **如果 P3a 发现远程读不经过远端 L2**（比如直接从远端 HBM 读），立刻写进“需要用户决定”，因为这会影响 CuPath 原来的 owner-L2 假设和 T7 的代理设计
- [ ] 全部通过的话，把探针的 Go 代码整理成 **SPMD 骨架**（每个 GPM 分配分片、建立指针表、每个 GPM 一个队列、按阶段 launch 和 drain），给 T10 复用


## 10-13 补充：P3/P4 要用论文配置重跑 `[已定]`

T11 第一轮用的是 baseline 二进制的**默认参数**（`-bandwidth=1` 即 16 GB/s，`-switch-latency=20`，没有 `-magic-memory-copy`、`-num-memory-banks=4`、`-mmutlb-lookup-latency=80`），**不是论文的配置**（`-bandwidth=48` 即 768 GB/s，`-switch-latency=32`，等等，见 README 的“实验配置”一节）。

- P1、P2 是功能性结论（数据放置、kernel 放置），不受影响。
- **P3a、P3b、P3c、P4 的性能数字不能代表论文配置**：之前测到的“远程带宽是本地的 1/18”和“每跳约 52 ns”都是在 16 GB/s、20 周期的设置下测的。要用论文配置重跑，结果写进 STATUS.md，旧数字标为“默认参数，作废”。
- `t11_run_probe.sh` 改为从 `runall2_constants.py` 取参数，不要手写。
