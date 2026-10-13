# ISCA'27 准备工作新增的工具（T11、T12）

本文件说明 2026-10 为 CuPath 改投 ISCA 2027 新增的运行脚本、探针 benchmark、模拟器心跳和 driver 修复。任务背景和进度记录在仓库外的 `~/Desktop/CuPath/isca27/`（`README.md`、`STATUS.md`、`tasks/`）。

## 1. 用论文配置运行一次实验：`akkalat/isca27_run.sh`

```bash
# 在仓库根目录、服务器上运行（通常经由 exp run 启动）
akkalat/isca27_run.sh <exp_id> <commit> <task> [isca27_flags 选项 ...] -- <benchmark 参数 ...>
```

- 配置参数**不手写**：由 `akkalat/isca27_flags.py` 调用 runall2 自己的 `build_common_flags()` 生成，和论文的运行脚本完全一样（`BASE_COMMON_FLAGS` + `-disable-servers` + `-mmutlb-lookup-latency=80`）。
- 结果写到 `results/<exp_id>/`：`meta.json`、`summary.csv`（Driver 的 `kernel_time`、`total_time`，探针的每遍时间）、`stats/metrics.csv`、`stdout.log`、`stderr.log`。
- `meta.json` 里的 `config_deviation` 逐条列出和论文配置的差异（多了、少了哪些参数，以及原因）。
- 模拟器二进制按 commit 缓存在服务器的 `~/isca27bin/baseline_<commit>`；不存在时自动在服务器上编译。服务器上同步的代码没有 `.git`，所以 commit 由调用方传入，**调用方要先确认本机工作区是干净的**。

`isca27_flags.py` 的选项：

| 选项 | 作用 | 例子 |
|---|---|---|
| `--runall2=<参数>` | 传一个参数给 runall2 的参数解析器（可以重复） | `--runall2=--max-wg --runall2=4096` |
| `--drop=<参数>=REASON:<原因>` | 从生成的参数里去掉一个，原因写进 `config_deviation` | `--drop=-magic-memory-copy=REASON:T12-R3验证` |
| `--list` | 打印最终的公共参数，每行一个 | |
| `--deviation [benchmark 参数]` | 打印 `config_deviation` 的 JSON | |

例子（T12 的回归）：

```bash
exp run yss2 r1-fir "akkalat/isca27_run.sh 1013_t12-R1-fir_wafer48_paper_9c30a89 9c30a89 T12 --runall2=--max-wg --runall2=4096 -- -benchmark fir"
```

T11 探针用的 `akkalat/t11_run_probe.sh` + `akkalat/t11_probe_flags.py` 是同样的思路的早期版本（参数固定为 `--disable-servers --max-wg 0 --report-l2-source`）。

## 2. 模拟器心跳：`-heartbeat-file`

`akkalat/baseline/runner/heartbeat.go`。可选，不影响模拟结果。

- 开启方式：`-heartbeat-file <路径>`，或者设置环境变量 `EXP_HEARTBEAT`（`exp run` 会自动设成 `logs/<实验名>.heartbeat`）。`-heartbeat-interval` 设写入间隔，默认 10 秒（墙钟时间）。
- 文件内容（JSON）：`sim_time_ns`（当前模拟时间）、`wgs_completed`（已完成的 workgroup 总数）、`seq`、`wall`、`pid`、`args`，退出时写一条 `finished: true`。
- 判定卡死：**模拟时间不推进**（死锁：引擎没有事件了），或者**完成的 WG 数长时间不增加**（模拟时间在走但算不完），或者心跳文件本身不更新（进程被冻结）。`exp web` 网页按这个规则标“疑似卡住”（默认阈值 3 / 15 / 3 分钟）。
- 注意：WG 是成批完成的，大 workload 可能几分钟才完成第一批，WG 数的阈值不要设得太短。

## 3. 探针 benchmark：`-benchmark probe`

代码在 `mgpusim/benchmarks/microbench/probe/`，在 `akkalat/benchmarkselection/probe.go` 注册。一个 fp32 的 `touch` kernel：mode 0 读（累加后写一个值防止被优化掉），mode 1 写。

| 参数 | 默认 | 作用 |
|---|---|---|
| `-probe-owner` | 5 | 缓冲区放在哪个 GPM（driver GPU ID，从 1 开始） |
| `-probe-runner` | 5 | kernel 在哪个 GPM 上跑 |
| `-probe-bytes` | 2 MB | 缓冲区大小（`-probe-all-gpms` 时是每个 GPM 的大小） |
| `-probe-mode` | read | `read` 或 `write` |
| `-probe-passes` | 2 | 连续 launch 几遍（每遍 drain 完才开始下一遍） |
| `-probe-threads` | 32768 | 每次 launch 的线程数（256 = 1 个 WG，用来测延迟） |
| `-probe-all-gpms` | false | 48 个 GPM 各读自己的分片，并发执行 |
| `-probe-shared-ctx` | false | 配合 `-probe-all-gpms`：所有 GPM 的 queue 放在**同一个** driver Context 里（T12 的回归用） |

输出：`PROBE_CONFIG ...` 和每遍一行 `PROBE_PASS pass=<n> start_ns=... end_ns=... elapsed_ns=...`。汇总：`python3 akkalat/t11_analyze_probe.py results/<exp_id>`（读 `-report-l2-source` 的输出，给出本地/远程 × L2/DRAM 的数据来源、按跳数的远程延迟、RDMA 事务数、哪些 GPM 有指令）。

**GPM 编号**：driver GPU ID 是 1–48；组件名 `GPU[t]` 的 t 是 7×7 mesh 上的格子编号，中心格子 24 留给 driver。ID ≤ 24 时 t = ID − 1，ID ≥ 25 时 t = ID。`-report-l2-source` 的输出用的是 t。

## 4. SPMD 骨架（给 T10 复用）：`probe/spmd.go`

| 函数 | 作用 |
|---|---|
| `NewSPMD(driver, baseCtx, gpus)` | 每个 GPM 一个 rank：**一个 Context**（`InitWithExistingPID`，共享 PID，所以虚拟地址空间相同）和一个 command queue |
| `NewSharedCtxSPMD(...)` | 所有 rank 共用 `baseCtx`（旧写法，只用于测试） |
| `AllocFor(rank, gpu, bytes)` / `AllocShards(bytes)` | 分配在指定 GPM 上的缓冲区，由该 rank 的 Context 跟踪脏状态 |
| `UploadPtrTable(shards)` | 把所有分片的地址写成 uint64 数组，复制到每个 GPM；kernel 用 `table[j]` 访问第 j 个分片 |
| `RunPhase(co, []Launch)` | 先给每个 rank enqueue，再全部 drain；一个阶段结束才开始下一个 |
| `Now()` | 当前模拟时间（秒） |

要点：
- 每次 enqueue 前都要选中这个 rank 的 GPM（`RunPhase` 已经这样做），因为 driver 把 code object、kernarg、AQL packet 分配在当前选中的 GPU 上，否则取指和读 kernarg 会变成远程访问。
- driver 把当前选中的 GPU 存在 Context 里，所以一个 SPMD 只能由一个 goroutine 驱动。
- **推荐每个 GPM 一个 Context**（`NewSPMD`）。T12 修复之后共用一个 Context 也不会死锁，但没有 `-magic-memory-copy` 时，任一 rank 的 kernel launch 会把整个 Context 的 buffer 标脏，其他 rank 的 kernarg 拷贝就会触发对全部 GPM 的 L2 flush（flush 会清空 L2），影响跨 kernel 的 L2 复用。论文配置带 `-magic-memory-copy`，不会发 flush。

## 5. driver 修复（T12）：`mgpusim/driver/memorycopy.go`

- **问题**：一次 H2D/D2H 拷贝如果和脏 buffer 重叠，会先给所有 GPU 发 `FlushReq`，这些请求也算在这条拷贝命令里。原来只有 H2D/D2H 的回复处理会检查命令是否完成，`processFlushReturn` 只删除自己的请求；**flush 回复最后到达时，命令永远不出队，queue 卡死**。只在不加 `-magic-memory-copy`、且一个 Context 里有多个 queue 时会出现。
- **修复**：新增 `completeIfDone`，三种回复都调用它；命令的请求全部返回时收尾（D2H 先解码数据）。flush 的触发条件和范围、`-magic-memory-copy` 的路径、共享的 `cyclesLeft` 都没有改。
- **验证**：`mgpusim/driver/memorycopy_test.go`（两种回复顺序 × H2D/D2H）；fir、matrixmultiplication 修复前后周期数和全部统计一致；48 个 GPM 共用 Context 的探针修复前死锁、修复后完成。
- 比较两次运行的 `metrics.csv` 时要先排序：`report.go:302` 遍历 Go map，同一个二进制每次输出的行顺序都不同。

## 6. 监控

服务器上用 `exp` 启动的实验可以在本机用 `exp web` 打开网页监控（`~/.local/share/exp/exp_web.py`，说明见 `exp help`）：运行状态、模拟进度（心跳）、疑似卡住的判定、日志、`results/` 目录。
