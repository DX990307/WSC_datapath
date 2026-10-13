# 论文配置（不可修改）

> ⚠️ **10-13 更新：配置正在切换中。** 用户决定把 GPM 改成 **1/8 MI300X**（见 `tasks/T13_mi300x_config.md`）。**在 T13 的校准完成、用户确认之前，下面的旧配置（95b4f46）仍然是唯一有效的配置。** 新配置确认之后，Cowork 侧会更新本文件：新预设成为论文配置，旧配置保留为“HPCA 投稿版”，用来复现旧结果。

> **用户 10-13 的规定**：之前设置的实验配置是**论文始终使用的配置，不要改**。**每一个 task 执行之前，都要先按本文件做一次配置确认。**
> 本文件只记录事实，不提出任何修改。配置以代码为准，本文件是它的快照，用来对照检查。

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
**再加上**：`-disable-servers -mmutlb-lookup-latency=80`（`DEFAULT_MMUTLB_LOOKUP_LATENCY = 80`）

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
