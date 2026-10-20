# 论文配置（不可修改）

> ⏸ **10-20 晚：D23 暂缓（用户："先等会再搞，维持原来的 28 个 CU"），论文配置仍是 `mi200.json`。下面这段只是备用方案。**
>
> ⚠️ **10-20 更新（D23，用户决定）：论文配置改为每个 GPM 14 个 CU（`configs/mi200_eighth.json`，1/8 个 MI250X GCD）。** 原因：1/4 GCD（28 CU）的实验跑不动。做法按 scale-model 的规则：每个 CU 的资源不变，GPM 的共享资源（L2 slice、HBM channel、链路带宽、网络接口跟踪表和流水宽度、GPM 内的地址转换结构）全部减半，所有延迟、mesh 7×7、48 个 GPM、放置规则不变，输入随机器一起缩（弱缩放）。具体数值见 `prompts/P14_config_14cu.md`。**在 Claude Code 实现、校准、用户确认数值并冻结之前，`mi200.json` 仍是论文配置；冻结之后 `config_deviation` 改为相对 `mi200_eighth.json` 计算，mi200 上的结果作废（D22）。**

> ⚠️ **10-16 更新：目标配置改为 mi200（D19，取代 D15 的 MI300X）。** `configs/mi200.json` 冻结（标签 `isca27-mi200-v1`）并经用户确认之后，成为论文配置和所有新实验的默认基准（`config_deviation` 相对 `mi200.json` 计算）；`hpca.json` 保留为 HPCA 投稿版，`mi300x_eighth.json` 只作参照。**冻结之前，下面的 hpca 规则仍然有效。** mi200 有意比真实 MI200 稍弱（D20），论文必须写明。

> ⚠️ **10-13 更新：配置正在切换中。** 用户决定把 GPM 改成 **1/8 MI300X**（见 `tasks/T13_mi300x_config.md`）。**在 T13 的校准完成、用户确认之前，下面的旧配置（95b4f46）仍然是唯一有效的配置。** 新配置确认之后，Cowork 侧会更新本文件：新预设成为论文配置，旧配置保留为“HPCA 投稿版”，用来复现旧结果。

> **用户 10-13 的规定**：之前设置的实验配置是**论文始终使用的配置，不要改**。**每一个 task 执行之前，都要先按本文件做一次配置确认。**
> 本文件只记录事实，不提出任何修改。配置以代码为准，本文件是它的快照，用来对照检查。


> 🔄 **10-13 更新（T13 阶段 2 之后）**
> - 配置改为**配置文件**：`akkalat/configs/hpca.json`（HPCA 投稿版，**当前论文配置**）和 `akkalat/configs/mi300x_eighth.json`（`extends` hpca，只改 `gpm-preset=mi300x-eighth`、`num-memory-banks=16`；**T13 校准确认之前不是论文配置**）。运行时用 `-config <文件>`，命令行只写配置名和 benchmark 参数；`-config-out` 写出每个参数的实际值和来源。
> - **不再使用 `-disable-servers`**（用户决定；已验证开着服务结果完全一致）。
> - `hpca.json` ≡ `BASE_COMMON_FLAGS` + `-mmutlb-lookup-latency=80`，由 `test_isca27_configs.py` 保证；论文原有的 runall2 / run_cupath_* 脚本照旧显式传参，默认生成的参数和以前逐字相同。
> - **新的基准 commit：`1b1e829`**（分支 `isca27/t13-mi300x`）。7 个文件因为加预设而改动（用户同意），旧配置的数值没有变；**检查方法改为两条**：①下表 blob 一致；② `hpca` 预设的回归和 95b4f46 完全一致（T13 阶段 2 已验证：fir、matrixmultiplication 十万多项统计排序后一致）。
>
> | 文件 | 1b1e829 时的 blob |
> |---|---|
> | `akkalat/runall2_constants.py` | `0a6b0c4f2f5c` |
> | `akkalat/runall2_config.py` | `ea22add79a66` |
> | `akkalat/baseline/runner/flag.go` | `10c50a3b9fc5` |
> | `akkalat/baseline/runner/shaderarray.go` | `743e0ae29af1` |
> | `akkalat/baseline/runner/r9nanobuilder.go` | `6c7c78ea4916` |
> | `akkalat/baseline/runner/timingplatform.go` | `c8636d99cc06` |
> | `akkalat/baseline/runner/runner.go` | `378f7313f8d4` |
> | `akkalat/baseline/runner/gpm_preset.go`（新；`0e38983` 加了 `CUsPerGPM()` 之后为 `4a149c51b45a`，配置数值不变） | `4a149c51b45a` |
> | `akkalat/baseline/runner/config_file.go`（新） | `f31cc47e59fb` |
> | `akkalat/configs/hpca.json`（新） | `90954272240e` |
> | `akkalat/configs/mi300x_eighth.json`（新） | `9accc5e93b62` |
>
> 下面第 1–3 节是 95b4f46 时的原始快照，保留作历史记录；第 3 节的确认流程仍然适用，只是“打印完整参数”改成“给出配置文件名、`-config-out` 的结果和差异”。

## 1. 配置的唯一来源

基准 commit：**`95b4f46`**（2026-10-12，“Update CuPath sensitivity runs, CACTI cost analysis, plots, and paper sections”），在仓库 `WSC_datapath` 中。

| 文件 | 作用 | 95b4f46 时的 blob |
|---|---|---|
| `akkalat/runall2_constants.py` | `BASE_COMMON_FLAGS`、`DEFAULT_BENCHMARK_FLAGS`、`DEFAULT_MMUTLB_LOOKUP_LATENCY`、`DEFAULT_L1V_*`、`CONFIGS` | `c46181e7ddbc` |
| `akkalat/runall2_config.py` | `build_common_flags()`：把上面的常量拼成完整的参数列表 | `073257215378` |
| `akkalat/baseline/runner/flag.go` | 所有命令行参数的**默认值**（命令行没写的参数，取的就是这里的默认值） | `142c66f8fec7` |
| `akkalat/baseline/runner/shaderarray.go` | CU、cache 的配置 | `0cad15f3f860` |
| `akkalat/baseline/runner/r9nanobuilder.go` | GPM 内部的组装方式 | `be11d0d9cd45` |
| `akkalat/baseline/runner/timingplatform.go` | 7×7 wafer、mesh、RDMA 的组装方式 | `7f4c214b78e6` |
| `akkalat/baseline/runner/runner.go` | runner（10-13 加心跳之后它没有变，只新增了 `heartbeat.go`，`tracers.go` 加了 2 行） | `c84865b80dcc` |

**检查方法**：`git rev-parse HEAD:<文件> | cut -c1-12` 应该和上表一致。不一致时，用 `git diff 95b4f46 HEAD -- <文件>` 看改了什么。

## 2. 完整参数（95b4f46 时的值）

**`BASE_COMMON_FLAGS`**：
```
-timing -num-memory-banks=4 -l1v-mshr-entries=16 -l1v-max-concurrent-trans=16
-bandwidth=48 -switch-latency=32 -rdma-pipeline-width=8 -rdma-pipeline-latency=10
-rdma-max-outstanding=64 -magic-memory-copy -report-all
```
**再加上**：`-mmutlb-lookup-latency=80`（10-13 起不再加 `-disable-servers`）（`DEFAULT_MMUTLB_LOOKUP_LATENCY = 80`）

**机制相关的实验另外加 `DEFAULT_BENCHMARK_FLAGS`**：
```
-typed-filter-slots-per-bucket=4 -typed-filter-fingerprint-bits=13
-typed-filter-lookup-latency=1 -typed-filter-lookup-width=16
-typed-filter-update-latency=1 -typed-filter-update-width=16
-prefetch-predictor-entries=256
```
各个机制组合的参数见 `runall2_constants.py` 里的 `CONFIGS`，**也是用到哪个就照抄哪个，不要改**。

**`-magic-memory-copy` 必须保留**（用户 10-13）：所有运行都带上它，包括测试、探针和回归；不允许为了覆盖某条代码路径而去掉它。

**要点**：`-bandwidth=48` 表示 48 × 16 GB/s = 768 GB/s，`-switch-latency=32` 表示 32 周期，和论文 Table II 一致。二进制的默认值（比如 `-bandwidth=1`、`-switch-latency=20`）**不是**论文的配置。

## 3. 每个 task 执行前的配置确认（必做）

在跑任何实验之前（包括冒烟测试和探针），Claude Code 要完成以下 4 步：

1. **检查文件有没有变**：对第 1 节表里的 7 个文件做 blob 对比，结果写进 STATUS 的日志。
2. **打印完整参数**：打印这次实验要用的完整命令行。参数必须**从 `runall2_constants.py` 导入**（或者调用 `build_common_flags`），不能手写。
3. **列出和论文配置的差异**：逐条列出这次实验比论文配置多了、少了或者改了哪些参数，并说明原因。新增的统计开关（例如 `-report-l2-source`）和 benchmark 自己的参数（例如 `-benchmark`、`-max-wg`、`-probe-*`）也要列出来，但它们不算对配置的修改。
4. **把第 2、3 步的内容给用户看，等用户确认之后才能运行。** 用户确认过的差异写进这次实验的 `meta.json`（加一个字段 `config_deviation`，没有差异就写空列表）。

**禁止**：修改第 1 节中的任何文件里和配置有关的内容；在脚本里手写配置参数；用二进制的默认值代替论文配置。如果确实需要偏离论文配置（比如 T4 的 `cu8`），必须先在 STATUS 的“需要用户决定”里提出，用户批准之后才能做，而且只能作为**附加的敏感性实验**，不能替代主实验。
