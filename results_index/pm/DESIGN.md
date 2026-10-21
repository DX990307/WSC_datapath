# 页迁移对比基线：设计说明

Claude Code，2026-10-21。

- 分支：`isca27/page-migration`（worktree `~/Desktop/CuPath/wt-pm`），从 `isca27/cupath-v3` @ `4235991` 开出。
- 用途：回答审稿人"为什么不直接改数据放置或迁移页"。页迁移作为对比基线，所以实现要公平：代价参数有出处，可以调，并做敏感性分析。另外给一个"理想迁移"变体作为上限。
- 结论和数字见 `README.md`、`analysis.md`。本文只讲设计、正确性和用法。

---

## 1. 为什么不用 MGPUSim 原有的迁移流程

MGPUSim 原有的流程是为多 GPU 统一内存（CPU 内存 + 缺页迁到 GPU）写的：
1. GPU 的 MMU 发现页在别的设备上，向 driver 发 `PageMigrationReqToDriver`；
2. driver 排空所有 GPU 的 RDMA，让访问方 GPU 的 CP 做 shootdown；
3. 目的 GPU 的 PMC 从源 GPU 的 PMC 拉数据；
4. 拉完以后重启各 GPU。

在 48-GPM 平台（`akkalat/baseline/runner`）上，它有下面 5 个问题。

| # | 问题 | 代码位置（4235991） |
|---|---|---|
| 1 | **根本没接线**：`-use-unified-memory` 一跑就 panic。MMU 的 `migrationPort` 是 nil，平台从来没有给 MMU 接迁移端口。实测 fir `-max-wg 512 -use-unified-memory`：nil pointer，panic 在 `MMU.sendMigrationToDriver`。 | `akita/mem/vm/mmu/mmu.go:243`（`mmu.migrationPort.Send`）；`timingplatform.go` 里没有连接 MMU 的 Migration 端口 |
| 2 | **每次缺页都停住整片晶圆**：<br>- driver 先排空全部 48 个 GPM 的 RDMA；<br>- 再让访问方的 CP 做 shootdown，包括 CU 流水线 flush、AT 丢弃事务、TLB flush、cache flush；<br>- 然后搬页，最后重启。<br>任何真实系统都不会为一个 4 KB 页停住全部 GPU，这样做就是稻草人。 | `mgpusim/driver/driver.go:589` `initiateRDMADrain`（遍历全部 GPU）；`:613` `sendShootDownReqs`；`mgpusim/timing/cp/commandprocessor.go:394` `processShootdownCommand`（CU pipeline flush → AT discard → TLB/cache flush） |
| 3 | **全局串行，一次只迁一页**：<br>- MMU 同时只处理一个迁移；<br>- driver 同时只处理一个迁移请求，同时只向 CP 发一个页；<br>- PMC 同时只处理一个迁移。 | `mmu.go:221` `if mmu.isDoingMigration`；`driver.go:566` `parseFromMMU`（`isCurrentlyHandlingMigrationReq`）、`:753` `isCurrentlyMigratingOnePage`；`pagemigrationcontroller/pmc.go:95,130` `isHandlingPageMigration` |
| 4 | **语义不对**：<br>- 只有 `Unified` 页才会触发；<br>- `AllocateUnified` 把整个 buffer 放在设备 1（GPM 0）上，相当于先把所有数据集中到 GPM 0；<br>- 迁过一次就把页 pin 住，以后不能再迁。<br>所以它只能表达"从 CPU 首次触碰迁移"，不能表达 GPM 之间按访问模式迁移，也不能表达 access counter。 | `mmu.go:183` `!walking.page.Unified`；`mmu.go:325` `page.IsPinned = true`；`mgpusim/driver/internal/memoryallocator.go:128` `allocatePages(..., 1, true)` |
| 5 | **PMC 的数据通路在多 GPM 下不正确**：<br>- 源端 PMC 用 `MemCtrlFinder` 直接读 DRAM，绕过了写回 L2，会读到旧数据（脏行还在 L2 里）。原流程靠 shootdown 时 flush 整个 cache 来弥补，这正是问题 2 的代价。<br>- 源端只记得一个请求方端口（`requestingPMCtrlPort`），几个 GPM 同时来拉，就会把响应发错 GPM；<br>- 一次只拉 64 B，所有端口都是单消息缓冲。 | `pmc.go:212`（`WithDst(e.MemCtrlFinder.Find(address))`）；`pmc.go:195` `e.requestingPMCtrlPort = req.Src`；`pmc.go:476` `onDemandPagingDataTransferSize = 64`；`NewLimitNumMsgPort(e, 1, ...)` |

用户 10-21 已经确认：不保留原 driver/PMC 流程，新写迁移管理器。原代码一行都没有删，也没有改，只是不用。

---

## 2. 新架构

### 2.1 组件

```
            ┌──────────────────────── PageMigrationManager（全局，1 GHz）────────────────────────┐
            │ 每个 GPM 一个软件处理队列（批处理）· unit 状态表 · 物理帧表（在途计数、状态）          │
            │ 访问计数器 · 帧分配器 · shootdown（直接调各 TLB 的 InvalidatePage）· 统计             │
            └───▲──────────────▲──────────────────────────────▲─────────────────────▲────────────┘
     Resolve/Issued/Completed  │ RemoteAccessHook              │ InvalidatePage      │ enqueue / chunkDone
                │              │                               │                     │
  GPM i:  L1V/L1S/L1I AT ──┐   RDMA(ToL1 入口)        L1V/L1S/L1I TLB, L2TLB      CopyEngine i
          （每个 CU 一个 L1V AT）                      （IOMMU TLB 是全局的）       ├─ Remote 端口（挂在 mesh 上）
                                                                                     ├─ L2 端口（l1ToL2Conn，源端读）
                                                                                     └─ DRAM 端口（l2ToDramConn，目的端写）
```

| 部件 | 文件 | 作用 |
|---|---|---|
| `pagemigration.Manager` | `mgpusim/timing/pagemigration/manager.go` | 决定迁什么，串起一次迁移的各步，维护不变量检查和统计 |
| `pagemigration.CopyEngine` | `mgpusim/timing/pagemigration/engine.go` | 每个 GPM 一个 DMA 引擎，同时当源端和目的端，走真实 mesh 网络 |
| `PullReq` / `PullRsp` | `mgpusim/timing/pagemigration/protocol.go` | 拷贝引擎之间的消息 |
| `Config`、`ParsePolicy` | `mgpusim/timing/pagemigration/config.go` | 参数和默认值 |
| AT 钩子 `MigrationHook` | `akita/mem/vm/addresstranslator/addresstranslator.go`：`parseTranslation`、`releaseHeld`、`sendTranslated`、`respond` | 迁移期间把访问挂起；把 TLB 里的旧映射纠正成页表里的权威映射；统计在途访问 |
| `InvalidatePage` | `akita/mem/vm/tlb/tlb.go`、`l2tlb/l2tlb.go`、`mmuTLB/tlb.go` | 丢掉一个页的 TLB 项 |
| `RemoteAccessHook` | `mgpusim/timing/rdma/comp.go`：`processFromL1` | 每个被 RDMA 从 L1 接收的远程访问调用一次，用于 access counter 和"远程读按 owner 的分布" |
| 平台接线 | `akkalat/baseline/runner/pagemigration.go`：`buildPageMigrationEngine`、`recordPageMigrationParts`、`buildPageMigrationManager`、`reportPageMigrationStats`；`r9nanobuilder.go`：`connectL1ToL2`、`connectL2AndDRAM`、`populateExternalPorts` 里各有一处 `if b.pmEngine != nil` | 只在开启时创建和连接 |

关掉时（`-page-migration=none`，默认），上表里的组件一个也不建，端口一个也不加，钩子都是 nil。AT 和 RDMA 里新加的分支都以 `if hook != nil` 开头，所以和 `4235991` 逐位一致（见第 7 节的回归）。

### 2.2 消息协议（CopyEngine 之间，走 mesh）

| 消息 | 方向 | TrafficBytes | 内容 |
|---|---|---|---|
| `PullReq` | 目的 → 源 | 12（和内存读请求的开销一样） | 源物理地址、长度（默认 4 KB 一块） |
| `PullRsp` | 源 → 目的 | 数据长度 + 4（和 DataReady 的开销一样） | 这一块的数据 |

- 源端收到 `PullReq` 后，把它拆成 64 B 的读，发给本 GPM 的 L2 slice（`L2Finder`，和 L1 用的是同一个交错映射）。读回来的是最新数据，包括 L2 里的脏行。凑齐后回一个 `PullRsp`。
- 目的端收到 `PullRsp` 后，把它拆成 64 B 的写，直接写本 GPM 的 DRAM 控制器（`DRAMFinder`，和 DMA、PMC 用的是同一个交错映射），不经过 L2。理由见 4.1。
- 窗口：
  - 每个目的引擎最多同时拉 `PullWindow` 块（默认 16 块，即 64 KB）；
  - 每个引擎对 L2 或 DRAM 最多同时发 `L2Window` 个行访问（默认 128）。
  - 这些都是拷贝引擎自己的资源，不占 RDMA 的 W=64 窗口，也不走 M1 的组合路径。真实 GPU 的 copy engine 和 SM 的访存通路也是分开的。
  - 数据仍然和正常流量一起走同一张 mesh，抢同样的链路带宽。

### 2.3 一次迁移的完整时序

一个 unit（4 KB 页，或 2 MB 对齐的页组）的状态机（`unit.phase`）：

```
idle ──触发──▶ queued ──取进批──▶ service ──软件延迟到──▶ shootdown ──ShootdownLatency 到──▶ drain ──在途=0──▶ copy ──全部块写完──▶ remap ──▶ idle
```

| 步骤 | 做什么 | 代码 |
|---|---|---|
| 1. 触发 | on-fault 和 access counter 两种，见第 3 节 | `atHook.Resolve`（缺页）；`Manager.remoteAccess`（计数）；都会调用 `Manager.trigger` |
| 2. 排队、软件服务 | unit 进入目的 GPM 的处理队列。处理器空闲时一次取至多 `BatchSize` 个 unit 组成一批，过 `SoftwareLatencyNS` 后整批进入下一步。每个 GPM 一个处理器，各 GPM 之间并行。 | `Manager.Tick` 前半段 |
| 3. shootdown | 收集 unit 里还不在目的 GPM 上的页（都已经在了就是 no-op）。从这一刻起，**所有 GPM** 对这个 unit 的访问都在 AT 处挂起。对每个页，在访问过它的每个 GPM 的 L1V/L1S/L1I TLB 和 L2TLB，以及全局 IOMMU TLB 上调用 `InvalidatePage`。然后等 `ShootdownLatencyNS`。 | `Manager.startShootdown`、`Manager.invalidate` |
| 4. drain | 等到这些页的旧帧上没有在途访问（AT 已经发出、还没收到应答的访问）。旧帧随后标记为 `frameFrozen`。 | `Manager.Tick` 后半段、`Manager.drained`；`startCopy` 开头 |
| 5. 搬运 | 每个页在目的 GPM 分一个新帧（`allocFrame`），按 4 KB 一块交给目的 GPM 的 CopyEngine，由它从源 GPM 的 CopyEngine 拉。 | `Manager.startCopy`；`CopyEngine.sendPulls / serve / land / recvMem` |
| 6. remap | 全部块写完后，在页表里把每个页的 `PAddr` 和 `DeviceID` 改成新帧，旧帧标记为 `frameRetired`。unit 回到 idle，迁移次数加 1，记下结束时间（冷却从这里算）。唤醒所有挂着访问的 AT（`TickLater`）。 | `Manager.chunkDone`、`Manager.remap`、`Manager.wake` |
| 7. 放行 | 被唤醒的 AT 按到达顺序重新 `Resolve` 被挂起的访问。拿到的是页表里的新映射，于是用新物理地址发出去。 | `AddressTranslator.releaseHeld` |

AT 的挂起不会堵住流水线：被挂起的访问放在 AT 自己的 `held` 列表里，它的翻译响应已经从端口取走，后面其它页的访问照常翻译、照常发出。挂起的计数在 `AddressTranslator.HeldAccesses`。

---

## 3. 策略、粒度、参数

### 3.1 两种策略

**on-fault（`-page-migration=fault`）**
- 一个 GPM 的**向量访存** AT（L1V AT）拿到翻译后，如果页在别的 GPM 上，并且这个 unit 可以迁（见下面的防抖），就算一次缺页：
  - 这次访问挂起；
  - 这个 GPM 记为 faulter，它之后对这个 unit 的访问在 queued/service 阶段也挂起；
  - unit 进入**本 GPM**（缺页方）的处理队列。
- 别的 GPM 在 shootdown 之前照常远程访问。
- 标量和取指 AT（L1S、L1I）不触发缺页，但同样受挂起和重映射的约束。
- 和 UVM 的做法对应：缺页方阻塞，由 driver 批量服务，然后在缺页方那边建立映射。

**access counter（`-page-migration=counter`）**
- 计数位置：RDMA 每从 L1 接收一个访问（也就是一个离开 GPM 去远程的 L1 缺失），就在 `(unit, 发起方 GPM)` 上加 1。用的是物理地址，再经帧表反查到 unit。在 Complete 下，这个定义和"实际发到网络的访问"不一样，见第 5 节第 3 条。
- 某个 GPM 的计数达到 `Threshold` 并且 unit 可以迁时，把 unit 迁到**计数最大的 GPM**，然后把这个 unit 的所有计数清零。
- 是异步的：触发以后访问不挂起，到 shootdown 才挂起。
- 对应 NVIDIA Volta 以后的硬件 access counter：硬件对远程访问计数，到阈值通知 UVM，UVM 异步迁移。

**防抖（两种策略都有）**
- 冷却时间：unit 上次迁完以后 `CooldownNS` 内不再迁。
- 次数上限：unit 迁满 `MaxMigrations` 次以后 pin 住（0 表示不限）。
- pin 住以后访问照常远程。
- 对应 UVM 的 thrashing 检测：一个页在一段时间内被来回迁 `threshold` 次就 pin 住。
- 来回迁移统计在 `pm_pingpong`：unit 迁到一个它以前待过的 GPM，就算一次。

**observe（`-page-migration=observe`）**：钩子和统计都在，但从不迁移。时序必须和 none 一样，用来验证钩子对时序没有影响，同时在 baseline 上测"远程读按 owner 的分布"。

### 3.2 两种粒度

- 模拟器的页大小是 4 KB（`log2PageSize=12`）。TLB 项、页表项、静态放置（每个 buffer 按 48 等分连续切）都以 4 KB 为单位。
- `-pm-unit-kb=4`：unit 就是一页。
- `-pm-unit-kb=2048`：unit 是虚拟地址 2 MB 对齐的一组页（最多 512 页），计数、触发、shootdown、搬运、remap 都以整组为单位。
  - 组里已经在目的 GPM 上的页不动；
  - 组里的页可以来自多个源 GPM，各自从自己的源拉。
- **没有改页大小**：TLB 仍然按 4 KB 一项，所以 2 MB 变体没有大页带来的 TLB 覆盖范围收益，也没有大页带来的 TLB 损失。和 baseline 比，差别只在迁移本身。这是有意的，否则就不能和 baseline 的 TLB 行为直接比。
- 2 MB 组会把相邻的小 buffer 一起搬走。不到 192 KB 的 buffer 都放在 GPM 0，在虚拟地址上相邻，往往在同一个 2 MB 组里，这是 2 MB 粒度本身的代价（伪共享）。

### 3.3 参数和默认值

| 参数（flag） | 默认 | 含义 | 依据 |
|---|---|---|---|
| `-page-migration` | none | none / observe / fault / counter | |
| `-pm-unit-kb` | 4 | 4 或 2048 | NVIDIA access counter 默认粒度 2 MB（`UVM_PERF_ACCESS_COUNTER_GRANULARITY = UVM_ACCESS_COUNTER_GRANULARITY_2M`）；4 KB 是本平台的页 |
| `-pm-sw-latency-ns` | 5000 | 一批缺页或通知的软件服务时间 | Zheng 等，HPCA'16 "Towards High Performance Paged Memory for GPUs"：GPU 缺页（far-fault）端到端 20–50 µs，含经 PCIe 到 CPU 的往返和 DMA；作者把 5 µs 称为"不现实的低"。晶圆上没有 PCIe 和主机往返，数据搬运又单独建模，所以取 5 µs，是对迁移**有利**的取值，敏感性里加 20 µs |
| `-pm-batch` | 256 | 每批最多 unit 数 | UVM fault batch 和 access counter batch 都是 256（`UVM_PERF_ACCESS_COUNTER_BATCH_COUNT_DEFAULT 256`；fault buffer 的批大小也是 256） |
| `-pm-shootdown-ns` | 1000 | TLB shootdown 从开始到所有 GPM 确认的时间 | 没有找到 GPU 硬件 shootdown 的公开测量值。CPU 上用 IPI 做 shootdown 是数 µs（Amit，ATC'17）。晶圆上可以用片上网络广播失效，取 1 µs，敏感性分析时可以调。ideal 变体取 0 |
| `-pm-threshold` | 256 | access counter 阈值 | NVIDIA `uvm_perf_access_counter_threshold` 默认 256（`UVM_PERF_ACCESS_COUNTER_THRESHOLD_DEFAULT`，范围 1–65535） |
| `-pm-cooldown-ns` | 5000 | 冷却时间 | 和软件延迟同一量级。UVM 的 `uvm_perf_thrashing_lapse_usec` 是 500 µs，比我们整个模拟还长，照搬就等于"迁一次就 pin" |
| `-pm-max-migrations` | 3 | 迁满几次 pin 住 | NVIDIA `uvm_perf_thrashing_threshold` 默认 3（`UVM_PERF_THRASHING_THRESHOLD_DEFAULT`） |
| `-pm-chunk-bytes` | 4096 | 每次 Pull 的大小 | DMA burst |
| `-pm-pull-window` | 16 | 每个目的引擎同时在拉的块数 | 64 KB 在途；RTT 约 0.5 µs 时约 128 GB/s，是单条链路 384 GB/s 的 1/3，留给正常流量 |
| `-pm-l2-window` | 128 | 引擎对 L2 或 DRAM 的在途行访问数 | 不超过一个 GPM 的 L2 MSHR 总数（8 × 32） |
| `-pm-ideal` | false | 把软件延迟和 shootdown 延迟都设成 0。TLB 失效（以及随后的重新 walk）、drain、挂起和真实网络上的搬运都照算 | 理想迁移上限 |
| `-pm-check` | false | 昂贵的不变量检查（4.6） | 验证用 |
| 帧区域 | 每个 GPM 8 GB 物理空间里的 [6 GB, 8 GB) | 迁移用的新帧从这里顺序分配，从不复用 | driver 的分配从低地址开始，本文的 workload 每个 GPM 用不到几百 MB |

出处：
- NVIDIA open-gpu-kernel-modules，`kernel-open/nvidia-uvm/uvm_gpu_access_counters.c`（threshold 256、batch 256、granularity 2M）和 `uvm_perf_thrashing.c`（threshold 3、pin_threshold 10、lapse 500 µs）。
- T. Zheng, D. Nellans, A. Zulfiqar, M. Stephenson, S. W. Keckler，"Towards High Performance Paged Memory for GPUs"，HPCA 2016，第 1 节："The end-to-end cost of this process may range from as much as 50 microseconds to as little as 20 microseconds"，"even if far-fault latency is reduced to an unrealistic 5 microseconds"。
- N. Amit，"Optimizing the TLB Shootdown Algorithm with Page Access Tracking"，USENIX ATC 2017：x86 上的 shootdown 是数 µs。

配置叠加文件：`akkalat/configs/pm_{observe,fault_4k,fault_2m,counter_4k,counter_2m,ideal}.json`。用法是 `mi200,pm_fault_4k` 或 `v3_complete,pm_counter_2m,pm_ideal`。

---

## 4. 正确性论证

前提：引擎是串行的（`buildPageMigrationManager` 遇到并行引擎直接 panic），所有钩子都是同一个线程里的直接调用。

### 4.1 目的 L2 会不会已经有新帧的旧行？写 DRAM 之前要不要先失效？

**不会，也不需要。**
- 新帧由 `allocFrame` 从 [6 GB, 8 GB) 区域顺序分配，地址只增不减，**从不复用**（页迁回原 GPM 时也拿新帧，单元测试 `TestRetiredFramesAndFreshFrames` 覆盖）。driver 的分配从每个 GPM 的低地址开始，碰不到这个区域。
- cache 里的行只会因为以下几种访问出现：AT 发出的访问、这些访问引发的 L1、L2、M1/M3 动作（包括 M1 的页内预取）、拷贝引擎的读。
- `allocFrame` 每次检查帧表里从来没有出现过这个帧，也就是说从来没有任何 AT 访问过它，否则 panic。这项检查常开。
- M1 的预取器是页内的（`NewPageLocalDemandStridePredictor`，`remotePageBytes=4096`）：只会预取和某个需求访问在同一个 4 KB 物理页里的行。而新帧在 remap 之前不会有需求访问（4.2）。所以新帧在 remap 前不会被预取，不会进入任何 L2 或副本。
- 本文用到的配置（mi200、v3_complete）里，本地 L2 预取器都是关的（`l2-filter-prefetch-enable` 等都是 false）。
- `-pm-check` 在帧分配时和 remap 时，用 `AuthoritativeResidentLookup` 和 `AuthoritativeMSHRLookup` 逐行查**所有 GPM 的所有 L2 slice**（本地行和 M3 副本在同一个目录里）。命中即 panic。所以"新帧在拷贝期间被缓存过"这个前提在每次带检查的运行中都被直接验证了。
- 为什么写 DRAM 而不写目的 L2：写进 L2 会留下新帧的脏行。driver 的 magic H2D 只写 storage，不经过 cache，之后被这些脏行盖住，会读到旧数据。实测 atax 和 bfs 的 2 MB 变体因此读到旧的 kernel 代码，挂住了（commit `be68777` 修复）。写 DRAM 以后，新帧和刚分配的页一样不在任何 cache 里，和 baseline 面对的情况相同。

### 4.2 旧物理地址在 L1V、L1S 和 M3 副本里的行：remap 后会不会被命中？写会不会丢？

**不会命中。**
- L1V 和 L1S 是物理地址标签：在 AT 之后，请求带的是物理地址，PID 是 0。
- M3 的副本在请求方 L2 里，也按物理地址存。
- 迁移从 shootdown 开始，到 remap 结束，这个 unit 的每一个访问都在 AT 处挂起。
- remap 以后，每一个经过 AT 的访问都被 `Resolve` 纠正为页表里的新映射。
  - 这里的"每一个"包括：TLB 里还留着旧项（在途的 walk 在 shootdown 之后回填的），以及被挂起后放行的访问。
  - 判断依据是 `unit.everMigrated` 为真时用 `pageTable.Find`。
- 所以 remap 以后不会再有任何访问用旧物理地址发出去，旧地址的 L1 行和 M3 副本永远不会再被查到。
- 常开检查：
  - 旧帧在搬运开始时标记为 `frameFrozen`，remap 时标记为 `frameRetired`；
  - 任何 AT 对 frozen 或 retired 帧发出访问（`atHook.Issued`）都会 panic；
  - RDMA 收到对 retired 帧的远程访问（`remoteAccess`）也会 panic。
- 指令取指：L1I 是虚拟地址标签，它的缺失经过 L1I AT，同样会被纠正。代码内容本身没有变，所以 L1I 里的行仍然有效。

**写不会丢**，靠的是"所有在途访问排空以后才开始拷贝"：
- 旧帧上所有已经发出的访问（读和写）在 AT 处计数（`Issued` +1，`Completed` −1）。shootdown 以后，`drained()` 要等到每个旧帧的计数都是 0，才开始拷贝。
- 写的应答都是**非 posted** 的，AT 收到 WriteDone 时，写已经到达 owner GPM 的 L2（L2 是一致性点）：
  - L1V（writearound）只在收到下层的 WriteDone 后才回应答（`writearound/bottomparser.go` `processDoneRsp` → `respondstage.go` `respondWriteTrans` 要求 `trans.done`）；
  - RDMA 的远程写转发给 owner，owner 把写交给 L2，L2 的 WriteDone 再原路返回。T18/M1 的组合路径对写只做 `t18CloseLines`，然后走原来的转发路径（`t18_requester.go`、`t18_owner.go` `t18OwnerReqFromOutside` 对 WriteReq 返回 `handled=false`）；
  - M3 在请求方遇到写，会把对应副本作废，并把这一行标记为 uncacheable（`remote_requester.go` `noteLegacyRemoteReqSent`），写本身仍然转发给 owner。
- 源端经 L2 读，所以读到的是包括脏行在内的最新数据。

### 4.3 迁移期间 RDMA 路上的写（目标是旧帧）怎么处理

- 它是 shootdown 之前由某个 AT 发出的，所以计在旧帧的在途数里。
- drain 会一直等到它的 WriteDone 回到 AT（4.2），所以它一定先写进源 GPM 的 L2，然后拷贝才开始读。
- shootdown 以后，这个 unit 不会再有新的写发出（挂起），也就不存在"拷贝读完以后才到的写"。
- 单元测试 `TestInFlightWriteLandsBeforeCopy`：一个写在途时迁移开始，写在 3 µs 时才落到源帧；检查新帧里有这个写的数据。如果拷贝早于 drain，测试会失败。

### 4.4 并发迁移同一页

- 一个 unit 在任何时刻只有一个阶段。只有 `phase==idle` 的 unit 才能被触发（`eligible`）。
  - on-fault：别的 GPM 对正在 queued/service 的 unit 发生"缺页"时，不会再触发，照常远程访问，直到 shootdown 才挂起；
  - access counter：只在 idle 时计数。
- 不同 unit 的迁移完全并行：
  - 每个 GPM 一个软件处理器；
  - 拷贝引擎按块流水；
  - 同一个源可以同时服务多个目的，`PullRsp` 按请求的 `Src` 回送。原 PMC 只记一个请求方，这里没有这个问题。
- 2 MB 组的页可能来自多个源 GPM，各自拉取，全部块完成后整组一起 remap。

### 4.5 写回、一致性，以及和 M3、RDMA 的交互

- 迁移不改变 baseline 的一致性模型：L2 是各自 owner 地址的一致性点，kernel 边界失效 L1（Bug B 的修复）。在 Complete 下，M3 副本在 kernel 边界失效。
- 迁移以后，一个页的 owner 变了，访问它的路径也跟着变：
  - 对新 owner 来说是本地访问（L2TLB 按页表的 DeviceID 选择本地 GMMU 还是 IOMMU）；
  - 对别的 GPM 来说是发往新 owner 的远程访问。
  - M1 和 M3 对新物理地址照常工作。
- 旧帧的脏行留在源 L2 里，以后会写回旧帧的 DRAM。这是无害的：旧帧永不复用，也不会再被访问（4.2）。
- driver 的 magic D2H 按页表找物理地址，再叠加各 L2 的脏行（coherent peek），所以读到的是新帧上的数据。magic H2D 也按页表写，写到新帧。
- RDMA 的 drain、restart 这些控制消息不参与，迁移不会停住 RDMA。

### 4.6 检查和测试的覆盖

| 情况 | 怎么检查 |
|---|---|
| 迁移不改变结果 | 8 个 workload 的 `-verify`：bfs、atax、sssp、graphtc、npbcg、npb_mg、rodinia_lud（就地写）、w2-fft2d（就地写）。<br>每个 workload 都跑 4 个变体，再加上压力配置：<br>- 阈值 16、冷却 0、不限迁移次数（大量来回迁移，迁移紧接着写）；<br>- 理想（软件延迟和 shootdown 延迟都是 0，竞争窗口最窄）；<br>- Complete+迁移（M1/M3 + 多 kernel 的边界失效）。<br>结果见 README |
| remap 后用了旧物理地址（L1 或 M3 副本命中旧行） | 常开：frozen 或 retired 帧上出现 `Issued`，或者 RDMA 收到对 retired 帧的访问，都会 panic |
| 新帧在拷贝前或拷贝中被缓存 | 常开：新帧必须从未被访问过；`-pm-check`：分配和 remap 时查所有 L2 的目录和 MSHR |
| 在途写被拷贝漏掉 | `TestInFlightWriteLandsBeforeCopy`；加上 lud、fft2d、mg 这些就地写的 workload 在压力配置下 `-verify` |
| 被挂起的访问放行后用新帧，在途计数正确 | `addresstranslator/migrationhook_test.go` `TestMigrationHookHoldsAndRemaps` |
| TLB 失效后不会被旧项遮住 | `tlb/invalidatepage_test.go` |
| 策略语义（阈值、argmax、冷却、上限、pin、来回迁移计数、2 MB 整组、observe 不迁） | `pagemigration/manager_test.go`，共 9 个测试 |

`-verify` 只能抓到结果错，抓不到"恰好没出错"的竞争。所以上面这些不变量都直接检查访问用的地址和 cache 的内容，不依赖结果对不对。

---

## 5. 已知局限，以及和真实硬件的差别

1. **shootdown 是功能性的直接调用**，加一个固定延迟：
   - 不产生网络消息，也不建模各 GPM 依次确认的过程；
   - 只失效"访问过这个页的 GPM"的 TLB（以及 IOMMU TLB），没访问过的 GPM 本来就没有这个页的项。
   - shootdown 之后，在途的 walk 回填的旧 TLB 项不会再被失效。这些访问由 `Resolve` 纠正到新地址，所以功能上正确；时序上，这些访问少付了一次 walk，对迁移略微有利。
2. **软件服务模型**：
   - 每个 GPM 一个处理器，各 GPM 之间并行，批内也并行，对迁移有利。真实 UVM 用的是每个 GPU 一个 bottom-half 线程。
   - 不建模 fault replay 的开销，也不建模缺页缓冲区溢出。
3. **access counter 的计数位置**：在 RDMA 的 L1 入口计数（`rdma/comp.go` `processFromL1`，请求被接收即计一次），定义是"本 GPM 发往远程地址的 L1 缺失"。
   - **baseline 和 baseline+迁移**：没有 M3，每个这样的缺失都会作为一个请求发到网络上。所以这个定义就等于"真正发往网络的远程访问"，和 NVIDIA 的定义一致。NVIDIA 是在 GPU 访问远程物理内存时，按物理区域计数。
   - **Complete+迁移**：计数还包括之后会在本 GPM 的 M3 副本上命中的访问，以及被 M1 合并或打包、没有单独发包的访问，所以比实际离开 GPM 的访问多。
     - 影响：被多次读的远程页（M3 正好在缓解它）会更快达到阈值，迁移更积极。
     - 迁走以后，这些访问变成本地访问，M3 本来能省下的网络往返也就不需要了。所以多计带来的主要后果是多做迁移（多付搬运和挂起的代价），不会让迁移的收益被高估。
   - **为什么没有加"只计网络包"的选项**：
     - 实际发到网络的包分散在多条路径上：旧路径、T18 的请求队列、M1 的位图打包、M3 的 fill。一个包可以代表多条 line、多个请求方，计数的语义本身就要重新定义。
     - 主要结论（迁移和 baseline、和 Complete 比）用的是不带 M3 的 baseline+迁移，这时两种定义完全相同。
     - 只有"Complete+迁移"这一格受影响，而且偏向多迁移；README 里会标出这一点。
     - 如果"Complete+counter"成了需要细看的结论，再在 M3 命中处（`isRemoteProbeRsp` 命中分支）补一个反向计数，就能得到"未被本地副本满足"的计数。
   - 我们不计 owner 自己的本地访问。NVIDIA 也是这样，所以迁移有可能把页从它的主要使用者那里搬走。
4. **NVIDIA 的 access counter 迁移目标是"发出通知的 GPU"**；我们取计数最大的 GPM。在多个 GPM 争同一页时，这个选择更合理，对迁移有利。
5. **不建模页表更新本身的延迟**（PTE 写回内存），也不建模目的 GPM 的物理内存分配开销。这两项算进了 `SoftwareLatencyNS`。
6. **2 MB 粒度没有大页的 TLB 好处**（3.2）。
7. **拷贝引擎的资源**：拷贝引擎自带缓冲和窗口，不占 RDMA 的窗口。真实 GPU 的 copy engine 也是独立的，但会和 SM 抢 L2 和 HBM 带宽：
   - 源端我们经 L2 读，所以抢源 GPM 的 L2 带宽；
   - 目的端写 DRAM，所以抢 HBM 带宽。
8. **不迁移 kernel 代码和 kernarg 以外的元数据**：它们本来就是普通页，和数据页一样对待；不区分只读页（UVM 对只读页可以做复制，这里不做）。
9. **帧不回收**：迁走的旧帧不会释放。容量用到 2 GB/GPM 才会 panic，本文的 workload 远远到不了。
10. **只支持串行引擎**，只支持一个进程（用 PID 区分 unit，所以多进程不会出错，但没有测过）。

---

## 6. 开关和复现

所有命令都在仓库根目录运行，二进制的编译方式和 `isca27_run.sh` 一样。

```
# 关闭（默认），和 4235991 逐位一致
baseline -config akkalat/configs/mi200.json -magic-memory-copy=true -benchmark fir -max-wg 4096

# 只装钩子和统计（时序必须和关闭一样）
... -config akkalat/configs/mi200.json,akkalat/configs/pm_observe.json ...

# 四个主变体（384 GB/s 主配置）
akkalat/isca27_run.sh 1021_abl_pm-bfs-s18e2_mi200+pmf4k_2a2197b 2a2197b PM mi200,pm_fault_4k   -benchmark bfs -bfs-scale 18 -bfs-edge-factor 2 -bandwidth=24 -switch-buffer-flits=16 -report-cupath-v3-stats
akkalat/isca27_run.sh 1021_abl_pm-bfs-s18e2_mi200+pmf2m_2a2197b 2a2197b PM mi200,pm_fault_2m   ...
akkalat/isca27_run.sh 1021_abl_pm-bfs-s18e2_mi200+pmc4k_2a2197b 2a2197b PM mi200,pm_counter_4k ...
akkalat/isca27_run.sh 1021_abl_pm-bfs-s18e2_mi200+pmc2m_2a2197b 2a2197b PM mi200,pm_counter_2m ...

# 理想上限：再叠加 pm_ideal
... mi200,pm_counter_2m,pm_ideal ...

# 和 Complete 叠加
... v3_complete,pm_counter_2m ...

# 改参数：直接在命令行加 -pm-*，命令行优先于配置文件
... -pm-sw-latency-ns 20000 -pm-threshold 64 -pm-max-migrations 1 -pm-cooldown-ns 0

# 验证：-verify 加上昂贵检查
... -verify -page-migration counter -pm-threshold 16 -pm-cooldown-ns 0 -pm-max-migrations 0 -pm-check
```

输出的统计在 `metrics.csv` 的 `PageMigration` 行，每个 GPM 还有一行 `GPU<n>, pm_remote_reads_served`：

| 指标 | 含义 |
|---|---|
| `pm_faults`、`pm_counter_triggers` | 两种策略的触发次数 |
| `pm_batches` | 软件处理的批数 |
| `pm_migrations` | 迁移的 unit 数 |
| `pm_noop_migrations` | 服务时发现已经在目的 GPM 上、什么都没搬的 unit 数 |
| `pm_pages_migrated`、`pm_bytes_migrated` | 迁移的页数和字节数 |
| `pm_pingpong` | 迁回以前待过的 GPM 的次数 |
| `pm_pinned_units` | 迁满次数被 pin 住的 unit 数 |
| `pm_units_migrated_<k>x` | 迁了 k 次的 unit 数 |
| `pm_shootdowns`、`pm_tlb_entries_invalidated` | shootdown 次数和失效的 TLB 项数 |
| `pm_held_accesses` | 在 AT 处被挂起的访问数 |
| `pm_avg_migration_time`、`pm_avg_blocked_time`、`pm_avg_drain_wait`、`pm_avg_copy_time` | 每次迁移的平均时间：从触发到 remap、挂起时长、drain 等待、拷贝时长 |
| `pm_dma_pulled_bytes`、`pm_dma_served_bytes` | 拷贝引擎拉取和服务的字节数 |
| `pm_remote_accesses`、`pm_remote_reads` | 离开 L1 的远程访问数和远程读数 |
| `pm_hot_owner_read_share`、`pm_hot_owner_device` | 远程读里份额最大的 owner 的份额，以及它是哪个设备 |
| `pm_checked_frames` | `-pm-check` 检查过的帧数 |
