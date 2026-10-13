# T13 把 GPM 配置改成 1/8 MI300X（按比例缩放），并全量重跑

**状态**：`[已定]`（用户 10-13）· **周次**：W1–W2 · **依赖**：T12（driver 修复）· **被依赖**：之后的所有实验
**这个任务会更换论文配置。** 在新配置校准完成、用户确认之前，`CONFIG.md` 里的旧配置（95b4f46）仍然是唯一有效的配置。

> 🔒 **执行前必做**：按 `../CONFIG.md` 第 3 节做配置确认。本任务的差异就是下表的全部内容，**每一阶段运行之前都要把完整参数和差异给用户看，等用户确认**。
>
> **项目背景（重复）**：CuPath（HPCA 2027 #1329 被拒）改投 ISCA 2027。审稿人 C 指出论文声称“4 个 GPM 相当于一张 MI100”，但实际配置是 128 CU、16 MB L2、1.5 TB/s，而真实 MI100 是 120 CU、8 MB L2、1.228 TB/s，L2 是真实值的 2 倍。用户决定改为**直接对齐 MI300X**：每个 GPM = 1/8 张 MI300X（1 个 XCD 加 1 个 HBM3 stack）。
> 索引：`../README.md` · 背景：`../00_background.md` · 进度写入：`../STATUS.md`

## 用户 10-13 的决定

| 问题 | 决定 |
|---|---|
| GPM 怎么对应 MI300X | **1 GPM = 1/8 MI300X**，按整个芯片的 1/8 等比例缩放 |
| 时钟 | **保持 1 GHz，按每周期等效**：所有带宽换算成“MI300X 在 2.1 GHz 下每周期的字节数” |
| Infinity Cache（256 MB） | **不建模**，作为论文的局限写明 |
| cache line | **保持 64B**（MI300X 是 128B），论文里说明 |
| L2 和内存 channel | **改成每 GPM 16 个**（和 MI300X 的 L2、HBM3 一致） |
| HBM 容量 | **保持每 GPM 8 GB**（MI300X 是 24 GB），论文里说明 |
| 重跑范围 | **全量重跑** |

## 目标配置（每个 GPM，1 GHz，每周期等效）

MI300X 的数据来源：AMD CDNA 3 白皮书、MI300X 产品页。8 个 XCD × 38 个有效 CU = 304 CU；每 XCD 4 MB L2，16 路，16 个 256 KB 的 channel，每 channel 读 128 B 的 line，每 XCD 2 KB/clk；L1 每 CU 32 KB；I-cache 64 KB、8 路、2 个 CU 共享；LDS 64 KB；8 个 HBM3 stack，5.3 TB/s，192 GB；2.1 GHz。

| 参数 | 旧论文配置（95b4f46） | **新配置：1/8 MI300X** | 怎么算出来的 |
|---|---|---|---|
| 每 GPM 的 CU 数 | 32（8 个 shader array × 4） | **38**（19 组，每组 2 个 CU） | 304 / 8；I-cache 每 2 个 CU 共享 |
| L1 vector | 16 KB/CU，4 路 | **32 KB/CU**，路数沿用 4 路，line 64B | CDNA3 是 32 KB；line 保持 64B（用户决定） |
| L1 instruction | 每个 shader array 32 KB | **每 2 个 CU 共享 64 KB，8 路** | CDNA3 |
| L1 scalar | 每个 shader array 16 KB | **每 2 个 CU 共享 16 KB**（白皮书没给，沿用旧值） | — |
| LDS | 64 KB | 64 KB | 不变 |
| L2 容量 / 组织 | 4 MB = 4 × 1 MB，16 路 | **4 MB = 16 × 256 KB，16 路** | CDNA3 |
| L2 吞吐 | 4 个分区 × 16 请求/周期（约 4 KB/周期） | **合计 2 KB/周期**：16 个 channel，在 64B line 下每个 channel 每周期 2 个请求 | CDNA3 每 XCD 2 KB/clk；每周期等效时这个数不变 |
| L2 MSHR | 每分区 64 个（每 GPM 共 256 个） | **每 channel 16 个（每 GPM 仍是 256 个）** | 保持每 GPM 的总量不变 |
| 内存 channel 数 | `-num-memory-banks=4` | **16** | HBM3 每个 stack 16 个 channel |
| HBM 带宽 | 每 GPM 375 B/周期 | **每 GPM 约 315 B/周期**（每 channel 约 19.7 B/周期） | 5.3 TB/s ÷ 8 ÷ 2.1 GHz |
| HBM 容量 | 8 GB | 8 GB（不变，用户决定） | — |
| Infinity Cache | 无 | 无（局限） | 用户决定 |
| 时钟 | 1 GHz | 1 GHz | 用户决定 |
| CuPath typed filter | 每个分区 16K 槽（= 1 MB / 64B） | **每个 channel 4K 槽（= 256 KB / 64B）**，每 GPM 总量不变；槽的格式、端口数、延迟沿用 `DEFAULT_BENCHMARK_FLAGS` | 按比例缩放 |
| `-prefetch-predictor-entries` | 256（= 4 × 64 个 MSHR） | 256（每 GPM 的 MSHR 总数不变） | 不变 |
| **wafer 网络、RDMA、MMU、L1 MSHR、地址交错粒度** | `BASE_COMMON_FLAGS` 等 | **全部不变** | 这些是 wafer 本身的假设，不来自 MI300X |

> 两点说明：(1) “L2 吞吐”和“HBM 带宽”的目标值是按每周期等效换算出来的，**必须用微基准实测校准**，不能只看参数。(2) 新配置下每个 GPM 的 L2 吞吐（2 KB/周期）比旧配置（约 4 KB/周期）低一半，HBM 带宽也低了约 16%，所以新配置整体上访存压力更大。这个差异要写进论文。

## 要做什么

### 阶段 1：定位参数，不改任何代码
逐一查清楚上表的每个参数是**命令行参数**，还是**写死在** `shaderarray.go`、`r9nanobuilder.go`、`timingplatform.go` 或 DRAM 模型里的；哪些参数需要改代码才能支持（例如 19 × 2 的 shader array、16 个 L2 channel 对应 16 个内存控制器、HBM 的时序参数）。
另外确认：38 个 CU 和 16 个 channel 会不会和某些按 2 的幂设计的逻辑冲突（例如地址交错、filter 的 bucket 数、`-max-wg` 的分配方式）。
**结果写进 `STATUS.md`，然后停下来等用户确认。**

### 阶段 2：实现成一个“预设”，旧配置要保持可复现
- 新增一个预设开关，比如 `-gpm-preset=mi300x-eighth`。**默认值保持旧配置**，用来复现 HPCA 投稿版的结果。
- 在 `runall2_constants.py` 里**新增**（不要改动已有的 `BASE_COMMON_FLAGS`）一组 `MI300X_EIGHTH_FLAGS`，并让 `build_common_flags`、`run_cupath_*` 可以选择这个预设。
- 用论文旧配置跑 2–3 个 benchmark 做回归，**周期数必须和修改前完全一致**。

### 阶段 3：校准
用微基准测出新配置下单个 GPM 的：①本地 HBM 带宽（目标约 315 B/周期）；②L2 带宽（目标约 2 KB/周期）；③L1、L2、HBM 的空载延迟；④单个 GPM 的远程读带宽，以及 1、3、6、12 跳的延迟（就是 T11 P3 用新配置再跑一遍）。
目标值和实测值的偏差超过 10% 就停下来报告，**不要自己调参数凑数**。
**校准结果写进 `STATUS.md`，等用户确认之后**，Cowork 侧会把 `CONFIG.md` 的有效配置切换到新预设（旧配置保留，作为“HPCA 投稿版”）。

### 阶段 4：全量重跑（在用户确认新配置之后）
按下面的顺序，**全部使用论文原有的运行脚本和机制组合**，只额外加上新预设：
1. T11 探针 P1–P4
2. 论文的主结果：14 个 workload × {baseline、Remote Aggregation、Remote Reuse、Local Pairing、Complete}（对应论文的 Fig 11 和 Fig 12），加上 filter 的统计（Fig 13）
3. 敏感性实验（Fig 14–16）
4. LLM 算子实验（Fig 17）

**开跑之前**，先估算总的模拟时间（新配置每个 GPM 多了 19% 的 CU），连同完整参数一起给用户确认。`-max-wg` 等截断设置沿用原脚本的值，并在 meta 里记录。
旧结果不要删，用 `exp_id` 里的预设名区分。

## 验收标准
- [ ] 阶段 1 的参数定位表（每个参数在哪里设置、要不要改代码）
- [ ] 阶段 2：旧配置的回归结果完全一致
- [ ] 阶段 3：校准表（目标值、实测值、偏差）
- [ ] 阶段 4：新旧配置下各 workload 的加速比对照表（baseline 的周期数、CuPath 的加速比、GMEAN）


## 10-13 补充：阶段 3 和 T14 一起跑

阶段 3 的校准和 `T14_wafer_platform_data.md` 第一档共用微基准和运行。T14 第 2 项（带宽—CU 数）就是阶段 3 的 HBM 和 L2 带宽校准；第 1、3、4 项覆盖各级延迟、远程带宽和跳数延迟。校准结论写在 T13（STATUS 里），平台数据表写在 T14。
