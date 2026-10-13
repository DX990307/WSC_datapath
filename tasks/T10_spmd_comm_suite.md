# T10 自写的通信型 benchmark 套件（SPMD 分片 + 显式通信阶段）

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认（检查文件有没有变 → 打印完整参数 → 列出和论文配置的差异 → **等用户确认**）。论文配置不可修改（用户 10-13）。

**状态**：`[提案]`（10-12 提出，等用户确认方向和第一批的选择）· **在用户确认之前不要写代码**
**和 T5、T9 的关系**：用户希望**自己写新的 benchmark，不复用旧代码**，而且要有 GPU（GPM）之间的通信。如果确认采用本方案，T5a–T5d、T9 的部分内容会被本任务取代或合并，届时同步修改那几个文档。

> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 F 批评 benchmark 太旧；原来的 workload 中有不少 page placement 太理想，远程通信很少，CuPath 没有什么可以优化的。新主线 `[提案]` 中，机制 1（T7）在区域内复制跨 GPM 共享的远程数据，机制 2（T8）用写签名让只读数据跨 kernel 保留。
> 平台：48 GPM，7×7 mesh，统一地址空间；远程访问走 L1 → requester NI → mesh → owner NI → owner L2/HBM。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 核心设计：SPMD 分片 + 显式通信阶段

每个 benchmark 都按真实的分布式并行策略来组织：

1. **分片**：数据按 GPM 切分，每个 GPM 的分片放在本地 HBM，这个 GPM 的 WG 只处理自己的分片。计算阶段基本都是本地访问。
2. **通信阶段**：用 kernel **直接 load/store 对端 GPM 的缓冲区**来完成通信，kernel 参数里传入一张“各 GPM 缓冲区指针表”。NCCL 和 NVSHMEM 在 GPU 上也是用 kernel 通过 P2P 映射直接读写对端内存来实现集合通信的，所以这种写法是有现实依据的。
3. **阶段之间用 kernel 边界隔开**：天然就是全局同步点，也正好是机制 2 起作用的时机。

**为什么不用 DMA 或 copy engine 做通信**：那样数据不经过 L1、NI、L2 这条路径，CuPath 的任何机制都作用不到，评估就没有意义了。上游 MGPUSim 的 `benchmarks/mccl` 已经确认是**基于 kernel 的**集合通信（ring all-reduce、broadcast，kernel 直接写对端缓冲区），可以作为通信 kernel 的参考，甚至直接复用（前提是 fork 里有这个包，T11 第 0 步确认）。

## 通信模式和候选 benchmark

| 通信模式 | benchmark | 现实来源 | 预计和 CuPath 的关系 |
|---|---|---|---|
| **一对多热点读** | 共享前缀的 attention（KV 前缀放在一个或少数几个 GPM 上） | vLLM prefix caching | 机制 1 的主场 |
| **all-reduce** | 张量并行（TP）的 LLM 层：列切分 GEMM → all-reduce（reduce-scatter + all-gather 用 kernel 实现）→ 下一层 | Megatron-LM 的 TP | 部分受益：合并和打包能帮上忙；跨 kernel 时，被写过的数据要正确失效 |
| **偏斜的 all-to-all** | MoE 专家并行（EP）：gating → dispatch（all-to-all）→ 本地 expert FFN → combine（all-to-all） | GShard、DeepSpeed-MoE | 热门 expert 所在的 GPM 成为热点；同 page 打包会有帮助 |
| **不规则的多对多 gather** | 分布式 GNN：图按 GPM 分区，本地聚合 + 从其他 GPM 拉取边界点（halo）的特征 | DistDGL | 幂律分布下的热点顶点被很多 GPM 拉取，对机制 1 和机制 2 都有利 |
| **只和邻居通信** | 3D stencil，按 7×7 mesh 做区域分解 | HPC 的区域分解 | **对照组**：距离都是 1 跳，预计收益很小 |
| **均匀的稠密 all-to-all** | 分布式 FFT 或矩阵转置 | HPC 的 FFT | **对照组**：没有复用，只考验带宽 |
| **写方向的 all-to-all** | sample sort：分桶后把元素散写到目标 GPM | 分布式排序 | 读写混合，回应审稿人 B；检验机制 2 的正确性 |

## 建议的第一批（4 个）

1. **TP + 共享前缀的 LLM 层**：同时覆盖 all-reduce 和一对多热点读
2. **MoE EP 层**：偏斜的 all-to-all
3. **分布式 GNN**：不规则的多对多 gather，数据用 ogbn-arxiv 或 products
4. **3D stencil**：对照组，而且写起来最快，可以用它来先把 SPMD 框架跑通

有余力再加 sample sort（读写混合）和 FFT（第二个对照组）。

## 开工前必须在代码里确认的 3 件事（→ 已拆成 T11 探针实验）

> 10-12 更新：上游 MGPUSim 里这 3 件事很可能都能做到，具体 API 和 `mccl`（基于 kernel 的集合通信）见 `T11_probe_placement_comm.md`。**mccl 的 push 是远程写**，所以本套件的通信 kernel 要同时实现 push 和 pull 两个版本。

1. **能不能把一个缓冲区放在指定的 GPM 上**（或者按 GPM 分片分配）？
2. **能不能把一个 kernel（或者一部分 WG）只放到指定的 GPM 上运行**？比如先选中某个 GPU 再 launch，或者每个 GPM 建一个 command queue。
3. **kernel 能不能通过统一地址空间直接 load/store 其他 GPM 上的缓冲区**？这样的访问是不是走 L1 → NI → 远端 L2 这条路径？

这 3 件事都可以的话，先写一个通用的 **SPMD 骨架**：为每个 GPM 分配分片、构建指针表、按阶段在每个 GPM 上 launch、阶段之间等待全部完成。然后每个 benchmark 只需要实现自己的“计算 kernel + 通信 kernel”。

## 避免被说“为了机制去设计 benchmark”

- 每个 benchmark 都要对应一个公开的并行策略（Megatron TP、GShard EP、DistDGL 等），参数取自真实模型或数据集。
- 必须包含对照组（stencil、FFT），并且对所有 benchmark 都报告 T2 的 M1、M2 统计。
- 分片方式采用对应策略的标准做法，不为了制造远程访问去调整。


## T11 的结论（10-13，Cowork 侧根据 STATUS.md 整理）

- **可行性已确认**：数据放置、kernel 放置、远程 load/store 都不需要改模拟器就能实现；远程读和远程写都会经过远端 L2。
- **SPMD 骨架已经写好**：`mgpusim/benchmarks/microbench/probe/spmd.go`，提供 `NewSPMD`、`AllocShards`、`UploadPtrTable`、`RunPhase`、`Now`。
- **Context**：论文配置带 `-magic-memory-copy`（必须保留），这时不会发 flush，所以**共用一个 Context 也可以**（等 T12 的 R3 确认）。每个 GPM 一个 Context 的写法也仍然可用。
- **每次 enqueue 之前都要先 `SelectGPU` 到这个 rank 对应的 GPM**，否则 code object 和 kernarg 会被分配到别的 GPM 上，变成远程访问。
- **单个 GPM 的远程带宽约为本地的 1/18**（10.4 GB/s 对 184 GB/s），带宽受限时远端 L2 命中的好处会被排队掩盖。网络参数需要先核实，见 STATUS 的“需要用户决定”。
- **阶段之间不要做 host 端的 H2D/D2H 拷贝**：拷贝可能触发全部 L2 的 flush，把跨 kernel 保留的数据清空，影响机制 2（T8）的评估。
