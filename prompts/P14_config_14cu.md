# P14：论文配置改为每个 GPM 14 个 CU（`mi200_eighth`）——实现、校准、验证、重新定 workload 规模

> ⏸ **暂缓（用户 10-20 晚）：先维持 28 CU，试试缩小 footprint。不要执行本文件。** 第 3 步（workload 重新定规模的检查条件）在 28 CU 下也适用。

---
继续 CuPath 的 ISCA 2027 工作。

**0. 先同步**
- 读 SYNC 日志里你上一条之后的所有 `[cowork]` 条目，用 `git diff` 读 Cowork 改过的文件（`CONFIG.md` 顶部 10-20 的说明、`00_background.md` 的 D23、本文件、`prompts/P13_observation_data.md` 顶部的说明、`observations/README.md`）。
- 以 `cowork: ...` 提交，推送到 `isca27/docs`。
- **用户 10-20 的决定（D23）**：28 CU 的 GPM 实验跑不动，论文配置改为每个 GPM 14 个 CU（1/8 个 MI250X GCD）。做法是 scale-model 的规则（SeyyedAghaei 等，HPCA'24）：
  - 每个 CU 的资源不变；
  - GPM 的共享资源全部减半；
  - 延迟和拓扑不变；
  - 输入随机器一起缩（弱缩放）。
- **和 P13 的关系**：P13 的 A（新统计的代码）照常做；P13 的 B、C 里的**运行**都改到本文件第 5 步用户确认之后，用新配置和新规模跑。
- **正在跑的 mi200 运行**：用户交给你这个 prompt 时会说明怎么处理。没说明的话，先不要停，列出每组运行的进度和预计完成时间，写进 STATUS，问用户。Cowork 的建议是：
  - 保留 T18 评估里 FW、PR、R-GAT、PagedAttention 的 baseline 和"全开 W=1024"（第 4 步验证要用的 28 CU 参照）；
  - 其余 mi200 运行停掉，按 D22 删除；
  - HPCA 规模比较（`hpca.json`）不受影响，照常跑。

**1. 新预设和配置文件**（代码分支从你当前最新的 T18 分支开出，例如 `isca27/cfg-14cu`）
- 新 GPM 预设 `mi200-eighth`：14 CU = 7 个 shader array × 2 CU。每个 CU / 每个 shader array 的资源和 `mi200` 预设完全相同（L1V 16 KB 4-way、每 2 个 CU 一个 L1I 32 KB 和 L1S 16 KB、MSHR 数等）。
- `configs/mi200_eighth.json`（`extends: mi200.json`），用户批准的数值：

| 项 | mi200（28 CU） | mi200_eighth（14 CU） |
|---|---|---|
| CU | 28（14 SA × 2） | 14（7 SA × 2） |
| L2 | 2 MB = 8 slice × 256 KB，16-way | 1 MB = 4 slice × 256 KB，16-way；每个 slice 的 MSHR、每周期请求数不变 |
| HBM2e | 8 个 channel（1 个 stack），约 241 B/周期 | 4 个 channel（半个 stack），约 120 B/周期；DRAM 时序不变 |
| 链路带宽 | `-bandwidth=48`（768 GB/s 每方向） | `-bandwidth=24`（384 GB/s） |
| 网络接口 | `-rdma-max-outstanding=64`、`-rdma-pipeline-width=8` | `32`、`4`；`-rdma-pipeline-latency=10` 不变 |
| GPM 内地址转换 | L2 TLB 512 项、32 MSHR；GMMU 16 个在途遍历 | 256 项（组数减半、路数不变）、16 MSHR；8 个 |
| 中心 IOMMU（全 wafer 共享） | IOMMU TLB 2,048 项；MMU 256 个在途遍历 | 1,024 项；128 个 |
| 不变 | — | 所有延迟（switch 32 周期、网络接口 10 周期、L2、DRAM、TLB 查找、页表遍历）、mesh 7×7、48 个 GPM、时钟、页大小、页 / WG 放置规则、`-magic-memory-copy` |

- 跟着带宽定义的宽度和缓冲（switch 输入口流水宽度、端口缓冲、endpoint channel）：如果是由 `-bandwidth` 自动推出来的，就跟着走；如果是单独的参数，按 1/2 设，并列出来。
- 旧 m2（M2）、旧 m3（M3）、T18（M1）里按 GPM 定大小的结构（filter、表、缓冲），检查是按 slice 定还是按 GPM 定。按 GPM 定的减半，列出来。
- T18 的配置文件按新配置做一套（例如 `t18_*_eighth.json`），跟踪表 W 全部减半：baseline 32；扫描 32 / 128 / 512 / 2048（对应原来的 64 / 256 / 1024 / 4096）。
- **回归**：`mi200`、`hpca` 预设在 fir、MM（`-max-wg 4096`）上和改动前逐位一致。
- **配置确认**（CONFIG.md 第 3 节）：给出 `-config-out` 的完整参数和相对 `mi200.json` 的差异。
  - 和上表**完全一致**，就直接做第 2 步。
  - **任何一项做不到正好减半**（例如某个参数只能取整，或者某个结构没法拆），⏸ 停下来问用户。

**2. 平台校准**（T14 的方法，新配置，只跑微基准）
- 要测的：本地延迟阶梯（L1V / L2 / HBM）；本地读带宽；单对远程带宽随跳数（1、6 跳）和随跟踪表项数（16 / 32 / 64 / 128）的变化；incast（k = 1、2、4、16、47）；链路利用率。
- 和 mi200 并排列表。预期：延迟基本不变，带宽约减半，每个 CU 的远程带宽不变，"受跟踪表限制、不受链路限制"不变。不符合预期的地方说明原因。

**3. workload 重新定规模**（benchmark 参数，不算配置修改；但这是论文的 workload 集，⏸ 要用户确认）
- 对远程型 7 个（FW、I2C、PR、MT、MM、SPMV、KM）和 T17 三个（PagedAttention、MoE、R-GAT），列一张表。
  - 现状：输入参数、footprint、每个 kernel 的 WG 数、kernel 数和迭代次数、在 28 CU 上跑完整和按现在 `-max-wg` 截断各要多少墙钟（已有的用实测，没有的估计）。
  - 建议：在 14 CU 上的新输入、footprint、WG 数、迭代次数，以及预计墙钟。
- 新规模要满足：
  1. 每个 GPM 的数据量是 L2（1 MB）的 5 倍以上，最好和现在的比例接近（现在 1 GB / 48 / 2 MB ≈ 10×，即新配置约 512 MB）；
  2. 每个 kernel 的 WG 数至少是 4 × 672；
  3. 每个主要 buffer 远大于 48 页（避免整个 buffer 落在 GPM 1 或余数页集中在最后一个 GPM）；
  4. 迭代型的（PR、KM、FW 的 pass、PagedAttention 的 decode 步）各轮相似时，只跑能代表的轮数，并说明理由；
  5. 尽量让每个运行在 24 小时内跑完、不截断。做不到的，说明需要截断到多少 WG，以及按 T15 规则的有效性风险。
- 说明哪些 workload 的工作量和数据量不成比例（例如 FW、MM 是 n³ 对 n²），新规模是保"每 CU 工作量"还是保"每 GPM 数据量 / L2"，两个比例都报。

**4. 28 CU 对 14 CU 的验证**（弱缩放）
- workload：FW、PR、R-GAT、PagedAttention。
- 28 CU 用原规模（T18 评估里同规模的运行）；14 CU 用输入或 `-max-wg` 减半，使每个 CU 的工作量相同。
- 配置：baseline 和 T18 全开大窗口（28 CU W=1024 对 14 CU W=512）。
- 比较：远程访问占比（个数和字节）、远程平均延迟、跟踪表占满的时间比例（P13 的统计如果已经好了就用）、链路利用率、T18 加速比。
- 每一项给出相对差。差距明显（例如超过 10%）的，分析原因。
- 这一步用的是第 1 步确认过的新配置，不需要再确认。

**5. 完成后**
- 更新 STATUS：
  - 任务总览加 P14；
  - 结果摘要写：配置差异、校准表、验证表；
  - "需要用户决定"写：第 3 步的规模表（⏸）、第 1 步无法正好减半的项（如果有）、正在跑的 mi200 运行怎么处理（如果用户还没说）；
  - 日志。
- 追加 SYNC，推送代码分支和 `isca27/docs`。
- ⏸ 停下来等用户确认规模，之后再用新配置重跑 T18 评估和 P13 的 R1–R3。

**规则**
- 代码只由你修改。
- 除本文件列出、用户已批准的数值外，不改任何配置参数。
- 所有运行保留 `-magic-memory-copy`，不跑 verify。
- 只推 `isca27/*` 分支（main 只在用户要求时更新），不要 force push。
- 作废或被取代的结果目录直接删除（D22）。
- T19 不再推进。
---
